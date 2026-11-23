package etcd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd/testdata/fake-etcdctl/mock"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const resetMemberNamespace = "test-ns"

type resetMemberScript struct {
	body             string
	name             string
	discoveryService string
}

// loadResetMemberScript loads the production asset and, for shards, applies the
// adapter that replaces its reset-member script.
func loadResetMemberScript(t *testing.T, shard bool) resetMemberScript {
	t.Helper()
	g := NewWithT(t)
	name, discoveryService := "etcd", "etcd-discovery"
	if shard {
		name, discoveryService = "etcd-events", "etcd-discovery-events"
	}
	sts, err := assets.LoadStatefulSetManifestTemplated(ComponentName, etcdTemplateData(name))
	g.Expect(err).NotTo(HaveOccurred())
	if shard {
		cpContext := component.WorkloadContext{HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: resetMemberNamespace},
			Spec: hyperv1.HostedControlPlaneSpec{
				Networking: hyperv1.ClusterNetworking{ClusterNetwork: []hyperv1.ClusterNetworkEntry{
					{CIDR: mustParseCIDR("10.0.0.0/16")},
				}},
			},
		}}
		g.Expect(adaptStatefulSetForShard(cpContext, sts, hyperv1.ManagedEtcdShardSpec{
			Name: "events", Replicas: 3,
			Storage: hyperv1.ManagedEtcdShardStorageSpec{Type: hyperv1.EmptyDirEtcdShardStorage},
		})).To(Succeed())
	}
	c := podspec.FindContainer("reset-member", sts.Spec.Template.Spec.InitContainers)
	g.Expect(c).NotTo(BeNil(), "missing reset-member init container")
	g.Expect(c.Command).To(Equal([]string{"/bin/bash"}))
	g.Expect(c.Args).To(HaveLen(2))
	g.Expect(c.Args[0]).To(Equal("-c"))
	// Redirect the two mounted paths into the subprocess working directory.
	const statePath = "/etc/etcd/clusterstate/state"
	const dbPath = "/var/lib/data/member/snap/db"
	g.Expect(c.Args[1]).To(ContainSubstring(statePath))
	g.Expect(c.Args[1]).To(ContainSubstring(dbPath))
	return resetMemberScript{
		body:             strings.NewReplacer(statePath, "./state", dbPath, "./data/member/snap/db").Replace(c.Args[1]),
		name:             name,
		discoveryService: discoveryService,
	}
}

func (s resetMemberScript) hostname(ordinal int) string {
	return fmt.Sprintf("%s-%d", s.name, ordinal)
}

func buildFakeEtcdctl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "etcdctl"), "./testdata/fake-etcdctl")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building fake etcdctl: %v\n%s", err, output)
	}
	return dir
}

func (s resetMemberScript) runScenario(t *testing.T, binDir string, scenario resetMemberScenario) {
	t.Helper()
	g := NewWithT(t)
	dir := t.TempDir()
	db := filepath.Join(dir, "data/member/snap/db")
	if scenario.preserveSnapshot {
		g.Expect(os.MkdirAll(filepath.Dir(db), 0700)).To(Succeed())
		g.Expect(os.WriteFile(db, []byte("snapshot"), 0600)).To(Succeed())
	}
	for i, attempt := range scenario.attempts {
		t.Logf("Attempt %d", i+1)
		s.runAttempt(t, dir, binDir, attempt)
	}
	if scenario.preserveSnapshot {
		g.Expect(os.ReadFile(db)).To(Equal([]byte("snapshot")))
	}
}

func (s resetMemberScript) runAttempt(t *testing.T, dir, binDir string, attempt resetMemberAttempt) {
	t.Helper()
	g := NewWithT(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", s.body)
	cmd.Dir = dir
	url, finish := mock.Start(ctx, attempt.etcdctl)
	cmd.Env = append(cmd.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOSTNAME="+s.hostname(1), "NAMESPACE="+resetMemberNamespace, mock.ServerEnv+"="+url)
	output, err := cmd.CombinedOutput()
	mockErr := finish()
	if ctx.Err() != nil {
		t.Fatalf("reset-member interrupted: %v\n%s", ctx.Err(), output)
	}
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() < 1) {
		t.Fatalf("running reset-member: %v\n%s", err, output)
	}
	if mockErr != nil {
		t.Fatalf("%v\n%s", mockErr, output)
	}
	if attempt.wantFailure {
		g.Expect(err).To(HaveOccurred(), "expected reset-member to exit unsuccessfully\n%s", output)
		return
	}
	g.Expect(err).NotTo(HaveOccurred(), "%s", output)
	state, err := os.ReadFile(filepath.Join(dir, "state"))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(state)).To(Equal(attempt.wantClusterState + "\n"))
}
