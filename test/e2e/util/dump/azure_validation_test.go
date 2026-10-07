package dump

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVerifyAzureMachineDiagnostics(t *testing.T) {
	for _, tt := range []struct {
		name                       string
		withMachine, withArtifacts bool
		want                       string
	}{
		{"When no workers exist, it should reject a vacuous diagnostics pass", false, true, "expected at least one Azure worker"},
		{"When an artifact directory is not provided, it should reject validation", true, false, "artifact directory is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := capiazure.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := hostedClusterForDiagnostics(hyperv1.AzurePlatform)
			hc.Spec.InfraID = "hc-infra"
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.withMachine {
				builder = builder.WithObjects(&capiazure.AzureMachine{ObjectMeta: metav1.ObjectMeta{Name: "worker-0", Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), Labels: map[string]string{clusterv1.ClusterNameLabel: hc.Spec.InfraID}}})
			}
			var artifacts string
			if tt.withArtifacts {
				artifacts = t.TempDir()
			}
			err := VerifyAzureMachineDiagnostics(t.Context(), builder.Build(), hc, "", artifacts, "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
		})
	}
}
