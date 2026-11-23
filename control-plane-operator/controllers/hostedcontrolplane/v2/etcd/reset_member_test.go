package etcd

import (
	"fmt"
	"testing"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd/testdata/fake-etcdctl/mock"
)

// Each attempt declares every expected etcdctl call, in order. Attempts within
// a scenario run the init script again against the same pod volumes.
func resetMemberScenarios(calls expectedEtcdctlCalls) []resetMemberScenario {
	return []resetMemberScenario{
		{
			name: "When addition fails after removal, it should retry the add and select existing cluster state",
			attempts: []resetMemberAttempt{
				{
					etcdctl: []mock.Exchange{
						calls.probeCluster,
						calls.listWithStaleMember,
						calls.removeStaleMember,
						calls.addReplacement.Fails("etcdserver: unhealthy cluster"),
					},
					wantFailure: true,
				},
				{
					etcdctl:          []mock.Exchange{calls.probeCluster, calls.listSurvivorsOnly, calls.addReplacement},
					wantClusterState: "existing",
				},
			},
		},
		{
			name: "When the cluster probe fails on retry after removal, it should reproduce the known bootstrap fallback",
			todo: "TODO(OCPBUGS-140635): The retry exits successfully with cluster state new. Update this expectation when the bug is fixed.",
			attempts: []resetMemberAttempt{
				{
					etcdctl: []mock.Exchange{
						calls.probeCluster,
						calls.listWithStaleMember,
						calls.removeStaleMember,
						calls.addReplacement.Fails("etcdserver: unhealthy cluster"),
					},
					wantFailure: true,
				},
				{
					etcdctl:          []mock.Exchange{calls.probeCluster.Fails("context deadline exceeded")},
					wantClusterState: "new",
				},
			},
		},
		{
			name: "When the add times out but the replacement peer URL is registered, it should reproduce the redundant remove and add",
			todo: "TODO(OCPBUGS-140635): The retry removes and re-adds the registered replacement. Update the expected calls when the bug is fixed.",
			attempts: []resetMemberAttempt{
				{
					etcdctl: []mock.Exchange{
						calls.probeCluster,
						calls.listWithStaleMember,
						calls.removeStaleMember,
						calls.addReplacement.Fails("context deadline exceeded"),
					},
					wantFailure: true,
				},
				{
					etcdctl: []mock.Exchange{
						calls.probeCluster,
						calls.listWithUnstartedReplacement,
						calls.removeUnstartedReplacement,
						calls.addReplacement,
					},
					wantClusterState: "existing",
				},
			},
		},
		{
			name: "When replacement succeeds, it should remove the stale member before adding",
			attempts: []resetMemberAttempt{{
				etcdctl:          []mock.Exchange{calls.probeCluster, calls.listWithStaleMember, calls.removeStaleMember, calls.addReplacement},
				wantClusterState: "existing",
			}},
		},
		{
			name:             "When a local snapshot exists, it should preserve it and leave membership unchanged",
			preserveSnapshot: true,
			attempts: []resetMemberAttempt{{
				etcdctl:          []mock.Exchange{},
				wantClusterState: "new",
			}},
		},
		{
			name: "When the availability probe fails, it should leave membership unchanged and allow bootstrap",
			attempts: []resetMemberAttempt{{
				etcdctl:          []mock.Exchange{calls.probeCluster.Fails("context deadline exceeded")},
				wantClusterState: "new",
			}},
		},
		{
			name: "When membership lookup fails after a successful probe, it should retry without changing membership",
			attempts: []resetMemberAttempt{{
				etcdctl:     []mock.Exchange{calls.probeCluster, calls.listSurvivorsOnly.Fails("context deadline exceeded")},
				wantFailure: true,
			}},
		},
	}
}

type resetMemberScenario struct {
	name             string
	todo             string
	preserveSnapshot bool // Start with a snapshot and verify its contents after the last attempt.
	attempts         []resetMemberAttempt
}

type resetMemberAttempt struct {
	etcdctl          []mock.Exchange
	wantFailure      bool
	wantClusterState string
}

// expectedEtcdctlCalls holds the argument lists and replies that scenarios select
// from. The stale member is pod 1 (ID b); pods 0 and 2 survive its removal.
type expectedEtcdctlCalls struct {
	probeCluster                 mock.Exchange
	listWithStaleMember          mock.Exchange
	listSurvivorsOnly            mock.Exchange
	listWithUnstartedReplacement mock.Exchange
	removeStaleMember            mock.Exchange
	removeUnstartedReplacement   mock.Exchange
	addReplacement               mock.Exchange
}

func expectedCallsFor(script resetMemberScript) expectedEtcdctlCalls {
	peerURL := func(ordinal int) string {
		return fmt.Sprintf("https://%s.%s.%s.svc:2380", script.hostname(ordinal), script.discoveryService, resetMemberNamespace)
	}
	memberRow := func(id string, ordinal int) string {
		host := script.hostname(ordinal)
		return fmt.Sprintf("%s, started, %s, %s, https://%s:2379, false\n", id, host, peerURL(ordinal), host)
	}

	survivors := memberRow("a", 0) + memberRow("c", 2)
	unstartedReplacement := fmt.Sprintf("d, unstarted, , %s, , false\n", peerURL(1))
	listArgs := []string{"member", "list", "-w", "simple"}
	return expectedEtcdctlCalls{
		probeCluster:                 mock.Exchange{Args: []string{"member", "list"}},
		listWithStaleMember:          mock.Exchange{Args: listArgs, Reply: mock.Reply{Stdout: survivors + memberRow("b", 1)}},
		listSurvivorsOnly:            mock.Exchange{Args: listArgs, Reply: mock.Reply{Stdout: survivors}},
		listWithUnstartedReplacement: mock.Exchange{Args: listArgs, Reply: mock.Reply{Stdout: survivors + unstartedReplacement}},
		removeStaleMember:            mock.Exchange{Args: []string{"member", "remove", "b"}},
		removeUnstartedReplacement:   mock.Exchange{Args: []string{"member", "remove", "d"}},
		addReplacement:               mock.Exchange{Args: []string{"member", "add", script.hostname(1), "--peer-urls", peerURL(1)}},
	}
}

func TestResetMemberScript(t *testing.T) {
	t.Parallel()
	binDir := buildFakeEtcdctl(t)

	for _, workload := range []struct {
		name  string
		shard bool
	}{
		{name: "When using the default workload, it should handle member replacement"},
		{name: "When using a shard workload, it should handle member replacement", shard: true},
	} {
		t.Run(workload.name, func(t *testing.T) {
			t.Parallel()
			script := loadResetMemberScript(t, workload.shard)
			calls := expectedCallsFor(script)
			for _, scenario := range resetMemberScenarios(calls) {
				t.Run(scenario.name, func(t *testing.T) {
					t.Parallel()
					if scenario.todo != "" {
						t.Log(scenario.todo)
					}
					script.runScenario(t, binDir, scenario)
				})
			}
		})
	}
}
