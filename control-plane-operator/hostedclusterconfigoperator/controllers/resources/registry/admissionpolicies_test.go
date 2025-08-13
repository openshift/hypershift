package registry

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/upsert"

	admissionv1beta1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	k8sfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"
)

func TestReconcileRegistryConfigValidatingAdmissionPolicies(t *testing.T) {
	ctx := t.Context()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: "bar",
		},
	}

	fakeClient := k8sfake.NewClientBuilder().
		WithScheme(api.Scheme).
		Build()

	createOrUpdate := upsert.New(true).CreateOrUpdate

	err := ReconcileRegistryConfigValidatingAdmissionPolicies(ctx, hcp, fakeClient, createOrUpdate)
	require.NoError(t, err, "expected no error during reconciliation")

	// validate ValidatingAdmissionPolicy creation
	vap := &admissionv1beta1.ValidatingAdmissionPolicy{}
	vapName := AdmissionPolicyNameManagementState
	err = fakeClient.Get(ctx, client.ObjectKey{Name: vapName}, vap)
	require.NoError(t, err, "expected ValidatingAdmissionPolicy to be created")
	require.Len(t, vap.Spec.Validations, 1)
	require.Contains(t, vap.Spec.Validations[0].Expression, "managementState != 'Removed'")

	// validate ValidatingAdmissionPolicyBinding
	vapb := &admissionv1beta1.ValidatingAdmissionPolicyBinding{}
	err = fakeClient.Get(ctx, client.ObjectKey{Name: vapName + "-binding"}, vapb)
	require.NoError(t, err, "expected ValidatingAdmissionPolicyBinding to be created")
	require.Equal(t, vapb.Spec.PolicyName, vapName)
}
