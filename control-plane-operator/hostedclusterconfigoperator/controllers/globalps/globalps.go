package globalps

import (
	"context"
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/thirdparty/kubernetes/pkg/credentialprovider"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	crreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "globalps"
)

type Reconciler struct {
	cpClient               crclient.Client
	kubeSystemSecretClient crclient.Client
	hcUncachedClient       crclient.Client
	hcpName                string
	hcpNamespace           string
	hccoImage              string
	upsert.CreateOrUpdateProvider
}

func (r *Reconciler) Reconcile(ctx context.Context, req crreconcile.Request) (crreconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling global pull secret")

	// Reconcile GlobalPullSecret
	if err := r.reconcileGlobalPullSecret(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile global pull secret: %w", err)
	}

	return ctrl.Result{}, nil
}

// reconcileGlobalPullSecret reconciles the original pull secret given by HCP and merges it with a new pull secret provided by the user.
// The new pull secret is only stored in the DataPlane side so, it's not exposed in the API. It lives in the kube-system namespace of the DataPlane.
// If that PS exists, the HCCO deploys a DaemonSet which mounts the whole Root FS of the node, and merges the new PS with the original one.
// If the PS doesn't exist, the HCCO doesn't do anything.
func (r *Reconciler) reconcileGlobalPullSecret(ctx context.Context) error {
	var (
		userProvidedPullSecretBytes []byte
		originalPullSecretBytes     []byte
		globalPullSecretBytes       []byte
		err                         error
	)
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling global pull secret")

	// Get the original pull secret once at the beginning (used in both scenarios)
	originalPullSecret := manifests.PullSecret(r.hcpNamespace)
	if err := r.cpClient.Get(ctx, crclient.ObjectKeyFromObject(originalPullSecret), originalPullSecret); err != nil {
		return fmt.Errorf("failed to get original pull secret: %w", err)
	}
	originalPullSecretBytes = originalPullSecret.Data[corev1.DockerConfigJsonKey]

	// Get the user provided pull secret
	exists, additionalPullSecret, err := additionalPullSecretExists(ctx, r.kubeSystemSecretClient)
	if err != nil {
		return fmt.Errorf("failed to check if user provided pull secret exists: %w", err)
	}

	hcp := manifests.HostedControlPlane(r.hcpNamespace, r.hcpName)
	if err := r.cpClient.Get(ctx, crclient.ObjectKeyFromObject(hcp), hcp); err != nil {
		return fmt.Errorf("failed to get hosted control plane: %w", err)
	}

	managedServices := isManagedServices(hcp)

	if !exists || additionalPullSecret.Data == nil {
		// Delete global pull secret if it exists
		secret := manifests.GlobalPullSecret()
		if err := r.kubeSystemSecretClient.Delete(ctx, secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete global pull secret: %w", err)
			}
		}

		// Create/Update original pull secret in the DataPlane's kube-system namespace
		originalSecret := manifests.OriginalPullSecret()
		if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, originalSecret, func() error {
			originalSecret.Data = map[string][]byte{
				corev1.DockerConfigJsonKey: originalPullSecretBytes,
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to create original pull secret: %w", err)
		}

		// Generate a hash of the original pull secret content to trigger pod recreation
		originalPullSecretSeed := util.HashSimple(originalPullSecretBytes)

		// Reconcile DaemonSet with only original pull secret (global-pull-secret will be optional and empty)
		daemonSet := manifests.GlobalPullSecretDaemonSet()
		if err := reconcileDaemonSet(ctx, daemonSet, originalSecret.Name, "", originalPullSecretSeed, r.hcUncachedClient, r.CreateOrUpdate, r.hccoImage); err != nil {
			return fmt.Errorf("failed to reconcile global pull secret daemon set: %w", err)
		}

		return nil
	}

	if userProvidedPullSecretBytes, err = validateAdditionalPullSecret(additionalPullSecret); err != nil {
		return fmt.Errorf("failed to validate user provided pull secret: %w", err)
	}

	log.Info("Valid additional pull secret found in the DataPlane, reconciling global pull secret")

	// Merge the additional pull secret with the original pull secret
	if globalPullSecretBytes, err = mergePullSecrets(ctx, originalPullSecretBytes, userProvidedPullSecretBytes, managedServices); err != nil {
		return fmt.Errorf("failed to merge pull secrets: %w", err)
	}

	// Create original pull secret in the DataPlane's kube-system namespace
	originalSecret := manifests.OriginalPullSecret()
	if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, originalSecret, func() error {
		originalSecret.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: originalPullSecretBytes,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create original pull secret: %w", err)
	}

	// Create global pull secret in the DataPlane
	secret := manifests.GlobalPullSecret()
	if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, secret, func() error {
		secret.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: globalPullSecretBytes,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create global pull secret: %w", err)
	}

	// Generate a hash of the global pull secret content to trigger pod recreation when content changes
	globalPullSecretSeed := util.HashSimple(globalPullSecretBytes)
	daemonSet := manifests.GlobalPullSecretDaemonSet()
	if err := reconcileDaemonSet(ctx, daemonSet, originalSecret.Name, secret.Name, globalPullSecretSeed, r.hcUncachedClient, r.CreateOrUpdate, r.hccoImage); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret daemon set: %w", err)
	}

	return nil
}

func reconcileDaemonSet(ctx context.Context, daemonSet *appsv1.DaemonSet, originalPullSecretName, globalPullSecretName, globalPullSecretSeed string, c crclient.Client, createOrUpdate upsert.CreateOrUpdateFN, hccoImage string) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling global pull secret daemon set")

	if _, err := createOrUpdate(ctx, c, daemonSet, func() error {
		daemonSet.Spec = appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": manifests.GlobalPullSecretDSName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name":   manifests.GlobalPullSecretDSName,
						"config": globalPullSecretSeed,
					},
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(true),
					SecurityContext:              &corev1.PodSecurityContext{},
					DNSPolicy:                    corev1.DNSDefault,
					Tolerations:                  []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{
						{
							Name:            manifests.GlobalPullSecretDSName,
							Image:           hccoImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command: []string{
								"/usr/bin/control-plane-operator",
							},
							// TODO: remove the flag --global-pull-secret-name from the relevant places
							Args: []string{
								"sync-global-pullsecret",
								fmt.Sprintf("--global-pull-secret-name=%s", globalPullSecretName),
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
							VolumeMounts: func() []corev1.VolumeMount {
								volumeMounts := []corev1.VolumeMount{
									{
										Name:      "kubelet-config",
										MountPath: "/var/lib/kubelet",
									},
									{
										Name:      "dbus",
										MountPath: "/var/run/dbus",
									},
									{
										Name:      "original-pull-secret",
										MountPath: "/etc/original-pull-secret",
										ReadOnly:  true,
									},
								}
								if globalPullSecretName != "" {
									volumeMounts = append(volumeMounts, corev1.VolumeMount{
										Name:      "global-pull-secret",
										MountPath: "/etc/global-pull-secret",
										ReadOnly:  true,
									})
								}
								return volumeMounts
							}(),
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("50Mi"),
									corev1.ResourceCPU:    resource.MustParse("40m"),
								},
							},
						},
					},
					Volumes: func() []corev1.Volume {
						volumes := []corev1.Volume{
							{
								Name: "kubelet-config",
								VolumeSource: corev1.VolumeSource{
									HostPath: &corev1.HostPathVolumeSource{
										Path: "/var/lib/kubelet",
										Type: ptr.To(corev1.HostPathDirectory),
									},
								},
							},
							{
								Name: "dbus",
								VolumeSource: corev1.VolumeSource{
									HostPath: &corev1.HostPathVolumeSource{
										Path: "/var/run/dbus",
										Type: ptr.To(corev1.HostPathDirectory),
									},
								},
							},
							{
								Name: "original-pull-secret",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: originalPullSecretName,
									},
								},
							},
						}
						if globalPullSecretName != "" {
							volumes = append(volumes, corev1.Volume{
								Name: "global-pull-secret",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: globalPullSecretName,
										Optional:   ptr.To(true), // Make the secret optional
									},
								},
							})
						}
						return volumes
					}(),
				},
			},
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create global pull secret daemon set: %w", err)
	}

	return nil
}

func validateAdditionalPullSecret(pullSecret *corev1.Secret) ([]byte, error) {
	var dockerConfigJSON credentialprovider.DockerConfigJSON

	// Validate that the pull secret contains the dockerConfigJson key
	if _, ok := pullSecret.Data[corev1.DockerConfigJsonKey]; !ok {
		return nil, fmt.Errorf("pull secret data is not a valid docker config json")
	}

	// Validate that the pull secret is a valid DockerConfigJSON
	pullSecretBytes := pullSecret.Data[corev1.DockerConfigJsonKey]
	if err := json.Unmarshal(pullSecretBytes, &dockerConfigJSON); err != nil {
		return nil, fmt.Errorf("invalid docker config json format: %w", err)
	}

	// Validate that the pull secret contains at least one auth entry
	if len(dockerConfigJSON.Auths) == 0 {
		return nil, fmt.Errorf("docker config json must contain at least one auth entry")
	}

	return pullSecretBytes, nil
}

// MergePullSecrets merges two pull secrets into a single pull secret.
// The additional pull secret is merged with the original pull secret.
// If an auth entry already exists, it will be overwritten.
// The resulting pull secret is returned as a JSON string.
// Not using credentialprovider.DockerConfigJSON because it does not support
// marshaling the auth field.
func mergePullSecrets(ctx context.Context, originalPullSecret, userProvidedPullSecret []byte, managedServices bool) ([]byte, error) {
	var (
		originalAuths         map[string]any
		userProvidedAuths     map[string]any
		finalAuths            map[string]any
		originalJSON          map[string]any
		userProvidedJSON      map[string]any
		globalPullSecretBytes []byte
		err                   error
	)

	log := ctrl.LoggerFrom(ctx)
	// Unmarshal original pull secret
	if err = json.Unmarshal(originalPullSecret, &originalJSON); err != nil {
		return nil, fmt.Errorf("invalid original pull secret format: %w", err)
	}
	originalAuths = originalJSON["auths"].(map[string]any)

	// Unmarshal additional pull secret
	if err = json.Unmarshal(userProvidedPullSecret, &userProvidedJSON); err != nil {
		return nil, fmt.Errorf("invalid user provided pull secret format: %w", err)
	}
	userProvidedAuths = userProvidedJSON["auths"].(map[string]any)

	// If managedServices, that means the prcedence of the original pull secret is higher than the user provided pull secret so we need to merge the user provided pull secret into the original pull secret in the other case, the precedence is the opposite
	if !managedServices {
		log.Info("Non-managed services detected, merging auths with precedence of user provided pull secret")
		for k, v := range userProvidedAuths {
			originalAuths[k] = v
		}
		finalAuths = originalAuths
	} else {
		log.Info("Managed services detected, merging auths with precedence of original pull secret")
		for k, v := range originalAuths {
			userProvidedAuths[k] = v
		}
		finalAuths = userProvidedAuths
	}

	// Create final JSON
	finalJSON := map[string]any{
		"auths": finalAuths,
	}

	globalPullSecretBytes, err = json.Marshal(finalJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged pull secret: %w", err)
	}

	return globalPullSecretBytes, nil
}

func additionalPullSecretExists(ctx context.Context, c crclient.Client) (bool, *corev1.Secret, error) {
	additionalPullSecret := manifests.AdditionalPullSecret()
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(additionalPullSecret), additionalPullSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	return true, additionalPullSecret, nil
}

// isManagedServices returns true if the hosted control plane has managed services enabled
func isManagedServices(hcp *hyperv1.HostedControlPlane) bool {
	// Check if is an ARO HCP
	if azureutil.IsAroHCP() {
		return true
	}

	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		return awsutil.IsROSAHCP(hcp)
	}

	return false
}
