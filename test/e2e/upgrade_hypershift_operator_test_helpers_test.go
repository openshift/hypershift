//go:build e2e

package e2e

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCaptureBootstrapChain(t *testing.T) {
	for _, version := range []string{"v1beta1", "v1beta2"} {
		for _, upgradeType := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
			for _, missing := range []string{"none", "workload", "userdata", "token", "bootstrap reference"} {
				t.Run(fmt.Sprintf("When %s uses %s with %s missing, it should validate bootstrap capture/%s", upgradeType, version, missing, version), func(t *testing.T) {
					fixture := newBootstrapChainTestFixture(t, version, upgradeType)
					switch missing {
					case "workload":
						if err := fixture.client.Delete(t.Context(), fixture.workload); err != nil {
							t.Fatal(err)
						}
					case "userdata", "token":
						name := fixture.state.UserdataSecretName
						if missing == "token" {
							name = fixture.state.TokenSecretName
						}
						if err := fixture.client.Delete(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "control-plane"}}); err != nil {
							t.Fatal(err)
						}
					case "bootstrap reference":
						unstructured.RemoveNestedField(fixture.workload.Object, "spec", "template", "spec", "bootstrap", "dataSecretName")
						if err := fixture.client.Update(t.Context(), fixture.workload); err != nil {
							t.Fatal(err)
						}
					}
					states, err := captureBootstrapChain(t.Context(), fixture.client, "control-plane", map[string]*hyperv1.NodePool{"workers": fixture.nodePool}, version == "v1beta1")
					if missing != "none" {
						if err == nil {
							t.Fatal("expected incomplete bootstrap chain to fail capture")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(states) != 1 || !reflect.DeepEqual(states["workers"], fixture.state) {
						t.Fatal("capture must preserve the complete expected bootstrap chain")
					}
				})
			}
		}
	}
}

func TestVerifyBootstrapChainIntact(t *testing.T) {
	for _, version := range []string{"v1beta1", "v1beta2"} {
		for _, upgradeType := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
			for _, change := range []string{"none", "bootstrap reference", "userdata identity"} {
				t.Run(fmt.Sprintf("When %s uses %s with %s changed, it should validate bootstrap integrity", upgradeType, version, change), func(t *testing.T) {
					fixture := newBootstrapChainTestFixture(t, version, upgradeType)
					switch change {
					case "bootstrap reference":
						fixture.state.BootstrapSecretName = "unexpected-reference"
					case "userdata identity":
						fixture.state.UserdataSecretUID = "unexpected-uid"
					}
					err := verifyBootstrapChainIntact(t.Context(), fixture.client, "control-plane", fixture.state, version == "v1beta1", fixture.nodePool)
					if (err != nil) != (change != "none") {
						t.Fatalf("bootstrap integrity error = %v, expected failure = %v", err, change != "none")
					}
				})
			}
		}
	}
}

type bootstrapChainTestFixture struct {
	client   client.Client
	nodePool *hyperv1.NodePool
	workload *unstructured.Unstructured
	state    *bootstrapChainState
}

func newBootstrapChainTestFixture(t *testing.T, version string, upgradeType hyperv1.UpgradeType) bootstrapChainTestFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	addCAPI := capiv1beta2.AddToScheme
	if version == "v1beta1" {
		addCAPI = capiv1beta1.AddToScheme
	}
	if err := addCAPI(scheme); err != nil {
		t.Fatal(err)
	}
	kind := "MachineDeployment"
	if upgradeType == hyperv1.UpgradeTypeInPlace {
		kind = "MachineSet"
	}
	workload := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cluster.x-k8s.io/" + version, "kind": kind,
		"metadata": map[string]interface{}{"name": "workers", "namespace": "control-plane"},
		"spec": map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"bootstrap": map[string]interface{}{"dataSecretName": "user-data-workers-fixturehash"},
		}}},
	}}
	state := &bootstrapChainState{
		NodePoolName: "workers", NodePoolUID: "nodepool-uid", BootstrapSecretName: "user-data-workers-fixturehash",
		TokenSecretName: "token-workers-fixturehash", TokenSecretUID: "token-uid", TokenID: "fixture-token",
		GenerationTime:     time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC),
		UserdataSecretName: "user-data-workers-fixturehash", UserdataSecretUID: "userdata-uid",
	}
	userdata := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.UserdataSecretName, Namespace: "control-plane", UID: state.UserdataSecretUID}}
	token := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: state.TokenSecretName, Namespace: "control-plane", UID: state.TokenSecretUID,
			Annotations: map[string]string{tokenSecretAnnotation: "true", tokenSecretTokenGenerationTime: state.GenerationTime.Format(time.RFC3339Nano)}},
		Data: map[string][]byte{"token": []byte(state.TokenID)},
	}
	nodePool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers", UID: state.NodePoolUID}, Spec: hyperv1.NodePoolSpec{Management: hyperv1.NodePoolManagement{UpgradeType: upgradeType}}}
	return bootstrapChainTestFixture{client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(workload, userdata, token).Build(), nodePool: nodePool, workload: workload, state: state}
}
