package secretencryption

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/go-logr/logr"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// EncryptionConfigurationKey is the data key used in the Secret that holds the EncryptionConfiguration YAML.
	EncryptionConfigurationKey = "config.yaml"

	// EncryptionConfigurationKind is the Kind used in EncryptionConfiguration manifests.
	EncryptionConfigurationKind = "EncryptionConfiguration"

	// EncryptionConfigHashAnnotation is set on the KAS pod template to record the
	// encryption config content that triggered the current rollout.
	EncryptionConfigHashAnnotation = "kube-apiserver.hypershift.openshift.io/encryption-config-hash"
)

var (
	encScheme   = runtime.NewScheme()
	yamlDecoder runtime.Decoder
)

func init() {
	_ = apiserverv1.AddToScheme(encScheme)
	yamlDecoder = json.NewSerializerWithOptions(json.DefaultMetaFactory, encScheme, encScheme, json.SerializerOptions{Yaml: true})
}

// EncryptionConfigHash returns a stable hash of encryption config bytes.
// The format matches the kube-apiserver encryption config controller.
func EncryptionConfigHash(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

// SetEncryptionConfigHashAnnotation records the encryption config hash on a pod template.
func SetEncryptionConfigHashAnnotation(podTemplate *corev1.PodTemplateSpec, configBytes []byte) {
	hash := EncryptionConfigHash(configBytes)
	if hash == "" {
		return
	}
	if podTemplate.Annotations == nil {
		podTemplate.Annotations = map[string]string{}
	}
	podTemplate.Annotations[EncryptionConfigHashAnnotation] = hash
}

// KASDeploymentConvergedWithEncryptionConfig reports whether the KAS deployment
// has fully rolled out with the given encryption config hash and all old pods
// have finished terminating. Kubernetes deployment status fields exclude
// terminating pods, so IsDeploymentReady alone returns true while old pods are
// still running — this function additionally checks for their absence.
func KASDeploymentConvergedWithEncryptionConfig(ctx context.Context, c client.Reader, deployment *appsv1.Deployment, expectedConfigHash string) bool {
	if expectedConfigHash == "" || !podspec.IsDeploymentReady(ctx, deployment) {
		return false
	}
	if deployment.Spec.Template.Annotations[EncryptionConfigHashAnnotation] != expectedConfigHash {
		return false
	}
	return !hasTerminatingPods(ctx, c, deployment)
}

// hasTerminatingPods returns true if any pods matching the deployment's selector
// have a non-nil DeletionTimestamp (i.e. are still shutting down). Returns true
// on error to fail closed.
func hasTerminatingPods(ctx context.Context, c client.Reader, deployment *appsv1.Deployment) bool {
	logger := logr.FromContextOrDiscard(ctx)
	if deployment.Spec.Selector == nil {
		return false
	}
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		logger.Error(err, "Failed to parse deployment selector, failing closed")
		return true
	}
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList,
		client.InNamespace(deployment.Namespace),
		client.MatchingLabelsSelector{Selector: selector}); err != nil {
		logger.Error(err, "Failed to list pods for termination check, failing closed")
		return true
	}
	for i := range podList.Items {
		if podList.Items[i].DeletionTimestamp != nil {
			return true
		}
	}
	return false
}

// DecodeEncryptionConfiguration parses raw YAML bytes into an EncryptionConfiguration.
func DecodeEncryptionConfiguration(data []byte) (*apiserverv1.EncryptionConfiguration, error) {
	cfg := &apiserverv1.EncryptionConfiguration{}
	gvks, _, err := encScheme.ObjectKinds(cfg)
	if err != nil || len(gvks) == 0 {
		return nil, fmt.Errorf("cannot determine gvk: %w", err)
	}
	if _, _, err := yamlDecoder.Decode(data, &gvks[0], cfg); err != nil {
		return nil, fmt.Errorf("cannot decode EncryptionConfiguration: %w", err)
	}
	return cfg, nil
}

// TargetKeyRole represents where the target key appears in the EncryptionConfiguration.
type TargetKeyRole int

const (
	TargetKeyAbsent   TargetKeyRole = iota // target key not in config
	TargetKeyReadOnly                      // target key is a read-only provider (not first)
	TargetKeyWrite                         // target key is the write provider (first)
)

// FindKeyRole locates the target key name in the EncryptionConfiguration and
// returns its role. For KMS, each key is a separate provider entry; the first
// KMS provider is the write key. For AESCBC, keys are entries inside a single
// AESCBC provider; the first key is the write key.
func FindKeyRole(cfg *apiserverv1.EncryptionConfiguration, targetName string, encType hyperv1.SecretEncryptionType) TargetKeyRole {
	if cfg == nil || len(cfg.Resources) == 0 {
		return TargetKeyAbsent
	}
	providers := cfg.Resources[0].Providers

	switch encType {
	case hyperv1.KMS:
		kmsIndex := -1
		firstKMSIndex := -1
		for i, p := range providers {
			if p.KMS != nil {
				if firstKMSIndex == -1 {
					firstKMSIndex = i
				}
				if p.KMS.Name == targetName {
					kmsIndex = i
					break
				}
			}
		}
		if kmsIndex == -1 {
			return TargetKeyAbsent
		}
		if kmsIndex == firstKMSIndex {
			return TargetKeyWrite
		}
		return TargetKeyReadOnly

	case hyperv1.AESCBC:
		for _, p := range providers {
			if p.AESCBC != nil {
				for j, key := range p.AESCBC.Keys {
					if key.Name == targetName {
						if j == 0 {
							return TargetKeyWrite
						}
						return TargetKeyReadOnly
					}
				}
			}
		}
		return TargetKeyAbsent
	}

	return TargetKeyAbsent
}

// ShouldPromoteTargetKey determines whether the target key should be promoted
// to write provider based on the current EncryptionConfiguration and KAS
// convergence state.
//
// Returns true when the target key should be the write key (WritePromote/Migrating stage).
// Returns false when the old key should remain the write key (ReadOnlyDeploy stage).
func ShouldPromoteTargetKey(cfg *apiserverv1.EncryptionConfiguration, targetName string, encType hyperv1.SecretEncryptionType, kasConverged bool) bool {
	role := FindKeyRole(cfg, targetName, encType)
	return role == TargetKeyWrite || (role == TargetKeyReadOnly && kasConverged)
}
