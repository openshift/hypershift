package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"k8s.io/apimachinery/pkg/util/sets"
)

const testSubID = "11111111-1111-1111-1111-111111111111"

func rgScope(rg string) string {
	return "/subscriptions/" + testSubID + "/resourceGroups/" + rg
}

func TestIsUnderSubscription(t *testing.T) {
	tests := []struct {
		name  string
		scope string
		want  bool
	}{
		{name: "When scope is the subscription itself, it should be under the subscription", scope: "/subscriptions/" + testSubID, want: true},
		{name: "When scope is a resource group in the subscription, it should be under the subscription", scope: rgScope("os4-common"), want: true},
		{name: "When scope is a resource in the subscription, it should be under the subscription", scope: rgScope("os4-common") + "/providers/Microsoft.KeyVault/vaults/kv", want: true},
		{name: "When scope casing differs from the subscription ID, it should still be under the subscription", scope: "/SUBSCRIPTIONS/" + testSubID + "/resourceGroups/os4-common", want: true},
		{name: "When scope is a different subscription, it should not be under the subscription", scope: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg", want: false},
		{name: "When scope is a management group, it should not be under the subscription", scope: "/providers/Microsoft.Management/managementGroups/mg", want: false},
		{name: "When scope is the tenant root, it should not be under the subscription", scope: "/", want: false},
		{name: "When scope only shares the subscription ID as a prefix, it should not be under the subscription", scope: "/subscriptions/" + testSubID + "-other/resourceGroups/rg", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnderSubscription(tc.scope, testSubID); got != tc.want {
				t.Errorf("isUnderSubscription(%q) = %v, want %v", tc.scope, got, tc.want)
			}
		})
	}
}

func TestRequireDeleteScope(t *testing.T) {
	tests := []struct {
		name    string
		opts    options
		wantErr bool
	}{
		{name: "When dry-run is enabled, it should allow an unconstrained run", opts: options{dryRun: true}, wantErr: false},
		{name: "When deleting with no filters and no opt-in, it should be rejected", opts: options{dryRun: false}, wantErr: true},
		{name: "When deleting with a scope filter, it should be allowed", opts: options{dryRun: false, scopeFilter: "os4-common"}, wantErr: false},
		{name: "When deleting with a role filter, it should be allowed", opts: options{dryRun: false, roleFilter: "Key Vault Secrets User"}, wantErr: false},
		{name: "When deleting with the all-orphans opt-in and no filters, it should be allowed", opts: options{dryRun: false, allOrphans: true}, wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := requireDeleteScope(tc.opts)
			if (err != nil) != tc.wantErr {
				t.Errorf("requireDeleteScope(%+v) error = %v, wantErr %v", tc.opts, err, tc.wantErr)
			}
		})
	}
}

func TestScopeMatchesFilter(t *testing.T) {
	tests := []struct {
		name   string
		scope  string
		filter string
		want   bool
	}{
		{name: "When the filter is empty, it should match any scope", scope: rgScope("os4-common"), filter: "", want: true},
		{name: "When a bare name matches the resource group exactly, it should match", scope: rgScope("os4-common"), filter: "os4-common", want: true},
		{name: "When a bare name is a prefix of the resource group, it should not match", scope: rgScope("os4-common-backup"), filter: "os4-common", want: false},
		{name: "When a bare name matches the resource group in a different case, it should match", scope: rgScope("OS4-Common"), filter: "os4-common", want: true},
		{name: "When a bare name is used against a subscription-level scope, it should not match", scope: "/subscriptions/" + testSubID, filter: "os4-common", want: false},
		{name: "When a bare name appears only in a resource name, it should not match", scope: rgScope("other") + "/providers/Microsoft.KeyVault/vaults/os4-common", filter: "os4-common", want: false},
		{name: "When a path filter is the exact scope, it should match", scope: rgScope("os4-common"), filter: rgScope("os4-common"), want: true},
		{name: "When a path filter is an ancestor scope, it should match descendants", scope: rgScope("os4-common") + "/providers/Microsoft.KeyVault/vaults/kv", filter: rgScope("os4-common"), want: true},
		{name: "When a path filter is a sibling prefix, it should not match", scope: rgScope("os4-common-backup"), filter: rgScope("os4-common"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopeMatchesFilter(tc.scope, tc.filter); got != tc.want {
				t.Errorf("scopeMatchesFilter(%q, %q) = %v, want %v", tc.scope, tc.filter, got, tc.want)
			}
		})
	}
}

func TestSelectCandidates(t *testing.T) {
	now := time.Now()
	old := now.Add(-72 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	// A stable base assignment for an orphaned service principal in os4-common.
	orphan := func(id, principalID, role string) assignmentInfo {
		return assignmentInfo{
			id:            id,
			principalID:   principalID,
			principalType: "ServicePrincipal",
			scope:         rgScope("os4-common"),
			roleName:      role,
			createdOn:     old,
		}
	}

	tests := []struct {
		name     string
		all      []assignmentInfo
		existing sets.Set[string]
		opts     options
		wantIDs  []string
	}{
		{
			name:     "When a principal is missing from the directory, it should select the orphaned assignment",
			all:      []assignmentInfo{orphan("a1", "p-old", "Key Vault Secrets User")},
			existing: sets.New[string](),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal"},
			wantIDs:  []string{"a1"},
		},
		{
			name:     "When the principal still exists in the directory, it should not select the assignment",
			all:      []assignmentInfo{orphan("a1", "p-live", "Key Vault Secrets User")},
			existing: sets.New[string]("p-live"),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal"},
			wantIDs:  nil,
		},
		{
			name:     "When ARM returns a mixed-case principal ID that Graph returns lower-cased, it should treat the principal as existing",
			all:      []assignmentInfo{orphan("a1", "ABCD-EF12", "Key Vault Secrets User")},
			existing: sets.New[string]("abcd-ef12"),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal"},
			wantIDs:  nil,
		},
		{
			name: "When the principal type is not in the filter, it should skip the assignment",
			all: []assignmentInfo{func() assignmentInfo {
				a := orphan("a1", "p-user", "Reader")
				a.principalType = "User"
				return a
			}()},
			existing: sets.New[string](),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal"},
			wantIDs:  nil,
		},
		{
			name:     "When a scope filter names a different resource group, it should skip the assignment",
			all:      []assignmentInfo{orphan("a1", "p-old", "Key Vault Secrets User")},
			existing: sets.New[string](),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal", scopeFilter: "os4-common-backup"},
			wantIDs:  nil,
		},
		{
			name:     "When a role filter does not match the assignment role, it should skip the assignment",
			all:      []assignmentInfo{orphan("a1", "p-old", "Contributor")},
			existing: sets.New[string](),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal", roleFilter: "Key Vault Secrets User"},
			wantIDs:  nil,
		},
		{
			name: "When an orphaned assignment is inherited from a parent scope, it should never be selected",
			all: []assignmentInfo{func() assignmentInfo {
				a := orphan("a1", "p-old", "Reader")
				a.scope = "/providers/Microsoft.Management/managementGroups/mg"
				return a
			}()},
			existing: sets.New[string](),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal"},
			wantIDs:  nil,
		},
		{
			name: "When an orphaned assignment is newer than min-age, it should skip it",
			all: []assignmentInfo{func() assignmentInfo {
				a := orphan("a1", "p-old", "Key Vault Secrets User")
				a.createdOn = recent
				return a
			}()},
			existing: sets.New[string](),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal", minAge: 24 * time.Hour},
			wantIDs:  nil,
		},
		{
			name: "When min-age is zero, it should select even freshly-created orphaned assignments",
			all: []assignmentInfo{func() assignmentInfo {
				a := orphan("a1", "p-old", "Key Vault Secrets User")
				a.createdOn = recent
				return a
			}()},
			existing: sets.New[string](),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal", minAge: 0},
			wantIDs:  []string{"a1"},
		},
		{
			name: "When an assignment has no creation timestamp, it should treat it as old and select it",
			all: []assignmentInfo{func() assignmentInfo {
				a := orphan("a1", "p-old", "Key Vault Secrets User")
				a.createdOn = time.Time{}
				return a
			}()},
			existing: sets.New[string](),
			opts:     options{subscriptionID: testSubID, principalTypes: "ServicePrincipal", minAge: 24 * time.Hour},
			wantIDs:  []string{"a1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectCandidates(tc.all, tc.existing, tc.opts)
			var gotIDs []string
			for _, a := range got {
				gotIDs = append(gotIDs, a.id)
			}
			if !equalStringSets(gotIDs, tc.wantIDs) {
				t.Errorf("selectCandidates() selected %v, want %v", gotIDs, tc.wantIDs)
			}
		})
	}
}

// fakeDeleter records DeleteByID calls and fails those whose ID is in failIDs.
type fakeDeleter struct {
	mutex     sync.Mutex
	calls     []string
	failIDs   sets.Set[string]
	entered   chan context.Context
	release   <-chan struct{}
	canceled  chan struct{}
	active    int
	maxActive int
}

func (f *fakeDeleter) DeleteByID(ctx context.Context, roleAssignmentID string, _ *armauthorization.RoleAssignmentsClientDeleteByIDOptions) (armauthorization.RoleAssignmentsClientDeleteByIDResponse, error) {
	f.mutex.Lock()
	f.calls = append(f.calls, roleAssignmentID)
	f.active++
	f.maxActive = max(f.maxActive, f.active)
	f.mutex.Unlock()
	defer func() {
		f.mutex.Lock()
		f.active--
		f.mutex.Unlock()
	}()
	if f.entered != nil {
		f.entered <- ctx
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			if f.canceled != nil {
				f.canceled <- struct{}{}
			}
			<-f.release
		}
	}
	if err := ctx.Err(); err != nil {
		return armauthorization.RoleAssignmentsClientDeleteByIDResponse{}, err
	}
	if f.failIDs.Has(roleAssignmentID) {
		return armauthorization.RoleAssignmentsClientDeleteByIDResponse{}, fmt.Errorf("simulated delete failure for %s", roleAssignmentID)
	}
	return armauthorization.RoleAssignmentsClientDeleteByIDResponse{}, nil
}

func TestDeleteCandidates(t *testing.T) {
	candidates := []assignmentInfo{
		{id: "a1", roleName: "Key Vault Secrets User"},
		{id: "a2", roleName: "Reader"},
	}

	t.Run("When dry-run is enabled, it should not call the deleter and should succeed", func(t *testing.T) {
		f := &fakeDeleter{}
		if err := deleteCandidates(context.Background(), f, candidates, options{dryRun: true, deleteConcurrency: 5}); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(f.calls) != 0 {
			t.Errorf("expected no delete calls in dry-run, got %v", f.calls)
		}
	})

	t.Run("When all deletions succeed, it should delete every candidate and return no error", func(t *testing.T) {
		f := &fakeDeleter{}
		if err := deleteCandidates(context.Background(), f, candidates, options{deleteConcurrency: 5}); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !equalStringSets(f.calls, []string{"a1", "a2"}) {
			t.Errorf("expected all candidates deleted, got %v", f.calls)
		}
	})

	for _, concurrency := range []int{1, 5} {
		t.Run(fmt.Sprintf("When one deletion fails with concurrency %d, it should still attempt every candidate", concurrency), func(t *testing.T) {
			f := &fakeDeleter{failIDs: sets.New[string]("a1")}
			err := deleteCandidates(context.Background(), f, candidates, options{deleteConcurrency: concurrency})
			if err == nil || err.Error() != "1 of 2 role assignment deletions failed" {
				t.Fatalf("expected an accurate aggregate error, got %v", err)
			}
			if !equalStringSets(f.calls, []string{"a1", "a2"}) {
				t.Errorf("expected all candidates attempted despite failure, got %v", f.calls)
			}
		})
	}

	for _, concurrency := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("When concurrency is %d, it should overlap requests up to the limit", concurrency), func(t *testing.T) {
			assertDeleteConcurrency(t, concurrency)
		})
	}

	t.Run("When cancellation occurs during deletion, it should stop scheduling and wait for started calls", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		watchdog, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		fake := &fakeDeleter{entered: make(chan context.Context, 2), canceled: make(chan struct{}, 2), release: release}
		result := make(chan error, 1)
		go func() {
			result <- deleteCandidates(ctx, fake, append(candidates, assignmentInfo{id: "a3"}), options{deleteConcurrency: 2})
		}()
		for request := 0; request < 2; request++ {
			select {
			case <-fake.entered:
			case <-watchdog.Done():
				t.Fatal("timed out waiting for started calls")
			}
		}
		cancel()
		for request := 0; request < 2; request++ {
			select {
			case <-fake.canceled:
			case <-watchdog.Done():
				t.Fatal("parent cancellation did not reach started calls")
			}
		}
		select {
		case err := <-result:
			t.Fatalf("returned before started calls finished: %v", err)
		default:
		}
		unblock()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "0 deleted, 2 failed, 1 not attempted") {
				t.Fatalf("expected cancellation with accurate totals, got %v", err)
			}
		case <-watchdog.Done():
			t.Fatal("timed out waiting for cancellation")
		}
		if len(fake.calls) != 2 || fake.active != 0 {
			t.Fatalf("expected only started calls, all finished; calls=%v active=%d", fake.calls, fake.active)
		}
	})

	t.Run("When already canceled, it should not start any deletions", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		fake := &fakeDeleter{}
		err := deleteCandidates(ctx, fake, candidates, options{deleteConcurrency: 5})
		if !errors.Is(err, context.Canceled) || len(fake.calls) != 0 {
			t.Fatalf("expected cancellation without calls, got err=%v calls=%v", err, fake.calls)
		}
	})

	t.Run("When no candidates exist, it should succeed without calls", func(t *testing.T) {
		fake := &fakeDeleter{}
		if err := deleteCandidates(context.Background(), fake, nil, options{deleteConcurrency: 5}); err != nil || len(fake.calls) != 0 {
			t.Fatalf("expected success without calls, got %v", err)
		}
	})

	for _, concurrency := range []int{0, -1} {
		t.Run(fmt.Sprintf("When concurrency is %d, it should reject deletion before any calls", concurrency), func(t *testing.T) {
			fake := &fakeDeleter{}
			if err := deleteCandidates(context.Background(), fake, candidates, options{deleteConcurrency: concurrency}); err == nil || len(fake.calls) != 0 {
				t.Fatalf("expected invalid concurrency without calls, got %v", err)
			}
		})
	}
}

func TestValidateDeleteConcurrency(t *testing.T) {
	for _, concurrency := range []int{-1, 0, 1, 5} {
		t.Run(fmt.Sprintf("When concurrency is %d, it should require a positive value", concurrency), func(t *testing.T) {
			err := validateDeleteConcurrency(options{deleteConcurrency: concurrency})
			if (err != nil) != (concurrency <= 0) {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func assertDeleteConcurrency(t *testing.T, concurrency int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	release := make(chan struct{})
	defer close(release)
	batch := make([]assignmentInfo, 8)
	for index := range batch {
		batch[index].id = fmt.Sprintf("assignment-%d", index)
	}
	fake := &fakeDeleter{entered: make(chan context.Context, len(batch)), release: release}
	result := make(chan error, 1)
	go func() { result <- deleteCandidates(ctx, fake, batch, options{deleteConcurrency: concurrency}) }()
	for request := 0; request < len(batch); request++ {
		select {
		case callCtx := <-fake.entered:
			deadline, ok := callCtx.Deadline()
			if !ok || time.Until(deadline) > apiTimeout || time.Until(deadline) < apiTimeout-10*time.Second {
				t.Error("expected a bounded request deadline")
			}
		case <-watchdog.Done():
			t.Fatal("timed out waiting for delete calls")
		}
		if request >= concurrency-1 {
			select {
			case release <- struct{}{}:
			case <-watchdog.Done():
				t.Fatal("timed out releasing delete call")
			}
		}
	}
	for request := 1; request < concurrency; request++ {
		select {
		case release <- struct{}{}:
		case <-watchdog.Done():
			t.Fatal("timed out releasing remaining calls")
		}
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-watchdog.Done():
		t.Fatal("timed out waiting for workers")
	}
	if fake.maxActive != concurrency || len(fake.calls) != len(batch) || fake.active != 0 {
		t.Fatalf("expected %d concurrent calls and all attempts completed, got max=%d calls=%d active=%d", concurrency, fake.maxActive, len(fake.calls), fake.active)
	}
}

func TestRun(t *testing.T) {
	for _, concurrency := range []int{-1, 0} {
		for _, dryRun := range []bool{true, false} {
			t.Run(fmt.Sprintf("When concurrency is %d and dry-run is %t, it should fail before creating Azure clients", concurrency, dryRun), func(t *testing.T) {
				err := run(context.Background(), options{deleteConcurrency: concurrency, dryRun: dryRun})
				if err == nil || !strings.Contains(err.Error(), "-delete-concurrency must be > 0") {
					t.Fatalf("expected concurrency validation before Azure operations, got %v", err)
				}
			})
		}
	}
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
