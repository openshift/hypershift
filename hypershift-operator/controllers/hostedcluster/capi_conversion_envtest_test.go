//go:build envtest

package hostedcluster

import (
	"encoding/pem"
	"net/http/httptest"
	"testing"
	"time"

	crdassets "github.com/openshift/hypershift/cmd/install/assets/crds"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capiconversion "sigs.k8s.io/cluster-api/core/webhooks/conversion"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

func newCAPIMachineStatusTestClient(t *testing.T) client.Client {
	t.Helper()
	scheme := freshConversionScheme(t)
	registry, err := newCAPIConversionRegistry(scheme)
	if err != nil {
		t.Fatal(err)
	}
	restore := capiconversion.SwapAPIVersionGetter(capiAPIVersionGetter(scheme))
	t.Cleanup(restore)
	server := httptest.NewTLSServer(conversion.NewWebhookHandler(scheme, registry))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	crds := crdassets.CustomResourceDefinitions(capiv1beta2.GroupVersion.Version, func(_ string, crd *apiextensionsv1.CustomResourceDefinition) bool {
		return crd.Name == "machines.cluster.x-k8s.io"
	}, nil)
	if len(crds) != 1 {
		t.Fatalf("expected the installed Machine CRD, got %d objects", len(crds))
	}
	crd := crds[0].(*apiextensionsv1.CustomResourceDefinition).DeepCopy()
	url := server.URL + "/convert"
	crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
		Strategy: apiextensionsv1.WebhookConverter,
		Webhook: &apiextensionsv1.WebhookConversion{
			ConversionReviewVersions: []string{"v1"},
			ClientConfig:             &apiextensionsv1.WebhookClientConfig{URL: &url, CABundle: ca},
		},
	}
	env := &envtest.Environment{}
	config, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	// Environment.Start strips registry-based converters because the types do not implement Hub/Spoke.
	if _, err := envtest.InstallCRDs(config, envtest.CRDInstallOptions{CRDs: []*apiextensionsv1.CustomResourceDefinition{crd}}); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	installedCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := c.Get(t.Context(), client.ObjectKey{Name: crd.Name}, installedCRD); err != nil {
		t.Fatal(err)
	}
	if !equality.Semantic.DeepEqual(installedCRD.Status.StoredVersions, []string{capiv1beta2.GroupVersion.Version}) {
		t.Fatalf("Machine CRD stored versions = %v, want [v1beta2]", installedCRD.Status.StoredVersions)
	}
	if installedCRD.Spec.Conversion == nil || installedCRD.Spec.Conversion.Strategy != apiextensionsv1.WebhookConverter || installedCRD.Spec.Conversion.Webhook.ClientConfig.URL == nil || *installedCRD.Spec.Conversion.Webhook.ClientConfig.URL != url {
		t.Fatal("installed CRD does not use the production conversion webhook")
	}
	ctx := t.Context()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "clusters"}}); err != nil {
		t.Fatal(err)
	}
	return c
}

//nolint:staticcheck // Verify status fields still written by supported v1beta1 CAPI controllers.
func TestCAPIMachineStatusCompatibility(t *testing.T) {
	c := newCAPIMachineStatusTestClient(t)
	ctx := t.Context()
	for _, testCase := range []struct {
		name          string
		machineName   string
		storedPhase   string
		legacyPhase   string
		expectedPhase string
	}{
		{name: "When a legacy status patch sets an absent phase to Running, it should persist Running", machineName: "running", legacyPhase: "Running", expectedPhase: "Running"},
		{name: "When a legacy status patch advances Provisioned to Running, it should persist Running", machineName: "advance", storedPhase: "Provisioned", legacyPhase: "Running", expectedPhase: "Running"},
		{name: "When a legacy status patch leaves Updating as legacy Running, it should preserve Updating", machineName: "updating", storedPhase: "Updating", legacyPhase: "Running", expectedPhase: "Updating"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			machine := &capiv1beta2.Machine{
				ObjectMeta: metav1.ObjectMeta{Name: testCase.machineName, Namespace: "clusters"},
				Spec: capiv1beta2.MachineSpec{
					ClusterName:       "legacy-cluster",
					Bootstrap:         capiv1beta2.Bootstrap{DataSecretName: new("bootstrap-data")},
					InfrastructureRef: capiv1beta2.ContractVersionedObjectReference{APIGroup: "infrastructure.cluster.x-k8s.io", Kind: "KubevirtMachine", Name: testCase.machineName},
				},
			}
			if err := c.Create(ctx, machine); err != nil {
				t.Fatal(err)
			}
			machine.Status.Phase = testCase.storedPhase
			if err := c.Status().Update(ctx, machine); err != nil {
				t.Fatal(err)
			}
			legacy := &capiv1beta1.Machine{}
			key := client.ObjectKeyFromObject(machine)
			if err := c.Get(ctx, key, legacy); err != nil {
				t.Fatal(err)
			}
			expectedLegacyPhase := testCase.storedPhase
			if expectedLegacyPhase == "Updating" {
				expectedLegacyPhase = "Running"
			}
			if legacy.Status.Phase != expectedLegacyPhase {
				t.Fatalf("unexpected legacy phase before patch: %q", legacy.Status.Phase)
			}
			now := metav1.NewTime(time.Date(2026, 11, 5, 20, 32, 32, 0, time.UTC))
			conditionBefore := legacy.DeepCopy()
			legacy.Status.Conditions = capiv1beta1.Conditions{{Type: capiv1beta1.ReadyCondition, Status: corev1.ConditionTrue, LastTransitionTime: now}}
			legacy.Status.V1Beta2 = &capiv1beta1.MachineV1Beta2Status{Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: now}}}
			if err := c.Status().Patch(ctx, legacy, client.MergeFromWithOptions(conditionBefore, client.MergeFromWithOptimisticLock{})); err != nil {
				t.Fatal(err)
			}
			before := legacy.DeepCopy()
			legacy.Status.Phase = testCase.legacyPhase
			legacy.Status.BootstrapReady = true
			legacy.Status.InfrastructureReady = true
			legacy.Status.NodeRef = &corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: testCase.machineName}
			expectedLegacyStatus := *legacy.Status.DeepCopy()
			if err := c.Status().Patch(ctx, legacy, client.MergeFrom(before)); err != nil {
				t.Fatal(err)
			}
			stored := &capiv1beta2.Machine{}
			if err := c.Get(ctx, key, stored); err != nil {
				t.Fatal(err)
			}
			if stored.Status.Phase != testCase.expectedPhase {
				t.Errorf("v1beta2 readback phase = %q, want %q", stored.Status.Phase, testCase.expectedPhase)
			}
			if stored.Status.Initialization.BootstrapDataSecretCreated == nil || !*stored.Status.Initialization.BootstrapDataSecretCreated || stored.Status.Initialization.InfrastructureProvisioned == nil || !*stored.Status.Initialization.InfrastructureProvisioned {
				t.Error("legacy readiness writes did not persist in hub initialization")
			}
			if !equality.Semantic.DeepEqual(stored.Status.Conditions, legacy.Status.V1Beta2.Conditions) || stored.Status.Deprecated == nil || stored.Status.Deprecated.V1Beta1 == nil || len(stored.Status.Deprecated.V1Beta1.Conditions) != 1 || stored.Status.Deprecated.V1Beta1.Conditions[0].Status != corev1.ConditionTrue {
				t.Error("separate condition patch did not survive subsequent status patch")
			}
			readback := &capiv1beta1.Machine{}
			if err := c.Get(ctx, key, readback); err != nil {
				t.Fatal(err)
			}
			if !equality.Semantic.DeepEqual(readback.Status, expectedLegacyStatus) {
				t.Errorf("legacy status patch did not survive readback: got %+v, want %+v", readback.Status, expectedLegacyStatus)
			}
		})
	}
}
