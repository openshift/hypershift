package etcdbackup

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

	return &HCPEtcdBackupReconciler{
		Client: fakeClient,
		// The fake client plays both the cached client and the API server, so
		// point the quorum reader at it to exercise the non-cache read paths.
		APIReader:               fakeClient,
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
			// Real backups are always stamped by the API server; the fake client is
			// not, so set a recent timestamp to keep the credential-wait deadline
			// (credentialWaitTimeout) from tripping immediately in tests.
			CreationTimestamp: metav1.Now(),
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
		Spec: hyperv1.HostedClusterSpec{
			Release: hyperv1.Release{
				Image: testReleaseImage,
			},
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
			ObjectMeta: metav1.ObjectMeta{Name: "4.16.0"},
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

	t.Run("When a multi-shard backup lacks the CPO capability label it should wait without creating a Job", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 1,
				Resources: []hyperv1.EtcdShardResource{{
					APIGroup: ptr.To(""),
					Resource: "events",
				}},
				Storage: hyperv1.ManagedEtcdShardStorageSpec{
					Type: hyperv1.PersistentVolumeEtcdShardStorage,
				},
			}},
		}
		defaultSTS := newEtcdStatefulSet(3, 3)
		eventsSTS := newEtcdStatefulSet(1, 1)
		eventsSTS.Name = "etcd-events"
		r := newReconciler(backup, hcp, defaultSTS, eventsSTS)

		result, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(credentialWaitRequeueInterval))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(hyperv1.BackupWaitingForCPOCapabilityReason))
		g.Expect(cond.Message).To(ContainSubstring(hyperv1.MultiShardEtcdBackupCapabilityLabel))

		// No Job was created while waiting.
		jobList := &batchv1.JobList{}
		g.Expect(r.List(context.Background(), jobList)).To(Succeed())
		g.Expect(jobList.Items).To(BeEmpty())
	})

	t.Run("When a multi-shard backup has the CPO capability label it should proceed past the gate", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		hcp.Labels = map[string]string{hyperv1.MultiShardEtcdBackupCapabilityLabel: "true"}
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 1,
				Resources: []hyperv1.EtcdShardResource{{
					APIGroup: ptr.To(""),
					Resource: "events",
				}},
				Storage: hyperv1.ManagedEtcdShardStorageSpec{
					Type: hyperv1.PersistentVolumeEtcdShardStorage,
				},
			}},
		}
		defaultSTS := newEtcdStatefulSet(3, 3)
		eventsSTS := newEtcdStatefulSet(1, 1)
		eventsSTS.Name = "etcd-events"
		hc := newHostedCluster()
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("fake-creds")},
		}
		r := newReconciler(backup, hcp, defaultSTS, eventsSTS, hc, pullSecret, credSecret)

		_, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())

		// The gate passed: a backup Job was created.
		jobList := &batchv1.JobList{}
		g.Expect(r.List(context.Background(), jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Reason).ToNot(Equal(hyperv1.BackupWaitingForCPOCapabilityReason))
	})

	t.Run("When a single-shard backup lacks the CPO capability label it should not be gated", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane() // no shards: single-shard, no label
		sts := newEtcdStatefulSet(3, 3)
		hc := newHostedCluster()
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("fake-creds")},
		}
		r := newReconciler(backup, hcp, sts, hc, pullSecret, credSecret)

		_, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(context.Background(), jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))
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

	t.Run("When credential Secret does not exist it should wait (non-terminal) and requeue without creating RBAC or NetworkPolicy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)
		// No credential secret — should mark waiting and requeue before creating any resources.
		r := newReconciler(backup, hcp, sts)
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testBackupName,
				Namespace: testHCPNamespace,
			},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{RequeueAfter: credentialWaitRequeueInterval}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions).To(HaveLen(1))
		g.Expect(updated.Status.Conditions[0].Type).To(Equal(string(hyperv1.BackupCompleted)))
		g.Expect(updated.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupWaitingForCredentialsReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("credential Secret"))
		// Waiting is non-terminal so the backup can still recover.
		g.Expect(isTerminal(updated)).To(BeFalse())

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

		// Non-terminal backup from tc4a (still in-progress)
		tc4aBackup := &hyperv1.HCPEtcdBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pr8139-tc4a",
				Namespace: testHCPNamespace,
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

		r := newReconciler(rejectedBackup, tc4aBackup, activeJob, np, role, rb)
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

	// Formerly the top-level TestReconcileConcurrentBackupSerialization.
	newOtherBackupJob := func(name, backupName string, conditions ...batchv1.JobCondition) *batchv1.Job {
		return &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelHCPNamespace: testHCPNamespace,
					LabelBackupName:   backupName,
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
				Conditions: conditions,
			},
		}
	}

	t.Run("When another backup's fresh Job is invisible to the cache it should reject", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)

		// The cache client (what the reconciler's cached Client sees) has no
		// Jobs at all; the API reader sees a Job for another backup that was
		// just created and has no status yet (Active == 0, no conditions).
		r := newReconciler(backup, hcp, sts)
		r.APIReader = fake.NewClientBuilder().
			WithScheme(newScheme()).
			WithObjects(newOtherBackupJob("etcd-backup-first", "first-backup")).
			Build()
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(hyperv1.BackupRejectedReason))
		g.Expect(cond.Message).To(ContainSubstring("etcd-backup-first"))
		g.Expect(isTerminal(updated)).To(BeTrue(), "a rejected backup must be terminal so retention can clean it up")

		// No Job may have been created for the rejected backup.
		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(BeEmpty())
	})

	t.Run("When another backup's Job already completed it should proceed", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("fake-creds")},
		}

		// A completed Job for a prior backup is visible only via the API
		// reader; it must not block a new backup.
		r := newReconciler(backup, hcp, sts, newHostedCluster(), pullSecret, credSecret)
		r.APIReader = fake.NewClientBuilder().
			WithScheme(newScheme()).
			WithObjects(newOtherBackupJob("etcd-backup-prior", "prior-backup",
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue})).
			Build()
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Reason).To(Equal(hyperv1.BackupInProgressReason))

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))
	})

	t.Run("When its own Job is invisible to the cache it should not create a duplicate", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)

		// The backup's own Job exists on the API server but is not yet in the
		// cache. findJobForBackup performs a quorum read, so the reconciler
		// must adopt the existing Job rather than create a duplicate.
		ownJob := newOtherBackupJob("etcd-backup-own", backup.Name)
		ownJob.Labels[LabelBackupName] = backup.Name
		r := newReconciler(backup, hcp, sts)
		r.APIReader = fake.NewClientBuilder().
			WithScheme(newScheme()).
			WithObjects(ownJob).
			Build()
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		// Job is neither Complete nor Failed: the reconciler monitors it and requeues.
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		// No duplicate Job was created through the cached client.
		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(BeEmpty())
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

	t.Run("When ensureRBAC is called it should create a Role and RoleBinding scoped to the result ConfigMap", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		r := newReconciler(backup)
		ctx := context.Background()

		err := r.ensureRBAC(ctx, backup)
		g.Expect(err).ToNot(HaveOccurred())

		name := resultConfigMapName(backup)
		role := &rbacv1.Role{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: name, Namespace: testHONamespace}, role)).To(Succeed())
		g.Expect(role.Rules).To(ConsistOf(rbacv1.PolicyRule{
			APIGroups:     []string{""},
			Resources:     []string{"configmaps"},
			ResourceNames: []string{name},
			Verbs:         []string{"create", "update", "patch", "get"},
		}))

		rb := &rbacv1.RoleBinding{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: name, Namespace: testHONamespace}, rb)).To(Succeed())
		g.Expect(rb.RoleRef.Name).To(Equal(name))
		g.Expect(rb.Subjects).To(ConsistOf(rbacv1.Subject{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      jobServiceAccountName,
			Namespace: testHONamespace,
		}))
	})
}

func TestEnsureNetworkPolicy(t *testing.T) {
	t.Run("When ensureNetworkPolicy is called it should create NetworkPolicy in HCP namespace", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		r := newReconciler(backup, hcp)
		ctx := context.Background()

		err := r.ensureNetworkPolicy(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		np := &networkingv1.NetworkPolicy{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, np)).To(Succeed())
		g.Expect(np.Spec.PodSelector.MatchExpressions).To(HaveLen(1))
		g.Expect(np.Spec.PodSelector.MatchExpressions[0].Key).To(Equal("app"))
		g.Expect(np.Spec.PodSelector.MatchExpressions[0].Operator).To(Equal(metav1.LabelSelectorOpIn))
		g.Expect(np.Spec.PodSelector.MatchExpressions[0].Values).To(ContainElement("etcd"))
		g.Expect(np.Spec.Ingress).To(HaveLen(1))
		g.Expect(np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels).To(HaveKeyWithValue("kubernetes.io/metadata.name", testHONamespace))
		g.Expect(np.Spec.Ingress[0].Ports[0].Port.IntValue()).To(Equal(2379))
		g.Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))
	})

	// Formerly the top-level TestEnsureNetworkPolicyMultiShard.
	t.Run("When shards are configured the NetworkPolicy should include shard app labels", func(t *testing.T) {
		g := NewWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 3,
				Storage:  hyperv1.ManagedEtcdShardStorageSpec{Type: hyperv1.PersistentVolumeEtcdShardStorage},
			}},
		}
		r := newReconciler(backup, hcp)
		ctx := context.Background()

		err := r.ensureNetworkPolicy(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		np := &networkingv1.NetworkPolicy{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, np)).To(Succeed())
		g.Expect(np.Spec.PodSelector.MatchExpressions).To(HaveLen(1))
		values := np.Spec.PodSelector.MatchExpressions[0].Values
		g.Expect(values).To(ContainElement("etcd"))
		g.Expect(values).To(ContainElement("etcd-events"))
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

	t.Run("When another non-terminal backup exists it should skip cleanup", func(t *testing.T) {
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

		// Another non-terminal backup in the same namespace (no Job yet — race window)
		otherBackup := &hyperv1.HCPEtcdBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-backup",
				Namespace: testHCPNamespace,
			},
		}

		r := newReconciler(backup, np, role, rb, otherBackup)
		ctx := context.Background()

		err := r.cleanupResources(ctx, backup)
		g.Expect(err).ToNot(HaveOccurred())

		// All resources should still exist
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, &networkingv1.NetworkPolicy{})).To(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.Role{})).To(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.RoleBinding{})).To(Succeed())
	})

	t.Run("When all other backups are terminal it should proceed with cleanup", func(t *testing.T) {
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

		// Another backup that already completed (terminal)
		completedBackup := &hyperv1.HCPEtcdBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "completed-backup",
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

		r := newReconciler(backup, np, role, rb, completedBackup)
		ctx := context.Background()

		err := r.cleanupResources(ctx, backup)
		g.Expect(err).ToNot(HaveOccurred())

		// All resources should be deleted
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, &networkingv1.NetworkPolicy{})).ToNot(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.Role{})).ToNot(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.RoleBinding{})).ToNot(Succeed())
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

		// Verify HCP condition was set
		updatedHCP := &hyperv1.HostedControlPlane{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testHCPName, Namespace: testHCPNamespace}, updatedHCP)).To(Succeed())
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
	t.Run("When Job succeeds with a result ConfigMap it should persist results and delete the ConfigMap", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		job := newTestJob(batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		})
		job.Annotations = map[string]string{annotationExpectedShardSnapshots: "default,events"}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resultConfigMapName(backup),
				Namespace: testHONamespace,
			},
			Data: map[string]string{resultConfigMapKey: `[{"name":"default","url":"s3://bucket/default.db"},{"name":"events","url":"s3://bucket/events.db"}]`},
		}
		r := newReconciler(backup, job, hcp, hc, cm)

		result, err := r.handleJobStatus(context.Background(), backup, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(updated.Status.ShardSnapshots).To(ConsistOf(
			hyperv1.HCPEtcdShardSnapshot{Name: "default", SnapshotURL: "s3://bucket/default.db"},
			hyperv1.HCPEtcdShardSnapshot{Name: "events", SnapshotURL: "s3://bucket/events.db"},
		))
		g.Expect(updated.Status.SnapshotURL).To(Equal("s3://bucket/default.db"))

		// The result ConfigMap is deleted once the results are persisted.
		err = r.Get(context.Background(), types.NamespacedName{Name: resultConfigMapName(backup), Namespace: testHONamespace}, &corev1.ConfigMap{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
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

func TestBuildJobVolumes(t *testing.T) {
	t.Run("When Azure client-secret credentials originate from cloud it should project them as credentials", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()

		volumes := r.buildJobVolumes(resolvedCredentials{
			Mode:           credentialModeAzureClientSecret,
			SecretName:     "azure-creds",
			CredentialsKey: secretKeyCloud,
		})

		var credentialsVolume *corev1.Volume
		for i := range volumes {
			if volumes[i].Name == volumeCredentials {
				credentialsVolume = &volumes[i]
				break
			}
		}

		g.Expect(credentialsVolume).ToNot(BeNil())
		if credentialsVolume == nil {
			return
		}
		g.Expect(credentialsVolume.Secret.SecretName).To(Equal("azure-creds"))
		g.Expect(credentialsVolume.Secret.Items).To(ConsistOf(corev1.KeyToPath{
			Key:  secretKeyCloud,
			Path: secretKeyCredentials,
		}))
	})
}

func TestBuildUploadArgs(t *testing.T) {
	newAzureBackup := func(encryptionKeyURL string) *hyperv1.HCPEtcdBackup {
		return &hyperv1.HCPEtcdBackup{
			Spec: hyperv1.HCPEtcdBackupSpec{
				Storage: hyperv1.HCPEtcdBackupStorage{
					StorageType: hyperv1.AzureBlobBackupStorage,
					AzureBlob: hyperv1.HCPEtcdBackupAzureBlob{
						Container:        "my-container",
						StorageAccount:   "mystorageaccount",
						KeyPrefix:        "backups",
						Credentials:      hyperv1.SecretReference{Name: "azure-creds"},
						EncryptionKeyURL: encryptionKeyURL,
					},
				},
			},
		}
	}

	tests := []struct {
		name           string
		backup         *hyperv1.HCPEtcdBackup
		creds          resolvedCredentials
		wantErr        string
		wantContain    []string
		wantNotContain []string
	}{
		{
			name:   "When storage type is S3 with static credentials it should build S3 upload args with credentials-file",
			backup: newHCPEtcdBackup(),
			creds:  resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"},
			wantContain: []string{
				"--storage-type", "S3",
				"--aws-bucket", "my-bucket",
				"--aws-region", "us-east-1",
				"--key-prefix", "backups/test",
				"--credentials-file",
			},
		},
		{
			name:           "When storage type is S3 with STS credentials it should omit credentials-file",
			backup:         newHCPEtcdBackup(),
			creds:          resolvedCredentials{Mode: credentialModeAWSSTS, SecretName: "aws-creds", RoleARN: "arn:aws:iam::123:role/test"},
			wantContain:    []string{"--storage-type", "S3"},
			wantNotContain: []string{"--credentials-file"},
		},
		{
			name: "When S3 has KMS key it should include kms-key-arn flag",
			backup: func() *hyperv1.HCPEtcdBackup {
				b := newHCPEtcdBackup()
				b.Spec.Storage.S3.KMSKeyARN = "arn:aws:kms:us-east-1:123456789012:key/test"
				return b
			}(),
			creds:       resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"},
			wantContain: []string{"--aws-kms-key-arn", "arn:aws:kms:us-east-1:123456789012:key/test"},
		},
		{
			name:   "When storage type is AzureBlob with client-secret it should include credentials-file and azure-auth-type",
			backup: newAzureBackup(""),
			creds:  resolvedCredentials{Mode: credentialModeAzureClientSecret, SecretName: "azure-creds"},
			wantContain: []string{
				"--storage-type", "AzureBlob",
				"--azure-container", "my-container",
				"--azure-storage-account", "mystorageaccount",
				"--credentials-file",
				"--azure-auth-type", "client-secret",
			},
		},
		{
			name:           "When storage type is AzureBlob with workload-identity it should omit credentials-file and azure-auth-type",
			backup:         newAzureBackup(""),
			creds:          resolvedCredentials{Mode: credentialModeAzureWorkloadIdentity, SecretName: "azure-creds", ClientID: "client-123"},
			wantContain:    []string{"--storage-type", "AzureBlob"},
			wantNotContain: []string{"--credentials-file", "--azure-auth-type"},
		},
		{
			name:        "When storage type is AzureBlob with encryption key it should include azure-encryption-scope",
			backup:      newAzureBackup("https://myvault.vault.azure.net/keys/mykey"),
			creds:       resolvedCredentials{Mode: credentialModeAzureClientSecret, SecretName: "azure-creds"},
			wantContain: []string{"--azure-encryption-scope", "https://myvault.vault.azure.net/keys/mykey"},
		},
		{
			name: "When storage type is unsupported, it should return an error",
			backup: &hyperv1.HCPEtcdBackup{
				Spec: hyperv1.HCPEtcdBackupSpec{
					Storage: hyperv1.HCPEtcdBackupStorage{StorageType: "UnknownStorage"},
				},
			},
			creds:   resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "some-creds"},
			wantErr: "unsupported storage type",
		},
	}

	// Default single shard for backward-compat tests
	singleShard := []etcdShardInfo{{name: "etcd", identity: "default", endpoint: "https://etcd-client.test-ns.svc:2379"}}

	// Verify single-shard uses --snapshot-path for backward compat
	t.Run("When single shard it should use --snapshot-path for backward compatibility", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()
		args, err := r.buildUploadArgs(newHCPEtcdBackup(), singleShard, resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(ContainElement("--snapshot-path"))
		g.Expect(args).ToNot(ContainElement("--snapshot-dir"))
	})

	// Verify multi-shard uses --snapshot-dir
	t.Run("When multiple shards it should use --snapshot-dir", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()
		multiShards := []etcdShardInfo{
			{name: "etcd", identity: "default", endpoint: "https://etcd-client.test-ns.svc:2379"},
			{name: "etcd-events", identity: "events", endpoint: "https://etcd-client-events.test-ns.svc:2379"},
		}
		args, err := r.buildUploadArgs(newHCPEtcdBackup(), multiShards, resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(ContainElement("--snapshot-dir"))
		g.Expect(args).ToNot(ContainElement("--snapshot-path"))
	})

	// Verify multi-shard passes the deterministic result ConfigMap name so
	// etcd-upload stores its results where the controller reads them.
	t.Run("When multiple shards it should pass the result ConfigMap name", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()
		backup := newHCPEtcdBackup()
		shards := []etcdShardInfo{
			{name: "etcd", identity: "default", endpoint: "https://etcd-client.test-ns.svc:2379"},
			{name: "etcd-events", identity: "events", endpoint: "https://etcd-client-events.test-ns.svc:2379"},
		}
		args, err := r.buildUploadArgs(backup, shards, resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(ContainElements("--result-configmap", resultConfigMapName(backup)))
	})

	// Single-shard Jobs keep the legacy --snapshot-path arg surface and must
	// not receive --result-configmap, so older CPO images that only know
	// --snapshot-path are unaffected.
	t.Run("When single shard it should not pass --result-configmap", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()
		args, err := r.buildUploadArgs(newHCPEtcdBackup(), singleShard, resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).ToNot(ContainElement("--result-configmap"))
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := newReconciler()

			args, err := r.buildUploadArgs(tt.backup, singleShard, tt.creds)
			if tt.wantErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.wantErr))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			for _, elem := range tt.wantContain {
				g.Expect(args).To(ContainElement(elem), "expected arg %q to be present", elem)
			}
			for _, elem := range tt.wantNotContain {
				g.Expect(args).ToNot(ContainElement(elem), "expected arg %q to be absent", elem)
			}
		})
	}
}

func TestBuildUploadContainer(t *testing.T) {
	t.Run("When building upload container, it should set FallbackToLogsOnError termination message policy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()
		container := r.buildUploadContainer("cpo:latest", []string{"control-plane-operator", "etcd-upload"}, resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "creds"})
		g.Expect(container.TerminationMessagePolicy).To(Equal(corev1.TerminationMessageFallbackToLogsOnError))
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
					Name:      fmt.Sprintf("backup-%d", i),
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
		hcp := newHostedControlPlane()
		r := newReconciler(sts, hcp)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeTrue())
		g.Expect(msg).To(BeEmpty())
	})

	t.Run("When etcd StatefulSet has fewer ready replicas it should return unhealthy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		sts := newEtcdStatefulSet(2, 3)
		hcp := newHostedControlPlane()
		r := newReconciler(sts, hcp)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeFalse())
		g.Expect(msg).To(ContainSubstring("2/3"))
	})

	// Formerly the top-level TestCheckEtcdHealthMultiShard.
	t.Run("When all shard StatefulSets are ready it should return healthy", func(t *testing.T) {
		g := NewWithT(t)
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 3,
				Storage:  hyperv1.ManagedEtcdShardStorageSpec{Type: hyperv1.PersistentVolumeEtcdShardStorage},
			}},
		}
		defaultSTS := newEtcdStatefulSet(3, 3)
		shardSTS := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "etcd-events", Namespace: testHCPNamespace},
			Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To(int32(3))},
			Status:     appsv1.StatefulSetStatus{ReadyReplicas: 3},
		}
		r := newReconciler(hcp, defaultSTS, shardSTS)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeTrue())
		g.Expect(msg).To(BeEmpty())
	})

	t.Run("When a shard StatefulSet is not ready it should return unhealthy", func(t *testing.T) {
		g := NewWithT(t)
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 3,
				Storage:  hyperv1.ManagedEtcdShardStorageSpec{Type: hyperv1.PersistentVolumeEtcdShardStorage},
			}},
		}
		defaultSTS := newEtcdStatefulSet(3, 3)
		shardSTS := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "etcd-events", Namespace: testHCPNamespace},
			Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To(int32(3))},
			Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
		}
		r := newReconciler(hcp, defaultSTS, shardSTS)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeFalse())
		g.Expect(msg).To(ContainSubstring("etcd-events"))
		g.Expect(msg).To(ContainSubstring("1/3"))
	})

	// Scaled-to-zero must not be reported as healthy: desired == 0 makes the
	// ready-replica comparison trivially true, but the snapshot init container
	// would target pods that do not exist and the backup Job would fail.
	t.Run("When a shard StatefulSet is scaled to zero it should return unhealthy", func(t *testing.T) {
		g := NewWithT(t)
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 3,
				Storage:  hyperv1.ManagedEtcdShardStorageSpec{Type: hyperv1.PersistentVolumeEtcdShardStorage},
			}},
		}
		defaultSTS := newEtcdStatefulSet(3, 3)
		shardSTS := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "etcd-events", Namespace: testHCPNamespace},
			Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To(int32(0))},
			Status:     appsv1.StatefulSetStatus{ReadyReplicas: 0},
		}
		r := newReconciler(hcp, defaultSTS, shardSTS)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeFalse())
		g.Expect(msg).To(ContainSubstring("etcd-events"))
		g.Expect(msg).To(ContainSubstring("scaled to zero"))
	})

	// A nil Replicas pointer defaults to 1, so it must never be treated as
	// scaled-to-zero.
	t.Run("When a shard StatefulSet has no replicas set it should use the default of one", func(t *testing.T) {
		g := NewWithT(t)
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 3,
				Storage:  hyperv1.ManagedEtcdShardStorageSpec{Type: hyperv1.PersistentVolumeEtcdShardStorage},
			}},
		}
		defaultSTS := newEtcdStatefulSet(3, 3)
		shardSTS := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "etcd-events", Namespace: testHCPNamespace},
			// Replicas deliberately left nil.
			Status: appsv1.StatefulSetStatus{ReadyReplicas: 0},
		}
		r := newReconciler(hcp, defaultSTS, shardSTS)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeFalse())
		g.Expect(msg).To(ContainSubstring("etcd-events"))
		g.Expect(msg).To(ContainSubstring("0/1"))
	})

	t.Run("When a shard StatefulSet is not found it should return unhealthy", func(t *testing.T) {
		g := NewWithT(t)
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 3,
				Storage:  hyperv1.ManagedEtcdShardStorageSpec{Type: hyperv1.PersistentVolumeEtcdShardStorage},
			}},
		}
		defaultSTS := newEtcdStatefulSet(3, 3)
		// No shard StatefulSet
		r := newReconciler(hcp, defaultSTS)

		healthy, msg, err := r.checkEtcdHealth(context.Background(), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(healthy).To(BeFalse())
		g.Expect(msg).To(ContainSubstring("etcd-events"))
		g.Expect(msg).To(ContainSubstring("not found"))
	})
}

func TestFindActiveJob(t *testing.T) {
	backup := newHCPEtcdBackup()

	newJob := func(name, backupName string, conditions ...batchv1.JobCondition) *batchv1.Job {
		return &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelHCPNamespace: testHCPNamespace,
					LabelBackupName:   backupName,
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
				Conditions: conditions,
			},
		}
	}

	t.Run("When no active jobs exist it should return nil", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()

		job, err := r.findActiveJob(context.Background(), backup, testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(job).To(BeNil())
	})

	t.Run("When active job exists it should return it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		activeJob := newJob("etcd-backup-active", "other-backup")
		activeJob.Status.Active = 1
		r := newReconciler(activeJob)

		job, err := r.findActiveJob(context.Background(), backup, testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(job).ToNot(BeNil())
		g.Expect(job.Name).To(Equal("etcd-backup-active"))
	})

	t.Run("When a job was just created and has no status yet it should return it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		// Simulates the window between Job creation and the job controller
		// setting status.active: no conditions, Active == 0. The serialization
		// guard must still treat this Job as active.
		freshJob := newJob("etcd-backup-fresh", "other-backup")
		r := newReconciler(freshJob)

		job, err := r.findActiveJob(context.Background(), backup, testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(job).ToNot(BeNil())
		g.Expect(job.Name).To(Equal("etcd-backup-fresh"))
	})

	t.Run("When the only job belongs to this backup it should return nil", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ownJob := newJob("etcd-backup-own", backup.Name)
		ownJob.Status.Active = 1
		r := newReconciler(ownJob)

		job, err := r.findActiveJob(context.Background(), backup, testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(job).To(BeNil())
	})

	t.Run("When terminated jobs exist they should not count as active", func(t *testing.T) {
		g := NewGomegaWithT(t)
		completed := newJob("etcd-backup-completed", "other-backup",
			batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue})
		failed := newJob("etcd-backup-failed", "another-backup",
			batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue})
		r := newReconciler(completed, failed)

		job, err := r.findActiveJob(context.Background(), backup, testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(job).To(BeNil())
	})

	t.Run("When a job is only visible to the API reader it should return it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		// Simulates informer cache lag: the Job was created against the API
		// server but is not yet in the cache the reconciler's Client reads from.
		cacheClient := fake.NewClientBuilder().
			WithScheme(newScheme()).
			WithStatusSubresource(&hyperv1.HCPEtcdBackup{}, &hyperv1.HostedControlPlane{}, &hyperv1.HostedCluster{}).
			Build()
		apiClient := fake.NewClientBuilder().
			WithScheme(newScheme()).
			WithObjects(newJob("etcd-backup-lagging", "other-backup")).
			Build()
		r := newReconciler()
		r.Client = cacheClient
		r.APIReader = apiClient

		job, err := r.findActiveJob(context.Background(), backup, testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(job).ToNot(BeNil())
		g.Expect(job.Name).To(Equal("etcd-backup-lagging"))
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
		r := newReconciler(backup, hcp, newHostedCluster(), pullSecret, credSecret)
		ctx := context.Background()

		creds := resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"}
		err := r.createBackupJob(ctx, backup, hcp, creds)
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
		// One container snapshots all shards concurrently via a shell script.
		g.Expect(snapshot.Command).To(HaveLen(3))
		g.Expect(snapshot.Command[0]).To(Equal("/bin/sh"))
		g.Expect(snapshot.Command[2]).To(ContainSubstring("/usr/bin/etcdctl"))
		g.Expect(snapshot.Command[2]).To(ContainSubstring(`snap 'default' 'https://etcd-client.` + testHCPNamespace + `.svc:2379' &`))
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
		r := newReconciler(backup, hcp, newHostedCluster(), pullSecret)
		ctx := context.Background()

		creds := resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"}
		err := r.createBackupJob(ctx, backup, hcp, creds)
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
		r := newReconciler(backup, hcp, newHostedCluster(), pullSecret, credSecret)
		ctx := context.Background()

		creds := resolvedCredentials{Mode: credentialModeAzureClientSecret, SecretName: "azure-creds"}
		err := r.createBackupJob(ctx, backup, hcp, creds)
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

		creds := resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"}
		err := r.createBackupJob(ctx, backup, hcp, creds)
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
		r := newReconciler(backup, hcp, newHostedCluster(), pullSecret, credSecret)
		ctx := context.Background()

		creds := resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"}
		err := r.createBackupJob(ctx, backup, hcp, creds)
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		upload := jobList.Items[0].Spec.Template.Spec.Containers[0]
		g.Expect(upload.Command).To(ContainElements("--aws-kms-key-arn", "arn:aws:kms:us-east-1:123456789012:key/test-key"))
	})

	t.Run("When AWS STS mode it should create Job with projected token volume and env vars", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
		}
		r := newReconciler(backup, hcp, newHostedCluster(), pullSecret, credSecret)
		ctx := context.Background()

		creds := resolvedCredentials{
			Mode:       credentialModeAWSSTS,
			SecretName: "aws-creds",
			RoleARN:    "arn:aws:iam::123456789012:role/etcd-backup",
		}
		err := r.createBackupJob(ctx, backup, hcp, creds)
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		podSpec := jobList.Items[0].Spec.Template.Spec

		// Should NOT have credentials volume
		for _, v := range podSpec.Volumes {
			g.Expect(v.Name).ToNot(Equal(volumeCredentials), "credentials volume should not be present in STS mode")
		}

		// Should have projected token volume
		var tokenVolume *corev1.Volume
		for i := range podSpec.Volumes {
			if podSpec.Volumes[i].Name == volumeAWSIAMToken {
				tokenVolume = &podSpec.Volumes[i]
				break
			}
		}
		g.Expect(tokenVolume).ToNot(BeNil(), "projected token volume should be present")
		g.Expect(tokenVolume.Projected).ToNot(BeNil())
		g.Expect(tokenVolume.Projected.Sources).To(HaveLen(1))
		g.Expect(tokenVolume.Projected.Sources[0].ServiceAccountToken.Audience).To(Equal("sts.amazonaws.com"))

		// Upload container env vars
		upload := podSpec.Containers[0]
		g.Expect(upload.Env).To(ContainElements(
			corev1.EnvVar{Name: "AWS_ROLE_ARN", Value: "arn:aws:iam::123456789012:role/etcd-backup"},
			corev1.EnvVar{Name: "AWS_WEB_IDENTITY_TOKEN_FILE", Value: mountPathAWSIAMToken + "/token"},
		))

		// Upload container should mount token volume, not credentials
		var hasCreds, hasToken bool
		for _, m := range upload.VolumeMounts {
			if m.Name == volumeCredentials {
				hasCreds = true
			}
			if m.Name == volumeAWSIAMToken {
				hasToken = true
			}
		}
		g.Expect(hasCreds).To(BeFalse(), "credentials mount should not be present in STS mode")
		g.Expect(hasToken).To(BeTrue(), "token mount should be present in STS mode")

		// Upload args should NOT have --credentials-file
		g.Expect(upload.Command).ToNot(ContainElement("--credentials-file"))
	})

	t.Run("When Azure Workload Identity mode it should create Job with WI pod label and no credentials volume", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			ObjectMeta: metav1.ObjectMeta{Name: testBackupName, Namespace: testHCPNamespace},
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
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds", Namespace: testHONamespace},
		}
		r := newReconciler(backup, hcp, newHostedCluster(), pullSecret, credSecret)
		ctx := context.Background()

		creds := resolvedCredentials{
			Mode:       credentialModeAzureWorkloadIdentity,
			SecretName: "azure-creds",
			ClientID:   "client-789",
		}
		err := r.createBackupJob(ctx, backup, hcp, creds)
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		podSpec := jobList.Items[0].Spec.Template.Spec

		// Should NOT have credentials volume
		for _, v := range podSpec.Volumes {
			g.Expect(v.Name).ToNot(Equal(volumeCredentials), "credentials volume should not be present in WI mode")
		}

		// Pod template should have WI label
		podLabels := jobList.Items[0].Spec.Template.Labels
		g.Expect(podLabels["azure.workload.identity/use"]).To(Equal("true"))

		// Upload container should NOT mount credentials
		upload := podSpec.Containers[0]
		for _, m := range upload.VolumeMounts {
			g.Expect(m.Name).ToNot(Equal(volumeCredentials), "credentials mount should not be present in WI mode")
		}

		// Upload args should NOT have --credentials-file or --azure-auth-type
		g.Expect(upload.Command).ToNot(ContainElement("--credentials-file"))
		g.Expect(upload.Command).ToNot(ContainElement("--azure-auth-type"))
	})

	t.Run("When Azure managed-identity mode it should include credentials-file and azure-auth-type managed-identity", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			ObjectMeta: metav1.ObjectMeta{Name: testBackupName, Namespace: testHCPNamespace},
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
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds", Namespace: testHONamespace},
		}
		r := newReconciler(backup, hcp, newHostedCluster(), pullSecret, credSecret)
		ctx := context.Background()

		creds := resolvedCredentials{Mode: credentialModeAzureManagedIdentity, SecretName: "azure-creds"}
		err := r.createBackupJob(ctx, backup, hcp, creds)
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		upload := jobList.Items[0].Spec.Template.Spec.Containers[0]
		g.Expect(upload.Command).To(ContainElements("--credentials-file"))
		g.Expect(upload.Command).To(ContainElements("--azure-auth-type", "managed-identity"))

		// Pod template should NOT have WI label
		podLabels := jobList.Items[0].Spec.Template.Labels
		g.Expect(podLabels).ToNot(HaveKey("azure.workload.identity/use"))
	})

	t.Run("When creating a backup Job it should record expected shards, attempt timestamp arg, and attempt key prefix", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("fake-creds")},
		}
		r := newReconciler(backup, hcp, newHostedCluster(), pullSecret, credSecret)
		ctx := context.Background()

		creds := resolvedCredentials{Mode: credentialModeAWSStatic, SecretName: "aws-creds"}
		err := r.createBackupJob(ctx, backup, hcp, creds)
		g.Expect(err).ToNot(HaveOccurred())

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))
		job := &jobList.Items[0]

		// The expected shard set is recorded on the Job for the completion path.
		g.Expect(job.Annotations[annotationExpectedShardSnapshots]).To(Equal("default"))

		// The upload container receives the attempt timestamp used in object keys.
		var uploadArgs []string
		for _, c := range job.Spec.Template.Spec.Containers {
			if c.Name == "upload" {
				uploadArgs = c.Command
			}
		}
		g.Expect(uploadArgs).To(ContainElement("--attempt-timestamp"))
		ts, err := strconv.ParseInt(uploadArgs[indexOf(uploadArgs, "--attempt-timestamp")+1], 10, 64)
		g.Expect(err).ToNot(HaveOccurred())

		// The status records the exact object key prefix for the attempt.
		g.Expect(backup.Status.AttemptKeyPrefix).To(Equal(fmt.Sprintf("backups/test/%d", ts)))
	})
}

func TestEnsureServiceAccountWithCredentials(t *testing.T) {
	t.Run("When Azure Workload Identity mode and SA has pre-configured annotation it should preserve it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		preConfiguredSA := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobServiceAccountName,
				Namespace: testHONamespace,
				Annotations: map[string]string{
					"azure.workload.identity/client-id": "preconfigured-client-id",
				},
			},
		}
		r := newReconciler(preConfiguredSA)
		ctx := context.Background()

		creds := resolvedCredentials{
			Mode:     credentialModeAzureWorkloadIdentity,
			ClientID: "different-client-id-from-secret",
		}
		g.Expect(r.ensureServiceAccount(ctx, creds)).To(Succeed())

		sa := &corev1.ServiceAccount{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: jobServiceAccountName, Namespace: testHONamespace}, sa)).To(Succeed())
		g.Expect(sa.Annotations["azure.workload.identity/client-id"]).To(Equal("preconfigured-client-id"))
	})

	t.Run("When Azure Workload Identity mode and SA has no annotation it should set it from credential secret", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()
		ctx := context.Background()

		creds := resolvedCredentials{
			Mode:     credentialModeAzureWorkloadIdentity,
			ClientID: "client-789",
		}
		g.Expect(r.ensureServiceAccount(ctx, creds)).To(Succeed())

		sa := &corev1.ServiceAccount{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: jobServiceAccountName, Namespace: testHONamespace}, sa)).To(Succeed())
		g.Expect(sa.Annotations["azure.workload.identity/client-id"]).To(Equal("client-789"))
	})

	t.Run("When non-WI mode it should not annotate the ServiceAccount", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := newReconciler()
		ctx := context.Background()

		creds := resolvedCredentials{Mode: credentialModeAWSStatic}
		g.Expect(r.ensureServiceAccount(ctx, creds)).To(Succeed())

		sa := &corev1.ServiceAccount{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: jobServiceAccountName, Namespace: testHONamespace}, sa)).To(Succeed())
		g.Expect(sa.Annotations).ToNot(HaveKey("azure.workload.identity/client-id"))
	})

	t.Run("When switching from WI to static mode it should remove the annotation", func(t *testing.T) {
		g := NewGomegaWithT(t)
		preConfiguredSA := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobServiceAccountName,
				Namespace: testHONamespace,
				Annotations: map[string]string{
					"azure.workload.identity/client-id": "client-789",
				},
			},
		}
		r := newReconciler(preConfiguredSA)
		ctx := context.Background()

		// First: verify WI preserves annotation
		wiCreds := resolvedCredentials{Mode: credentialModeAzureWorkloadIdentity, ClientID: "ignored"}
		g.Expect(r.ensureServiceAccount(ctx, wiCreds)).To(Succeed())

		// Then: update with static
		staticCreds := resolvedCredentials{Mode: credentialModeAWSStatic}
		g.Expect(r.ensureServiceAccount(ctx, staticCreds)).To(Succeed())

		sa := &corev1.ServiceAccount{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: jobServiceAccountName, Namespace: testHONamespace}, sa)).To(Succeed())
		g.Expect(sa.Annotations).ToNot(HaveKey("azure.workload.identity/client-id"))
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
		r := newReconciler(backup, hcp, newHostedCluster(), sts, pullSecret, credSecret)
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

		// Verify HCP condition was set
		updatedHCP := &hyperv1.HostedControlPlane{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testHCPName, Namespace: testHCPNamespace}, updatedHCP)).To(Succeed())
		hcpCond := meta.FindStatusCondition(updatedHCP.Status.Conditions, string(hyperv1.EtcdBackupSucceeded))
		g.Expect(hcpCond).ToNot(BeNil())
		g.Expect(hcpCond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(hcpCond.Reason).To(Equal(hyperv1.BackupInProgressReason))
	})
}

func TestHCPEtcdBackupReconciler_UpdateHostedClusterBackupURLs(t *testing.T) {
	newCompletedBackup := func() *hyperv1.HCPEtcdBackup {
		backup := newHCPEtcdBackup()
		backup.Status.SnapshotURL = "s3://bucket/default.db"
		backup.Status.ShardSnapshots = []hyperv1.HCPEtcdShardSnapshot{
			{Name: "default", SnapshotURL: "s3://bucket/default.db"},
			{Name: "events", SnapshotURL: "s3://bucket/events.db"},
		}
		return backup
	}

	t.Run("When HCP has HostedClusterAnnotation it should update HC status with the snapshot URL and the per-shard mapping", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		r := newReconciler(hcp, hc)

		err := r.updateHostedClusterBackupURLs(context.Background(), hcp, newCompletedBackup())
		g.Expect(err).ToNot(HaveOccurred())

		updatedHC := &hyperv1.HostedCluster{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testHCName, Namespace: testHCNamespace}, updatedHC)).To(Succeed())
		g.Expect(updatedHC.Status.LastSuccessfulEtcdBackupURL).To(Equal("s3://bucket/default.db"))
		g.Expect(updatedHC.Status.LastSuccessfulEtcdShardBackupURLs).To(ConsistOf(
			hyperv1.HCPEtcdShardSnapshot{Name: "default", SnapshotURL: "s3://bucket/default.db"},
			hyperv1.HCPEtcdShardSnapshot{Name: "events", SnapshotURL: "s3://bucket/events.db"},
		))
	})

	t.Run("When HCP is missing HostedClusterAnnotation it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		hcp.Annotations = nil
		r := newReconciler(hcp)

		err := r.updateHostedClusterBackupURLs(context.Background(), hcp, newCompletedBackup())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("missing"))
	})

	t.Run("When HostedCluster does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		r := newReconciler(hcp) // no HC in the fake client

		err := r.updateHostedClusterBackupURLs(context.Background(), hcp, newCompletedBackup())
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

		err := r.updateHostedClusterBackupURLs(context.Background(), hcp, newCompletedBackup())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(callCount).To(Equal(2))

		updatedHC := &hyperv1.HostedCluster{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testHCName, Namespace: testHCNamespace}, updatedHC)).To(Succeed())
		g.Expect(updatedHC.Status.LastSuccessfulEtcdBackupURL).To(Equal("s3://bucket/default.db"))
		g.Expect(updatedHC.Status.LastSuccessfulEtcdShardBackupURLs).To(HaveLen(2))
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
		name         string
		backup       *hyperv1.HCPEtcdBackup
		objects      []client.Object
		expectDone   bool
		expectError  bool
		expectReason string
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
			name:         "When credential secret is missing, it should wait (non-terminal) and return done=true",
			backup:       newHCPEtcdBackup(),
			objects:      []client.Object{},
			expectDone:   true,
			expectReason: hyperv1.BackupWaitingForCredentialsReason,
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
			objects:      []client.Object{},
			expectDone:   true,
			expectReason: hyperv1.BackupFailedReason,
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

			if tt.expectReason != "" {
				updated := &hyperv1.HCPEtcdBackup{}
				g.Expect(r.Get(t.Context(), types.NamespacedName{Name: tt.backup.Name, Namespace: tt.backup.Namespace}, updated)).To(Succeed())
				cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Reason).To(Equal(tt.expectReason))
			}
		})
	}
}

func TestResolveControlPlaneOperatorImage(t *testing.T) {
	t.Run("When the HostedCluster has the ControlPlaneOperatorImage annotation it should use the override", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		hc.Annotations = map[string]string{hyperv1.ControlPlaneOperatorImageAnnotation: "quay.io/custom/cpo:override"}
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		r := newReconciler(hcp, hc, pullSecret)

		image, err := r.resolveControlPlaneOperatorImage(context.Background(), hcp, pullSecret.Data[corev1.DockerConfigJsonKey])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(image).To(Equal("quay.io/custom/cpo:override"))
	})

	t.Run("When no annotation is set it should resolve from the release payload", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		r := newReconciler(hcp, hc)

		image, err := r.resolveControlPlaneOperatorImage(context.Background(), hcp, []byte(`{"auths":{}}`))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(image).To(Equal(testCPOImage))
	})

	t.Run("When the release payload has no hypershift component it should fall back to the HO image", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		r := newReconciler(hcp, hc)
		r.ReleaseProvider = &emptyReleaseProvider{}

		image, err := r.resolveControlPlaneOperatorImage(context.Background(), hcp, []byte(`{"auths":{}}`))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(image).To(Equal("quay.io/hypershift/hypershift:latest"))
	})

	t.Run("When the HostedCluster cannot be found it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := newHostedControlPlane()
		// No HostedCluster in the fake client.
		r := newReconciler(hcp)

		_, err := r.resolveControlPlaneOperatorImage(context.Background(), hcp, []byte(`{"auths":{}}`))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to look up HostedCluster"))
	})
}

type emptyReleaseProvider struct{}

func (f *emptyReleaseProvider) Lookup(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
	return &releaseinfo.ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.16.0"},
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{},
			},
		},
	}, nil
}

func (f *emptyReleaseProvider) GetRegistryOverrides() map[string]string { return nil }
func (f *emptyReleaseProvider) GetOpenShiftImageRegistryOverrides() map[string][]string {
	return nil
}
func (f *emptyReleaseProvider) GetMirroredReleaseImage() string { return "" }

func TestCreateResourcesAndJob(t *testing.T) {
	t.Run("When createBackupJob fails with NotFound it should cleanup resources and set BackupFailed", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("fake-creds")},
		}
		// No pull secret — createBackupJob will fail with NotFound
		r := newReconciler(backup, hcp, credSecret)
		ctx := context.Background()

		result, err := r.createResourcesAndJob(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions).To(HaveLen(1))
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupFailedReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("pull secret"))
	})

	t.Run("When credential secret is not found it should wait (non-terminal) and requeue", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		r := newReconciler(backup, hcp) // no credential secret
		ctx := context.Background()

		result, err := r.createResourcesAndJob(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{RequeueAfter: credentialWaitRequeueInterval}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupWaitingForCredentialsReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("credential Secret"))
		g.Expect(isTerminal(updated)).To(BeFalse())
	})

	t.Run("When credential secret is not found past the wait deadline it should fail terminally", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		// Created longer ago than credentialWaitTimeout so the wait deadline is exceeded.
		backup.CreationTimestamp = metav1.NewTime(time.Now().Add(-(credentialWaitTimeout + time.Minute)))
		hcp := newHostedControlPlane()
		r := newReconciler(backup, hcp) // no credential secret
		ctx := context.Background()

		result, err := r.createResourcesAndJob(ctx, backup, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupFailedReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("timed out"))
		g.Expect(isTerminal(updated)).To(BeTrue())
	})
}

func TestReconcileRecoversWhenCredentialSecretAppears(t *testing.T) {
	t.Run("When the credential Secret is missing then created it should wait then proceed without failing terminally", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		// Credential Secret intentionally absent for the first reconcile.
		r := newReconciler(backup, hcp, newHostedCluster(), sts, pullSecret)
		ctx := context.Background()

		req := ctrl.Request{NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}}

		// First reconcile: credential Secret not yet in cache -> wait, non-terminal, requeue.
		result, err := r.Reconcile(ctx, req)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{RequeueAfter: credentialWaitRequeueInterval}))

		waiting := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, req.NamespacedName, waiting)).To(Succeed())
		g.Expect(waiting.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupWaitingForCredentialsReason))
		g.Expect(isTerminal(waiting)).To(BeFalse())

		// The credential Secret lands.
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("fake-creds")},
		}
		g.Expect(r.Create(ctx, credSecret)).To(Succeed())

		// Second reconcile: prerequisites satisfied -> Job created, backup in progress.
		result, err = r.Reconcile(ctx, req)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, req.NamespacedName, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupInProgressReason))

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))
	})
}

func TestBackupsForCredentialSecret(t *testing.T) {
	g := NewGomegaWithT(t)

	// Non-terminal backup referencing credential Secret "aws-creds".
	waitingBackup := newHCPEtcdBackup()

	// Terminal (failed) backup referencing the same credential Secret name.
	terminalBackup := newHCPEtcdBackup()
	terminalBackup.Name = "terminal-backup"
	terminalBackup.Status.Conditions = []metav1.Condition{{
		Type:   string(hyperv1.BackupCompleted),
		Status: metav1.ConditionFalse,
		Reason: hyperv1.BackupFailedReason,
	}}

	r := newReconciler(waitingBackup, terminalBackup)
	ctx := context.Background()

	newSecret := func(name, namespace string) *corev1.Secret {
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	}

	t.Run("When a matching credential Secret lands in the operator namespace it enqueues only the non-terminal backup", func(t *testing.T) {
		requests := r.backupsForCredentialSecret(ctx, newSecret("aws-creds", testHONamespace))
		g.Expect(requests).To(ConsistOf(reconcile.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		}))
	})

	t.Run("When the Secret name does not match any backup it enqueues nothing", func(t *testing.T) {
		requests := r.backupsForCredentialSecret(ctx, newSecret("other-creds", testHONamespace))
		g.Expect(requests).To(BeEmpty())
	})

	t.Run("When the Secret is in a different namespace it enqueues nothing", func(t *testing.T) {
		requests := r.backupsForCredentialSecret(ctx, newSecret("aws-creds", testHCPNamespace))
		g.Expect(requests).To(BeEmpty())
	})
}

func TestCleanupResourcesWithDeleteErrors(t *testing.T) {
	t.Run("When delete returns a real error it should propagate the first error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()

		np := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: NetworkPolicyName, Namespace: testHCPNamespace},
		}

		s := newScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(backup, np).
			WithStatusSubresource(&hyperv1.HCPEtcdBackup{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*networkingv1.NetworkPolicy); ok {
						return fmt.Errorf("simulated delete failure")
					}
					return cl.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		r := &HCPEtcdBackupReconciler{
			Client:            fakeClient,
			OperatorNamespace: testHONamespace,
		}

		err := r.cleanupResources(context.Background(), backup)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("simulated delete failure"))
	})
}

func TestHandleJobStatusEdgeCases(t *testing.T) {
	t.Run("When Job succeeds but result extraction fails it should requeue and retry, then fail terminally after bounded attempts", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		job := newTestJob(batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		})

		s := newScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(backup, job, hcp, hc).
			WithStatusSubresource(&hyperv1.HCPEtcdBackup{}, &hyperv1.HostedControlPlane{}, &hyperv1.HostedCluster{}).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*corev1.PodList); ok {
						return fmt.Errorf("simulated pod list failure")
					}
					return cl.List(ctx, list, opts...)
				},
			}).
			Build()

		r := &HCPEtcdBackupReconciler{
			Client:            fakeClient,
			OperatorNamespace: testHONamespace,
		}

		// The first extraction failure requeues with the attempt recorded...
		result, err := r.handleJobStatus(context.Background(), backup, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(resultExtractionRetryInterval))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Annotations[annotationResultExtractionAttempts]).To(Equal("1"))
		g.Expect(meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))).To(BeNil())

		// ...and repeated failures up to the bound fail the backup terminally.
		for range maxResultExtractionAttempts - 2 {
			_, err := r.handleJobStatus(context.Background(), updated, job, hcp)
			g.Expect(err).ToNot(HaveOccurred())
		}
		_, err = r.handleJobStatus(context.Background(), updated, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(hyperv1.BackupResultExtractionFailedReason))
		g.Expect(cond.Message).To(ContainSubstring("simulated pod list failure"))
		g.Expect(updated.Status.SnapshotURL).To(BeEmpty())

		// The HostedCluster must not have been updated with a URL.
		updatedHC := &hyperv1.HostedCluster{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testHCName, Namespace: testHCNamespace}, updatedHC)).To(Succeed())
		g.Expect(updatedHC.Status.LastSuccessfulEtcdBackupURL).To(BeEmpty())
	})

	t.Run("When Job succeeds with no termination message it should requeue and retry, never recording success", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		job := newTestJob(batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		})
		// Pod exists but upload container has no termination message
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "backup-pod", Namespace: testHONamespace,
				Labels: map[string]string{"batch.kubernetes.io/job-name": "etcd-backup-test"},
			},
			Spec: corev1.PodSpec{
				Containers:    []corev1.Container{{Name: "upload", Image: "test:latest"}},
				RestartPolicy: corev1.RestartPolicyNever,
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "upload", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
				},
			},
		}
		r := newReconciler(backup, job, hcp, hc, pod)

		result, err := r.handleJobStatus(context.Background(), backup, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(resultExtractionRetryInterval))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		// No success was recorded and the HC was not updated.
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).To(BeNil())
		g.Expect(updated.Status.SnapshotURL).To(BeEmpty())

		updatedHC := &hyperv1.HostedCluster{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testHCName, Namespace: testHCNamespace}, updatedHC)).To(Succeed())
		g.Expect(updatedHC.Status.LastSuccessfulEtcdBackupURL).To(BeEmpty())
	})

	t.Run("When results are incomplete it should fail terminally without recording success", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		// The Job was built for two shards...
		job := newTestJob(batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		})
		job.Annotations = map[string]string{annotationExpectedShardSnapshots: "default,events"}
		// ...but the termination message only reports the default shard.
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "backup-pod", Namespace: testHONamespace,
				Labels: map[string]string{"batch.kubernetes.io/job-name": "etcd-backup-test"},
			},
			Spec: corev1.PodSpec{
				Containers:    []corev1.Container{{Name: "upload", Image: "test:latest"}},
				RestartPolicy: corev1.RestartPolicyNever,
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "upload", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 0,
						Message:  `[{"name":"default","url":"s3://bucket/default.db"}]`,
					}}},
				},
			},
		}
		r := newReconciler(backup, job, hcp, pod)

		result, err := r.handleJobStatus(context.Background(), backup, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(hyperv1.BackupResultIncompleteReason))
		g.Expect(cond.Message).To(ContainSubstring(`missing result for shard "events"`))
		g.Expect(updated.Status.SnapshotURL).To(BeEmpty())
	})

	t.Run("When results contain an unexpected shard it should fail terminally", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		job := newTestJob(batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		})
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "backup-pod", Namespace: testHONamespace,
				Labels: map[string]string{"batch.kubernetes.io/job-name": "etcd-backup-test"},
			},
			Spec: corev1.PodSpec{
				Containers:    []corev1.Container{{Name: "upload", Image: "test:latest"}},
				RestartPolicy: corev1.RestartPolicyNever,
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "upload", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 0,
						Message:  `[{"name":"default","url":"s3://bucket/default.db"},{"name":"unknown","url":"s3://bucket/other.db"}]`,
					}}},
				},
			},
		}
		r := newReconciler(backup, job, hcp, pod)

		_, err := r.handleJobStatus(context.Background(), backup, job, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(context.Background(), types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.BackupCompleted))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Reason).To(Equal(hyperv1.BackupResultIncompleteReason))
		g.Expect(cond.Message).To(ContainSubstring(`unexpected result for shard "unknown"`))
	})
}
func TestReconcileTerminalCleansUpAndEnforcesRetention(t *testing.T) {
	t.Run("When backup is terminal it should cleanup RBAC and NetworkPolicy and enforce retention", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctx := context.Background()

		baseTime := time.Now().Add(-7 * time.Hour)

		// The backup being reconciled is terminal (succeeded)
		backup := newHCPEtcdBackup()
		backup.ObjectMeta.CreationTimestamp = metav1.NewTime(baseTime.Add(6 * time.Hour))
		backup.Status.Conditions = []metav1.Condition{
			{
				Type:   string(hyperv1.BackupCompleted),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.BackupSucceededReason,
			},
		}

		// 6 older completed backups (total 7 with above, max is 5 → should delete 2 oldest)
		var objs []client.Object
		objs = append(objs, backup)
		for i := range 6 {
			b := &hyperv1.HCPEtcdBackup{
				ObjectMeta: metav1.ObjectMeta{
					Name:              fmt.Sprintf("old-backup-%d", i),
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
			objs = append(objs, b)
		}

		// Shared resources that should be cleaned up (no other non-terminal backup)
		np := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: NetworkPolicyName, Namespace: testHCPNamespace},
		}
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: RBACName, Namespace: testHCPNamespace},
		}
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: RBACName, Namespace: testHCPNamespace},
		}
		objs = append(objs, np, role, rb)

		r := newReconciler(objs...)
		r.MaxBackupCount = 5

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		// RBAC and NetworkPolicy should be gone
		g.Expect(r.Get(ctx, types.NamespacedName{Name: NetworkPolicyName, Namespace: testHCPNamespace}, &networkingv1.NetworkPolicy{})).ToNot(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.Role{})).ToNot(Succeed())
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, &rbacv1.RoleBinding{})).ToNot(Succeed())

		// Retention: should have kept only 5 completed backups
		remaining := &hyperv1.HCPEtcdBackupList{}
		g.Expect(r.List(ctx, remaining, client.InNamespace(testHCPNamespace))).To(Succeed())
		g.Expect(remaining.Items).To(HaveLen(5))
	})
}

func TestReconcileMonitorsExistingJob(t *testing.T) {
	t.Run("When backup has an existing completed Job it should set BackupSucceeded via Reconcile", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		hc := newHostedCluster()
		sts := newEtcdStatefulSet(3, 3)
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("fake-creds")},
		}

		completedJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-backup-" + testBackupName,
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelBackupName:   testBackupName,
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
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
				},
			},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "backup-pod", Namespace: testHONamespace,
				Labels: map[string]string{"batch.kubernetes.io/job-name": "etcd-backup-" + testBackupName},
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
								Message:  "s3://bucket/snapshot.db",
							},
						},
					},
				},
			},
		}

		r := newReconciler(backup, hcp, hc, sts, credSecret, completedJob, pod)
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupSucceededReason))
		g.Expect(updated.Status.SnapshotURL).To(Equal("s3://bucket/snapshot.db"))
	})

	t.Run("When backup has an existing failed Job it should set BackupFailed via Reconcile", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("fake-creds")},
		}

		failedJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-backup-" + testBackupName,
				Namespace: testHONamespace,
				Labels: map[string]string{
					LabelApp:          LabelName,
					LabelBackupName:   testBackupName,
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
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Message: "BackoffLimitExceeded"},
				},
			},
		}

		r := newReconciler(backup, hcp, sts, credSecret, failedJob)
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		updated := &hyperv1.HCPEtcdBackup{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace}, updated)).To(Succeed())
		g.Expect(updated.Status.Conditions[0].Reason).To(Equal(hyperv1.BackupFailedReason))
		g.Expect(updated.Status.Conditions[0].Message).To(ContainSubstring("BackoffLimitExceeded"))
	})
}

func TestReconcileHappyPathWithSTS(t *testing.T) {
	t.Run("When AWS STS credentials it should create Job with projected token via full Reconcile", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		hcp := newHostedControlPlane()
		sts := newEtcdStatefulSet(3, 3)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: testHONamespace},
			Data:       map[string][]byte{"credentials": []byte("arn:aws:iam::123456789012:role/etcd-backup")},
		}
		r := newReconciler(backup, hcp, newHostedCluster(), sts, pullSecret, credSecret)
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))

		podSpec := jobList.Items[0].Spec.Template.Spec
		// Should have projected token volume, not credentials
		var hasToken, hasCreds bool
		for _, v := range podSpec.Volumes {
			if v.Name == volumeAWSIAMToken {
				hasToken = true
			}
			if v.Name == volumeCredentials {
				hasCreds = true
			}
		}
		g.Expect(hasToken).To(BeTrue())
		g.Expect(hasCreds).To(BeFalse())

		upload := podSpec.Containers[0]
		g.Expect(upload.Env).To(ContainElement(corev1.EnvVar{
			Name:  "AWS_ROLE_ARN",
			Value: "arn:aws:iam::123456789012:role/etcd-backup",
		}))
	})
}

func TestReconcileHappyPathWithAzureWI(t *testing.T) {
	t.Run("When Azure Workload Identity credentials it should annotate SA and create Job via full Reconcile", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := &hyperv1.HCPEtcdBackup{
			ObjectMeta: metav1.ObjectMeta{Name: testBackupName, Namespace: testHCPNamespace},
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
		sts := newEtcdStatefulSet(3, 3)
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: testHCPNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		}
		credSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds", Namespace: testHONamespace},
			Data: map[string][]byte{
				"cloud": []byte("AZURE_CLIENT_ID=my-client-id\nAZURE_TENANT_ID=my-tenant\n"),
			},
		}
		r := newReconciler(backup, hcp, newHostedCluster(), sts, pullSecret, credSecret)
		ctx := context.Background()

		result, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: testBackupName, Namespace: testHCPNamespace},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(requeueInterval))

		// Verify SA was annotated with the client ID
		sa := &corev1.ServiceAccount{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: jobServiceAccountName, Namespace: testHONamespace}, sa)).To(Succeed())
		g.Expect(sa.Annotations["azure.workload.identity/client-id"]).To(Equal("my-client-id"))

		// Verify Job has WI pod label
		jobList := &batchv1.JobList{}
		g.Expect(r.List(ctx, jobList, client.InNamespace(testHONamespace))).To(Succeed())
		g.Expect(jobList.Items).To(HaveLen(1))
		g.Expect(jobList.Items[0].Spec.Template.Labels["azure.workload.identity/use"]).To(Equal("true"))

		// Verify no credentials volume
		for _, v := range jobList.Items[0].Spec.Template.Spec.Volumes {
			g.Expect(v.Name).ToNot(Equal(volumeCredentials))
		}
	})
}

func TestEnsureRBACIdempotency(t *testing.T) {
	t.Run("When ensureRBAC is called twice it should not error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		r := newReconciler(backup)
		ctx := context.Background()

		g.Expect(r.ensureRBAC(ctx, backup)).To(Succeed())
		g.Expect(r.ensureRBAC(ctx, backup)).To(Succeed())

		role := &rbacv1.Role{}
		g.Expect(r.Get(ctx, types.NamespacedName{Name: RBACName, Namespace: testHCPNamespace}, role)).To(Succeed())
		g.Expect(role.Rules).To(HaveLen(2))
	})
}

func TestEnforceRetentionDeleteRace(t *testing.T) {
	t.Run("When an old backup was already deleted by another controller it should skip NotFound and succeed", func(t *testing.T) {
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

		deleteCount := 0
		s := newScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(backups...).
			WithStatusSubresource(&hyperv1.HCPEtcdBackup{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					deleteCount++
					if deleteCount == 1 {
						// Simulate race: backup-0 already deleted by another controller
						return apierrors.NewNotFound(
							schema.GroupResource{Group: "hypershift.openshift.io", Resource: "hcpetcdbackups"},
							obj.GetName(),
						)
					}
					return cl.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		r := &HCPEtcdBackupReconciler{
			Client:            fakeClient,
			OperatorNamespace: testHONamespace,
			MaxBackupCount:    5,
		}

		err := r.enforceRetention(context.Background(), testHCPNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(deleteCount).To(Equal(2))
	})
}

func TestGetShardSnapshotsFromPod(t *testing.T) {
	tests := []struct {
		name        string
		pods        []client.Object
		jobName     string
		expected    []hyperv1.HCPEtcdShardSnapshot
		expectError string
	}{
		{
			name: "When upload container has JSON termination message, it should return shard snapshots",
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
										Message: `[{"name":"default","url":"s3://bucket/path/default.db"},{"name":"events","url":"s3://bucket/path/events.db"}]`,
									},
								},
							},
						},
					},
				},
			},
			jobName: "my-job",
			expected: []hyperv1.HCPEtcdShardSnapshot{
				{Name: "default", SnapshotURL: "s3://bucket/path/default.db"},
				{Name: "events", SnapshotURL: "s3://bucket/path/events.db"},
			},
		},
		{
			name: "When upload container has plain URL, it should return backward-compat single snapshot",
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
			jobName: "my-job",
			expected: []hyperv1.HCPEtcdShardSnapshot{
				{Name: "default", SnapshotURL: "s3://bucket/path/snapshot.db"},
			},
		},
		{
			name:        "When no pods exist for the job, it should return an extraction error",
			pods:        []client.Object{},
			jobName:     "my-job",
			expectError: "no terminated upload container termination message found",
		},
		{
			name: "When upload container has no termination message, it should return an extraction error",
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
			jobName:     "my-job",
			expectError: "no terminated upload container termination message found",
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

			snapshots, err := r.getShardSnapshotsFromPod(t.Context(), job)
			if tt.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectError))
				g.Expect(snapshots).To(BeNil())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			if tt.expected == nil {
				g.Expect(snapshots).To(BeNil())
			} else {
				g.Expect(snapshots).To(Equal(tt.expected))
			}
		})
	}
}

func TestUpdateHCPBackupCondition(t *testing.T) {
	t.Parallel()

	t.Run("When patching a backup condition, it should not stomp unrelated status fields", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := newHostedControlPlane()
		hcp.Generation = 3
		hcp.Status.AutoNode = hyperv1.AutoNodeStatus{
			VCPUs: ptr.To[int32](8),
		}

		r := newReconciler(hcp)

		err := r.updateHCPBackupCondition(t.Context(), hcp, metav1.Condition{
			Type:    string(hyperv1.EtcdBackupSucceeded),
			Status:  metav1.ConditionTrue,
			Reason:  "BackupComplete",
			Message: "backup finished",
		})
		g.Expect(err).ToNot(HaveOccurred())

		var updated hyperv1.HostedControlPlane
		g.Expect(r.Get(t.Context(), types.NamespacedName{
			Name:      testHCPName,
			Namespace: testHCPNamespace,
		}, &updated)).To(Succeed())

		cond := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.EtcdBackupSucceeded))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(cond.Reason).To(Equal("BackupComplete"))
		g.Expect(cond.ObservedGeneration).To(Equal(int64(3)))

		g.Expect(updated.Status.AutoNode.VCPUs).To(Equal(ptr.To[int32](8)),
			"autoNode should be preserved — patch must not stomp unrelated fields")
	})

	t.Run("When patching a backup condition onto an HCP with existing conditions, it should preserve them", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := newHostedControlPlane()
		hcp.Status.Conditions = []metav1.Condition{
			{
				Type:   string(hyperv1.ClusterVersionAvailable),
				Status: metav1.ConditionTrue,
				Reason: "OK",
			},
		}

		r := newReconciler(hcp)

		err := r.updateHCPBackupCondition(t.Context(), hcp, metav1.Condition{
			Type:    string(hyperv1.EtcdBackupSucceeded),
			Status:  metav1.ConditionTrue,
			Reason:  "BackupComplete",
			Message: "backup finished",
		})
		g.Expect(err).ToNot(HaveOccurred())

		var updated hyperv1.HostedControlPlane
		g.Expect(r.Get(t.Context(), types.NamespacedName{
			Name:      testHCPName,
			Namespace: testHCPNamespace,
		}, &updated)).To(Succeed())

		g.Expect(updated.Status.Conditions).To(HaveLen(2),
			"both the existing condition and the new backup condition should be present")
		g.Expect(meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.ClusterVersionAvailable))).ToNot(BeNil())
		g.Expect(meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.EtcdBackupSucceeded))).ToNot(BeNil())
	})
}

func TestEtcdShards(t *testing.T) {
	t.Parallel()

	t.Run("When no shards are configured it should return only the default shard", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		hcp := newHostedControlPlane()
		shards := etcdShards(hcp)
		g.Expect(shards).To(HaveLen(1))
		g.Expect(shards[0].name).To(Equal("etcd"))
		g.Expect(shards[0].identity).To(Equal("default"))
		g.Expect(shards[0].endpoint).To(ContainSubstring("etcd-client."))
	})

	t.Run("When PV-backed shards are configured it should include them", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{
				{
					Name:     "events",
					Replicas: 3,
					Storage: hyperv1.ManagedEtcdShardStorageSpec{
						Type: hyperv1.PersistentVolumeEtcdShardStorage,
					},
				},
			},
		}
		shards := etcdShards(hcp)
		g.Expect(shards).To(HaveLen(2))
		g.Expect(shards[0].name).To(Equal("etcd"))
		g.Expect(shards[0].identity).To(Equal("default"))
		g.Expect(shards[1].name).To(Equal("etcd-events"))
		g.Expect(shards[1].identity).To(Equal("events"))
		g.Expect(shards[1].endpoint).To(ContainSubstring("etcd-client-events."))
	})

	t.Run("When EmptyDir shards are configured it should skip them", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		hcp := newHostedControlPlane()
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Shards: []hyperv1.ManagedEtcdShardSpec{
				{
					Name:     "events",
					Replicas: 1,
					Storage: hyperv1.ManagedEtcdShardStorageSpec{
						Type: hyperv1.EmptyDirEtcdShardStorage,
					},
				},
				{
					Name:     "leases",
					Replicas: 3,
					Storage: hyperv1.ManagedEtcdShardStorageSpec{
						Type: hyperv1.PersistentVolumeEtcdShardStorage,
					},
				},
			},
		}
		shards := etcdShards(hcp)
		g.Expect(shards).To(HaveLen(2))
		g.Expect(shards[0].name).To(Equal("etcd"))
		g.Expect(shards[0].identity).To(Equal("default"))
		g.Expect(shards[1].name).To(Equal("etcd-leases"))
		g.Expect(shards[1].identity).To(Equal("leases"))
	})
}

func TestBuildSnapshotInitContainers(t *testing.T) {
	t.Parallel()

	scriptFor := func(containers []corev1.Container) string {
		if len(containers) != 2 {
			return ""
		}
		return containers[1].Command[2]
	}

	t.Run("When single shard it should create fetch-certs + 1 parallel snapshot container", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		r := newReconciler()
		shards := []etcdShardInfo{{
			name:     "etcd",
			identity: "default",
			endpoint: "https://etcd-client.test-ns.svc:2379",
		}}

		containers := r.buildSnapshotInitContainers("cpo:latest", "etcd:latest", "test-ns", shards)
		g.Expect(containers).To(HaveLen(2))
		g.Expect(containers[0].Name).To(Equal("fetch-certs"))
		g.Expect(containers[1].Name).To(Equal("snapshot"))
		g.Expect(containers[1].Image).To(Equal("etcd:latest"))
		g.Expect(containers[1].Command[0]).To(Equal("/bin/sh"))
		script := scriptFor(containers)
		g.Expect(script).To(ContainSubstring("etcd-client.test-ns.svc"))
		g.Expect(script).To(ContainSubstring(`snap 'default' 'https://etcd-client.test-ns.svc:2379' &`))
	})

	t.Run("When multiple shards it should snapshot every shard from one container", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		r := newReconciler()
		shards := []etcdShardInfo{
			{name: "etcd", identity: "default", endpoint: "https://etcd-client.test-ns.svc:2379"},
			{name: "etcd-events", identity: "events", endpoint: "https://etcd-client-events.test-ns.svc:2379"},
			{name: "etcd-leases", identity: "leases", endpoint: "https://etcd-client-leases.test-ns.svc:2379"},
		}

		containers := r.buildSnapshotInitContainers("cpo:latest", "etcd:latest", "test-ns", shards)
		// One fetch-certs + one snapshot container regardless of shard count.
		g.Expect(containers).To(HaveLen(2))
		script := scriptFor(containers)
		// Every shard's snapshot command runs concurrently (backgrounded).
		for _, shard := range shards {
			g.Expect(script).To(ContainSubstring(fmt.Sprintf("snap '%s' '%s' &", shard.identity, shard.endpoint)))
		}
		// Snapshot files are named by shard identity.
		g.Expect(script).To(ContainSubstring(`"$name.db"`))
		// All snapshots are waited on, and any failure fails the container.
		g.Expect(script).To(ContainSubstring("wait\n"))
		g.Expect(script).To(ContainSubstring("snapshot failed"))
		g.Expect(script).To(ContainSubstring("exit 1"))
	})
}
func TestParseShardSnapshots(t *testing.T) {
	t.Parallel()

	t.Run("When JSON array is provided it should parse all shard snapshots", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		msg := `[{"name":"default","url":"s3://b/default.db"},{"name":"events","url":"s3://b/events.db"}]`
		snapshots, err := parseShardSnapshots(msg)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(snapshots).To(HaveLen(2))
		g.Expect(snapshots[0].Name).To(Equal("default"))
		g.Expect(snapshots[0].SnapshotURL).To(Equal("s3://b/default.db"))
		g.Expect(snapshots[1].Name).To(Equal("events"))
		g.Expect(snapshots[1].SnapshotURL).To(Equal("s3://b/events.db"))
	})

	t.Run("When plain URL is provided it should return single default shard", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		snapshots, err := parseShardSnapshots("s3://b/snapshot.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(snapshots).To(HaveLen(1))
		g.Expect(snapshots[0].Name).To(Equal("default"))
		g.Expect(snapshots[0].SnapshotURL).To(Equal("s3://b/snapshot.db"))
	})

	t.Run("When empty string is provided it should return nil", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		snapshots, err := parseShardSnapshots("")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(snapshots).To(BeNil())
	})

	t.Run("When invalid JSON is provided it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		_, err := parseShardSnapshots("[invalid")
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When JSON entries have invalid URLs they should be skipped", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		msg := `[{"name":"default","url":"s3://b/default.db"},{"name":"events","url":"ftp://b/events.db"},{"name":"logs","url":"not-a-url"},{"name":"","url":"s3://b/unnamed.db"}]`
		snapshots, err := parseShardSnapshots(msg)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(snapshots).To(HaveLen(1))
		g.Expect(snapshots[0].Name).To(Equal("default"))
		g.Expect(snapshots[0].SnapshotURL).To(Equal("s3://b/default.db"))
	})

	t.Run("When plain URL has an unsupported scheme it should return nil", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		snapshots, err := parseShardSnapshots("some stray log line from the upload container")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(snapshots).To(BeNil())
	})
}

func TestBackupJobDeadlineSeconds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		shards  int
		storage hyperv1.HCPEtcdBackupStorageType
		want    int64
	}{
		{"single shard S3 keeps the legacy 900s deadline", 1, hyperv1.S3BackupStorage, 900},
		{"11 shards S3 scale per shard", 11, hyperv1.S3BackupStorage, 900 + 120*10},
		{"single shard Azure gets the larger Azure base", 1, hyperv1.AzureBlobBackupStorage, 1200},
		{"3 shards Azure scale per shard", 3, hyperv1.AzureBlobBackupStorage, 1200 + 300*2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			shards := make([]etcdShardInfo, tt.shards)
			for i := range shards {
				shards[i] = etcdShardInfo{name: fmt.Sprintf("etcd-%d", i), identity: fmt.Sprintf("shard-%d", i)}
			}
			backup := newHCPEtcdBackup()
			backup.Spec.Storage.StorageType = tt.storage
			g.Expect(backupJobDeadlineSeconds(shards, backup)).To(Equal(tt.want))
		})
	}
}

func TestAttemptKeyPrefix(t *testing.T) {
	t.Parallel()
	t.Run("S3 with trailing slash in key prefix", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		backup.Spec.Storage.S3.KeyPrefix = "backups/"
		g.Expect(attemptKeyPrefix(backup, 1700000000)).To(Equal("backups/1700000000"))
	})
	t.Run("Azure key prefix", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		backup.Spec.Storage.StorageType = hyperv1.AzureBlobBackupStorage
		backup.Spec.Storage.AzureBlob.KeyPrefix = "backups"
		g.Expect(attemptKeyPrefix(backup, 1700000000)).To(Equal("backups/1700000000"))
	})
	t.Run("empty key prefix matches etcd-upload key construction", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		backup.Spec.Storage.S3.KeyPrefix = ""
		g.Expect(attemptKeyPrefix(backup, 1700000000)).To(Equal("/1700000000"))
	})
}

func TestValidateShardSnapshotResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		snapshots []hyperv1.HCPEtcdShardSnapshot
		expected  []string
		want      []string // substrings expected in the problems
	}{
		{
			name: "When the result set is complete, it should report no problems",
			snapshots: []hyperv1.HCPEtcdShardSnapshot{
				{Name: "etcd", SnapshotURL: "s3://b/etcd.db"},
				{Name: "etcd-events", SnapshotURL: "s3://b/etcd-events.db"},
			},
			expected: []string{"etcd", "etcd-events"},
		},
		{
			name: "When a shard result is missing, it should be reported",
			snapshots: []hyperv1.HCPEtcdShardSnapshot{
				{Name: "etcd", SnapshotURL: "s3://b/etcd.db"},
			},
			expected: []string{"etcd", "etcd-events"},
			want:     []string{`missing result for shard "etcd-events"`},
		},
		{
			name: "When shard results are duplicated, it should be reported",
			snapshots: []hyperv1.HCPEtcdShardSnapshot{
				{Name: "etcd", SnapshotURL: "s3://b/etcd.db"},
				{Name: "etcd", SnapshotURL: "s3://b/etcd-2.db"},
			},
			expected: []string{"etcd"},
			want:     []string{`duplicate results for shard "etcd"`},
		},
		{
			name: "When a shard result is unexpected, it should be reported",
			snapshots: []hyperv1.HCPEtcdShardSnapshot{
				{Name: "etcd", SnapshotURL: "s3://b/etcd.db"},
				{Name: "etcd-unknown", SnapshotURL: "s3://b/other.db"},
			},
			expected: []string{"etcd"},
			want:     []string{`unexpected result for shard "etcd-unknown"`},
		},
		{
			name:      "When the result set is empty, it should report every expected shard missing",
			snapshots: nil,
			expected:  []string{"etcd"},
			want:      []string{`missing result for shard "etcd"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			problems := validateShardSnapshotResults(tt.snapshots, tt.expected)
			if len(tt.want) == 0 {
				g.Expect(problems).To(BeEmpty())
				return
			}
			for _, want := range tt.want {
				g.Expect(problems).To(ContainElement(ContainSubstring(want)))
			}
		})
	}
}

func TestExpectedShardNames(t *testing.T) {
	t.Parallel()
	t.Run("annotation on the Job wins", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{annotationExpectedShardSnapshots: "default,events"},
		}}
		hcp := newHostedControlPlane()
		g.Expect(expectedShardNames(job, hcp)).To(Equal([]string{"default", "events"}))
	})
	t.Run("missing annotation falls back to the HCP shard list", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		job := &batchv1.Job{}
		hcp := newHostedControlPlane()
		g.Expect(expectedShardNames(job, hcp)).To(Equal(shardIdentities(etcdShards(hcp))))
	})
}
func indexOf(slice []string, value string) int {
	for i, v := range slice {
		if v == value {
			return i
		}
	}
	return -1
}

func TestGetShardSnapshots(t *testing.T) {
	t.Parallel()
	newResultConfigMap := func(backup *hyperv1.HCPEtcdBackup, data string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resultConfigMapName(backup),
				Namespace: testHONamespace,
			},
			Data: map[string]string{resultConfigMapKey: data},
		}
	}
	newCompletedJob := func() *batchv1.Job {
		return newTestJob(batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		})
	}

	t.Run("When the result ConfigMap exists it should be preferred over the pod termination message", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		job := newCompletedJob()
		cm := newResultConfigMap(backup, `[{"name":"default","url":"s3://bucket/default.db"},{"name":"events","url":"s3://bucket/events.db"}]`)
		r := newReconciler(backup, job, cm)

		snapshots, err := r.getShardSnapshots(t.Context(), backup, job)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(snapshots).To(Equal([]hyperv1.HCPEtcdShardSnapshot{
			{Name: "default", SnapshotURL: "s3://bucket/default.db"},
			{Name: "events", SnapshotURL: "s3://bucket/events.db"},
		}))
	})

	t.Run("When the result ConfigMap is absent it should fall back to the pod termination message", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		job := newCompletedJob()
		// A legacy single-shard pod message, and no result ConfigMap.
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "backup-pod", Namespace: testHONamespace,
				Labels: map[string]string{"batch.kubernetes.io/job-name": "etcd-backup-test"},
			},
			Spec: corev1.PodSpec{
				Containers:    []corev1.Container{{Name: "upload", Image: "test:latest"}},
				RestartPolicy: corev1.RestartPolicyNever,
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "upload", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 0, Message: "s3://bucket/snapshot.db",
					}}},
				},
			},
		}
		r := newReconciler(backup, job, pod)

		snapshots, err := r.getShardSnapshots(t.Context(), backup, job)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(snapshots).To(Equal([]hyperv1.HCPEtcdShardSnapshot{
			{Name: "default", SnapshotURL: "s3://bucket/snapshot.db"},
		}))
	})

	t.Run("When the result ConfigMap holds unparsable data it should return no results, which the completeness check fails", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		backup := newHCPEtcdBackup()
		job := newCompletedJob()
		cm := newResultConfigMap(backup, "not-json")
		r := newReconciler(backup, job, cm)

		snapshots, err := r.getShardSnapshots(t.Context(), backup, job)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(snapshots).To(BeNil())
		g.Expect(validateShardSnapshotResults(snapshots, expectedShardNames(job, newHostedControlPlane()))).ToNot(BeEmpty())
	})
}

func TestResultConfigMapName(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	backup := newHCPEtcdBackup()
	g.Expect(resultConfigMapName(backup)).To(Equal("etcd-backup-result-" + testHCPNamespace + "-" + testBackupName))
}
