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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

var (
	maxUnavailable = intstr.FromString("10%")
	maxSurge       = intstr.FromInt(0)
)

const (
	NodePullSecretPath = "/var/lib/kubelet/config.json"
)

func ReconcileDaemonSet(ctx context.Context, daemonSet *appsv1.DaemonSet, globalPullSecretName string, c crclient.Client, createOrUpdate upsert.CreateOrUpdateFN, cpoImage string) error {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get logger: %w", err)
	}

	log.Info("Reconciling global pull secret daemon set")

	if _, err := createOrUpdate(ctx, c, daemonSet, func() error {
		daemonSet.Spec = appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "global-pull-secret-syncer",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "global-pull-secret-syncer",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           "global-pull-secret-syncer",
					AutomountServiceAccountToken: ptr.To(true),
					SecurityContext:              &corev1.PodSecurityContext{},
					DNSPolicy:                    corev1.DNSDefault,
					Tolerations:                  []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{
						{
							Name:            "global-pull-secret-syncer",
							Image:           cpoImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command: []string{
								"/usr/bin/control-plane-operator",
							},
							Args: []string{
								"sync-global-pullsecret",
								fmt.Sprintf("--global-pull-secret-name=%s", globalPullSecretName),
								"--check-interval=10s",
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
								},
							},
						},
					},
				},
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.RollingUpdateDaemonSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDaemonSet{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
			},
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create global pull secret daemon set: %w", err)
	}

	return nil
}

func ValidateUserProvidedPullSecret(pullSecret *corev1.Secret) ([]byte, error) {
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
func MergePullSecrets(ctx context.Context, originalPullSecret, userProvidedPullSecret []byte) ([]byte, error) {
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

func ReconcileGlobalPullSecretRBAC(ctx context.Context, c crclient.Client, createOrUpdate upsert.CreateOrUpdateFN, namespace string) error {
	// Remove the RBAC resources if the user provided pull secret is not present
	log, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get logger: %w", err)
	}

	exists, err := UserProvidedPullSecretExists(c, ctx)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check if user provided pull secret exists: %w", err)
		}
	}

	log.Info("Reconciling global pull secret RBAC", "exists", exists)

	if !exists {
		log.Info("Deleting global pull secret RBAC resources", "exists", exists)
		sa := manifests.GlobalPullSecretServiceAccount()
		if err := c.Delete(ctx, sa); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete service account for global pull secret: %w", err)
			}
		}
		clusterRole := manifests.GlobalPullSecretClusterRole()
		if err := c.Delete(ctx, clusterRole); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete cluster role for global pull secret: %w", err)
			}
		}
		clusterRoleBinding := manifests.GlobalPullSecretClusterRoleBinding()
		if err := c.Delete(ctx, clusterRoleBinding); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete cluster role binding for global pull secret: %w", err)
			}
		}

		return nil
	}

	log.Info("Creating global pull secret RBAC resources", "exists", exists)

	// Create ServiceAccount
	sa := manifests.GlobalPullSecretServiceAccount()
	if _, err := createOrUpdate(ctx, c, sa, func() error { return nil }); err != nil {
		return fmt.Errorf("failed to reconcile service account: %w", err)
	}

	// Create ClusterRole
	clusterRole := manifests.GlobalPullSecretClusterRole()
	if _, err := createOrUpdate(ctx, c, clusterRole, func() error {
		clusterRole.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list", "watch"},
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret syncer cluster role: %w", err)
	}

	// Create ClusterRoleBinding
	clusterRoleBinding := manifests.GlobalPullSecretClusterRoleBinding()
	if _, err := createOrUpdate(ctx, c, clusterRoleBinding, func() error {
		clusterRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     clusterRole.Name,
		}
		clusterRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret syncer cluster role binding: %w", err)
	}

	return nil
}

func UserProvidedPullSecretExists(c crclient.Client, ctx context.Context) (bool, error) {
	userProvidedPullSecret := manifests.UserProvidedPullSecret()
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(userProvidedPullSecret), userProvidedPullSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
