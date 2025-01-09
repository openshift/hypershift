package nodepool

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/google/uuid"
)

const (
	TokenSecretTokenGenerationTime       = "hypershift.openshift.io/last-token-generation-time"
	TokenSecretReleaseKey                = "release"
	TokenSecretTokenKey                  = "token"
	TokenSecretPullSecretHashKey         = "pull-secret-hash"
	TokenSecretHCConfigurationHashKey    = "hc-configuration-hash"
	TokenSecretAdditionalTrustBundleKey  = "additional-trust-bundle-hash"
	TokenSecretConfigKey                 = "config"
	TokenSecretAnnotation                = "hypershift.openshift.io/ignition-config"
	TokenSecretIgnitionReachedAnnotation = "hypershift.openshift.io/ignition-reached"
	TokenSecretNodePoolUpgradeType       = "hypershift.openshift.io/node-pool-upgrade-type"
)

// Token knows how to create an UUUID token for a unique configGenerator Hash.
// It also knows how to manage the lifecycle of a corresponding token secret that it is used by the tokenSecret controller to generate the final ignition payload
// and a user data secret that points to the ignition server URL using the UUUID as an authenticator header to get that payload.
type Token struct {
	upsert.CreateOrUpdateProvider
	cpoCapabilities *CPOCapabilities
	*ConfigGenerator
	// TODO(alberto): we don't really support content inplace changes for fields like pull secret and AdditionalTrustBundle.
	// In fact we only trigger a rollout if the .Name referenced in the field changes.
	// Consider removing these hash checks and consolidate with the rolloutConfig struct input.
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
	pullSecretHash            []byte
	additionalTrustBundleHash []byte
	globalConfigHash          []byte
	userData                  *userData
}

// userData contains the input necessary to generate the user data secret
// that points to the ignition server URL using the UUUID token as an authenticator header.
type userData struct {
	caCert                 []byte
	ignitionServerEndpoint string
	proxy                  *configv1.Proxy
	ami                    string
}

// NewToken is the contract to create a new Token struct.
func NewToken(ctx context.Context, configGenerator *ConfigGenerator, cpoCapabilities *CPOCapabilities) (*Token, error) {
	if configGenerator == nil {
		return nil, fmt.Errorf("configGenerator can't be nil")
	}

	if cpoCapabilities == nil {
		return nil, fmt.Errorf("cpoCapabilities can't be nil")
	}

	// TODO(alberto): tempReconciler is a NodePoolReconciler used temporarily until getPullSecretBytes and getAdditionalTrustBundle are factored.
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
	tempReconciler := &NodePoolReconciler{
		Client: configGenerator.Client,
	}
	pullSecretBytes, err := tempReconciler.getPullSecretBytes(ctx, configGenerator.hostedCluster)
	if err != nil {
		return nil, err
	}

	additionalTrustBundleCM := &corev1.ConfigMap{}
	additionalTrustBundle := ""
	if configGenerator.hostedCluster.Spec.AdditionalTrustBundle != nil {
		additionalTrustBundleCM, err = tempReconciler.getAdditionalTrustBundle(ctx, configGenerator.hostedCluster)
		if err != nil {
			return nil, err
		}
		additionalTrustBundle = additionalTrustBundleCM.Data["ca-bundle.crt"]
	}

	// TODO(alberto): This hash should be consolidated with configGenerator using globalConfigString as that is what configGenerator uses to create a configGenerator.Hash() and so what triggers a rollout.
	// This inconsistency was introduced by https://github.com/openshift/hypershift/pull/3795
	// See reconcileTokenSecret and https://github.com/openshift/hypershift/pull/4057 for more info on how this is used.
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
	hcConfigurationHash, err := supportutil.HashStruct(configGenerator.hostedCluster.Spec.Configuration)
	if err != nil {
		return nil, fmt.Errorf("failed to hash HostedCluster configuration: %w", err)
	}

	token := &Token{
		CreateOrUpdateProvider:    upsert.New(false),
		ConfigGenerator:           configGenerator,
		cpoCapabilities:           cpoCapabilities,
		pullSecretHash:            []byte(supportutil.HashSimple(pullSecretBytes)),
		additionalTrustBundleHash: []byte(supportutil.HashSimple(additionalTrustBundle)),
		globalConfigHash:          []byte(hcConfigurationHash),
	}

	// User data input.
	caCert, err := token.getIgnitionCACert(ctx)
	if err != nil {
		return nil, err
	}

	ignEndpoint := configGenerator.hostedCluster.Status.IgnitionEndpoint
	if ignEndpoint == "" {
		return nil, fmt.Errorf("ignition endpoint is not set")
	}

	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatusFromHostedCluster(proxy, configGenerator.hostedCluster)

	ami := ""
	if configGenerator.hostedCluster.Spec.Platform.AWS != nil {
		ami, err = defaultNodePoolAMI(configGenerator.hostedCluster.Spec.Platform.AWS.Region, configGenerator.nodePool.Spec.Arch, configGenerator.releaseImage)
		if err != nil {
			return nil, err
		}
	}

	token.userData = &userData{
		ignitionServerEndpoint: ignEndpoint,
		caCert:                 caCert,
		proxy:                  proxy,
		ami:                    ami,
	}

	return token, nil
}

// getInitionCACert gets the ignition CA cert from a secret.
// It's needed to generate a valid ignition config within the user data secret.
func (t *Token) getIgnitionCACert(ctx context.Context) ([]byte, error) {
	// Validate Ignition CA Secret.
	caSecret := ignitionserver.IgnitionCACertSecret(t.controlplaneNamespace)
	if err := t.Get(ctx, client.ObjectKeyFromObject(caSecret), caSecret); err != nil {
		return nil, err
	}

	caCertBytes, hasCACert := caSecret.Data[corev1.TLSCertKey]
	if !hasCACert {
		return nil, fmt.Errorf("CA Secret is missing tls.crt key")
	}

	return caCertBytes, nil
}

func (t *Token) isOutdated() bool {
	return t.Hash() != t.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]
}

func (t *Token) cleanupOutdated(ctx context.Context) error {
	tokenSecret := t.outdatedTokenSecret()
	err := t.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get token Secret: %w", err)
	}
	if err == nil {
		if err := setExpirationTimestampOnToken(ctx, t.Client, tokenSecret, nil); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to set expiration on token Secret: %w", err)
		}
	}

	// For AWS, we keep the old userdata Secret so old Machines during rolled out can be deleted.
	// Otherwise, deletion fails because of https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3805.
	// TODO (Alberto): enable back deletion when the PR above gets merged.
	if t.nodePool.Spec.Platform.Type != hyperv1.AWSPlatform {
		userDataSecret := t.outdatedUserDataSecret()
		err = t.Get(ctx, client.ObjectKeyFromObject(userDataSecret), userDataSecret)
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get user data Secret: %w", err)
		}
		if err == nil {
			if err := t.Delete(ctx, userDataSecret); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete user data Secret: %w", err)
			}
		}
	}
	return nil
}

func setExpirationTimestampOnToken(ctx context.Context, c client.Client, tokenSecret *corev1.Secret, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}

	// this should be a reasonable value to allow all in flight provisions to complete.
	timeUntilExpiry := 2 * time.Hour
	if tokenSecret.Annotations == nil {
		tokenSecret.Annotations = map[string]string{}
	}
	tokenSecret.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation] = now().Add(timeUntilExpiry).Format(time.RFC3339)
	return c.Update(ctx, tokenSecret)
}

func (t *Token) Reconcile(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	if t.isOutdated() {
		if err := t.cleanupOutdated(ctx); err != nil {
			return fmt.Errorf("failed to cleanup outdated token Secrets: %w", err)
		}
	}

	tokenSecret := t.TokenSecret()
	if result, err := t.CreateOrUpdate(ctx, t.Client, tokenSecret, func() error {
		return t.reconcileTokenSecret(tokenSecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile token Secret: %w", err)
	} else {
		log.Info("Reconciled token Secret", "result", result)
	}

	tokenBytes, hasToken := tokenSecret.Data[TokenSecretTokenKey]
	if !hasToken {
		// This should never happen by design.
		return fmt.Errorf("token secret is missing token key")
	}

	userDataSecret := t.UserDataSecret()
	if result, err := t.CreateOrUpdate(ctx, t.Client, userDataSecret, func() error {
		return t.reconcileUserDataSecret(userDataSecret, string(tokenBytes))
	}); err != nil {
		return err
	} else {
		log.Info("Reconciled user data Secret", "result", result)
	}
	return nil
}

const UserDataSecrePrefix = "user-data"

func (t *Token) UserDataSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, t.ConfigGenerator.nodePool.GetName(), t.ConfigGenerator.Hash()),
		},
	}
}

func (t *Token) outdatedUserDataSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, t.ConfigGenerator.nodePool.GetName(), t.nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion]),
		},
	}
}

const TokenSecretPrefix = "token"

func (t *Token) TokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, t.ConfigGenerator.nodePool.GetName(), t.ConfigGenerator.Hash()),
		},
	}
}

func (t *Token) outdatedTokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, t.ConfigGenerator.nodePool.GetName(), t.nodePool.GetAnnotations()[nodePoolAnnotationCurrentConfigVersion]),
		},
	}
}

func (t *Token) reconcileTokenSecret(tokenSecret *corev1.Secret) error {
	// The token secret controller updates expired token IDs for token Secrets.
	// When that happens the NodePool controller reconciles the userData Secret with the new token ID.
	// Therefore, this secret is mutable.
	tokenSecret.Immutable = ptr.To(false)
	if tokenSecret.Annotations == nil {
		tokenSecret.Annotations = make(map[string]string)
	}

	tokenSecret.Annotations[TokenSecretAnnotation] = "true"
	tokenSecret.Annotations[TokenSecretNodePoolUpgradeType] = string(t.nodePool.Spec.Management.UpgradeType)
	tokenSecret.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(t.nodePool).String()
	// active token should never be marked as expired.
	delete(tokenSecret.Annotations, hyperv1.IgnitionServerTokenExpirationTimestampAnnotation)

	if tokenSecret.Data == nil {
		// 2. - Reconcile towards expected state of the world.
		compressedConfig, err := t.CompressedAndEncoded()
		if err != nil {
			return fmt.Errorf("failed to compress and decode config: %w", err)
		}

		// TODO (alberto): Drop this after dropping < 4.12 support.
		// So all CPOs ign server will know to decompress and decode.
		if !t.cpoCapabilities.DecompressAndDecodeConfig {
			compressedConfig, err = t.Compressed()
			if err != nil {
				return fmt.Errorf("failed to compress config: %w", err)
			}
		}

		tokenSecret.Data = map[string][]byte{}
		tokenSecret.Annotations[TokenSecretTokenGenerationTime] = time.Now().Format(time.RFC3339Nano)
		tokenSecret.Data[TokenSecretTokenKey] = []byte(uuid.New().String())
		tokenSecret.Data[TokenSecretReleaseKey] = []byte(t.nodePool.Spec.Release.Image)
		tokenSecret.Data[TokenSecretConfigKey] = compressedConfig.Bytes()

		// Hash values that are used by the "token secret controller" / "local ignition provider"  to determine if this input
		// have changed before generating a payload for it.
		tokenSecret.Data[TokenSecretPullSecretHashKey] = t.pullSecretHash
		tokenSecret.Data[TokenSecretAdditionalTrustBundleKey] = t.additionalTrustBundleHash
		tokenSecret.Data[TokenSecretHCConfigurationHashKey] = t.globalConfigHash
	}
	// TODO (alberto): Only apply this on creation and change the hash generation to only use triggering upgrade fields.
	// We let this change to happen inplace now as the tokenSecret and the mcs config use the whole spec.Config for the comparing hash.
	// Otherwise if something which does not trigger a new token generation from spec.Config changes, like .IDP, both hashes would mismatch forever.
	tokenSecret.Data[TokenSecretHCConfigurationHashKey] = t.globalConfigHash

	return nil
}

func (t *Token) reconcileUserDataSecret(userDataSecret *corev1.Secret, token string) error {
	// The token secret controller deletes expired token Secrets.
	// When that happens the NodePool controller reconciles and create a new one.
	// Then it reconciles the userData Secret with the new generated token.
	// Therefore, this secret is mutable.
	userDataSecret.Immutable = ptr.To(false)

	if userDataSecret.Annotations == nil {
		userDataSecret.Annotations = make(map[string]string)
	}
	userDataSecret.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(t.nodePool).String()
	if userDataSecret.Labels == nil {
		userDataSecret.Labels = make(map[string]string)
	}

	if t.hostedCluster.Spec.AutoNode != nil && t.hostedCluster.Spec.AutoNode.Provisioner.Name == hyperv1.ProvisionerKarpeneter &&
		t.hostedCluster.Spec.AutoNode.Provisioner.Karpenter.Platform == hyperv1.AWSPlatform {
		// TODO(alberto): prevent nodePool name collisions adding prefix to karpenter NodePool.
		if t.nodePool.GetName() == "karpenter" {
			userDataSecret.Labels[hyperv1.NodePoolLabel] = fmt.Sprintf("%s-%s", t.nodePool.Spec.ClusterName, t.nodePool.GetName())
			userDataSecret.Labels["hypershift.openshift.io/ami"] = t.userData.ami
		}

	}

	encodedCACert := base64.StdEncoding.EncodeToString(t.userData.caCert)
	encodedToken := base64.StdEncoding.EncodeToString([]byte(token))
	ignConfig := ignConfig(encodedCACert, encodedToken, t.userData.ignitionServerEndpoint, t.Hash(), t.userData.proxy, t.nodePool)
	userDataValue, err := json.Marshal(ignConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal ignition config: %w", err)
	}
	userDataSecret.Data = map[string][]byte{
		"disableTemplating": []byte(base64.StdEncoding.EncodeToString([]byte("true"))),
		"value":             userDataValue,
	}
	return nil
}

func ignConfig(encodedCACert, encodedToken, endpoint, targetConfigVersionHash string, proxy *configv1.Proxy, nodePool *hyperv1.NodePool) ignitionapi.Config {
	cfg := ignitionapi.Config{
		Ignition: ignitionapi.Ignition{
			Version: "3.2.0",
			Security: ignitionapi.Security{
				TLS: ignitionapi.TLS{
					CertificateAuthorities: []ignitionapi.Resource{
						{
							Source: ptr.To(fmt.Sprintf("data:text/plain;base64,%s", encodedCACert)),
						},
					},
				},
			},
			Config: ignitionapi.IgnitionConfig{
				Merge: []ignitionapi.Resource{
					{
						Source: ptr.To(fmt.Sprintf("https://%s/ignition", endpoint)),
						HTTPHeaders: []ignitionapi.HTTPHeader{
							{
								Name:  "Authorization",
								Value: ptr.To(fmt.Sprintf("Bearer %s", encodedToken)),
							},
							{
								Name:  "NodePool",
								Value: ptr.To(client.ObjectKeyFromObject(nodePool).String()),
							},
							{
								Name:  "TargetConfigVersionHash",
								Value: ptr.To(targetConfigVersionHash),
							},
						},
					},
				},
			},
		},
	}
	if proxy.Status.HTTPProxy != "" {
		cfg.Ignition.Proxy.HTTPProxy = ptr.To(proxy.Status.HTTPProxy)
	}
	if proxy.Status.HTTPSProxy != "" {
		cfg.Ignition.Proxy.HTTPSProxy = ptr.To(proxy.Status.HTTPSProxy)
	}
	if proxy.Status.NoProxy != "" {
		for _, item := range strings.Split(proxy.Status.NoProxy, ",") {
			cfg.Ignition.Proxy.NoProxy = append(cfg.Ignition.Proxy.NoProxy, ignitionapi.NoProxyItem(item))
		}
	}
	return cfg
}
