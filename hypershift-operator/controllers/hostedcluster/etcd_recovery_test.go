package hostedcluster

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEtcdRecoveryJobStatus(t *testing.T) {
	tests := []struct {
		name               string
		job                *batchv1.Job
		jobExists          bool
		expectedExists     bool
		expectedFinished   bool
		expectedSuccessful bool
		expectError        bool
	}{
		{
			name:           "When job does not exist, it should return exists=false",
			jobExists:      false,
			expectedExists: false,
		},
		{
			name:      "When job exists with no conditions, it should return exists=true, finished=false",
			jobExists: true,
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-recovery",
					Namespace: "clusters-test",
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{{Name: "etcd-recovery", Image: "test:latest"}},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
				Status: batchv1.JobStatus{Active: 1},
			},
			expectedExists:   true,
			expectedFinished: false,
		},
		{
			name:      "When job completed successfully, it should return finished=true, successful=true",
			jobExists: true,
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-recovery",
					Namespace: "clusters-test",
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{{Name: "etcd-recovery", Image: "test:latest"}},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobComplete,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			expectedExists:     true,
			expectedFinished:   true,
			expectedSuccessful: true,
		},
		{
			name:      "When job failed, it should return finished=true, successful=false",
			jobExists: true,
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-recovery",
					Namespace: "clusters-test",
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{{Name: "etcd-recovery", Image: "test:latest"}},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobFailed,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			expectedExists:     true,
			expectedFinished:   true,
			expectedSuccessful: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var objects []crclient.Object
			if tt.jobExists && tt.job != nil {
				objects = append(objects, tt.job)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objects...).
				Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
			}

			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-recovery",
					Namespace: "clusters-test",
				},
			}

			status, err := r.etcdRecoveryJobStatus(t.Context(), job)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(status).ToNot(BeNil())
				g.Expect(status.exists).To(Equal(tt.expectedExists))
				g.Expect(status.finished).To(Equal(tt.expectedFinished))
				g.Expect(status.successful).To(Equal(tt.expectedSuccessful))
			}
		})
	}
}

func TestReconcileETCDMemberRecovery_ClearStaleCondition(t *testing.T) {
	tests := []struct {
		name              string
		existingCondition *metav1.Condition
		fullyAvailable    bool
		expectCleared     bool
		expectRequeue     bool
	}{
		{
			name: "When etcd is healthy and stale EtcdRecoveryJobFailed condition exists, it should clear to AsExpected",
			existingCondition: &metav1.Condition{
				Type:               string(hyperv1.EtcdRecoveryActive),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.EtcdRecoveryJobFailedReason,
				Message:            "Error in Etcd Recovery job: the Etcd cluster requires manual intervention.",
				LastTransitionTime: metav1.Now(),
			},
			fullyAvailable: true,
			expectCleared:  true,
			expectRequeue:  false,
		},
		{
			name:              "When etcd is healthy and no stale condition exists, it should return without changes",
			existingCondition: nil,
			fullyAvailable:    true,
			expectCleared:     false,
			expectRequeue:     false,
		},
		{
			name: "When etcd is healthy and condition is already AsExpected, it should not update",
			existingCondition: &metav1.Condition{
				Type:               string(hyperv1.EtcdRecoveryActive),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "ETCD Recovery job succeeded.",
				LastTransitionTime: metav1.Now(),
			},
			fullyAvailable: true,
			expectCleared:  false,
			expectRequeue:  false,
		},
		{
			name: "When etcd is not fully available with stale condition, it should requeue",
			existingCondition: &metav1.Condition{
				Type:               string(hyperv1.EtcdRecoveryActive),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.EtcdRecoveryJobFailedReason,
				Message:            "Error in Etcd Recovery job: the Etcd cluster requires manual intervention.",
				LastTransitionTime: metav1.Now(),
			},
			fullyAvailable: false,
			expectCleared:  false,
			expectRequeue:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			}
			if tt.existingCondition != nil {
				hcluster.Status.Conditions = append(hcluster.Status.Conditions, *tt.existingCondition)
			}

			hcpNS := "clusters-test-cluster"

			var readyReplicas, availableReplicas int32
			if tt.fullyAvailable {
				readyReplicas = 3
				availableReplicas = 3
			}

			etcdSts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: hcpNS,
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas:     readyReplicas,
					AvailableReplicas: availableReplicas,
				},
			}

			pods := make([]crclient.Object, 3)
			for i := range 3 {
				pods[i] = &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-" + string(rune('0'+i)),
						Namespace: hcpNS,
						Labels:    map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd",
								State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
							},
						},
					},
				}
			}

			objects := []crclient.Object{hcluster, etcdSts}
			objects = append(objects, pods...)

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objects...).
				WithStatusSubresource(&hyperv1.HostedCluster{}).
				Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
				now:    metav1.Now,
			}

			requeueAfter, err := r.reconcileETCDMemberRecovery(t.Context(), hcluster, nil)
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectRequeue {
				g.Expect(requeueAfter).ToNot(BeNil())
			} else {
				g.Expect(requeueAfter).To(BeNil())
			}

			updatedHC := &hyperv1.HostedCluster{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), updatedHC)).To(Succeed())
			cond := findCondition(updatedHC.Status.Conditions, string(hyperv1.EtcdRecoveryActive))

			if tt.expectCleared {
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Reason).To(Equal(hyperv1.AsExpectedReason))
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Message).To(ContainSubstring("healthy"))
			} else if tt.existingCondition == nil {
				g.Expect(cond).To(BeNil())
			} else {
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Reason).To(Equal(tt.existingCondition.Reason))
				g.Expect(cond.Status).To(Equal(tt.existingCondition.Status))
			}
		})
	}
}

func TestReconcileETCDMemberRecovery_ReasonCheckPreventsNoOpUpdate(t *testing.T) {
	g := NewWithT(t)

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters",
		},
		Status: hyperv1.HostedClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:               string(hyperv1.EtcdRecoveryActive),
					Status:             metav1.ConditionFalse,
					Reason:             hyperv1.EtcdRecoveryJobFailedReason,
					Message:            "Error in Etcd Recovery job: the Etcd cluster requires manual intervention.",
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}

	hcpNS := "clusters-test-cluster"

	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-recovery",
			Namespace: hcpNS,
			Labels:    map[string]string{"app": "etcd-recovery"},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "etcd-recovery", Image: "test:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
			},
		},
	}

	etcdSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: hcpNS,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(hcluster, failedJob, etcdSts).
		WithStatusSubresource(&hyperv1.HostedCluster{}).
		Build()

	r := &HostedClusterReconciler{
		Client: fakeClient,
		now:    metav1.Now,
	}

	_, err := r.reconcileETCDMemberRecovery(t.Context(), hcluster, nil)
	g.Expect(err).ToNot(HaveOccurred())

	updatedHC := &hyperv1.HostedCluster{}
	g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), updatedHC)).To(Succeed())
	cond := findCondition(updatedHC.Status.Conditions, string(hyperv1.EtcdRecoveryActive))
	g.Expect(cond).ToNot(BeNil())
	g.Expect(cond.Reason).To(Equal(hyperv1.EtcdRecoveryJobFailedReason))
}

func TestReconcileEtcdRecoveryRole(t *testing.T) {
	t.Run("When called, it should set expected RBAC rules", func(t *testing.T) {
		g := NewWithT(t)

		r := &HostedClusterReconciler{}
		role := &rbacv1.Role{}
		r.reconcileEtcdRecoveryRole(role)

		g.Expect(role.Rules).To(HaveLen(2))
		g.Expect(role.Rules[0].APIGroups).To(ConsistOf(""))
		g.Expect(role.Rules[0].Resources).To(ConsistOf("pods", "persistentvolumeclaims"))
		g.Expect(role.Rules[0].Verbs).To(ConsistOf("get", "list", "delete"))
		g.Expect(role.Rules[1].APIGroups).To(ConsistOf("apps"))
		g.Expect(role.Rules[1].Resources).To(ConsistOf("statefulsets"))
		g.Expect(role.Rules[1].Verbs).To(ConsistOf("get", "list"))
	})
}

func TestReconcileEtcdRecoveryRoleBinding(t *testing.T) {
	t.Run("When called, it should bind the role to the service account", func(t *testing.T) {
		g := NewWithT(t)

		r := &HostedClusterReconciler{}
		roleBinding := &rbacv1.RoleBinding{}
		role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "etcd-recovery"}}
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "etcd-recovery-sa", Namespace: "clusters-test"}}

		r.reconcileEtcdRecoveryRoleBinding(roleBinding, role, sa)

		g.Expect(roleBinding.RoleRef.Kind).To(Equal("Role"))
		g.Expect(roleBinding.RoleRef.Name).To(Equal("etcd-recovery"))
		g.Expect(roleBinding.RoleRef.APIGroup).To(Equal(rbacv1.GroupName))
		g.Expect(roleBinding.Subjects).To(HaveLen(1))
		g.Expect(roleBinding.Subjects[0].Kind).To(Equal("ServiceAccount"))
		g.Expect(roleBinding.Subjects[0].Name).To(Equal("etcd-recovery-sa"))
		g.Expect(roleBinding.Subjects[0].Namespace).To(Equal("clusters-test"))
	})
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
