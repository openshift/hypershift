package kubevirt

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
)

func TestReconcileCAPIInfraCR(t *testing.T) {
	kubevirt := Kubevirt{}
	fakeClient := fake.NewClientBuilder().Build()
	testNamespace := "testNamespace"
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			InfraID: "testInfraID",
		},
	}
	expectedResult := &capikubevirt.KubevirtCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      hcluster.Spec.InfraID,
			Annotations: map[string]string{
				hostedClusterAnnotation:    client.ObjectKeyFromObject(hcluster).String(),
				capiv1.ManagedByAnnotation: "external",
			},
		},
		Status: capikubevirt.KubevirtClusterStatus{
			Ready: true,
		},
	}
	testCases := []struct {
		name        string
		expectedErr error
	}{
		{
			name: "Happy flow",
		},
		{
			name:        "Expected err from func",
			expectedErr: errors.New("test error"),
		},
	}

	apiendpoint := hyperv1.APIEndpoint{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fnCallsCount := 0
			createOrUpdateFN := func(
				ctx context.Context,
				c client.Client,
				obj client.Object,
				f controllerutil.MutateFn,
			) (controllerutil.OperationResult, error) {
				fnCallsCount++
				err := f()
				if err != nil {
					return "", err
				}
				return "", tc.expectedErr
			}
			result, err := kubevirt.ReconcileCAPIInfraCR(
				context.Background(),
				fakeClient,
				createOrUpdateFN,
				hcluster,
				testNamespace,
				apiendpoint)
			if fnCallsCount != 1 {
				t.Fatalf("Expected the provided function to be called once")
			}
			if tc.expectedErr != nil {
				if err != tc.expectedErr {
					t.Fatalf("ReconcileCAPIInfraCR: Expected to fail. gotErr: %v, expectedErr: %v", err, tc.expectedErr)
				}
			} else if err != nil {
				t.Fatalf("ReconcileCAPIInfraCR: Got unexpected error: %v (expectedErr: %v)", err, tc.expectedErr)
			} else {
				if !equality.Semantic.DeepEqual(expectedResult, result) {
					t.Error(cmp.Diff(expectedResult, result))
				}

			}
		})
	}
}

func TestReconcileCredentials(t *testing.T) {
	kubevirt := Kubevirt{}
	fakeClient := fake.NewClientBuilder().Build()
	fnCallsCount := 0
	createOrUpdateFN := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		fnCallsCount++
		return "", nil
	}
	hcluster := &hyperv1.HostedCluster{}
	err := kubevirt.ReconcileCredentials(context.Background(), fakeClient, createOrUpdateFN, hcluster, "controlPlanNamespace")
	if err != nil {
		t.Fatalf("ReconcileCredentials failed: %v", err)
	}
	if fnCallsCount > 0 {
		t.Fatalf("create or update func should not be called")
	}
}

func TestReconcileSecretEncryption(t *testing.T) {
	kubevirt := Kubevirt{}
	fakeClient := fake.NewClientBuilder().Build()
	fnCallsCount := 0
	createOrUpdateFN := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		fnCallsCount++
		return "", nil
	}
	hcluster := &hyperv1.HostedCluster{}
	err := kubevirt.ReconcileSecretEncryption(context.Background(), fakeClient, createOrUpdateFN, hcluster, "controlPlanNamespace")
	if err != nil {
		t.Fatalf("ReconcileSecretEncryption failed: %v", err)
	}
	if fnCallsCount > 0 {
		t.Fatalf("create or update func should not be called")
	}
}
