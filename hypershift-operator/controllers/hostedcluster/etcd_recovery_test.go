package hostedcluster

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
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
		{
			name:      "When job has both Complete and Failed conditions with False status, it should return finished=false",
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
							Status: corev1.ConditionFalse,
						},
						{
							Type:   batchv1.JobFailed,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			expectedExists:   true,
			expectedFinished: false,
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

func TestFindFailingEtcdPod(t *testing.T) {
	tests := []struct {
		name         string
		pods         []crclient.Object
		namespace    string
		expectFound  bool
		expectedName string
	}{
		{
			name:      "When fewer than 3 etcd pods exist, it should return nil",
			namespace: "clusters-test",
			pods: []crclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-0", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-1", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
				},
			},
			expectFound: false,
		},
		{
			name:      "When 3 healthy etcd pods exist, it should return nil",
			namespace: "clusters-test",
			pods: []crclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-0", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-1", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-2", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
			},
			expectFound: false,
		},
		{
			name:      "When one etcd pod is in Waiting state with restarts, it should return that pod",
			namespace: "clusters-test",
			pods: []crclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-0", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-1", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name:         "etcd",
								State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
								RestartCount: 5,
							},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-2", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
			},
			expectFound:  true,
			expectedName: "etcd-1",
		},
		{
			name:      "When etcd pod has waiting state but zero restarts, it should not detect it as failing",
			namespace: "clusters-test",
			pods: []crclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-0", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-1", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name:         "etcd",
								State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
								RestartCount: 0,
							},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-2", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
			},
			expectFound: false,
		},
		{
			name:      "When a non-etcd container is failing, it should not detect the pod as failing",
			namespace: "clusters-test",
			pods: []crclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-0", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
							{
								Name:         "sidecar",
								State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
								RestartCount: 5,
							},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-1", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "etcd-2", Namespace: "clusters-test",
						Labels: map[string]string{"app": "etcd"},
					},
				},
			},
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tt.pods...).
				Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
			}

			log := zap.New(zap.UseDevMode(true))
			pod, err := r.findFailingEtcdPod(t.Context(), log, tt.namespace)

			g.Expect(err).ToNot(HaveOccurred())
			if tt.expectFound {
				g.Expect(pod).ToNot(BeNil())
				g.Expect(pod.Name).To(Equal(tt.expectedName))
			} else {
				g.Expect(pod).To(BeNil())
			}
		})
	}
}

func TestHandleExistingEtcdRecoveryJob(t *testing.T) {
	tests := []struct {
		name            string
		jobStatus       *etcdJobStatus
		expectDone      bool
		expectCondition bool
		expectedReason  string
		expectedStatus  metav1.ConditionStatus
	}{
		{
			name: "When job is not finished, it should return done=true and not set any condition",
			jobStatus: &etcdJobStatus{
				exists:   true,
				finished: false,
			},
			expectDone:      true,
			expectCondition: false,
		},
		{
			name: "When job failed, it should return done=true and set EtcdRecoveryActive with failure reason",
			jobStatus: &etcdJobStatus{
				exists:     true,
				finished:   true,
				successful: false,
			},
			expectDone:      true,
			expectCondition: true,
			expectedReason:  hyperv1.EtcdRecoveryJobFailedReason,
			expectedStatus:  metav1.ConditionFalse,
		},
		{
			name: "When job succeeded, it should return done=false and set EtcdRecoveryActive with success reason",
			jobStatus: &etcdJobStatus{
				exists:     true,
				finished:   true,
				successful: true,
			},
			expectDone:      false,
			expectCondition: true,
			expectedReason:  hyperv1.AsExpectedReason,
			expectedStatus:  metav1.ConditionFalse,
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

			recoveryJob := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-recovery",
					Namespace: "clusters-test-cluster",
				},
			}

			var objects []crclient.Object
			objects = append(objects, hcluster)
			if tt.jobStatus.finished && tt.jobStatus.successful {
				// For cleanup, the job must exist in the client
				objects = append(objects, recoveryJob)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objects...).
				WithStatusSubresource(&hyperv1.HostedCluster{}).
				Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
				now:    metav1.Now,
			}

			log := zap.New(zap.UseDevMode(true))
			done, err := r.handleExistingEtcdRecoveryJob(t.Context(), log, hcluster, recoveryJob, tt.jobStatus)

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(done).To(Equal(tt.expectDone))

			if tt.expectCondition {
				updatedHC := &hyperv1.HostedCluster{}
				g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), updatedHC)).To(Succeed())
				cond := findCondition(updatedHC.Status.Conditions, string(hyperv1.EtcdRecoveryActive))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Reason).To(Equal(tt.expectedReason))
				g.Expect(cond.Status).To(Equal(tt.expectedStatus))
			} else {
				updatedHC := &hyperv1.HostedCluster{}
				g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), updatedHC)).To(Succeed())
				cond := findCondition(updatedHC.Status.Conditions, string(hyperv1.EtcdRecoveryActive))
				g.Expect(cond).To(BeNil(), "EtcdRecoveryActive condition should not be set")
			}
		})
	}
}

func TestReconcileEtcdRecoveryRole(t *testing.T) {
	t.Run("When called, it should set expected RBAC rules", func(t *testing.T) {
		g := NewWithT(t)

		r := &HostedClusterReconciler{}
		role := &rbacv1.Role{}
		r.reconcileEtcdRecoveryRole(role)

		g.Expect(role.Rules).To(HaveLen(2))

		// First rule: pods and PVCs
		g.Expect(role.Rules[0].APIGroups).To(ConsistOf(""))
		g.Expect(role.Rules[0].Resources).To(ConsistOf("pods", "persistentvolumeclaims"))
		g.Expect(role.Rules[0].Verbs).To(ConsistOf("get", "list", "delete"))

		// Second rule: statefulsets
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

// findCondition is a test helper that looks up a condition by type.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
