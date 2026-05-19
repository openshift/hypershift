package etcdbackup

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftfake "github.com/openshift/hypershift/client/clientset/clientset/fake"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestMain(m *testing.M) {
	featuregate.ConfigureFeatureSet(string(configv1.TechPreviewNoUpgrade))
	os.Exit(m.Run())
}

const (
	testHCPNamespace = "clusters-test"
	testHONamespace  = "hypershift"
	testBackupName   = "backup-1"
	testHCPName      = "test-hcp"
	testHCNamespace  = "clusters"
	testHCName       = "test"
	testReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = hyperv1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = networkingv1.AddToScheme(s)
	_ = rbacv1.AddToScheme(s)
	return s
}

func newReconciler(objs ...client.Object) *HCPEtcdBackupReconciler {
	scheme := newScheme()
	clientObjs := make([]client.Object, len(objs))
	copy(clientObjs, objs)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(clientObjs...).
		WithStatusSubresource(&hyperv1.HCPEtcdBackup{}, &hyperv1.HostedControlPlane{}, &hyperv1.HostedCluster{}).
		Build()

	var hypershiftObjs []runtime.Object
	for _, obj := range objs {
		switch obj.(type) {
		case *hyperv1.HostedControlPlane, *hyperv1.HostedCluster, *hyperv1.HCPEtcdBackup:
			hypershiftObjs = append(hypershiftObjs, obj.DeepCopyObject())
		}
	}

	return &HCPEtcdBackupReconciler{
		Client:                  fakeClient,
		HypershiftClient:        hypershiftfake.NewSimpleClientset(hypershiftObjs...),
		OperatorNamespace:       testHONamespace,
		ReleaseProvider:         &fakeReleaseProvider{},
		HypershiftOperatorImage: "quay.io/hypershift/hypershift:latest",
		MaxBackupCount:          5,
	}
}

func newHCPEtcdBackup() *hyperv1.HCPEtcdBackup {
	return &hyperv1.HCPEtcdBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testBackupName,
			Namespace: testHCPNamespace,
		},
		Spec: hyperv1.HCPEtcdBackupSpec{
			Storage: hyperv1.HCPEtcdBackupStorage{
				StorageType: hyperv1.S3BackupStorage,
				S3: hyperv1.HCPEtcdBackupS3{
					Bucket:    "my-bucket",
					Region:    "us-east-1",
					KeyPrefix: "backups/test",
					Credentials: hyperv1.SecretReference{
						Name: "aws-creds",
					},
				},
			},
		},
	}
}

func newHostedCluster() *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testHCName,
			Namespace: testHCNamespace,
		},
	}
}

func newHostedControlPlane() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testHCPName,
			Namespace: testHCPNamespace,
			Annotations: map[string]string{
				k8sutil.HostedClusterAnnotation: testHCNamespace + "/" + testHCName,
			},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: testReleaseImage,
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}
}

func newEtcdStatefulSet(ready int32, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: testHCPNamespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "etcd"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "etcd"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "etcd", Image: "etcd:latest"}},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: ready,
		},
	}
}

const (
	testCPOImage  = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cpo-fake"
	testEtcdImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:etcd-fake"
)

// fakeReleaseProvider implements releaseinfo.ProviderWithOpenShiftImageRegistryOverrides.
type fakeReleaseProvider struct{}

func (f *fakeReleaseProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
	return &releaseinfo.ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{
					{Name: "hypershift", From: &corev1.ObjectReference{Name: testCPOImage}},
					{Name: "etcd", From: &corev1.ObjectReference{Name: testEtcdImage}},
				},
			},
		},
	}, nil
}

func (f *fakeReleaseProvider) GetRegistryOverrides() map[string]string {
	return nil
}

func (f *fakeReleaseProvider) GetOpenShiftImageRegistryOverrides() map[string][]string {
	return nil
}

func (f *fakeReleaseProvider) GetMirroredReleaseImage() string {
	return ""
}

func TestReconcile(t *testing.T) {
	t.Run("When HCPEtcdBackup is not found it should not requeue", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "nonexistent",
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))
	})

	t.Run("When HostedControlPlane is not found it should set BackupFailed", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		r := newReconciler(backup)

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		// Verify status
		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions).To(HaveLen(1))
		g.Expect(updated.Status.Conditions[0].Type).To(Equal(string(hyperv1.BackupCompleted)))
		g.Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupFailedReason))
	})

	t.Run("When etcd StatefulSet is not ready it should set EtcdUnhealthy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(1, 3) // Only 1 of 3 ready
		r := newReconciler(backup, hcp, sts)

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions).To(HaveLen(1))
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.EtcdUnhealthyReason))
	})

	t.Run("When etcd StatefulSet is not found it should set EtcdUnhealthy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		r := newReconciler(backup, hcp) // No StatefulSet

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.EtcdUnhealthyReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("not found"))
	})

	t.Run("When another backup is already active it should reject and not requeue", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)

		// Active Job for this HCP namespace
		activeJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-backup-other",
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelHCPNamespace: testHCPNamespace,
					LabelBackupName:   "other-backup",
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers:    []corev1.Container{{Name: "test", Image: "test:latest"}},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{
				Active: 1,
			},
		}
		r := newReconciler(backup, hcp, sts, activeJob)

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupRejectedReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("rejected"))
	})

	t.Run("When the backup's own Job is active it should monitor it instead of rejecting", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)

		// Active Job belonging to THIS backup (same LabelBackupName)
		ownJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-backup-" + testBackupName,
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelHCPNamespace: testHCPNamespace,
					LabelBackupName:   testBackupName,
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers:    []corev1.Container{{Name: "test", Image: "test:latest"}},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{
				Active: 1,
			},
		}
		r := newReconciler(backup, hcp, sts, ownJob)

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		// Should requeue to monitor the Job, NOT reject
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		// Should NOT be BackupRejected
		for _, c := range updated.Status.Conditions {
			g.Expect(c.Reason).ToNot(Equal(hyperv1.BackupRejectedReason),
				"backup should not reject its own active Job")
		}
	})

	t.Run("When backup is in terminal state it should cleanup and not requeue", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		backup.Status.Conditions = []metav1.Condition{
			{
				Type:   string(hyperv1.BackupCompleted),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.BackupSucceededReason,
			},
		}
		r := newReconciler(backup)

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))
	})

	t.Run("When credential Secret does not exist it should set BackupFailed without creating RBAC or NetworkPolicy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)
		// No credential secret — should trigger BackupFailed before creating any resources
		r := newReconciler(backup, hcp, sts)
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions).To(HaveLen(1))
		g.Expect(updated.Status.Conditions[0].Type).To(Equal(string(hyperv1.BackupCompleted)))
		g.Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupFailedReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("credential Secret"))

		// Verify no RBAC or NetworkPolicy was created (early validation prevents resource waste)
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.Role{})).ToNot(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.RoleBinding{})).ToNot(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, &networkingv1.NetworkPolicy{})).ToNot(Succeed())
	})

	t.Run("When rejected backup reconciles with active Job from another backup it should not delete shared resources", func(t *testing.T) {
		g := NewGomegaWithT(t)

		// Rejected backup (tc4b in the QE scenario)
		rejectedBackup := newHCPEtcdBackup()
		rejectedBackup.Name = "pr8139-tc4b"
		rejectedBackup.Status.Conditions = []metav1.Condition{
			{
				Type:               string(hyperv1.BackupCompleted),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.BackupRejectedReason,
				Message:            "rejected: another backup Job is already running",
				LastTransitionTime: metav1.Now(),
			},
		}

		// Active Job from the first backup (tc4a)
		activeJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-backup-pr8139-tc4a-xyz",
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelBackupName:   "pr8139-tc4a",
					LabelHCPNamespace: testHCPNamespace,
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers:    []corev1.Container{{Name: "test", Image: "test:latest"}},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{Active: 1},
		}

		// Shared resources created by tc4a
		np := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      NetworkPolicyName,
				Namespace: testHCPNamespace,
			},
		}
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RBACName,
				Namespace: testHCPNamespace,
			},
		}
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RBACName,
				Namespace: testHCPNamespace,
			},
		}

		r := newReconciler(rejectedBackup, activeJob, np, role, rb)
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "pr8139-tc4b",
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		// Shared resources must still exist — the active Job needs them
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, &networkingv1.NetworkPolicy{})).To(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.Role{})).To(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.RoleBinding{})).To(Succeed())
	})

	t.Run("When backup failed it should be terminal", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		backup.Status.Conditions = []metav1.Condition{
			{
				Type:   string(hyperv1.BackupCompleted),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.BackupFailedReason,
			},
		}
		r := newReconciler(backup)

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))
	})
}

func TestIsTerminal(t *testing.T) {
	t.Run("When no conditions exist it should not be terminal", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{}
		g.Expect(isTerminal(backup)).To(BeFalse())
	})

	t.Run("When BackupCompleted is True it should be terminal", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			Status: hyperv1.HCPEtcdBackupStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(hyperv1.BackupCompleted),
						Status: metav1.ConditionTrue,
						Reason: hyperv1.BackupSucceededReason,
					},
				},
			},
		}
		g.Expect(isTerminal(backup)).To(BeTrue())
	})

	t.Run("When BackupCompleted reason is BackupFailed it should be terminal", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			Status: hyperv1.HCPEtcdBackupStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(hyperv1.BackupCompleted),
						Status: metav1.ConditionFalse,
						Reason: hyperv1.BackupFailedReason,
					},
				},
			},
		}
		g.Expect(isTerminal(backup)).To(BeTrue())
	})

	t.Run("When BackupCompleted reason is BackupRejected it should be terminal", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			Status: hyperv1.HCPEtcdBackupStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(hyperv1.BackupCompleted),
						Status: metav1.ConditionFalse,
						Reason: hyperv1.BackupRejectedReason,
					},
				},
			},
		}
		g.Expect(isTerminal(backup)).To(BeTrue())
	})

	t.Run("When BackupCompleted is False with non-terminal reason it should not be terminal", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			Status: hyperv1.HCPEtcdBackupStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(hyperv1.BackupCompleted),
						Status: metav1.ConditionFalse,
						Reason: hyperv1.EtcdUnhealthyReason,
					},
				},
			},
		}
		g.Expect(isTerminal(backup)).To(BeFalse())
	})
}

func TestEnsureRBAC(t *testing.T) {
	t.Run("When ensureRBAC is called it should create Role and RoleBinding in HCP namespace", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		r := newReconciler(backup)
		ctx := context.Background()

		err := r.ensureRBAC(ctx, backup)
		g.Expect(err).ToNot(HaveOccurred())

		// Verify Role
		role := &rbacv1.Role{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, role)).To(Succeed())
		g.Expect(role.Rules).To(HaveLen(2))
		g.Expect(role.Rules[0].Resources).To(ContainElement("secrets"))
		g.Expect(role.Rules[0].ResourceNames).To(ContainElement("etcd-client-tls"))
		g.Expect(role.Rules[1].Resources).To(ContainElement("configmaps"))
		g.Expect(role.Rules[1].ResourceNames).To(ContainElement("etcd-ca"))

		// Verify RoleBinding
		rb := &rbacv1.RoleBinding{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, rb)).To(Succeed())
		g.Expect(rb.RoleRef.Name).To(Equal(RBACName))
		g.Expect(rb.Subjects).To(HaveLen(1))
		g.Expect(rb.Subjects[0].Name).To(Equal(jobServiceAccountName))
		g.Expect(rb.Subjects[0].Namespace).To(Equal(testHONamespace))
	})
}

func TestEnsureNetworkPolicy(t *testing.T) {
	t.Run("When ensureNetworkPolicy is called it should create NetworkPolicy in HCP namespace", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		r := newReconciler(backup)
		ctx := context.Background()

		err := r.ensureNetworkPolicy(ctx, backup)
		g.Expect(err).ToNot(HaveOccurred())

		np := &networkingv1.NetworkPolicy{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, np)).To(Succeed())
		g.Expect(np.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue("app", "etcd"))
		g.Expect(np.Spec.Ingress).To(HaveLen(1))
		g.Expect(np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels).To(HaveKeyWithValue("kubernetes.io/metadata.name", testHONamespace))
		g.Expect(np.Spec.Ingress[0].Ports[0].Port.IntValue()).To(Equal(2379))
		g.Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))
	})
}

func TestCleanupResources(t *testing.T) {
	t.Run("When cleanup is called it should delete NetworkPolicy, Role, and RoleBinding", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()

		np := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      NetworkPolicyName,
				Namespace: testHCPNamespace,
			},
		}
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RBACName,
				Namespace: testHCPNamespace,
			},
		}
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RBACName,
				Namespace: testHCPNamespace,
			},
		}
		r := newReconciler(backup, np, role, rb)
		ctx := context.Background()

		err := r.cleanupResources(ctx, backup)
		g.Expect(err).ToNot(HaveOccurred())

		// All resources should be deleted
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, &networkingv1.NetworkPolicy{})).ToNot(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.Role{})).ToNot(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.RoleBinding{})).ToNot(Succeed())
	})

	t.Run("When resources do not exist cleanup should succeed", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		r := newReconciler(backup)
		ctx := context.Background()

		err := r.cleanupResources(ctx, backup)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When another backup Job is active it should skip cleanup", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()

		// Resources that should NOT be deleted
		np := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      NetworkPolicyName,
				Namespace: testHCPNamespace,
			},
		}
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RBACName,
				Namespace: testHCPNamespace,
			},
		}
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RBACName,
				Namespace: testHCPNamespace,
			},
		}

		// Active Job from another backup in the same HCP namespace
		activeJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-backup-other-backup",
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelBackupName:   "other-backup",
					LabelHCPNamespace: testHCPNamespace,
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers:    []corev1.Container{{Name: "test", Image: "test:latest"}},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{
				Active: 1,
			},
		}

		r := newReconciler(backup, np, role, rb, activeJob)
		ctx := context.Background()

		err := r.cleanupResources(ctx, backup)
		g.Expect(err).ToNot(HaveOccurred())

		// All resources should still exist
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, &networkingv1.NetworkPolicy{})).To(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.Role{})).To(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.RoleBinding{})).To(Succeed())
	})
}

func newTestJob(status batchv1.JobStatus) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-test",
			Namespace: testHONamespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "test", Image: "test:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
		Status: status,
	}
}

func TestHandleJobStatus(t *testing.T) {
	t.Run("When Job succeeds it should set BackupCompleted True and read snapshotURL from termination message", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		job := newTestJob(batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:   batchv1.JobComplete,
					Status: corev1.ConditionTrue,
				},
			},
		})
		// Pod with termination message from the upload container
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-backup-test-pod",
				Namespace: testHONamespace,
				Labels: map[string]string{
					"batch.kubernetes.io/job-name": "etcd-backup-test",
				},
			},
			Spec: corev1.PodSpec{
				Containers:    []corev1.Container{{Name: "upload", Image: "test:latest"}},
				RestartPolicy: corev1.RestartPolicyNever,
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "upload",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 0,
								Message:  "s3://my-bucket/backups/test/snapshot.db",
							},
						},
					},
				},
			},
		}
		r := newReconciler(backup, job, hcp, hc, pod)

		result, err := r.handleJobStatus(context.Background(), backup, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupSucceededReason))
		g.Expect(updated.Status.SnapshotURL).To(Equal("s3://my-bucket/backups/test/snapshot.db"))

		// Verify HCP condition was set (read from typed client since SSA writes go there)
		updatedHCP, getErr := r.HypershiftClient.HypershiftV1beta1().HostedControlPlanes(testHCPNamespace).Get(context.Background(), testHCPName, metav1.GetOptions{})
		g.Expect(getErr).ToNot(HaveOccurred())
		hcpCond := meta.FindStatusCondition(updatedHCP.Status.Conditions, string(hyperv1.EtcdBackupSucceeded))
		g.Expect(hcpCond).ToNot(BeNil())
		g.Expect(hcpCond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(hcpCond.Reason).To(Equal(hyperv1.BackupSucceededReason))

		// Verify HostedCluster status has the snapshot URL persisted
		updatedHC := &hyperv1.HostedCluster{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testHCName, Namespace: testHCNamespace}, updatedHC)).To(Succeed())
		g.Expect(updatedHC.Status.LastSuccessfulEtcdBackupURL).To(Equal("s3://my-bucket/backups/test/snapshot.db"))
	})

	t.Run("When Job fails it should set BackupFailed", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		job := newTestJob(batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Message: "BackoffLimitExceeded",
				},
			},
		})
		r := newReconciler(backup, job, hcp)

		result, err := r.handleJobStatus(context.Background(), backup, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupFailedReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("BackoffLimitExceeded"))
	})

	t.Run("When Job is still running it should requeue", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		job := newTestJob(batchv1.JobStatus{Active: 1})
		r := newReconciler(backup, job, hcp)

		result, err := r.handleJobStatus(context.Background(), backup, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))
	})
}

func TestSetEncryptionMetadata(t *testing.T) {
	t.Run("When S3 with KMS key it should set AWS encryption metadata", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		backup.Spec.Storage.S3.KMSKeyARN = "arn:aws:kms:us-east-1:123456789012:key/test-key"
		r := newReconciler(backup)

		r.setEncryptionMetadata(backup)
		g.Expect(backup.Status.EncryptionMetadata.AWS.KMSKeyARN).To(Equal("arn:aws:kms:us-east-1:123456789012:key/test-key"))
	})

	t.Run("When S3 without KMS key it should not set encryption metadata", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		r := newReconciler(backup)

		r.setEncryptionMetadata(backup)
		g.Expect(backup.Status.EncryptionMetadata.AWS.KMSKeyARN).To(BeEmpty())
	})

	t.Run("When AzureBlob with encryption key it should set Azure encryption metadata", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
			Spec: hyperv1.HCPEtcdBackupSpec{
				Storage: hyperv1.HCPEtcdBackupStorage{
					StorageType: hyperv1.AzureBlobBackupStorage,
					AzureBlob: hyperv1.HCPEtcdBackupAzureBlob{
						Container:        "my-container",
						StorageAccount:   "mystorageaccount",
						KeyPrefix:        "backups/test",
						Credentials:      hyperv1.SecretReference{Name: "azure-creds"},
						EncryptionKeyURL: "https://myvault.vault.azure.net/keys/mykey",
					},
				},
			},
		}
		r := newReconciler(backup)

		r.setEncryptionMetadata(backup)
		g.Expect(backup.Status.EncryptionMetadata.Azure.EncryptionKeyURL).To(Equal("https://myvault.vault.azure.net/keys/mykey"))
	})
}

func TestBuildUploadArgs(t *testing.T) {
	t.Run("When storage type is S3 it should build S3 upload args", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		r := newReconciler(backup)

		args, credSecret, err := r.buildUploadArgs(backup)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(credSecret).To(Equal("aws-creds"))
		g.Expect(args).To(ContainElements(
			"--storage-type", "S3",
			"--aws-bucket", "my-bucket",
			"--aws-region", "us-east-1",
			"--key-prefix", "backups/test",
		))
	})

	t.Run("When S3 has KMS key it should include kms-key-arn flag", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		backup.Spec.Storage.S3.KMSKeyARN = "arn:aws:kms:us-east-1:123456789012:key/test"
		r := newReconciler(backup)

		args, _, err := r.buildUploadArgs(backup)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(ContainElements("--aws-kms-key-arn", "arn:aws:kms:us-east-1:123456789012:key/test"))
	})

	t.Run("When storage type is AzureBlob it should build Azure upload args", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			Spec: hyperv1.HCPEtcdBackupSpec{
				Storage: hyperv1.HCPEtcdBackupStorage{
					StorageType: hyperv1.AzureBlobBackupStorage,
					AzureBlob: hyperv1.HCPEtcdBackupAzureBlob{
						Container:      "my-container",
						StorageAccount: "mystorageaccount",
						KeyPrefix:      "backups",
						Credentials:    hyperv1.SecretReference{Name: "azure-creds"},
					},
				},
			},
		}
		r := newReconciler()

		args, credSecret, err := r.buildUploadArgs(backup)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(credSecret).To(Equal("azure-creds"))
		g.Expect(args).To(ContainElements(
			"--storage-type", "AzureBlob",
			"--azure-container", "my-container",
			"--azure-storage-account", "mystorageaccount",
		))
	})
}

func TestEnforceRetention(t *testing.T) {
	t.Run("When completed backup count exceeds max it should delete oldest", func(t *testing.T) {
		g := NewGomegaWithT(t)

		baseTime := time.Now().Add(-7 * time.Hour)
		var backups []client.Object
		for i := range 7 {
			b := &hyperv1.HCPEtcdBackup{
				ObjectMeta: metav1.ObjectMeta{
					Name:              fmt.Sprintf("backup-%d", i),
					Namespace:         testHCPNamespace,
					CreationTimestamp: metav1.NewTime(baseTime.Add(time.Duration(i) * time.Hour)),
				},
				Status: hyperv1.HCPEtcdBackupStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.BackupCompleted),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.BackupSucceededReason,
						},
					},
				},
			}
			backups = append(backups, b)
		}

		r := newReconciler(backups...)
		r.MaxBackupCount = 5

		err := r.enforceRetention(context.Background(), testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())

		// Should have deleted the 2 oldest (backup-0, backup-1)
		remaining := &hyperv1.HCPEtcdBackupList{}
		g.Expect(r.List(context.Background(), remaining, client.InNamespace(testHCPNamespace))).To(Succeed())
		g.Expect(remaining.Items).To(HaveLen(5))

		remainingNames := make([]string, len(remaining.Items))
		for i, b := range remaining.Items {
			remainingNames[i] = b.Name
		}
		g.Expect(remainingNames).To(ContainElements("backup-2", "backup-3", "backup-4", "backup-5", "backup-6"))
		g.Expect(remainingNames).ToNot(ContainElement("backup-0"))
		g.Expect(remainingNames).ToNot(ContainElement("backup-1"))
	})

	t.Run("When MaxBackupCount is 0 it should not enforce retention", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()
		r.MaxBackupCount = 0

		err := r.enforceRetention(context.Background(), testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When completed backup count is within max it should not delete", func(t *testing.T) {
		g := NewGomegaWithT(t)

		var backups []client.Object
		for i := range 3 {
			b := &hyperv1.HCPEtcdBackup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "backup-" + string(rune('a'+i)),
					Namespace: testHCPNamespace,
				},
				Status: hyperv1.HCPEtcdBackupStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.BackupCompleted),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.BackupSucceededReason,
						},
					},
				},
			}
			backups = append(backups, b)
		}

		r := newReconciler(backups...)
		r.MaxBackupCount = 5

		err := r.enforceRetention(context.Background(), testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())

		remaining := &hyperv1.HCPEtcdBackupList{}
		g.Expect(r.List(context.Background(), remaining, client.InNamespace(testHCPNamespace))).To(Succeed())
		g.Expect(remaining.Items).To(HaveLen(3))
	})
}

func TestCheckEtcdHealth(t *testing.T) {
	t.Run("When etcd StatefulSet is fully ready it should return healthy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		sts := newEtcdStatefulSet(3, 3)
		r := newReconciler(sts)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeTrue())
		g.Expect(msg).To(BeEmpty())
	})

	t.Run("When etcd StatefulSet has fewer ready replicas it should return unhealthy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		sts := newEtcdStatefulSet(2, 3)
		r := newReconciler(sts)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeFalse())
		g.Expect(msg).To(ContainSubstring("2/3"))
	})
}

func TestFindActiveJob(t *testing.T) {
	t.Run("When no active jobs exist it should return nil", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()

		job, err := r.findActiveJob(context.Background(), testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(job).To(BeNil())
	})

	t.Run("When active job exists it should return it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		activeJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-backup-active",
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelHCPNamespace: testHCPNamespace,
				},
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers:    []corev1.Container{{Name: "test", Image: "test:latest"}},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
			},
			Status: batchv1.JobStatus{
				Active: 1,
			},
		}
		r := newReconciler(activeJob)

		job, err := r.findActiveJob(context.Background(), testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(job).ToNot(BeNil())
		g.Expect(job.Name).To(Equal("etcd-backup-active"))
	})
}

func TestCreateBackupJob(t *testing.T) {
	t.Run("When creating a backup Job it should build correct 3-container PodSpec", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pullSecretName,
				Namespace: testHCPNamespace,
			},
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
			},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "aws-creds",
				Namespace: testHONamespace,
			},
			Data: map[string][]byte{
				"credentials": []byte("fake-creds"),
			},
		}
		r := newReconciler(backup, hcp, pullSecret, credSecret)
		ctx := context.Background()

		err := r.createBackupJob(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		// Find the created Job
		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		job := &jobList.Items[0]

		// Verify labels
		g.Expect(job.Labels[LabelApp]).To(Equal(LabelName))
		g.Expect(job.Labels[labelHCP]).To(Equal(testHCPName))
		g.Expect(job.Labels[LabelBackupName]).To(Equal(testBackupName))
		g.Expect(job.Labels[LabelHCPNamespace]).To(Equal(testHCPNamespace))

		// Verify Job spec
		g.Expect(*job.Spec.TTLSecondsAfterFinished).To(Equal(int32(600)))
		g.Expect(*job.Spec.ActiveDeadlineSeconds).To(Equal(int64(900)))
		g.Expect(*job.Spec.BackoffLimit).To(Equal(int32(0)))

		podSpec := job.Spec.Template.Spec

		// Verify ServiceAccount
		g.Expect(podSpec.ServiceAccountName).To(Equal(jobServiceAccountName))
		g.Expect(podSpec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))

		// Verify volumes
		g.Expect(podSpec.Volumes).To(HaveLen(3))
		g.Expect(podSpec.Volumes[0].Name).To(Equal(volumeEtcdCerts))
		g.Expect(podSpec.Volumes[0].EmptyDir).ToNot(BeNil())
		g.Expect(podSpec.Volumes[1].Name).To(Equal(volumeEtcdBackup))
		g.Expect(podSpec.Volumes[1].EmptyDir).ToNot(BeNil())
		g.Expect(podSpec.Volumes[2].Name).To(Equal(volumeCredentials))
		g.Expect(podSpec.Volumes[2].Secret.SecretName).To(Equal("aws-creds"))

		// Verify init containers
		g.Expect(podSpec.InitContainers).To(HaveLen(2))

		fetchCerts := podSpec.InitContainers[0]
		g.Expect(fetchCerts.Name).To(Equal("fetch-certs"))
		g.Expect(fetchCerts.Image).To(Equal(testCPOImage))
		g.Expect(fetchCerts.Command).To(ContainElements("fetch-etcd-certs", "--hcp-namespace", testHCPNamespace))

		snapshot := podSpec.InitContainers[1]
		g.Expect(snapshot.Name).To(Equal("snapshot"))
		g.Expect(snapshot.Image).To(Equal(testEtcdImage))
		g.Expect(snapshot.Command).To(ContainElement("/usr/bin/etcdctl"))
		g.Expect(snapshot.Command).To(ContainElement("snapshot"))
		g.Expect(snapshot.Env).To(ContainElement(corev1.EnvVar{Name: "ETCDCTL_API", Value: "3"}))

		// Verify main container
		g.Expect(podSpec.Containers).To(HaveLen(1))
		upload := podSpec.Containers[0]
		g.Expect(upload.Name).To(Equal("upload"))
		g.Expect(upload.Image).To(Equal(testCPOImage))
		g.Expect(upload.Command).To(ContainElements("etcd-upload", "--storage-type", "S3"))
		g.Expect(upload.Command).To(ContainElements("--aws-bucket", "my-bucket"))
		g.Expect(upload.Command).To(ContainElements("--aws-region", "us-east-1"))
	})

	t.Run("When credential Secret does not exist the Job should still be created", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pullSecretName,
				Namespace: testHCPNamespace,
			},
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
			},
		}
		// No credential secret — createBackupJob does not validate it;
		// validation is done earlier in Reconcile via getCredentialSecretName + Get.
		r := newReconciler(backup, hcp, pullSecret)
		ctx := context.Background()

		err := r.createBackupJob(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))
		// The Job references the credential Secret in its volume — Kubernetes will
		// fail the Pod at runtime if it doesn't exist, but that's caught by Reconcile.
		g.Expect(jobList.Items[0].Spec.Template.Spec.Volumes[2].Secret.SecretName).To(Equal("aws-creds"))
	})

	t.Run("When storage type is AzureBlob it should build correct Azure upload args", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
			Spec: hyperv1.HCPEtcdBackupSpec{
				Storage: hyperv1.HCPEtcdBackupStorage{
					StorageType: hyperv1.AzureBlobBackupStorage,
					AzureBlob: hyperv1.HCPEtcdBackupAzureBlob{
						Container:      "my-container",
						StorageAccount: "mystorageaccount",
						KeyPrefix:      "backups/test",
						Credentials:    hyperv1.SecretReference{Name: "azure-creds"},
					},
				},
			},
		}
		hcp := newHostedControlPlane()
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pullSecretName,
				Namespace: testHCPNamespace,
			},
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
			},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "azure-creds",
				Namespace: testHONamespace,
			},
		}
		r := newReconciler(backup, hcp, pullSecret, credSecret)
		ctx := context.Background()

		err := r.createBackupJob(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		upload := jobList.Items[0].Spec.Template.Spec.Containers[0]
		g.Expect(upload.Command).To(ContainElements("etcd-upload", "--storage-type", "AzureBlob"))
		g.Expect(upload.Command).To(ContainElements("--azure-container", "my-container"))
		g.Expect(upload.Command).To(ContainElements("--azure-storage-account", "mystorageaccount"))

		// Verify credential volume uses Azure secret
		credVolume := jobList.Items[0].Spec.Template.Spec.Volumes[2]
		g.Expect(credVolume.Secret.SecretName).To(Equal("azure-creds"))
	})

	t.Run("When pull secret does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		// No pull secret created
		r := newReconciler(backup, hcp)
		ctx := context.Background()

		err := r.createBackupJob(ctx, backup, hcp)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("pull secret"))
	})

	t.Run("When KMS key is set it should include kms-key-arn in upload args", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		backup.Spec.Storage.S3.KMSKeyARN = "arn:aws:kms:us-east-1:123456789012:key/test-key"
		hcp := newHostedControlPlane()
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pullSecretName,
				Namespace: testHCPNamespace,
			},
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
			},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "aws-creds",
				Namespace: testHONamespace,
			},
		}
		r := newReconciler(backup, hcp, pullSecret, credSecret)
		ctx := context.Background()

		err := r.createBackupJob(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		upload := jobList.Items[0].Spec.Template.Spec.Containers[0]
		g.Expect(upload.Command).To(ContainElements("--aws-kms-key-arn", "arn:aws:kms:us-east-1:123456789012:key/test-key"))
	})
}

func TestReconcileHappyPath(t *testing.T) {
	t.Run("When etcd is healthy and no active Job exists it should create backup resources and Job", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pullSecretName,
				Namespace: testHCPNamespace,
			},
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
			},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "aws-creds",
				Namespace: testHONamespace,
			},
			Data: map[string][]byte{
				"credentials": []byte("fake-creds"),
			},
		}
		r := newReconciler(backup, hcp, sts, pullSecret, credSecret)
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		// Verify Job was created
		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))
		g.Expect(jobList.Items[0].Labels[LabelBackupName]).To(Equal(testBackupName))

		// Verify ServiceAccount was created
		sa := &corev1.ServiceAccount{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: jobServiceAccountName, Namespace: testHONamespace}, sa)).To(Succeed())

		// Verify RBAC was created in HCP namespace
		role := &rbacv1.Role{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, role)).To(Succeed())
		rb := &rbacv1.RoleBinding{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, rb)).To(Succeed())

		// Verify NetworkPolicy was created in HCP namespace
		np := &networkingv1.NetworkPolicy{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, np)).To(Succeed())

		// Verify backup status set to BackupInProgress
		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions).To(HaveLen(1))
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupInProgressReason))
		g.Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))

		// Verify HCP condition was set (read from typed client since SSA writes go there)
		updatedHCP, getErr := r.HypershiftClient.HypershiftV1beta1().HostedControlPlanes(testHCPNamespace).Get(ctx, testHCPName, metav1.GetOptions{})
		g.Expect(getErr).ToNot(HaveOccurred())
		hcpCond := meta.FindStatusCondition(updatedHCP.Status.Conditions, string(hyperv1.EtcdBackupSucceeded))
		g.Expect(hcpCond).ToNot(BeNil())
		g.Expect(hcpCond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(hcpCond.Reason).To(Equal(hyperv1.BackupInProgressReason))
	})
}

func TestUpdateHostedClusterBackupURL(t *testing.T) {
	t.Run("When HCP has HostedClusterAnnotation it should update HC status with snapshot URL", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		r := newReconciler(hcp, hc)

		err := r.updateHostedClusterBackupURL(context.Background(), hcp, "s3://bucket/snapshot.db")
		g.Expect(err).ToNot(HaveOccurred())

		updatedHC := &hyperv1.HostedCluster{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testHCName, Namespace: testHCNamespace}, updatedHC)).To(Succeed())
		g.Expect(updatedHC.Status.LastSuccessfulEtcdBackupURL).To(Equal("s3://bucket/snapshot.db"))
	})

	t.Run("When HCP is missing HostedClusterAnnotation it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		hcp.Annotations = nil
		r := newReconciler(hcp)

		err := r.updateHostedClusterBackupURL(context.Background(), hcp, "s3://bucket/snapshot.db")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("missing"))
	})

	t.Run("When HostedCluster does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		r := newReconciler(hcp) // no HC in the fake client

		err := r.updateHostedClusterBackupURL(context.Background(), hcp, "s3://bucket/snapshot.db")
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When HC status update conflicts it should retry and succeed", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		hc := newHostedCluster()

		callCount := 0
		s := newScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(hcp, hc).
			WithStatusSubresource(&hyperv1.HCPEtcdBackup{}, &hyperv1.HostedControlPlane{}, &hyperv1.HostedCluster{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					if _, ok := obj.(*hyperv1.HostedCluster); ok {
						callCount++
						if callCount == 1 {
							return apierrors.NewConflict(
								schema.GroupResource{Group: "hypershift.openshift.io", Resource: "hostedclusters"},
								obj.GetName(),
								fmt.Errorf("the object has been modified"),
							)
						}
					}
					return cl.Status().Update(ctx, obj, opts...)
				},
			}).
			Build()

		r := &HCPEtcdBackupReconciler{
			Client:            fakeClient,
			OperatorNamespace: testHONamespace,
		}

		err := r.updateHostedClusterBackupURL(context.Background(), hcp, "s3://bucket/snapshot.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(callCount).To(Equal(2))

		updatedHC := &hyperv1.HostedCluster{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testHCName, Namespace: testHCNamespace}, updatedHC)).To(Succeed())
		g.Expect(updatedHC.Status.LastSuccessfulEtcdBackupURL).To(Equal("s3://bucket/snapshot.db"))
	})
}

func TestGetCredentialSecretName(t *testing.T) {
	tests := []struct {
		name        string
		backup      *hyperv1.HCPEtcdBackup
		expected    string
		expectError bool
	}{
		{
			name: "When storage type is S3, it should return S3 credential secret name",
			backup: &hyperv1.HCPEtcdBackup{
				Spec: hyperv1.HCPEtcdBackupSpec{
					Storage: hyperv1.HCPEtcdBackupStorage{
						StorageType: hyperv1.S3BackupStorage,
						S3: hyperv1.HCPEtcdBackupS3{
							Credentials: hyperv1.SecretReference{Name: "s3-creds"},
						},
					},
				},
			},
			expected: "s3-creds",
		},
		{
			name: "When storage type is AzureBlob, it should return Azure credential secret name",
			backup: &hyperv1.HCPEtcdBackup{
				Spec: hyperv1.HCPEtcdBackupSpec{
					Storage: hyperv1.HCPEtcdBackupStorage{
						StorageType: hyperv1.AzureBlobBackupStorage,
						AzureBlob: hyperv1.HCPEtcdBackupAzureBlob{
							Credentials: hyperv1.SecretReference{Name: "azure-creds"},
						},
					},
				},
			},
			expected: "azure-creds",
		},
		{
			name: "When storage type is unsupported, it should return an error",
			backup: &hyperv1.HCPEtcdBackup{
				Spec: hyperv1.HCPEtcdBackupSpec{
					Storage: hyperv1.HCPEtcdBackupStorage{
						StorageType: "UnsupportedType",
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := newReconciler()

			name, err := r.getCredentialSecretName(tt.backup)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("unsupported"))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal(tt.expected))
			}
		})
	}
}

func TestGetHostedControlPlane(t *testing.T) {
	tests := []struct {
		name      string
		objects   []client.Object
		namespace string
		expectNil bool
		expectErr bool
	}{
		{
			name:      "When no HCPs exist in the namespace, it should return nil",
			objects:   []client.Object{},
			namespace: testHCPNamespace,
			expectNil: true,
		},
		{
			name: "When one HCP exists in the namespace, it should return it",
			objects: []client.Object{
				newHostedControlPlane(),
			},
			namespace: testHCPNamespace,
			expectNil: false,
		},
		{
			name: "When HCP exists in a different namespace, it should return nil",
			objects: []client.Object{
				newHostedControlPlane(),
			},
			namespace: "other-namespace",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := newReconciler(tt.objects...)

			result, err := r.getHostedControlPlane(t.Context(), tt.namespace)

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.expectNil {
					g.Expect(result).To(BeNil())
				} else {
					g.Expect(result).ToNot(BeNil())
				}
			}
		})
	}
}

func TestValidatePrerequisites(t *testing.T) {
	tests := []struct {
		name        string
		backup      *hyperv1.HCPEtcdBackup
		objects     []client.Object
		expectDone  bool
		expectError bool
		expectFail  bool
	}{
		{
			name:   "When credential secret exists, it should return done=false (proceed)",
			backup: newHCPEtcdBackup(),
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-creds",
						Namespace: testHONamespace,
					},
				},
			},
			expectDone: false,
		},
		{
			name:       "When credential secret is missing, it should set BackupFailed and return done=true",
			backup:     newHCPEtcdBackup(),
			objects:    []client.Object{},
			expectDone: true,
			expectFail: true,
		},
		{
			name: "When storage type is unsupported, it should set BackupFailed and return done=true",
			backup: &hyperv1.HCPEtcdBackup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testBackupName,
					Namespace: testHCPNamespace,
				},
				Spec: hyperv1.HCPEtcdBackupSpec{
					Storage: hyperv1.HCPEtcdBackupStorage{
						StorageType: "UnsupportedType",
					},
				},
			},
			objects:    []client.Object{},
			expectDone: true,
			expectFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			allObjects := append([]client.Object{tt.backup}, tt.objects...)
			r := newReconciler(allObjects...)

			_, done, err := r.validatePrerequisites(t.Context(), tt.backup)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(done).To(Equal(tt.expectDone))

			if tt.expectFail {
				updated := &hyperv1.HCPEtcdBackup{}
				g.Expect(r.Get(t.Context(), types.NamespacedName{Name: tt.backup.Name, Namespace: tt.backup.Namespace}, updated)).To(Succeed())
				cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Reason).To(Equal(hyperv1.BackupFailedReason))
			}
		})
	}
}

func TestGetSnapshotURLFromPod(t *testing.T) {
	tests := []struct {
		name     string
		pods     []client.Object
		jobName  string
		expected string
	}{
		{
			name: "When upload container has termination message, it should return the URL",
			pods: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "backup-pod",
						Namespace: testHONamespace,
						Labels:    map[string]string{"batch.kubernetes.io/job-name": "my-job"},
					},
					Spec: corev1.PodSpec{
						Containers:    []corev1.Container{{Name: "upload", Image: "test:latest"}},
						RestartPolicy: corev1.RestartPolicyNever,
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "upload",
								State: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										Message: "  s3://bucket/path/snapshot.db  ",
									},
								},
							},
						},
					},
				},
			},
			jobName:  "my-job",
			expected: "s3://bucket/path/snapshot.db",
		},
		{
			name:     "When no pods exist for the job, it should return empty string",
			pods:     []client.Object{},
			jobName:  "my-job",
			expected: "",
		},
		{
			name: "When upload container has no termination message, it should return empty string",
			pods: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "backup-pod",
						Namespace: testHONamespace,
						Labels:    map[string]string{"batch.kubernetes.io/job-name": "my-job"},
					},
					Spec: corev1.PodSpec{
						Containers:    []corev1.Container{{Name: "upload", Image: "test:latest"}},
						RestartPolicy: corev1.RestartPolicyNever,
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "upload",
								State: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										ExitCode: 0,
									},
								},
							},
						},
					},
				},
			},
			jobName:  "my-job",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := newReconciler(tt.pods...)

			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.jobName,
					Namespace: testHONamespace,
				},
			}

			url, err := r.getSnapshotURLFromPod(t.Context(), job)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(url).To(Equal(tt.expected))
		})
	}
}
