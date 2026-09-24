package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	installerNamespace = "hypershift"
	installerName      = "hypershift-installer"
)

// installViaOperatorImage creates a Kubernetes Job that runs `hypershift install`
// from the operator image itself. This ensures the CLI binary and embedded CRDs
// match the operator version being installed, avoiding version mismatches when
// the test binary comes from a different branch.
func installViaOperatorImage(ctx context.Context, opts HyperShiftOperatorInstallOptions) error {
	client, err := GetClient()
	if err != nil {
		return fmt.Errorf("getting client: %w", err)
	}

	if err := ensureNamespace(ctx, client, installerNamespace); err != nil {
		return fmt.Errorf("ensuring namespace %s: %w", installerNamespace, err)
	}

	secretNames, err := createCredentialSecrets(ctx, client, opts)
	if err != nil {
		return fmt.Errorf("creating credential secrets: %w", err)
	}

	if err := ensureInstallerRBAC(ctx, client); err != nil {
		return fmt.Errorf("creating installer RBAC: %w", err)
	}

	args := buildInstallerArgs(opts, secretNames)

	if err := createInstallerJob(ctx, client, opts.HyperShiftOperatorLatestImage, args); err != nil {
		return fmt.Errorf("creating installer job: %w", err)
	}

	waitErr := waitForInstallerJob(ctx, client)
	logInstallerPodOutput(ctx)
	cleanupInstaller(ctx, client)
	if waitErr != nil {
		return fmt.Errorf("installer job failed: %w", waitErr)
	}
	return nil
}

type credentialSecretNames struct {
	oidcS3       string
	awsPrivate   string
	externalDNS  string
	azurePrivate string
}

func createCredentialSecrets(ctx context.Context, client crclient.Client, opts HyperShiftOperatorInstallOptions) (*credentialSecretNames, error) {
	names := &credentialSecretNames{}

	switch opts.Platform {
	case hyperv1.AWSPlatform, "":
		if opts.AWSOidcS3Credentials != "" {
			name, err := createSecretFromFile(ctx, client, installerName+"-oidc-s3", opts.AWSOidcS3Credentials)
			if err != nil {
				return nil, fmt.Errorf("creating OIDC S3 secret: %w", err)
			}
			names.oidcS3 = name
		}
		if opts.PrivatePlatform == string(hyperv1.AWSPlatform) && opts.AWSPrivateCredentialsFile != "" {
			name, err := createSecretFromFile(ctx, client, installerName+"-aws-private", opts.AWSPrivateCredentialsFile)
			if err != nil {
				return nil, fmt.Errorf("creating AWS private secret: %w", err)
			}
			names.awsPrivate = name
		}
		if opts.ExternalDNSProvider != "" && opts.ExternalDNSCredentials != "" {
			name, err := createSecretFromFile(ctx, client, installerName+"-external-dns", opts.ExternalDNSCredentials)
			if err != nil {
				return nil, fmt.Errorf("creating external DNS secret: %w", err)
			}
			names.externalDNS = name
		}
	case hyperv1.AzurePlatform:
		if opts.PrivatePlatform == string(hyperv1.AzurePlatform) && opts.AzurePrivateCredentialsFile != "" {
			name, err := createSecretFromFile(ctx, client, installerName+"-azure-private", opts.AzurePrivateCredentialsFile)
			if err != nil {
				return nil, fmt.Errorf("creating Azure private secret: %w", err)
			}
			names.azurePrivate = name
		}
		if opts.ExternalDNSProvider != "" && opts.ExternalDNSCredentials != "" {
			name, err := createSecretFromFile(ctx, client, installerName+"-external-dns", opts.ExternalDNSCredentials)
			if err != nil {
				return nil, fmt.Errorf("creating external DNS secret: %w", err)
			}
			names.externalDNS = name
		}
	case hyperv1.GCPPlatform:
		if opts.ExternalDNSProvider != "" && opts.ExternalDNSCredentials != "" {
			name, err := createSecretFromFile(ctx, client, installerName+"-external-dns", opts.ExternalDNSCredentials)
			if err != nil {
				return nil, fmt.Errorf("creating external DNS secret: %w", err)
			}
			names.externalDNS = name
		}
	default:
		if opts.AWSOidcS3BucketName != "" && opts.AWSOidcS3Credentials != "" {
			name, err := createSecretFromFile(ctx, client, installerName+"-oidc-s3", opts.AWSOidcS3Credentials)
			if err != nil {
				return nil, fmt.Errorf("creating OIDC S3 secret: %w", err)
			}
			names.oidcS3 = name
		}
	}

	return names, nil
}

func createSecretFromFile(ctx context.Context, client crclient.Client, name, filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filePath, err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: installerNamespace,
		},
		Data: map[string][]byte{
			"credentials": data,
		},
	}

	existing := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(secret), existing); err == nil {
		existing.Data = secret.Data
		if err := client.Update(ctx, existing); err != nil {
			return "", fmt.Errorf("updating secret %s: %w", name, err)
		}
	} else if apierrors.IsNotFound(err) {
		if err := client.Create(ctx, secret); err != nil {
			return "", fmt.Errorf("creating secret %s: %w", name, err)
		}
	} else {
		return "", fmt.Errorf("getting secret %s: %w", name, err)
	}

	return name, nil
}

func ensureNamespace(ctx context.Context, client crclient.Client, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(ns), ns); apierrors.IsNotFound(err) {
		return client.Create(ctx, ns)
	} else if err != nil {
		return err
	}
	return nil
}

func ensureInstallerRBAC(ctx context.Context, client crclient.Client) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      installerName,
			Namespace: installerNamespace,
		},
	}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(sa), sa); apierrors.IsNotFound(err) {
		if err := client.Create(ctx, sa); err != nil {
			return fmt.Errorf("creating service account: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("getting service account: %w", err)
	}

	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: installerName,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      installerName,
				Namespace: installerNamespace,
			},
		},
	}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(crb), crb); apierrors.IsNotFound(err) {
		if err := client.Create(ctx, crb); err != nil {
			return fmt.Errorf("creating cluster role binding: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("getting cluster role binding: %w", err)
	}

	return nil
}

func buildInstallerArgs(opts HyperShiftOperatorInstallOptions, secrets *credentialSecretNames) []string {
	args := []string{
		"install",
		"--hypershift-image", opts.HyperShiftOperatorLatestImage,
		"--wait-until-available",
	}

	if opts.PlatformMonitoring != "" {
		args = append(args, "--platform-monitoring", opts.PlatformMonitoring)
	}

	if opts.EnableCIDebugOutput {
		args = append(args, "--enable-ci-debug-output")
	}
	if opts.EnableSizeTagging {
		args = append(args, "--enable-size-tagging")
	}
	if opts.EnableCPOOverrides {
		args = append(args, "--enable-cpo-overrides")
	}
	if opts.DisableCAPIMigration {
		args = append(args, "--disable-capi-migration")
	}
	args = append(args,
		fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", opts.EnableDedicatedRequestServingIsolation),
		fmt.Sprintf("--enable-etcd-recovery=%t", opts.EnableEtcdRecovery),
	)

	switch opts.Platform {
	case hyperv1.AWSPlatform, "":
		if secrets.oidcS3 != "" {
			args = append(args,
				"--oidc-storage-provider-s3-bucket-name", opts.AWSOidcS3BucketName,
				"--oidc-storage-provider-s3-region", opts.AWSOidcS3Region,
				"--oidc-storage-provider-s3-secret", secrets.oidcS3,
			)
		}
		if opts.PrivatePlatform != "" {
			args = append(args, "--private-platform", opts.PrivatePlatform)
		}
		if secrets.awsPrivate != "" {
			args = append(args,
				"--aws-private-secret", secrets.awsPrivate,
				"--aws-private-region", opts.AWSPrivateRegion,
			)
		}
		if opts.ExternalDNSProvider != "" {
			args = append(args,
				"--external-dns-provider", opts.ExternalDNSProvider,
				"--external-dns-domain-filter", opts.ExternalDNSDomainFilter,
				"--external-dns-interval", "3m",
			)
			if secrets.externalDNS != "" {
				args = append(args, "--external-dns-secret", secrets.externalDNS)
			}
		}
	case hyperv1.AzurePlatform:
		if opts.PrivatePlatform != "" {
			args = append(args, "--private-platform", opts.PrivatePlatform)
		}
		if secrets.azurePrivate != "" {
			args = append(args, "--azure-private-secret", secrets.azurePrivate)
		}
		if opts.AzurePLSResourceGroup != "" {
			args = append(args, "--azure-pls-resource-group", opts.AzurePLSResourceGroup)
		}
		if opts.ExternalDNSProvider != "" {
			args = append(args,
				"--external-dns-provider", opts.ExternalDNSProvider,
				"--external-dns-domain-filter", opts.ExternalDNSDomainFilter,
				"--external-dns-interval", "3m",
			)
			if secrets.externalDNS != "" {
				args = append(args, "--external-dns-secret", secrets.externalDNS)
			}
		}
	case hyperv1.GCPPlatform:
		if opts.PrivatePlatform != "" {
			args = append(args, "--private-platform", opts.PrivatePlatform)
		}
		if opts.GCPProject != "" {
			args = append(args, "--gcp-project", opts.GCPProject)
		}
		if opts.GCPRegion != "" {
			args = append(args, "--gcp-region", opts.GCPRegion)
		}
		if opts.ExternalDNSProvider != "" {
			args = append(args,
				"--external-dns-provider", opts.ExternalDNSProvider,
				"--external-dns-domain-filter", opts.ExternalDNSDomainFilter,
				"--external-dns-interval", "3m",
			)
			if secrets.externalDNS != "" {
				args = append(args, "--external-dns-secret", secrets.externalDNS)
			}
			if opts.ExternalDNSGoogleProject != "" {
				args = append(args, "--external-dns-google-project", opts.ExternalDNSGoogleProject)
			}
		}
	default:
		if secrets.oidcS3 != "" {
			args = append(args,
				"--oidc-storage-provider-s3-bucket-name", opts.AWSOidcS3BucketName,
				"--oidc-storage-provider-s3-region", opts.AWSOidcS3Region,
				"--oidc-storage-provider-s3-secret", secrets.oidcS3,
			)
		}
	}

	return args
}

func createInstallerJob(ctx context.Context, client crclient.Client, image string, args []string) error {
	// Delete any previous installer job
	existing := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      installerName,
			Namespace: installerNamespace,
		},
	}
	if err := client.Delete(ctx, existing, crclient.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting previous installer job: %w", err)
	}
	// Wait for old job to be fully deleted
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := wait.PollUntilContextCancel(waitCtx, time.Second, true, func(ctx context.Context) (bool, error) {
		err := client.Get(ctx, crclient.ObjectKeyFromObject(existing), existing)
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("getting previous installer job: %w", err)
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("waiting for previous installer job deletion: %w", err)
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      installerName,
			Namespace: installerNamespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To[int32](0),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: installerName,
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "installer",
							Image:   image,
							Command: []string{"/usr/bin/hypershift"},
							Args:    args,
						},
					},
				},
			},
		},
	}

	return client.Create(ctx, job)
}

func waitForInstallerJob(ctx context.Context, client crclient.Client) error {
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	job := &batchv1.Job{}
	key := crclient.ObjectKey{Name: installerName, Namespace: installerNamespace}

	return wait.PollUntilContextCancel(waitCtx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, key, job); err != nil {
			fmt.Printf("Transient error fetching installer job (will retry): %v\n", err)
			return false, nil
		}

		for _, c := range job.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				return true, nil
			}
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				return false, fmt.Errorf("installer job failed: %s", c.Message)
			}
		}

		fmt.Printf("Waiting for installer job to complete...\n")
		return false, nil
	})
}

func logInstallerPodOutput(ctx context.Context) {
	cfg, err := GetConfig()
	if err != nil {
		fmt.Printf("Failed to get config for installer logs: %v\n", err)
		return
	}
	kubeClient, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		fmt.Printf("Failed to create client for installer logs: %v\n", err)
		return
	}
	pods, err := kubeClient.CoreV1().Pods(installerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + installerName,
	})
	if err != nil || len(pods.Items) == 0 {
		fmt.Printf("No installer pods found for log retrieval\n")
		return
	}

	artifactDir := os.Getenv("ARTIFACT_DIR")
	var logsDir string
	if artifactDir != "" {
		logsDir = filepath.Join(artifactDir, "namespaces", installerNamespace, "core", "pods", "logs")
		if mkdirErr := os.MkdirAll(logsDir, 0755); mkdirErr != nil {
			fmt.Printf("Failed to create installer logs directory %s: %v\n", logsDir, mkdirErr)
			logsDir = ""
		}
	}

	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			req := kubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container.Name})
			logs, err := req.DoRaw(ctx)
			if err != nil {
				fmt.Printf("Failed to get logs from pod %s container %s: %v\n", pod.Name, container.Name, err)
				continue
			}
			fmt.Printf("=== Installer pod %s/%s logs ===\n%s\n", pod.Name, container.Name, string(logs))
			if logsDir != "" {
				logFile := filepath.Join(logsDir, fmt.Sprintf("%s-%s.log", pod.Name, container.Name))
				if writeErr := os.WriteFile(logFile, logs, 0644); writeErr != nil {
					fmt.Printf("Failed to write installer logs to %s: %v\n", logFile, writeErr)
				}
			}
		}
	}
}

func cleanupInstaller(ctx context.Context, client crclient.Client) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      installerName,
			Namespace: installerNamespace,
		},
	}
	_ = client.Delete(ctx, job, crclient.PropagationPolicy(metav1.DeletePropagationBackground))

	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: installerName},
	}
	_ = client.Delete(ctx, crb)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      installerName,
			Namespace: installerNamespace,
		},
	}
	_ = client.Delete(ctx, sa)
}
