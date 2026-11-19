package etcd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

// fakeEtcdctl stands in for etcdctl when the reset-member script runs in a test. It keeps
// a member list in $FAKE_ETCD_DIR/members and supports the three commands the script uses.
//
// Faults are injected through files in $FAKE_ETCD_DIR:
//
//	quorum       "down" makes every call fail, as when the cluster has lost quorum.
//	plan.<verb>  a queue of one-shot outcomes for list, add or remove, consumed in order:
//	             "ok" succeeds, "fail" fails without changing the member list, and
//	             "failafter" applies the change and then reports a failure, like a commit
//	             whose response was lost.
//
// Every call is appended to calls.log together with the endpoint the script targeted.
const fakeEtcdctl = `#!/bin/bash
set -u
D=${FAKE_ETCD_DIR:?}
echo "endpoint=${ETCDCTL_ENDPOINTS:-} $*" >> "$D/calls.log"
[[ "${1:-}" == "member" ]] || { echo "unsupported: $*" >&2; exit 2; }
verb=${2:-}

mode=""
if [[ -s "$D/plan.$verb" ]]; then
  mode=$(head -n1 "$D/plan.$verb")
  sed -i '1d' "$D/plan.$verb"
fi
if [[ -z "$mode" ]]; then
  mode=ok
  [[ "$(cat "$D/quorum" 2>/dev/null || echo up)" == "down" ]] && mode=fail
fi
unavailable() { echo '{"level":"warn","error":"context deadline exceeded"}' >&2; exit 1; }

case "$verb" in
  list)
    [[ $mode == fail ]] && unavailable
    cat "$D/members"
    ;;
  remove)
    [[ $mode == fail ]] && unavailable
    grep -q "^${3:-}," "$D/members" || { echo "etcdserver: member not found" >&2; exit 1; }
    sed -i "/^${3:-},/d" "$D/members"
    [[ $mode == failafter ]] && unavailable
    echo "Member ${3:-} removed from cluster"
    ;;
  add)
    peer=""; prev=""
    for a in "$@"; do [[ "$prev" == "--peer-urls" ]] && peer=$a; prev=$a; done
    [[ $mode == fail ]] && unavailable
    if grep -qF "$peer" "$D/members"; then echo "etcdserver: Peer URLs already exists" >&2; exit 1; fi
    echo "$(printf '%016x' $((RANDOM * 65536 + RANDOM))), unstarted, , ${peer}, , false" >> "$D/members"
    [[ $mode == failafter ]] && unavailable
    echo "Member added to cluster"
    ;;
  *)
    echo "unsupported verb: $verb" >&2
    exit 2
    ;;
esac
`

// resetMemberPod runs the reset-member init container script the way the kubelet retries
// it: every attempt shares one emptyDir (the cluster-state marker directory), one empty
// data directory and one fake etcd cluster.
type resetMemberPod struct {
	t          *testing.T
	root       string
	scriptPath string
	hostname   string
	namespace  string
	peerURL    string
}

const (
	// A member that was bootstrapped with the cluster but has never started. It is
	// registered under its name and has no client URL.
	memberRegisteredByName = "registered by name"
	// A member that is not in the member list at all.
	memberAbsent = "absent"
	// A member that was added by peer URL and has not started. etcd lists it without a name.
	memberRegisteredByPeerURL = "registered by peer URL"
)

func newResetMemberPod(t *testing.T, script, memberPrefix, discoveryService, memberState string) *resetMemberPod {
	t.Helper()

	root := t.TempDir()
	pod := &resetMemberPod{
		t:         t,
		root:      root,
		hostname:  memberPrefix + "-2",
		namespace: "clusters-example-hcp",
	}
	pod.peerURL = pod.peerURLOf(pod.hostname, discoveryService)

	for _, dir := range []string{"clusterstate", "data", "tls", "bin", "fake"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "etcdctl"), []byte(fakeEtcdctl), 0o755); err != nil {
		t.Fatal(err)
	}

	// The script uses absolute pod paths. Point them at the temporary directory.
	rewritten := strings.NewReplacer(
		"/etc/etcd/clusterstate", filepath.Join(root, "clusterstate"),
		"/var/lib/data", filepath.Join(root, "data"),
		"/etc/etcd/tls", filepath.Join(root, "tls"),
	).Replace(script)
	pod.scriptPath = filepath.Join(root, "reset-member.sh")
	if err := os.WriteFile(pod.scriptPath, []byte(rewritten), 0o755); err != nil {
		t.Fatal(err)
	}

	members := []string{
		fmt.Sprintf("8e9e05c52164694d, started, %s-0, %s, https://%s-0.%s.%s.svc:2379, false", memberPrefix, pod.peerURLOf(memberPrefix+"-0", discoveryService), memberPrefix, discoveryService, pod.namespace),
		fmt.Sprintf("91bc3c398fb3c146, started, %s-1, %s, https://%s-1.%s.%s.svc:2379, false", memberPrefix, pod.peerURLOf(memberPrefix+"-1", discoveryService), memberPrefix, discoveryService, pod.namespace),
	}
	switch memberState {
	case memberRegisteredByName:
		members = append(members, fmt.Sprintf("fd422379fda50e48, started, %s, %s, , false", pod.hostname, pod.peerURL))
	case memberRegisteredByPeerURL:
		members = append(members, fmt.Sprintf("fd422379fda50e48, unstarted, , %s, , false", pod.peerURL))
	case memberAbsent:
	default:
		t.Fatalf("unknown member state %q", memberState)
	}
	pod.write("fake/members", strings.Join(members, "\n")+"\n")
	return pod
}

func (p *resetMemberPod) peerURLOf(member, discoveryService string) string {
	return fmt.Sprintf("https://%s.%s.%s.svc:2380", member, discoveryService, p.namespace)
}

func (p *resetMemberPod) write(rel, content string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.root, rel), []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func (p *resetMemberPod) read(rel string) string {
	data, err := os.ReadFile(filepath.Join(p.root, rel))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (p *resetMemberPod) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(p.root, rel))
	return err == nil
}

// attempt runs the script once, as one init-container try, and returns its exit code and output.
func (p *resetMemberPod) attempt() (int, string) {
	p.t.Helper()

	cmd := exec.CommandContext(p.t.Context(), "bash", p.scriptPath)
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(p.root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOSTNAME="+p.hostname,
		"NAMESPACE="+p.namespace,
		"FAKE_ETCD_DIR="+filepath.Join(p.root, "fake"),
	)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), string(out)
	default:
		p.t.Fatalf("failed to run the reset-member script: %v", err)
		return -1, ""
	}
}

// calls returns the recorded etcdctl invocations whose command contains the given text.
func (p *resetMemberPod) calls(command string) []string {
	var matched []string
	for _, line := range strings.Split(p.read("fake/calls.log"), "\n") {
		if strings.Contains(line, command) {
			matched = append(matched, line)
		}
	}
	return matched
}

// memberCountByPeerURL counts how many members in the cluster use this pod's peer URL.
func (p *resetMemberPod) memberCountByPeerURL() int {
	return strings.Count(p.read("fake/members"), p.peerURL)
}

type resetMemberStep struct {
	// plan holds one-shot outcomes per etcdctl verb, applied before this attempt.
	plan map[string][]string
	// quorumLost makes every etcdctl call fail for this attempt, and the attempts after it
	// until a step sets quorumRestored.
	quorumLost     bool
	quorumRestored bool
	// wantFailure expects a nonzero exit.
	wantFailure bool
	wantOutput  string
}

// TestResetMemberScriptBehavior executes the reset-member init container script, as
// rendered for the default etcd and for a shard, against a fake etcdctl. It exercises the
// retry paths that comparing the script text cannot: what the script does on the next
// attempt after an etcdctl call failed part way through.
//
//nolint:hypershiftlinter // testfuncstructure: executes the reset-member script from the etcd StatefulSet asset, not one Go function
func TestResetMemberScriptBehavior(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"bash", "awk", "grep", "sed", "head"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required to execute the reset-member script: %v", tool, err)
		}
	}

	defaultScript := defaultResetMemberScript(t)
	shardScript, err := shardResetMemberScript(defaultScript, "etcd-client-events", "etcd-discovery-events")
	NewWithT(t).Expect(err).ToNot(HaveOccurred())

	variants := []struct {
		name             string
		script           string
		memberPrefix     string
		discoveryService string
		clientService    string
	}{
		{name: "default etcd", script: defaultScript, memberPrefix: "etcd", discoveryService: "etcd-discovery", clientService: "etcd-client"},
		{name: "etcd shard", script: shardScript, memberPrefix: "etcd-events", discoveryService: "etcd-discovery-events", clientService: "etcd-client-events"},
	}

	tests := []struct {
		name        string
		memberState string
		// clusterState seeds the cluster-state marker, as left by an earlier attempt in this Pod.
		clusterState string
		steps        []resetMemberStep

		wantClusterState string
		wantRepairMarker bool
		wantRegistered   int // members using this pod's peer URL at the end
		// wantRemoves, wantAdds and wantLists are the number of etcdctl calls of each kind,
		// including failed ones. A negative value skips the check.
		wantRemoves int
		wantAdds    int
		wantLists   int
	}{
		{
			name:             "When the late member is registered by its bootstrap name, it should remove it and add it by peer URL",
			memberState:      memberRegisteredByName,
			steps:            []resetMemberStep{{}},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      1,
			wantAdds:         1,
			wantLists:        1,
		},
		{
			name:             "When the member is absent from the cluster, it should add it without removing anything",
			memberState:      memberAbsent,
			steps:            []resetMemberStep{{}},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      0,
			wantAdds:         1,
			wantLists:        -1,
		},
		{
			name:             "When an unnamed member already uses the peer URL, it should neither remove nor add",
			memberState:      memberRegisteredByPeerURL,
			steps:            []resetMemberStep{{}},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      0,
			wantAdds:         0,
			wantLists:        -1,
		},
		{
			name:        "When the cluster cannot be listed on first start, it should start in new state",
			memberState: memberRegisteredByName,
			steps: []resetMemberStep{
				{quorumLost: true},
			},
			wantClusterState: "new",
			wantRegistered:   1,
			wantRemoves:      0,
			wantAdds:         0,
			wantLists:        -1,
		},
		{
			// The sequence from the review of the original change: removal succeeds, addition
			// fails, the next attempt cannot list members, and connectivity then returns.
			name:        "When add fails after remove and members cannot be listed, it should refuse to start, keep existing, then add the member once and reuse it on the next retry",
			memberState: memberRegisteredByName,
			steps: []resetMemberStep{
				{plan: map[string][]string{"add": {"fail"}}, wantFailure: true},
				{quorumLost: true, wantFailure: true, wantOutput: "refusing to start etcd"},
				{quorumRestored: true},
				{},
			},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      1,
			wantAdds:         2,
			wantLists:        -1,
		},
		{
			name:        "When the member was never registered, add fails and members cannot be listed, it should refuse to start",
			memberState: memberAbsent,
			steps: []resetMemberStep{
				{plan: map[string][]string{"add": {"fail"}}, wantFailure: true},
				{quorumLost: true, wantFailure: true, wantOutput: "refusing to start etcd"},
				{quorumLost: true, wantFailure: true, wantOutput: "refusing to start etcd"},
			},
			wantClusterState: "existing",
			wantRepairMarker: true,
			wantRegistered:   0,
			wantRemoves:      0,
			wantAdds:         1,
			wantLists:        -1,
		},
		{
			name:        "When remove commits but its response is lost, it should add the member on retry",
			memberState: memberRegisteredByName,
			steps: []resetMemberStep{
				{plan: map[string][]string{"remove": {"failafter"}}, wantFailure: true},
				{},
			},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      1,
			wantAdds:         1,
			wantLists:        -1,
		},
		{
			name:        "When add commits but its response is lost, it should not add the member again",
			memberState: memberAbsent,
			steps: []resetMemberStep{
				{plan: map[string][]string{"add": {"failafter"}}, wantFailure: true},
				{},
			},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      0,
			wantAdds:         1,
			wantLists:        -1,
		},
		{
			name:        "When a list fails right after the cluster was listed, it should still finish because it lists members once",
			memberState: memberRegisteredByName,
			steps: []resetMemberStep{
				{plan: map[string][]string{"list": {"ok", "fail"}}},
			},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      1,
			wantAdds:         1,
			wantLists:        1,
		},
		{
			// The cluster was observed in an earlier attempt and no membership change is
			// pending, so the member is still registered and etcd must be able to start and
			// help the cluster regain quorum.
			name:         "When the cluster was observed and no membership change is pending, it should start etcd although members cannot be listed",
			memberState:  memberRegisteredByName,
			clusterState: "existing",
			steps: []resetMemberStep{
				{quorumLost: true, wantOutput: "so likely not up yet"},
			},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      0,
			wantAdds:         0,
			wantLists:        -1,
		},
		{
			name:        "When remove fails and members cannot be listed, it should refuse to start until the cluster can be listed again",
			memberState: memberRegisteredByName,
			steps: []resetMemberStep{
				{plan: map[string][]string{"remove": {"fail"}}, wantFailure: true},
				{quorumLost: true, wantFailure: true, wantOutput: "refusing to start etcd"},
				{quorumRestored: true},
			},
			wantClusterState: "existing",
			wantRegistered:   1,
			wantRemoves:      2,
			wantAdds:         1,
			wantLists:        -1,
		},
	}

	for _, variant := range variants {
		for _, test := range tests {
			t.Run(variant.name+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				g := NewWithT(t)

				pod := newResetMemberPod(t, variant.script, variant.memberPrefix, variant.discoveryService, test.memberState)
				if test.clusterState != "" {
					pod.write("clusterstate/state", test.clusterState+"\n")
				}

				for i, step := range test.steps {
					if step.quorumLost {
						pod.write("fake/quorum", "down")
					}
					if step.quorumRestored {
						pod.write("fake/quorum", "up")
					}
					for verb, outcomes := range step.plan {
						pod.write("fake/plan."+verb, strings.Join(outcomes, "\n")+"\n")
					}

					exitCode, output := pod.attempt()
					if step.wantFailure {
						g.Expect(exitCode).ToNot(BeZero(), "attempt %d should have failed:\n%s", i+1, output)
					} else {
						g.Expect(exitCode).To(BeZero(), "attempt %d should have succeeded:\n%s", i+1, output)
					}
					if step.wantOutput != "" {
						g.Expect(output).To(ContainSubstring(step.wantOutput), "attempt %d", i+1)
					}
				}

				g.Expect(pod.read("clusterstate/state")).To(Equal(test.wantClusterState), "cluster state marker")
				g.Expect(pod.exists("clusterstate/repair")).To(Equal(test.wantRepairMarker), "repair marker")
				g.Expect(pod.memberCountByPeerURL()).To(Equal(test.wantRegistered), "members registered with this pod's peer URL")
				g.Expect(pod.calls("member remove")).To(HaveLen(test.wantRemoves), "member remove calls")
				g.Expect(pod.calls("member add")).To(HaveLen(test.wantAdds), "member add calls")
				if test.wantLists >= 0 {
					g.Expect(pod.calls("member list")).To(HaveLen(test.wantLists), "member list calls")
				}

				// Every call must target this etcd's client service, and every added member
				// must use this etcd's discovery service.
				for _, call := range strings.Split(pod.read("fake/calls.log"), "\n") {
					if call == "" {
						continue
					}
					g.Expect(call).To(HavePrefix("endpoint=https://"+variant.clientService+":2379 "), "etcdctl endpoint")
				}
				for _, call := range pod.calls("member add") {
					g.Expect(call).To(ContainSubstring("--peer-urls "+pod.peerURL), "member add peer URL")
				}
			})
		}
	}
}
