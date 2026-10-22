//go:build e2ev2

package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	hyperapi "github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/onsi/ginkgo/v2/types"
)

func TestAWSPlatformConfigClusterSpecs(t *testing.T) {
	t.Run("When AWS cluster specs are built, it should omit Azure dump credentials for every variant", func(t *testing.T) {
		assertions := NewWithT(t)
		specs := (&AWSPlatformConfig{}).ClusterSpecs("release-image", "n1-image")
		assertions.Expect(specs).NotTo(BeEmpty())
		for _, spec := range specs {
			assertions.Expect(spec.DumpArgs).To(BeEmpty(), "variant %s", spec.Variant)
		}
	})
	t.Run("When opt-in AWS fixtures are defined, it should preserve their isolation and creation prerequisites", func(t *testing.T) {
		t.Setenv("EXTRA_ARGS", "")
		t.Setenv("E2E_AWS_KMS_KEY_ARN", "arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000000")
		specs := (&AWSPlatformConfig{}).ClusterSpecs("latest", "previous")
		byVariant := map[string]ClusterSpec{}
		for _, spec := range specs {
			if _, exists := byVariant[spec.Variant]; exists {
				t.Fatalf("duplicate variant %s", spec.Variant)
			}
			byVariant[spec.Variant] = spec
		}
		assertions := NewWithT(t)
		assertions.Expect(byVariant["private"].CreateArgs).To(ContainElements("--endpoint-access=Private", "--external-dns-domain="))
		assertions.Expect(byVariant["private-route"].CreateArgs).NotTo(ContainElement("--external-dns-domain="))
		assertions.Expect(byVariant["proxy"].CreateArgs).To(ContainElement("--enable-proxy"))
		assertions.Expect(byVariant["break-glass-ha"].InitialNodePoolReplicas).To(HaveValue(Equal(0)))
		assertions.Expect(byVariant["break-glass-ha"].CreateArgs).To(ContainElement("--control-plane-availability-policy=HighlyAvailable"))
		assertions.Expect(byVariant["custom-config"].CreateArgs).To(ContainElements("--disable-cluster-capabilities=ImageRegistry,openshift-samples,Insights,Console,NodeTuning,Ingress", "--kms-key-arn="+os.Getenv("E2E_AWS_KMS_KEY_ARN")))
		assertions.Expect(byVariant["upgrade"].ReleaseImage).To(Equal("previous"))
		assertions.Expect(byVariant["upgrade"].CreateArgs).To(ContainElement("--shared-role=false"))
		assertions.Expect(byVariant["upgrade-pki-disabled"].ReleaseImage).To(Equal("previous"))
		assertions.Expect(byVariant["upgrade-pki-disabled"].CreateArgs).To(ContainElements("--shared-role=false", "--control-plane-availability-policy=HighlyAvailable"))
		assertions.Expect(byVariant["upgrade-pki-disabled"].CreateArgs).NotTo(ContainElement("--annotations=" + hyperv1.DisablePKIReconciliationAnnotation + "=true"))
		assertions.Expect(byVariant["cilium"].CreateArgs).To(ContainElements("--public-only", "--network-type=Other", "--cluster-cidr=10.132.0.0/14"))
		assertions.Expect(byVariant["cilium"].InitialNodePoolReplicas).To(HaveValue(Equal(1)))
		assertions.Expect(byVariant["guest-olm"].CreateArgs).To(ContainElements("--public-only", "--olm-catalog-placement=Guest"))
		assertions.Expect(byVariant["guest-olm"].InitialNodePoolReplicas).To(HaveValue(Equal(1)))
		assertions.Expect(byVariant["trust-bundle"].CreateArgs).To(ContainElements("--public-only", "--shared-role=false"))
		assertions.Expect(byVariant["trust-bundle"].InitialNodePoolReplicas).To(HaveValue(Equal(0)))
		assertions.Expect(byVariant["nodepool-dispatch"].CreateArgs).To(ContainElements("--public-only", "--shared-role=false"))
		assertions.Expect(byVariant["nodepool-dispatch"].InitialNodePoolReplicas).To(HaveValue(Equal(0)))
		for _, variant := range []string{"karpenter", "karpenter-upgrade", "karpenter-upgrade-pki-disabled"} {
			assertions.Expect(byVariant[variant].CreateArgs).To(ContainElements("--additional-tags=red-hat-clustertype=rosa", "--additional-tags=red-hat-managed=true"))
			assertions.Expect(byVariant[variant].CreateArgs).NotTo(ContainElement("--use-rosa-managed-policies"))
		}
	})
	for _, test := range []struct {
		name  string
		zones []string
		ha    bool
	}{
		{name: "When fewer than three zones are configured, it should preserve the requested default worker count", zones: []string{"us-east-1a", "us-east-1b"}},
		{name: "When three zones are configured, it should use one worker per zone and highly available infrastructure", zones: []string{"us-east-1a", "us-east-1b", "us-east-1c"}, ha: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("EXTRA_ARGS", "")
			for _, spec := range (&AWSPlatformConfig{zones: test.zones}).ClusterSpecs("latest", "previous") {
				if spec.Variant != "public" {
					continue
				}
				assertions := NewWithT(t)
				if test.ha {
					assertions.Expect(spec.InitialNodePoolReplicas).To(HaveValue(Equal(1)))
					assertions.Expect(spec.CreateArgs).To(ContainElement("--infra-availability-policy=HighlyAvailable"))
				} else {
					assertions.Expect(spec.InitialNodePoolReplicas).To(BeNil())
					assertions.Expect(spec.CreateArgs).NotTo(ContainElement("--infra-availability-policy=HighlyAvailable"))
				}
			}
		})
	}
}

func TestAWSPlatformConfigTestMatrix(t *testing.T) {
	t.Run("When the cross-major plan is explicitly selected, it should reach the required-input upgrade without concurrent variant reuse", func(t *testing.T) {
		path := filepath.Join("..", "..", "..", "..", "contrib", "aws-e2e-v2-cross-major.yaml")
		plan, err := ReadTestPlan(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Validate((&AWSPlatformConfig{}).ClusterSpecs("latest", "previous")); err != nil {
			t.Fatal(err)
		}
		var upgradeFilter string
		for _, lane := range plan.TestMatrix.Sequential {
			for _, step := range lane.Steps {
				if step.Name == "aws-nodepool-major-version-upgrade" {
					upgradeFilter = step.LabelFilter
				}
			}
		}
		if upgradeFilter == "" {
			t.Fatal("explicit cross-major plan omits the original upgrade child")
		}
		filter, err := types.ParseLabelFilter(upgradeFilter)
		if err != nil {
			t.Fatal(err)
		}
		if !filter([]string{"lifecycle", "nodepool-lifecycle", "nodepool-major-version-upgrade"}) {
			t.Fatal("explicit cross-major plan does not select the actual upgrade body")
		}
	})
	t.Run("When the default AWS plan is selected, it should retain existing lanes and reach native lifecycle coverage", func(t *testing.T) {
		platform := &AWSPlatformConfig{}
		plan := platform.DefaultTestPlan()
		if err := plan.Validate(platform.ClusterSpecs("latest", "previous")); err != nil {
			t.Fatal(err)
		}
		filters := map[string]string{}
		for _, lane := range plan.TestMatrix.Sequential {
			for _, group := range lane.Steps {
				filters[group.Name] = group.LabelFilter
			}
		}
		assertions := NewWithT(t)
		assertions.Expect(filters["public"]).To(ContainSubstring("hosted-cluster-api-validation"))
		assertions.Expect(filters["public-nodepool-rollouts"]).To(ContainSubstring("nodepool-auto-repair"))
		assertions.Expect(filters["public-native-lifecycle"]).To(ContainSubstring("nodepool-aws-day2-tags"))
		assertions.Expect(filters["public-native-lifecycle"]).To(ContainSubstring("hosted-cluster-metrics-lifecycle"))
		assertions.Expect(filters["autoscaling-balancing"]).To(ContainSubstring("nodepool-autoscaling-scale-from-zero"))
		assertions.Expect(plan.TestMatrix.Variants()).NotTo(ContainElement("private"))
		assertions.Expect(plan.TestMatrix.Variants()).NotTo(ContainElement("upgrade-pki-disabled"))
		assertions.Expect(plan.TestMatrix.Variants()).NotTo(ContainElement("cilium"))
		for _, variant := range []string{"guest-olm", "trust-bundle", "nodepool-dispatch"} {
			assertions.Expect(plan.TestMatrix.Variants()).NotTo(ContainElement(variant))
		}
		assertions.Expect(plan.TestMatrix.Variants()).NotTo(ContainElement("karpenter-upgrade-pki-disabled"))
	})
	t.Run("When optional N3 and N4 release images are absent, it should not select their required-input specs in the default plan", func(t *testing.T) {
		for _, name := range []string{"E2E_N3_RELEASE_IMAGE", "E2E_N4_RELEASE_IMAGE", "OCP_IMAGE_N3", "OCP_IMAGE_N4"} {
			t.Setenv(name, "")
		}
		plan := (&AWSPlatformConfig{}).DefaultTestPlan()
		groups := append([]TestGroup{}, plan.TestMatrix.Parallel...)
		for _, lane := range plan.TestMatrix.Sequential {
			groups = append(groups, lane.Steps...)
		}
		for _, group := range groups {
			filter, err := types.ParseLabelFilter(group.LabelFilter)
			if err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{"nodepool-n3-release", "nodepool-n4-release"} {
				if filter([]string{"lifecycle", "nodepool-lifecycle", label}) {
					t.Fatalf("default group %s selects required-input spec %s without an image", group.Name, label)
				}
			}
		}
	})
}

func TestAWSPlatformConfigTestMatrixOptIns(t *testing.T) {
	t.Run("When the public opt-in plan is selected, it should resolve every AWS fixture without concurrent reuse", func(t *testing.T) {
		path := filepath.Join("..", "..", "..", "..", "contrib", "aws-e2e-v2-opt-in.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := ParseTestPlan(data, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Validate((&AWSPlatformConfig{}).ClusterSpecs("latest", "previous")); err != nil {
			t.Fatal(err)
		}
		assertions := NewWithT(t)
		assertions.Expect(plan.TestMatrix.Variants()).To(ContainElements("private", "private-route", "proxy", "custom-config", "break-glass-ha", "public"))
		groups := append([]TestGroup{}, plan.TestMatrix.Parallel...)
		for _, lane := range plan.TestMatrix.Sequential {
			groups = append(groups, lane.Steps...)
		}
		for _, label := range []string{"nodepool-n3-release", "nodepool-n4-release"} {
			selected := false
			for _, group := range groups {
				filter, err := types.ParseLabelFilter(group.LabelFilter)
				if err != nil {
					t.Fatal(err)
				}
				selected = selected || filter([]string{"lifecycle", "nodepool-lifecycle", label})
			}
			if !selected {
				t.Fatalf("explicit opt-in plan does not select %s", label)
			}
		}
		for _, variant := range []string{"private", "private-route", "break-glass-ha", "custom-config"} {
			var steps []TestGroup
			for _, lane := range plan.TestMatrix.Sequential {
				if len(lane.Steps) > 0 && lane.Steps[0].Variant == variant {
					steps = lane.Steps
				}
			}
			if len(steps) != 4 {
				t.Fatalf("%s must have common checks before and after the feature mutation", variant)
			}
			for _, step := range []TestGroup{steps[1], steps[len(steps)-1]} {
				filter, err := types.ParseLabelFilter(step.LabelFilter)
				if err != nil {
					t.Fatal(err)
				}
				for _, label := range []string{"hosted-cluster-health-management", "control-plane-workloads", "routes", "aws-control-plane-customization"} {
					if variant == "custom-config" && label == "routes" {
						if !filter([]string{"hosted-cluster-compliance", label}) {
							t.Fatalf("%s omits route validation", step.Name)
						}
						continue
					}
					if variant == "custom-config" && label == "hosted-cluster-health-management" {
						if !filter([]string{"hosted-cluster-health", label}) {
							t.Fatalf("%s omits %s", step.Name, label)
						}
						continue
					}
					if !filter([]string{label}) {
						t.Fatalf("%s omits inherited %s coverage", step.Name, label)
					}
				}
				if variant != "custom-config" && filter([]string{"hosted-cluster-health"}) {
					t.Fatalf("%s selects guest-dependent health on an inaccessible or zero-worker fixture", step.Name)
				}
			}
		}
	})
}

func TestAWSPlatformConfigCPOOverridePlan(t *testing.T) {
	platform := &AWSPlatformConfig{}
	plan, err := ResolveTestPlan(filepath.Join("..", "..", "..", "..", "contrib", "aws-e2e-v2-cpo-overrides.yaml"), platform)
	if err != nil {
		t.Fatal(err)
	}
	specs := platform.ClusterSpecs("unrelated-latest", "unrelated-previous")
	if err := plan.Validate(specs); err != nil {
		t.Fatal(err)
	}
	selected := plan.FilterClusterSpecs(specs)
	if len(selected) != 2 {
		t.Fatalf("expected exactly two override fixtures, got %d", len(selected))
	}
	latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
	expected := map[string]string{"cpo-override-latest": latest, "cpo-override-previous": previous}
	for _, spec := range selected {
		if spec.ReleaseImage == "" || spec.ReleaseImage != expected[spec.Variant] || spec.InitialNodePoolReplicas == nil || *spec.InitialNodePoolReplicas != 1 {
			t.Fatalf("override fixture did not use the production pair and ready-worker input: %#v", spec)
		}
	}
	if len(plan.TestMatrix.Parallel) != 0 || len(plan.TestMatrix.Sequential) != 2 {
		t.Fatal("CPO plan must compose two independent sequential fixture lanes")
	}
	for _, lane := range plan.TestMatrix.Sequential {
		if len(lane.Steps) != 4 {
			t.Fatal("CPO lane must retain initial, before, original required assertion and after")
		}
		group := lane.Steps[2]
		filter, err := types.ParseLabelFilter(group.LabelFilter)
		if err != nil {
			t.Fatal(err)
		}
		if group.Name != lane.Name || group.Variant != lane.Name || group.LabelFilter != "hosted-cluster-cpo-required" || !filter([]string{"hosted-cluster-cpo-required"}) || filter([]string{"hosted-cluster-cpo"}) || group.Skip != "" {
			t.Fatal("dedicated plan must select required coverage without a skip or optional default spec")
		}
	}
	defaults := platform.DefaultTestPlan()
	for _, variant := range defaults.TestMatrix.Variants() {
		if _, exists := expected[variant]; exists {
			t.Fatal("CPO opt-in fixture leaked into the default plan")
		}
	}
}

func TestAWSPlatformConfigPostAvailable(t *testing.T) {
	t.Run("When Cilium is not selected, it should leave the client unused", func(t *testing.T) {
		if err := (&AWSPlatformConfig{}).PostAvailable(t.Context(), nil, "clusters", nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("When the Cilium cluster is missing, it should report the lookup failure", func(t *testing.T) {
		client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		if err := (&AWSPlatformConfig{}).PostAvailable(t.Context(), client, "clusters", map[string]string{"cilium": "missing"}); err == nil {
			t.Fatal("expected lookup failure")
		}
	})
	t.Run("When the Cilium cluster has no fixed workers, it should reject the fixture before installing", func(t *testing.T) {
		cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cilium", Namespace: "clusters"}}
		client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(cluster).Build()
		if err := (&AWSPlatformConfig{}).PostAvailable(t.Context(), client, "clusters", map[string]string{"cilium": cluster.Name}); err == nil {
			t.Fatal("expected worker prerequisite failure")
		}
	})
}

func TestAWSPlatformConfigPostVersionRollout(t *testing.T) {
	t.Run("When both upgrade fixtures are selected, it should disable PKI only after their initial rollout", func(t *testing.T) {
		clusters := map[string]string{"upgrade-pki-disabled": "upgrade", "karpenter-upgrade-pki-disabled": "autonode"}
		client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(
			&hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "upgrade", Namespace: "clusters"}},
			&hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "autonode", Namespace: "clusters"}},
		).Build()
		if err := (&AWSPlatformConfig{}).PostVersionRollout(t.Context(), client, "clusters", clusters); err != nil {
			t.Fatal(err)
		}
		for _, name := range clusters {
			cluster := &hyperv1.HostedCluster{}
			if err := client.Get(t.Context(), crclient.ObjectKey{Name: name, Namespace: "clusters"}, cluster); err != nil {
				t.Fatal(err)
			}
			if cluster.Annotations[hyperv1.DisablePKIReconciliationAnnotation] != "true" {
				t.Fatalf("PKI reconciliation was not disabled for %s", name)
			}
		}
	})
	t.Run("When neither day-two fixture is selected, it should leave the client unused", func(t *testing.T) {
		if err := (&AWSPlatformConfig{}).PostVersionRollout(t.Context(), nil, "clusters", nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("When the PKI-disabled fixture is missing, it should return the lookup error", func(t *testing.T) {
		client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		if err := (&AWSPlatformConfig{}).PostVersionRollout(t.Context(), client, "clusters", map[string]string{"upgrade-pki-disabled": "missing"}); err == nil {
			t.Fatal("expected lookup error")
		}
	})
	for _, annotations := range []map[string]string{nil, {"hypershift.openshift.io/test-label": "preserve"}} {
		name := "When annotations are absent, it should disable PKI only after initial rollout"
		if annotations != nil {
			name = "When annotations exist, it should retain them while disabling PKI after initial rollout"
		}
		t.Run(name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "upgrade", Namespace: "clusters", Annotations: annotations}}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(cluster).Build()
			if err := (&AWSPlatformConfig{}).PostVersionRollout(t.Context(), client, "clusters", map[string]string{"upgrade-pki-disabled": cluster.Name}); err != nil {
				t.Fatal(err)
			}
			updated := &hyperv1.HostedCluster{}
			if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(cluster), updated); err != nil {
				t.Fatal(err)
			}
			assertions := NewWithT(t)
			assertions.Expect(updated.Annotations).To(HaveKeyWithValue(hyperv1.DisablePKIReconciliationAnnotation, "true"))
			if annotations != nil {
				assertions.Expect(updated.Annotations).To(HaveKeyWithValue("hypershift.openshift.io/test-label", "preserve"))
			}
			assertions.Expect(updated.Spec).To(Equal(cluster.Spec))
		})
	}
}

func TestAWSPlatformConfigPostCreate(t *testing.T) {
	t.Run("When custom-config is not selected, it should not require a client or mutate fixtures", func(t *testing.T) {
		if err := (&AWSPlatformConfig{}).PostCreate(t.Context(), nil, "clusters", map[string]string{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("When AWS fixtures are selected, it should leave initial configuration to the pre-apply consumer", func(t *testing.T) {
		if err := (&AWSPlatformConfig{}).PostCreate(t.Context(), nil, "clusters", map[string]string{"public": "public", "custom-config": "custom"}); err != nil {
			t.Fatal(err)
		}
	})
}
