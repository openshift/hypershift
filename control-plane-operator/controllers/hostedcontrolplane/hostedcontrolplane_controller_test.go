package hostedcontrolplane

import (
	"context"
	"testing"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/upsert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileKubeadminPassword(t *testing.T) {
	type args struct {
		ctx                 context.Context
		hcp                 *hyperv1.HostedControlPlane
		explicitOauthConfig bool
	}
	tests := []struct {
		name                 string
		args                 args
		expectedOutputSecret *corev1.Secret
	}{
		{
			name: "Oauth config specified results in no kubeadmin secret",
			args: args{
				ctx: context.TODO(),
				hcp: &hyperv1.HostedControlPlane{
					TypeMeta: metav1.TypeMeta{
						Kind:       "HostedControlPlane",
						APIVersion: hyperv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "master-cluster1",
						Name:      "cluster1",
					},
				},
				explicitOauthConfig: true,
			},
			expectedOutputSecret: nil,
		},

		{
			name: "Oauth config not specified results in default kubeadmin secret",
			args: args{
				ctx: context.TODO(),
				hcp: &hyperv1.HostedControlPlane{
					TypeMeta: metav1.TypeMeta{
						Kind:       "HostedControlPlane",
						APIVersion: hyperv1.GroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "master-cluster1",
						Name:      "cluster1",
					},
				},
				explicitOauthConfig: false,
			},
			expectedOutputSecret: common.KubeadminPasswordSecret("master-cluster1"),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().Build()
			r := &HostedControlPlaneReconciler{
				Client:                 fakeClient,
				Log:                    ctrl.LoggerFrom(context.TODO()),
				CreateOrUpdateProvider: upsert.New(false),
				EnableCIDebugOutput:    false,
			}
			if err := r.reconcileKubeadminPassword(testCase.args.ctx, testCase.args.hcp, testCase.args.explicitOauthConfig); err != nil {
				t.Error(err)
			}

			actualSecret := common.KubeadminPasswordSecret("master-cluster1")
			err := fakeClient.Get(testCase.args.ctx, client.ObjectKeyFromObject(actualSecret), actualSecret)
			if testCase.expectedOutputSecret != nil {
				if err != nil {
					t.Error("unexpected error fetching kubeAdmin Secret: %w", err)
				}
				if val, ok := actualSecret.Data["password"]; !ok || len(val) == 0 {
					t.Error("secret did not contain password key")
				}
			} else {
				if !errors.IsNotFound(err) {
					t.Error("kubeAdmin secret should not be found: %w", err)
				}
			}
		})
	}
}
