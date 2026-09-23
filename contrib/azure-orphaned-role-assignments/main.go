package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

type options struct {
	subscriptionID string
	roleFilter     string
	scopeFilter    string
	principalTypes string
	dryRun         bool
	verbose        bool
}

func main() {
	opts := options{}

	flag.StringVar(&opts.subscriptionID, "subscription-id", "", "Azure subscription ID (required)")
	flag.StringVar(&opts.roleFilter, "role-filter", "", "Comma-separated list of role definition names to restrict deletion to (optional; substring match, case-insensitive). If empty, all roles are considered.")
	flag.StringVar(&opts.scopeFilter, "scope-filter", "", "Only consider assignments whose scope contains this substring, case-insensitive (optional, e.g. a resource group name)")
	flag.StringVar(&opts.principalTypes, "principal-types", "ServicePrincipal", "Comma-separated principal types to consider for cleanup (e.g. ServicePrincipal,User,Group)")
	flag.BoolVar(&opts.dryRun, "dry-run", true, "If true, only print what would be deleted (default: true)")
	flag.BoolVar(&opts.verbose, "verbose", false, "Enable verbose logging")
	flag.Parse()

	if opts.subscriptionID == "" {
		flag.Usage()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Println("Received interrupt signal, canceling...")
		cancel()
	}()

	if err := run(ctx, opts); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

// assignmentInfo holds the details we care about for reporting and deletion.
type assignmentInfo struct {
	id            string // full role assignment resource ID
	principalID   string
	principalType string
	scope         string
	roleName      string
}

func run(ctx context.Context, opts options) error {
	// Create Azure credentials using DefaultAzureCredential.
	// This supports environment variables, managed identity, Azure CLI, etc.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure credentials: %w", err)
	}

	raClient, err := armauthorization.NewRoleAssignmentsClient(opts.subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	rdClient, err := armauthorization.NewRoleDefinitionsClient(cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create role definitions client: %w", err)
	}

	wantedTypes := parseCSVSet(opts.principalTypes)
	roleFilters := parseCSVList(opts.roleFilter)
	scopeFilter := strings.ToLower(opts.scopeFilter)

	// 1. List every role assignment in the subscription and below (RG + resource scopes).
	log.Printf("Listing all role assignments in subscription %s (this includes resource group and resource scopes)", opts.subscriptionID)

	var all []assignmentInfo
	roleNameCache := map[string]string{}
	principalIDSet := map[string]struct{}{}

	pager := raClient.NewListForSubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list role assignments: %w", err)
		}
		for _, ra := range page.Value {
			if ra.ID == nil || ra.Properties == nil || ra.Properties.PrincipalID == nil {
				continue
			}
			info := assignmentInfo{
				id:          *ra.ID,
				principalID: *ra.Properties.PrincipalID,
			}
			if ra.Properties.Scope != nil {
				info.scope = *ra.Properties.Scope
			}
			if ra.Properties.PrincipalType != nil {
				info.principalType = string(*ra.Properties.PrincipalType)
			}
			if ra.Properties.RoleDefinitionID != nil {
				info.roleName = resolveRoleName(ctx, rdClient, *ra.Properties.RoleDefinitionID, roleNameCache)
			}
			all = append(all, info)
			principalIDSet[info.principalID] = struct{}{}
		}
	}

	log.Printf("Total role assignments in subscription (all scopes): %d", len(all))
	reportByScopeKind(all)

	// 2. Resolve which principals still exist in the directory via Microsoft Graph.
	principalIDs := make([]string, 0, len(principalIDSet))
	for id := range principalIDSet {
		principalIDs = append(principalIDs, id)
	}
	log.Printf("Resolving %d distinct principals against Microsoft Graph to detect deleted identities...", len(principalIDs))

	existing, err := resolveExistingPrincipals(ctx, cred, principalIDs, opts.verbose)
	if err != nil {
		return fmt.Errorf("failed to resolve principals against Microsoft Graph: %w", err)
	}
	log.Printf("Principals still present in directory: %d; deleted (orphaned): %d", len(existing), len(principalIDs)-len(existing))

	// 3. Select orphaned assignments matching the requested filters.
	var candidates []assignmentInfo
	for _, a := range all {
		if _, ok := existing[a.principalID]; ok {
			continue // principal still exists, not orphaned
		}
		if len(wantedTypes) > 0 {
			if _, ok := wantedTypes[a.principalType]; !ok {
				continue
			}
		}
		if scopeFilter != "" && !strings.Contains(strings.ToLower(a.scope), scopeFilter) {
			continue
		}
		if len(roleFilters) > 0 && !matchesAnySubstring(a.roleName, roleFilters) {
			continue
		}
		candidates = append(candidates, a)
	}

	log.Printf("Orphaned role assignments selected for cleanup: %d", len(candidates))
	reportCandidates(candidates)

	if len(candidates) == 0 {
		log.Println("Nothing to do.")
		return nil
	}

	// 4. Delete (or, in dry-run, just report).
	var deleted, failed int
	for _, a := range candidates {
		if opts.dryRun {
			if opts.verbose {
				log.Printf("[DRY-RUN] Would delete assignment %s (role=%q principalType=%s principal=%s scope=%s)",
					shortID(a.id), a.roleName, a.principalType, a.principalID, a.scope)
			}
			continue
		}
		log.Printf("Deleting assignment %s (role=%q principal=%s scope=%s)", shortID(a.id), a.roleName, a.principalID, a.scope)
		if _, err := raClient.DeleteByID(ctx, a.id, nil); err != nil {
			log.Printf("  ERROR: failed to delete: %v", err)
			failed++
			continue
		}
		deleted++
	}

	log.Println("")
	if opts.dryRun {
		log.Printf("DRY RUN: would delete %d orphaned role assignments", len(candidates))
		log.Println("To actually delete them, run with -dry-run=false")
	} else {
		log.Printf("Deleted %d role assignments, %d failed", deleted, failed)
	}

	return nil
}

// resolveRoleName resolves a role definition ID to its display name, caching results.
// On any failure it falls back to the trailing GUID of the definition ID.
func resolveRoleName(ctx context.Context, client *armauthorization.RoleDefinitionsClient, roleDefinitionID string, cache map[string]string) string {
	if name, ok := cache[roleDefinitionID]; ok {
		return name
	}
	name := shortID(roleDefinitionID)
	// GetByID takes the scope and the role definition ID; for a full definition ID
	// the scope portion is ignored, so we can pass the ID as the resource ID.
	resp, err := client.GetByID(ctx, roleDefinitionID, nil)
	if err == nil && resp.Properties != nil && resp.Properties.RoleName != nil {
		name = *resp.Properties.RoleName
	}
	cache[roleDefinitionID] = name
	return name
}

// resolveExistingPrincipals returns the set of principal IDs that still exist in the
// directory, using the Microsoft Graph directoryObjects/getByIds endpoint in batches.
// Any ID not returned by Graph is treated as deleted (orphaned).
func resolveExistingPrincipals(ctx context.Context, cred azcore.TokenCredential, ids []string, verbose bool) (map[string]struct{}, error) {
	existing := make(map[string]struct{}, len(ids))
	if len(ids) == 0 {
		return existing, nil
	}

	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://graph.microsoft.com/.default"}})
	if err != nil {
		return nil, fmt.Errorf("failed to acquire Microsoft Graph token: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	const batchSize = 1000 // getByIds accepts up to 1000 ids per request

	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]

		body, _ := json.Marshal(map[string]any{"ids": batch})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://graph.microsoft.com/v1.0/directoryObjects/getByIds", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph getByIds request failed: %w", err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph getByIds returned %d: %s", resp.StatusCode, string(data))
		}

		var parsed struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse graph response: %w", err)
		}
		for _, o := range parsed.Value {
			existing[o.ID] = struct{}{}
		}
		if verbose {
			log.Printf("  Graph batch %d-%d: %d of %d principals exist", start, end, len(parsed.Value), len(batch))
		}
	}

	return existing, nil
}

var rgRe = regexp.MustCompile(`(?i)/resourcegroups/([^/]+)`)

// scopeKind classifies a scope string into a coarse level for reporting.
func scopeKind(scope string) string {
	s := strings.ToLower(scope)
	switch {
	case s == "" || s == "/":
		return "root (tenant)"
	case strings.HasPrefix(s, "/providers/microsoft.management"):
		return "management group"
	case strings.Contains(s, "/resourcegroups/") && strings.Contains(s[strings.Index(s, "/resourcegroups/"):], "/providers/"):
		return "resource"
	case strings.Contains(s, "/resourcegroups/"):
		return "resource group"
	default:
		return "subscription"
	}
}

func resourceGroupOf(scope string) string {
	if m := rgRe.FindStringSubmatch(scope); m != nil {
		return strings.ToLower(m[1])
	}
	return "(sub/mg/root scope)"
}

func reportByScopeKind(all []assignmentInfo) {
	kinds := map[string]int{}
	for _, a := range all {
		kinds[scopeKind(a.scope)]++
	}
	log.Println("Breakdown by scope level (only subscription/management-group/root show in the portal IAM list):")
	for _, kv := range sortedByCount(kinds) {
		log.Printf("  %6d  %s", kv.count, kv.key)
	}
}

func reportCandidates(candidates []assignmentInfo) {
	if len(candidates) == 0 {
		return
	}
	byRole := map[string]int{}
	byRG := map[string]int{}
	for _, a := range candidates {
		byRole[a.roleName]++
		byRG[resourceGroupOf(a.scope)]++
	}
	log.Println("Orphaned assignments by role:")
	for _, kv := range sortedByCount(byRole) {
		log.Printf("  %6d  %s", kv.count, kv.key)
	}
	log.Println("Orphaned assignments by resource group (top 25):")
	rgs := sortedByCount(byRG)
	for i, kv := range rgs {
		if i >= 25 {
			log.Printf("  ... and %d more resource groups", len(rgs)-25)
			break
		}
		log.Printf("  %6d  %s", kv.count, kv.key)
	}
}

type kv struct {
	key   string
	count int
}

func sortedByCount(m map[string]int) []kv {
	out := make([]kv, 0, len(m))
	for k, c := range m {
		out = append(out, kv{k, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].key < out[j].key
	})
	return out
}

func parseCSVSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, p := range parseCSVList(s) {
		out[p] = struct{}{}
	}
	return out
}

func parseCSVList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func matchesAnySubstring(s string, subs []string) bool {
	ls := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(ls, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

func shortID(id string) string {
	if idx := strings.LastIndex(id, "/"); idx != -1 && idx < len(id)-1 {
		return id[idx+1:]
	}
	return id
}
