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
	minAge         time.Duration
	dryRun         bool
	verbose        bool
}

// apiTimeout bounds each individual Azure/Graph API call so a stalled request
// cannot hang an unattended cleanup indefinitely. The parent context is still
// used for signal-driven cancellation.
const apiTimeout = 2 * time.Minute

func main() {
	opts := options{}

	flag.StringVar(&opts.subscriptionID, "subscription-id", "", "Azure subscription ID (required)")
	flag.StringVar(&opts.roleFilter, "role-filter", "", "Comma-separated list of role definition names to restrict deletion to (optional; substring match, case-insensitive). If empty, all roles are considered.")
	flag.StringVar(&opts.scopeFilter, "scope-filter", "", "Only consider assignments whose scope contains this substring, case-insensitive (optional, e.g. a resource group name)")
	flag.StringVar(&opts.principalTypes, "principal-types", "ServicePrincipal", "Comma-separated principal types to consider for cleanup (e.g. ServicePrincipal,User,Group)")
	flag.DurationVar(&opts.minAge, "min-age", 24*time.Hour, "Only consider assignments created at least this long ago. Guards against deleting grants for freshly-created principals that Microsoft Graph has not yet propagated. Set to 0 to disable.")
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
	createdOn     time.Time // zero if unknown
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

	// 1. List every role assignment in the subscription and below (RG + resource scopes).
	log.Print("Listing all role assignments in the target subscription (this includes resource group and resource scopes)")
	if opts.verbose {
		log.Printf("  subscription: %s", opts.subscriptionID)
	}
	all, principalIDs, err := listAllAssignments(ctx, raClient, rdClient)
	if err != nil {
		return err
	}
	log.Printf("Total role assignments in subscription (all scopes): %d", len(all))
	reportByScopeKind(all)

	// 2. Resolve which principals still exist in the directory via Microsoft Graph.
	log.Printf("Resolving %d distinct principals against Microsoft Graph to detect deleted identities...", len(principalIDs))
	existing, err := resolveExistingPrincipals(ctx, cred, principalIDs, opts.verbose)
	if err != nil {
		return fmt.Errorf("failed to resolve principals against Microsoft Graph: %w", err)
	}
	log.Printf("Principals still present in directory: %d; deleted (orphaned): %d", len(existing), len(principalIDs)-len(existing))

	// 3. Select orphaned assignments matching the requested filters.
	candidates := selectCandidates(all, existing, opts)
	reportCandidates(candidates)
	if len(candidates) == 0 {
		log.Println("Nothing to do.")
		return nil
	}

	// 4. Delete (or, in dry-run, just report).
	return deleteCandidates(ctx, raClient, candidates, opts)
}

// listAllAssignments pages through every role assignment in the subscription
// (including resource-group and resource scopes), resolving role names, and
// returns the assignments plus the set of distinct principal IDs seen.
func listAllAssignments(ctx context.Context, raClient *armauthorization.RoleAssignmentsClient, rdClient *armauthorization.RoleDefinitionsClient) ([]assignmentInfo, []string, error) {
	var all []assignmentInfo
	roleNameCache := map[string]string{}
	principalIDSet := map[string]struct{}{}

	pager := raClient.NewListForSubscriptionPager(nil)
	for pager.More() {
		page, err := func() (armauthorization.RoleAssignmentsClientListForSubscriptionResponse, error) {
			pageCtx, cancel := context.WithTimeout(ctx, apiTimeout)
			defer cancel()
			return pager.NextPage(pageCtx)
		}()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list role assignments: %w", err)
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
			if ra.Properties.CreatedOn != nil {
				info.createdOn = *ra.Properties.CreatedOn
			}
			if ra.Properties.RoleDefinitionID != nil {
				info.roleName = resolveRoleName(ctx, rdClient, *ra.Properties.RoleDefinitionID, roleNameCache)
			}
			all = append(all, info)
			principalIDSet[info.principalID] = struct{}{}
		}
	}

	principalIDs := make([]string, 0, len(principalIDSet))
	for id := range principalIDSet {
		principalIDs = append(principalIDs, id)
	}
	return all, principalIDs, nil
}

// selectCandidates returns the orphaned assignments (principal absent from the
// directory) that match the configured filters and are safe to delete, logging
// how many were skipped for inherited scope or the min-age guard.
func selectCandidates(all []assignmentInfo, existing map[string]struct{}, opts options) []assignmentInfo {
	wantedTypes := parseCSVSet(opts.principalTypes)
	roleFilters := parseCSVList(opts.roleFilter)
	scopeFilter := strings.ToLower(opts.scopeFilter)

	var candidates []assignmentInfo
	var skippedInherited, skippedRecent int
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
		// Only ever delete assignments at or beneath the target subscription.
		// Inherited management-group/root grants surface in the listing but may be
		// relied on by other subscriptions, so they are reported, never deleted.
		if !isUnderSubscription(a.scope, opts.subscriptionID) {
			skippedInherited++
			continue
		}
		// Guard against deleting grants for freshly-created principals that Graph
		// has not yet propagated: skip assignments newer than min-age.
		if opts.minAge > 0 && !a.createdOn.IsZero() && time.Since(a.createdOn) < opts.minAge {
			skippedRecent++
			continue
		}
		candidates = append(candidates, a)
	}

	log.Printf("Orphaned role assignments selected for cleanup: %d", len(candidates))
	if skippedInherited > 0 {
		log.Printf("Skipped %d orphaned assignments inherited from management-group/root scope (reported, never deleted)", skippedInherited)
	}
	if skippedRecent > 0 {
		log.Printf("Skipped %d orphaned assignments created within the last %s (min-age guard)", skippedRecent, opts.minAge)
	}
	return candidates
}

// deleteCandidates deletes the selected assignments (or, in dry-run, only reports
// them), attempting every candidate and returning an error if any deletion failed.
func deleteCandidates(ctx context.Context, raClient *armauthorization.RoleAssignmentsClient, candidates []assignmentInfo, opts options) error {
	var deleted, failed int
	for _, a := range candidates {
		if opts.dryRun {
			if opts.verbose {
				log.Printf("[DRY-RUN] Would delete assignment %s (role=%q principalType=%s principal=%s scope=%s)",
					shortID(a.id), a.roleName, a.principalType, a.principalID, a.scope)
			}
			continue
		}
		// Default logging avoids raw principal IDs and full ARM scopes; -verbose
		// includes them for auditing.
		if opts.verbose {
			log.Printf("Deleting assignment %s (role=%q principal=%s scope=%s)", shortID(a.id), a.roleName, a.principalID, a.scope)
		} else {
			log.Printf("Deleting orphaned assignment %s (role=%q)", shortID(a.id), a.roleName)
		}
		if err := func() error {
			delCtx, cancel := context.WithTimeout(ctx, apiTimeout)
			defer cancel()
			_, err := raClient.DeleteByID(delCtx, a.id, nil)
			return err
		}(); err != nil {
			log.Printf("  ERROR: failed to delete assignment %s: %v", shortID(a.id), err)
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

	if failed > 0 {
		return fmt.Errorf("%d of %d role assignment deletions failed", failed, deleted+failed)
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
	callCtx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	resp, err := client.GetByID(callCtx, roleDefinitionID, nil)
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

	tok, err := func() (azcore.AccessToken, error) {
		tokCtx, cancel := context.WithTimeout(ctx, apiTimeout)
		defer cancel()
		return cred.GetToken(tokCtx, policy.TokenRequestOptions{Scopes: []string{"https://graph.microsoft.com/.default"}})
	}()
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

		body, err := json.Marshal(map[string]any{"ids": batch})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal graph request: %w", err)
		}
		reqCtx, cancel := context.WithTimeout(ctx, apiTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
			"https://graph.microsoft.com/v1.0/directoryObjects/getByIds", bytes.NewReader(body))
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("graph getByIds request failed: %w", err)
		}
		data, readErr := io.ReadAll(resp.Body)
		if cerr := resp.Body.Close(); cerr != nil && readErr == nil {
			readErr = cerr
		}
		cancel()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read graph response: %w", readErr)
		}
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

// isUnderSubscription reports whether scope is the target subscription scope or a
// resource group/resource beneath it. Management-group and root scopes are not,
// so inherited grants are never selected for deletion.
func isUnderSubscription(scope, subscriptionID string) bool {
	s := strings.ToLower(scope)
	prefix := "/subscriptions/" + strings.ToLower(subscriptionID)
	return s == prefix || strings.HasPrefix(s, prefix+"/")
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
