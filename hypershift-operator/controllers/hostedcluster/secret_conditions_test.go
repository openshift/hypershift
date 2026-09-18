package hostedcluster

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcilePullSecretSync_Conditions(t *testing.T) {
	tests := []struct {
		name            string
		hcluster        *hyperv1.HostedCluster
		existingSecret  *corev1.Secret
		failGetSecret   bool
		expectError     bool
		expectCondition metav1.ConditionStatus
		expectReason    string
	}{
		{
			name: "When pull secret exists with .dockerconfigjson, condition is True",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
				},
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters"},
				Data: map[string][]byte{
					".dockerconfigjson": []byte(`{"auths":{}}`),
				},
			},
			expectCondition: metav1.ConditionTrue,
			expectReason:    hyperv1.AsExpectedReason,
		},
		{
			name: "When pull secret is missing, condition is False with SecretNotFound",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
				},
			},
			expectError:     true,
			expectCondition: metav1.ConditionFalse,
			expectReason:    hyperv1.SecretNotFoundReason,
		},
		{
			name: "When pull secret exists without .dockerconfigjson, condition is False",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
				},
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters"},
				Data:       map[string][]byte{"wrong-key": []byte("data")},
			},
			expectError:     true,
			expectCondition: metav1.ConditionFalse,
			expectReason:    hyperv1.PullSecretInvalidReason,
		},
		{
			name: "When Get fails with a transient error, no condition is set",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
				},
			},
			failGetSecret: true,
			expectError:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := []client.Object{tc.hcluster}
			if tc.existingSecret != nil {
				objs = append(objs, tc.existingSecret)
			}

			interceptFuncs := interceptor.Funcs{}
			if tc.failGetSecret {
				interceptFuncs.Get = func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*corev1.Secret); ok {
						return fmt.Errorf("transient API error")
					}
					return c.Get(ctx, key, obj, opts...)
				}
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				WithStatusSubresource(tc.hcluster).
				WithInterceptorFuncs(interceptFuncs).
				Build()

			r := &HostedClusterReconciler{Client: fakeClient}

			err := r.reconcilePullSecretSync(context.Background(), tc.hcluster, upsert.New(false).CreateOrUpdate, "test-cp-ns")
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			// Re-fetch the HostedCluster to check persisted condition.
			persisted := &hyperv1.HostedCluster{}
			g.Expect(fakeClient.Get(context.Background(), client.ObjectKeyFromObject(tc.hcluster), persisted)).To(Succeed())

			condition := meta.FindStatusCondition(persisted.Status.Conditions, string(hyperv1.PullSecretSynced))
			if tc.expectCondition == "" {
				g.Expect(condition).To(BeNil(), "condition should be absent")
			} else {
				g.Expect(condition).NotTo(BeNil(), "condition should be present")
				g.Expect(condition.Status).To(Equal(tc.expectCondition))
				g.Expect(condition.Reason).To(Equal(tc.expectReason))
			}
		})
	}
}

func TestReconcileSSHKeySync_Conditions(t *testing.T) {
	tests := []struct {
		name            string
		hcluster        *hyperv1.HostedCluster
		existingSecret  *corev1.Secret
		expectError     bool
		expectCondition metav1.ConditionStatus
		expectReason    string
	}{
		{
			name: "When sshKey is not set, condition is absent",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					SSHKey: corev1.LocalObjectReference{},
				},
			},
		},
		{
			name: "When sshKey secret exists with id_rsa.pub, condition is True",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					SSHKey: corev1.LocalObjectReference{Name: "ssh-key"},
				},
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "clusters"},
				Data:       map[string][]byte{"id_rsa.pub": []byte("ssh-rsa AAAA...")},
			},
			expectCondition: metav1.ConditionTrue,
			expectReason:    hyperv1.AsExpectedReason,
		},
		{
			name: "When sshKey secret is missing, condition is False with SecretNotFound",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					SSHKey: corev1.LocalObjectReference{Name: "ssh-key"},
				},
			},
			expectError:     true,
			expectCondition: metav1.ConditionFalse,
			expectReason:    hyperv1.SecretNotFoundReason,
		},
		{
			name: "When sshKey secret exists without id_rsa.pub, condition is False",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					SSHKey: corev1.LocalObjectReference{Name: "ssh-key"},
				},
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "clusters"},
				Data:       map[string][]byte{"wrong-key": []byte("data")},
			},
			expectError:     true,
			expectCondition: metav1.ConditionFalse,
			expectReason:    hyperv1.SSHKeyInvalidReason,
		},
		{
			name: "When sshKey previously set then cleared, stale condition is removed",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					SSHKey: corev1.LocalObjectReference{},
				},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.SSHKeySynced),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.AsExpectedReason,
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := []client.Object{tc.hcluster}
			if tc.existingSecret != nil {
				objs = append(objs, tc.existingSecret)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				WithStatusSubresource(tc.hcluster).
				Build()

			r := &HostedClusterReconciler{Client: fakeClient}

			err := r.reconcileSSHKeySync(context.Background(), tc.hcluster, upsert.New(false).CreateOrUpdate, "test-cp-ns")
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			persisted := &hyperv1.HostedCluster{}
			g.Expect(fakeClient.Get(context.Background(), client.ObjectKeyFromObject(tc.hcluster), persisted)).To(Succeed())

			condition := meta.FindStatusCondition(persisted.Status.Conditions, string(hyperv1.SSHKeySynced))
			if tc.expectCondition == "" {
				g.Expect(condition).To(BeNil(), "condition should be absent")
			} else {
				g.Expect(condition).NotTo(BeNil(), "condition should be present")
				g.Expect(condition.Status).To(Equal(tc.expectCondition))
				g.Expect(condition.Reason).To(Equal(tc.expectReason))
			}
		})
	}
}
