package globalps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/thirdparty/kubernetes/pkg/credentialprovider"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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

	// Get the user provided pull secret
	exists, additionalPullSecret, err := additionalPullSecretExists(ctx, r.kubeSystemSecretClient)
	if err != nil {
		return fmt.Errorf("failed to check if user provided pull secret exists: %w", err)
	}

	if !exists || additionalPullSecret.Data == nil {
		secret := manifests.GlobalPullSecret()
		if err := r.kubeSystemSecretClient.Delete(ctx, secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete global pull secret: %w", err)
			}
		}
		return nil
	}

	// Reconcile the RBAC for the Global Pull Secret
	if err := reconcileGlobalPullSecretRBAC(ctx, r.hcUncachedClient, r.CreateOrUpdate, "kube-system", "openshift-config"); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret RBAC: %w", err)
	}

	if userProvidedPullSecretBytes, err = validateAdditionalPullSecret(additionalPullSecret); err != nil {
		return fmt.Errorf("failed to validate user provided pull secret: %w", err)
	}

	log.Info("Valid additional pull secret found in the DataPlane, reconciling global pull secret")

	// Get the original pull secret
	originalPullSecret := manifests.PullSecret(r.hcpNamespace)
	if err := r.cpClient.Get(ctx, crclient.ObjectKeyFromObject(originalPullSecret), originalPullSecret); err != nil {
		return fmt.Errorf("failed to get original pull secret: %w", err)
	}

	// Asumming hcp pull secret is valid
	originalPullSecretBytes = originalPullSecret.Data[corev1.DockerConfigJsonKey]

	// Merge the additional pull secret with the original pull secret
	if globalPullSecretBytes, err = mergePullSecrets(ctx, originalPullSecretBytes, userProvidedPullSecretBytes); err != nil {
		return fmt.Errorf("failed to merge pull secrets: %w", err)
	}

	// Create secret in the DataPlane
	secret := manifests.GlobalPullSecret()
	if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, secret, func() error {
		secret.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: globalPullSecretBytes,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create global pull secret: %w", err)
	}

	daemonSet := manifests.GlobalPullSecretDaemonSet()
	if err := reconcileDaemonSet(ctx, daemonSet, secret.Name, r.hcUncachedClient, r.CreateOrUpdate, r.hccoImage); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret daemon set: %w", err)
	}

	return nil
}

func reconcileDaemonSet(ctx context.Context, daemonSet *appsv1.DaemonSet, globalPullSecretName string, c crclient.Client, createOrUpdate upsert.CreateOrUpdateFN, hccoImage string) error {
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
						"name": manifests.GlobalPullSecretDSName,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           manifests.GlobalPullSecretDSName,
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
							Args: []string{
								"sync-global-pullsecret",
								fmt.Sprintf("--global-pull-secret-name=%s", globalPullSecretName),
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "kubelet-config",
									MountPath: "/var/lib/kubelet",
								},
								{
									Name:      "dbus",
									MountPath: "/var/run/dbus",
								},
							},
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("50Mi"),
									corev1.ResourceCPU:    resource.MustParse("40m"),
								},
							},
						},
					},
					Volumes: []corev1.Volume{
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
					},
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
func mergePullSecrets(ctx context.Context, originalPullSecret, userProvidedPullSecret []byte) ([]byte, error) {
	var (
		originalAuths         map[string]any
		userProvidedAuths     map[string]any
		originalJSON          map[string]any
		userProvidedJSON      map[string]any
		globalPullSecretBytes []byte
		err                   error
	)

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

	// Merge auths
	for k, v := range userProvidedAuths {
		originalAuths[k] = v
	}

	// Create final JSON
	finalJSON := map[string]any{
		"auths": originalAuths,
	}

	globalPullSecretBytes, err = json.Marshal(finalJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged pull secret: %w", err)
	}

	return globalPullSecretBytes, nil
}

func reconcileGlobalPullSecretRBAC(ctx context.Context, c crclient.Client, createOrUpdate upsert.CreateOrUpdateFN, kubeSystemNS, openshiftConfigNS string) error {
	// Remove the RBAC resources if the user provided pull secret is not present
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling global pull secret RBAC")

	// Create ServiceAccount
	sa := manifests.GlobalPullSecretSyncerServiceAccount()
	if _, err := createOrUpdate(ctx, c, sa, func() error { return nil }); err != nil {
		return fmt.Errorf("failed to reconcile service account: %w", err)
	}

	// Create Role and RoleBinding for kube-system namespace
	globalPullSecretRole := manifests.GlobalPullSecretSyncerRole(kubeSystemNS)
	if _, err := createOrUpdate(ctx, c, globalPullSecretRole, func() error {
		globalPullSecretRole.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"list", "watch"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{"additional-pull-secret", "global-pull-secret"},
				Verbs:         []string{"get"},
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret syncer role in kube-system: %w", err)
	}

	// Create RoleBinding for kube-system namespace
	globalPullSecretRoleBinding := manifests.GlobalPullSecretSyncerRoleBinding(kubeSystemNS)
	if _, err := createOrUpdate(ctx, c, globalPullSecretRoleBinding, func() error {
		globalPullSecretRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     globalPullSecretRole.Name,
		}
		globalPullSecretRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret syncer role binding in kube-system: %w", err)
	}

	// Create Role and RoleBinding for openshift-config namespace
	globalPullSecretOpenshiftConfigRole := manifests.GlobalPullSecretSyncerRole(openshiftConfigNS)
	if _, err := createOrUpdate(ctx, c, globalPullSecretOpenshiftConfigRole, func() error {
		globalPullSecretOpenshiftConfigRole.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{"pull-secret"},
				Verbs:         []string{"get"},
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret syncer role in openshift-config: %w", err)
	}

	// Create RoleBinding for openshift-config namespace
	globalPullSecretOpenshiftConfigRoleBinding := manifests.GlobalPullSecretSyncerRoleBinding(openshiftConfigNS)
	if _, err := createOrUpdate(ctx, c, globalPullSecretOpenshiftConfigRoleBinding, func() error {
		globalPullSecretOpenshiftConfigRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     globalPullSecretOpenshiftConfigRole.Name,
		}
		globalPullSecretOpenshiftConfigRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret syncer role binding in openshift-config: %w", err)
	}

	return nil
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
