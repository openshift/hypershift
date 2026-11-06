package nodepool

import (
	"encoding/base64"
	"reflect"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

// Exercise the production token state machine, seeding and CAPI propagation in
// sequence. Legacy rollout annotations deliberately remain present: they must
// never be mistaken for a content-aware baseline.
func testSeedRolloutAnnotationTrustBundleMigration(t *testing.T) {
	for _, upgradeType := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
		for _, inFlight := range []bool{false, true} {
			for _, field := range []string{"additional", "proxy"} {
				name := "When " + string(upgradeType) + " migrates with " + field
				if inFlight {
					name += " and an active rollout"
				}
				t.Run(name+", it should retain bootstrap until the next content edit", func(t *testing.T) {
					cg := migrationConfigFixture()
					np := cg.nodePool
					np.Spec.Management.UpgradeType = upgradeType
					np.Status.Version = cg.Version()
					legacy := cg.hashAtVersion(ConfigHashVersionV1)
					completed := legacy
					if inFlight {
						completed = "previous-completed"
					}
					np.Annotations = map[string]string{
						nodePoolAnnotationCurrentConfigVersion:                    completed,
						"hypershift.openshift.io/nodePoolCurrentRolloutConfig":    "legacy-name-based-baseline",
						"hypershift.openshift.io/nodePoolInProgressRolloutConfig": "legacy-request",
					}
					// Content changed before the operator upgrade, without changing the reference.
					cg.additionalTrustBundleHash = util.HashSimple("pre-upgrade-edit")
					cg.renderedTrustBundleHash = util.HashSimple("pre-upgrade-editproxy")
					cg.Client = fake.NewClientBuilder().WithScheme(api.Scheme).Build()
					token := &Token{ConfigGenerator: cg, CreateOrUpdateProvider: upsert.New(false),
						cpoCapabilities:           &CPOCapabilities{DecompressAndDecodeConfig: true, RenderedTrustBundleValidation: true},
						additionalTrustBundleHash: []byte(cg.additionalTrustBundleHash), proxyTrustedCAHash: []byte(cg.proxyTrustedCAHash),
						userData:              &userData{ignitionServerEndpoint: "ignition.example.com", proxy: &configv1.Proxy{}},
						deployedBootstrapHash: legacy, deployedVersion: cg.Version()}
					md := &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Name: np.Name, Namespace: cg.controlplaneNamespace}}
					md.Spec.Template.Spec.Version = cg.Version()
					md.Spec.Template.Spec.InfrastructureRef.Name = "template"
					md.Spec.Template.Spec.Bootstrap.DataSecretName = ptr.To("user-data-workers-" + legacy)
					ms := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}, Spec: capiv1.MachineSetSpec{Template: md.Spec.Template}}
					before := md.Spec.Template.DeepCopy()
					capi := &CAPI{Token: token}
					propagate := func() bool {
						tmpl := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "template"}}
						if upgradeType == hyperv1.UpgradeTypeInPlace {
							return capi.propagateVersionAndTemplateToMachineSet(logr.Discard(), ms, tmpl)
						}
						return capi.propagateVersionAndTemplate(logr.Discard(), md, tmpl)
					}
					if err := token.Reconcile(t.Context()); err != nil {
						t.Fatal(err)
					}
					seedRolloutAnnotation(token)
					if propagate() {
						t.Fatal("formula migration changed the workload spec")
					}
					if !reflect.DeepEqual(*before, md.Spec.Template) || !reflect.DeepEqual(*before, ms.Spec.Template) {
						t.Fatal("migration changed a machine template")
					}
					if token.EffectiveHash() != legacy || np.Annotations[nodePoolAnnotationCurrentConfigVersion] != completed {
						t.Fatal("migration rewrote a bootstrap identity")
					}
					if np.Annotations[nodePoolAnnotationConfigHashVersion] != ConfigHashVersionV2 {
						t.Fatal("missing version marker")
					}
					baseline := np.Annotations[nodePoolAnnotationCurrentRolloutConfig]
					// Simulate reloading the persisted workload request and a token rotation.
					token.deployedRolloutConfig = token.RolloutHash()
					secret, userData := token.secretsForHash(legacy)
					if err := cg.Get(t.Context(), client.ObjectKeyFromObject(secret), secret); err != nil {
						t.Fatal(err)
					}
					secret.Data[TokenSecretTokenKey] = []byte("rotated-token")
					if err := cg.Update(t.Context(), secret); err != nil {
						t.Fatal(err)
					}
					cg.mcoRawConfig += "management-only image change"
					if err := token.Reconcile(t.Context()); err != nil {
						t.Fatal(err)
					}
					seedRolloutAnnotation(token)
					if propagate() || token.EffectiveHash() != legacy {
						t.Fatal("management-only change rolled the workload")
					}
					if err := cg.Get(t.Context(), client.ObjectKeyFromObject(userData), userData); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(userData.Data["value"]), base64.StdEncoding.EncodeToString([]byte("rotated-token"))) {
						t.Fatal("retained user data did not follow token rotation")
					}
					if field == "additional" {
						cg.additionalTrustBundleHash = util.HashSimple("post-migration-edit")
					} else {
						cg.proxyTrustedCAHash = util.HashSimple("post-migration-edit")
					}
					if err := token.Reconcile(t.Context()); err != nil {
						t.Fatal(err)
					}
					seedRolloutAnnotation(token)
					if !propagate() || token.EffectiveHash() == legacy {
						t.Fatal("subsequent content change did not propagate")
					}
					if np.Annotations[nodePoolAnnotationCurrentRolloutConfig] != baseline {
						t.Fatal("unfinished rollout advanced the completed baseline")
					}
				})
			}
		}
	}
}
