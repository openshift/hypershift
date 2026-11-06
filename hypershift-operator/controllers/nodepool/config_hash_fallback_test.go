package nodepool

import (
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestConfigHashVersion(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		legacy        bool
	}{
		{name: "When the CPO lacks validation, it should retain name-based payload and rollout hashes", version: ConfigHashVersionV1, legacy: true},
		{name: "When the CPO supports validation, it should use content-based payload and rollout hashes", version: ConfigHashVersionV2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cg := migrationConfigFixture()
			cg.useLegacyTrustBundleHash = tc.legacy
			if cg.ConfigHashVersion() != tc.version || cg.Hash() != cg.hashAtVersion(tc.version) || cg.HashWithoutVersion() != cg.hashWithoutVersionAtVersion(tc.version) {
				t.Fatal("payload hashes do not use the selected CPO's formula")
			}
			before := []string{cg.Hash(), cg.HashWithoutVersion(), cg.RolloutHash(), cg.RolloutHashWithoutVersion()}
			cg.additionalTrustBundleHash = util.HashSimple("changed additional")
			cg.proxyTrustedCAHash = util.HashSimple("changed proxy")
			after := []string{cg.Hash(), cg.HashWithoutVersion(), cg.RolloutHash(), cg.RolloutHashWithoutVersion()}
			for i := range before {
				if (before[i] == after[i]) != tc.legacy {
					t.Fatalf("hash %d did not follow the selected content-change policy", i)
				}
			}
			cg.hostedCluster.Spec.AdditionalTrustBundle.Name = "renamed-ca"
			if (cg.RolloutHash() != after[2]) != tc.legacy {
				t.Fatal("reference rename did not follow the selected rollout policy")
			}
		})
	}
}

func TestRolloutConfigAnnotation(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "When the CPO supports validation, it should keep a separate v2 baseline"
		want := nodePoolAnnotationCurrentRolloutConfig
		if legacy {
			name = "When the CPO lacks validation, it should use the legacy rollout baseline"
			want = "hypershift.openshift.io/nodePoolCurrentRolloutConfig"
		}
		t.Run(name, func(t *testing.T) {
			cg := &ConfigGenerator{useLegacyTrustBundleHash: legacy}
			if cg.RolloutConfigAnnotation() != want {
				t.Fatalf("baseline key = %q, want %q", cg.RolloutConfigAnnotation(), want)
			}
		})
	}
}

func testSeedRolloutAnnotationCapabilityUpgrade(t *testing.T) {
	for _, upgradeType := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
		t.Run("When "+string(upgradeType)+" gains validation capability, it should seed v2 without a rollout and roll on the next CA edit", func(t *testing.T) {
			cg := migrationConfigFixture()
			cg.useLegacyTrustBundleHash = true
			cg.nodePool.Spec.Management.UpgradeType = upgradeType
			cg.nodePool.Status.Version = cg.Version()
			cg.Client = fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			token := &Token{ConfigGenerator: cg, CreateOrUpdateProvider: upsert.New(false),
				cpoCapabilities:           &CPOCapabilities{DecompressAndDecodeConfig: true},
				additionalTrustBundleHash: []byte(cg.additionalTrustBundleHash), proxyTrustedCAHash: []byte(cg.proxyTrustedCAHash),
				userData: &userData{ignitionServerEndpoint: "ignition.example.com", proxy: &configv1.Proxy{}}}
			if err := token.Reconcile(t.Context()); err != nil {
				t.Fatalf("older CPO failed token reconciliation: %v", err)
			}
			seedRolloutAnnotation(token)
			legacyHash := token.EffectiveHash()
			cg.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = legacyHash
			if cg.nodePool.Annotations[nodePoolAnnotationConfigHashVersion] != ConfigHashVersionV1 {
				t.Fatal("older CPO did not record v1")
			}
			md := &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Name: "workers"}}
			md.Spec.Template.Spec.Version = cg.Version()
			md.Spec.Template.Spec.InfrastructureRef.Name = "template"
			md.Spec.Template.Spec.Bootstrap.DataSecretName = ptr.To("user-data-workers-" + legacyHash)
			ms := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}, Spec: capiv1.MachineSetSpec{Template: md.Spec.Template}}
			capi := &CAPI{Token: token}
			propagate := func() bool {
				template := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "template"}}
				if upgradeType == hyperv1.UpgradeTypeInPlace {
					return capi.propagateVersionAndTemplateToMachineSet(logr.Discard(), ms, template)
				}
				return capi.propagateVersionAndTemplate(logr.Discard(), md, template)
			}
			if propagate() {
				t.Fatal("steady legacy workload changed")
			}
			before := md.Spec.Template.DeepCopy()
			// The bundle changed before the CPO upgrade; enabling the new formula
			// must adopt its current content rather than roll existing nodes.
			cg.additionalTrustBundleHash = util.HashSimple("pre-upgrade content")
			token.additionalTrustBundleHash = []byte(cg.additionalTrustBundleHash)
			cg.renderedTrustBundleHash = util.HashSimple("pre-upgrade contentproxy")
			cg.useLegacyTrustBundleHash = false
			token.cpoCapabilities.RenderedTrustBundleValidation = true
			token.SetDeployedBootstrapHash(legacyHash)
			token.deployedVersion = cg.Version()
			// The v1 target annotation is deliberately not read as a v2 target.
			if err := token.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			seedRolloutAnnotation(token)
			if propagate() || token.EffectiveHash() != legacyHash || !reflect.DeepEqual(*before, md.Spec.Template) || !reflect.DeepEqual(*before, ms.Spec.Template) {
				t.Fatal("capability upgrade replaced the bootstrap identity or workload template")
			}
			if cg.nodePool.Annotations[nodePoolAnnotationConfigHashVersion] != ConfigHashVersionV2 {
				t.Fatal("new CPO did not record v2")
			}
			token.deployedRolloutConfig = token.RolloutHash()
			cg.additionalTrustBundleHash = util.HashSimple("post-upgrade content")
			token.additionalTrustBundleHash = []byte(cg.additionalTrustBundleHash)
			cg.renderedTrustBundleHash = util.HashSimple("post-upgrade contentproxy")
			if err := token.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !propagate() || token.EffectiveHash() == legacyHash {
				t.Fatal("content edit after the upgrade did not trigger a rollout")
			}
		})
	}
}
