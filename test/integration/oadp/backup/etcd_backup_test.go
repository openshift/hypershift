//go:build integration
// +build integration

// Package backup contains integration tests for the etcd backup flow.
//
// These tests validate the end-to-end backup process against a live
// management cluster hosting a HostedCluster.
//
// Required environment variables:
//   - KUBECONFIG: path to the management cluster kubeconfig
//   - ETCD_BACKUP_TEST_HCP_NAMESPACE: the HCP namespace (e.g. clusters-my-hcp)
//
// Optional environment variables:
//   - ETCD_BACKUP_TEST_HO_NAMESPACE: the HO namespace (defaults to "hypershift")
//
// Container images are derived from the running workloads in the HCP
// namespace (etcd StatefulSet and CPO deployment).
//
// NOTE: The Job construction in TestWhenBackupJobRunsFromHONamespace is a
// manual mock of what the HCPEtcdBackup controller will do once implemented.
//
// Run with:
//
//	KUBECONFIG=/path/to/management-cluster/kubeconfig \
//	ETCD_BACKUP_TEST_HCP_NAMESPACE=clusters-my-hcp \
//	  go test -tags integration -v -timeout 10m ./test/integration/oadp/backup/...
package backup

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/controlplaneoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
)

const (
	defaultHONamespace = "hypershift"

	// Polling constants for waitForJobCompletion
	jobPollInterval = 5 * time.Second
	jobPollTimeout  = 5 * time.Minute
)

type testConfig struct {
	HCPNamespace string
	HONamespace  string
}

func loadTestConfig(t *testing.T) testConfig {
	t.Helper()
	hcpNS := os.Getenv("ETCD_BACKUP_TEST_HCP_NAMESPACE")
	if hcpNS == "" {
		t.Skip("ETCD_BACKUP_TEST_HCP_NAMESPACE not set")
	}
	hoNS := os.Getenv("ETCD_BACKUP_TEST_HO_NAMESPACE")
	if hoNS == "" {
		hoNS = defaultHONamespace
	}
	return testConfig{
		HCPNamespace: hcpNS,
		HONamespace:  hoNS,
	}
}

func newClient(t *testing.T) crclient.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client-go scheme: %v", err)
	}

	restConfig, err := config.GetConfig()
	if err != nil {
		t.Fatalf("failed to get kubeconfig: %v", err)
	}

	k8sClient, err := crclient.New(restConfig, crclient.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("failed to create k8s client: %v", err)
	}
	return k8sClient
}

// getEtcdImage reads the etcd container image from the running etcd StatefulSet
// in the HCP namespace.
func getEtcdImage(ctx context.Context, t *testing.T, k8sClient crclient.Client, hcpNamespace string) string {
	t.Helper()
	sts := &appsv1.StatefulSet{}
	err := k8sClient.Get(ctx, crclient.ObjectKey{Name: "etcd", Namespace: hcpNamespace}, sts)
	if err != nil {
		t.Fatalf("failed to get etcd StatefulSet: %v", err)
	}
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == "etcd" {
			return c.Image
		}
	}
	t.Fatal("etcd container not found in StatefulSet")
	return ""
}

// getCPOImage reads the CPO container image from the running control-plane-operator
// deployment in the HCP namespace.
func getCPOImage(ctx context.Context, t *testing.T, k8sClient crclient.Client, hcpNamespace string) string {
	t.Helper()
	dep := &appsv1.Deployment{}
	err := k8sClient.Get(ctx, crclient.ObjectKey{Name: controlplaneoperator.ComponentName, Namespace: hcpNamespace}, dep)
	if err != nil {
		t.Fatalf("failed to get CPO Deployment: %v", err)
	}
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == controlplaneoperator.ComponentName {
			return c.Image
		}
	}
	t.Fatal("control-plane-operator container not found in Deployment")
	return ""
}

// cleanup deletes the given objects, ignoring not-found errors.
func cleanup(ctx context.Context, t *testing.T, k8sClient crclient.Client, objects ...crclient.Object) {
	t.Helper()
	for _, obj := range objects {
		if err := k8sClient.Delete(ctx, obj); crclient.IgnoreNotFound(err) != nil {
			t.Logf("warning: failed to delete %T %s/%s: %v", obj, obj.GetNamespace(), obj.GetName(), err)
		}
	}
}

// waitForJobCompletion polls the given Job until it completes or fails.
func waitForJobCompletion(ctx context.Context, k8sClient crclient.Client, job *batchv1.Job) error {
	return wait.PollUntilContextTimeout(ctx, jobPollInterval, jobPollTimeout, true, func(ctx context.Context) (bool, error) {
		if err := k8sClient.Get(ctx, crclient.ObjectKeyFromObject(job), job); err != nil {
			return false, err
		}
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return false, fmt.Errorf("job failed: %s", cond.Message)
			}
		}
		return false, nil
	})
}

// etcdEndpointURL returns the etcd client service endpoint for a given HCP namespace.
func etcdEndpointURL(hcpNamespace string) string {
	return fmt.Sprintf("https://etcd-client.%s.svc.cluster.local:%d", hcpNamespace, kas.DefaultEtcdPort)
}

// TestWhenBackupJobRunsFromHONamespace validates the new etcd backup flow where
// the Job runs from the HO namespace.
//
// NOTE: The Job is constructed manually here as a mock of the HCPEtcdBackup
// controller, which is not yet implemented.
//
// When a backup Job is created in the HO namespace it should complete
// successfully by fetching certs cross-namespace and taking an etcd snapshot.
//
// Flow:
//  1. Create ServiceAccount in HO namespace
//  2. Create Role in HCP namespace (read etcd secrets/configmaps)
//  3. Create RoleBinding in HCP namespace (cross-namespace SA binding)
//  4. Create NetworkPolicy in HCP namespace (allow etcd:2379 from HO pods)
//  5. Create Job in HO namespace (fetch-etcd-certs + etcdctl snapshot save)
//  6. Wait for Job to complete
//  7. Verify Job succeeded
//  8. Clean up all resources
func TestWhenBackupJobRunsFromHONamespace_ItShouldCompleteSuccessfully(t *testing.T) {
	cfg := loadTestConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	k8sClient := newClient(t)
	g := NewWithT(t)

	etcdImage := getEtcdImage(ctx, t, k8sClient, cfg.HCPNamespace)
	cpoImage := getCPOImage(ctx, t, k8sClient, cfg.HCPNamespace)
	t.Logf("Using etcd image: %s", etcdImage)
	t.Logf("Using CPO image: %s", cpoImage)

	etcdEndpoint := etcdEndpointURL(cfg.HCPNamespace)

	// Define all resources to be created
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-job-test",
			Namespace: cfg.HONamespace,
		},
	}

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-job-test",
			Namespace: cfg.HCPNamespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{"etcd-client-tls"},
				Verbs:         []string{"get"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"configmaps"},
				ResourceNames: []string{"etcd-ca"},
				Verbs:         []string{"get"},
			},
		},
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-job-test",
			Namespace: cfg.HCPNamespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     role.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: cfg.HONamespace,
			},
		},
	}

	etcdPort := intstr.FromInt32(2379)
	protocol := corev1.ProtocolTCP
	networkPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-etcd-backup-test",
			Namespace: cfg.HCPNamespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "etcd",
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": cfg.HONamespace,
								},
							},
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "etcd-backup",
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port:     &etcdPort,
							Protocol: &protocol,
						},
					},
				},
			},
		},
	}

	certsVolume := "etcd-certs"
	backupVolume := "etcd-backup"
	certsDir := "/etc/etcd-certs"
	backupDir := "/tmp/etcd-backup"

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-integration-test",
			Namespace: cfg.HONamespace,
			Labels: map[string]string{
				"app": "etcd-backup",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To[int32](0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "etcd-backup",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: sa.Name,
					RestartPolicy:      corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{
						{
							Name:    "fetch-etcd-certs",
							Image:   cpoImage,
							Command: []string{"/usr/bin/control-plane-operator", "fetch-etcd-certs"},
							Args: []string{
								"--hcp-namespace", cfg.HCPNamespace,
								"--output-dir", certsDir,
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: certsVolume, MountPath: certsDir},
							},
						},
						{
							Name:    "etcd-snapshot",
							Image:   etcdImage,
							Command: []string{"etcdctl"},
							Args: []string{
								"--endpoints", etcdEndpoint,
								"--cacert", fmt.Sprintf("%s/ca.crt", certsDir),
								"--cert", fmt.Sprintf("%s/etcd-client.crt", certsDir),
								"--key", fmt.Sprintf("%s/etcd-client.key", certsDir),
								"snapshot", "save", fmt.Sprintf("%s/snapshot.db", backupDir),
							},
							Env: []corev1.EnvVar{
								{Name: "ETCDCTL_API", Value: "3"},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: certsVolume, MountPath: certsDir, ReadOnly: true},
								{Name: backupVolume, MountPath: backupDir},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "verify-snapshot",
							Image:   etcdImage,
							Command: []string{"etcdutl"},
							Args: []string{
								"--write-out", "json",
								"snapshot", "status", fmt.Sprintf("%s/snapshot.db", backupDir),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: backupVolume, MountPath: backupDir, ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: certsVolume,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: backupVolume,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	// Register cleanup before creating any resources
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		t.Log("Cleaning up test resources...")
		cleanup(cleanupCtx, t, k8sClient, job, networkPolicy, roleBinding, role, sa)
	})

	// Step 1: Create RBAC resources
	t.Log("Creating ServiceAccount, Role, RoleBinding...")
	for _, obj := range []crclient.Object{sa, role, roleBinding} {
		g.Expect(k8sClient.Create(ctx, obj)).To(Succeed(), "failed to create %T %s", obj, obj.GetName())
	}

	// Step 2: Verify RBAC resources exist
	g.Expect(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(sa), sa)).To(Succeed())
	g.Expect(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(role), role)).To(Succeed())
	g.Expect(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(roleBinding), roleBinding)).To(Succeed())

	// Step 3: Create NetworkPolicy
	t.Log("Creating NetworkPolicy...")
	g.Expect(k8sClient.Create(ctx, networkPolicy)).To(Succeed())
	g.Expect(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(networkPolicy), networkPolicy)).To(Succeed())

	// Step 4: Create Job
	t.Log("Creating etcd backup Job...")
	g.Expect(k8sClient.Create(ctx, job)).To(Succeed())

	// Step 5: Wait for Job to complete
	t.Log("Waiting for Job to complete...")
	err := waitForJobCompletion(ctx, k8sClient, job)

	// Step 6: Verify result
	g.Expect(err).ToNot(HaveOccurred(), "etcd backup Job did not complete successfully")
	g.Expect(job.Status.Succeeded).To(Equal(int32(1)))
	t.Log("etcd backup Job completed successfully")
}

// TestWhenBackupJobRunsFromHCPNamespace validates the legacy etcd backup flow
// where the backup runs directly in the HCP namespace with certs mounted as
// volumes from the existing secrets and configmaps. No NetworkPolicy or
// cross-namespace RBAC is needed since everything runs within the HCP namespace.
//
// When a backup Job is created in the HCP namespace it should complete
// successfully using locally-mounted etcd TLS secrets.
//
// Flow:
//  1. Get etcd image from StatefulSet
//  2. Create Job in HCP namespace mounting etcd-client-tls and etcd-ca directly
//  3. Wait for Job to complete
//  4. Verify Job succeeded and snapshot is valid
//  5. Clean up the Job
func TestWhenBackupJobRunsFromHCPNamespace_ItShouldCompleteSuccessfully(t *testing.T) {
	cfg := loadTestConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	k8sClient := newClient(t)
	g := NewWithT(t)

	etcdImage := getEtcdImage(ctx, t, k8sClient, cfg.HCPNamespace)
	t.Logf("Using etcd image: %s", etcdImage)

	// Verify the etcd TLS resources exist in the HCP namespace
	secret := &corev1.Secret{}
	g.Expect(k8sClient.Get(ctx, crclient.ObjectKey{
		Name: "etcd-client-tls", Namespace: cfg.HCPNamespace,
	}, secret)).To(Succeed(), "etcd-client-tls secret not found in HCP namespace")

	cm := &corev1.ConfigMap{}
	g.Expect(k8sClient.Get(ctx, crclient.ObjectKey{
		Name: "etcd-ca", Namespace: cfg.HCPNamespace,
	}, cm)).To(Succeed(), "etcd-ca configmap not found in HCP namespace")

	etcdEndpoint := etcdEndpointURL(cfg.HCPNamespace)
	backupDir := "/tmp/etcd-backup"

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-backup-legacy-test",
			Namespace: cfg.HCPNamespace,
			Labels: map[string]string{
				"app": "etcd-backup",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To[int32](0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "etcd-backup",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{
						{
							Name:    "etcd-snapshot",
							Image:   etcdImage,
							Command: []string{"etcdctl"},
							Args: []string{
								"--endpoints", etcdEndpoint,
								"--cacert", "/etc/etcd/tls/etcd-ca/ca.crt",
								"--cert", "/etc/etcd/tls/client/etcd-client.crt",
								"--key", "/etc/etcd/tls/client/etcd-client.key",
								"snapshot", "save", fmt.Sprintf("%s/snapshot.db", backupDir),
							},
							Env: []corev1.EnvVar{
								{Name: "ETCDCTL_API", Value: "3"},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "etcd-client-tls", MountPath: "/etc/etcd/tls/client", ReadOnly: true},
								{Name: "etcd-ca", MountPath: "/etc/etcd/tls/etcd-ca", ReadOnly: true},
								{Name: "backup", MountPath: backupDir},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "verify-snapshot",
							Image:   etcdImage,
							Command: []string{"etcdutl"},
							Args: []string{
								"--write-out", "json",
								"snapshot", "status", fmt.Sprintf("%s/snapshot.db", backupDir),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "backup", MountPath: backupDir, ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "etcd-client-tls",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "etcd-client-tls",
								},
							},
						},
						{
							Name: "etcd-ca",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "etcd-ca",
									},
								},
							},
						},
						{
							Name: "backup",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		t.Log("Cleaning up legacy test Job...")
		cleanup(cleanupCtx, t, k8sClient, job)
	})

	t.Log("Creating legacy etcd backup Job in HCP namespace...")
	g.Expect(k8sClient.Create(ctx, job)).To(Succeed())

	t.Log("Waiting for legacy Job to complete...")
	err := waitForJobCompletion(ctx, k8sClient, job)

	g.Expect(err).ToNot(HaveOccurred(), "legacy etcd backup Job did not complete successfully")
	g.Expect(job.Status.Succeeded).To(Equal(int32(1)))
	t.Log("Legacy etcd backup Job completed successfully")
}
