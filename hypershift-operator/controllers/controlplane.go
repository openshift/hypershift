package controllers

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io"
	"math/big"
	"math/rand"
	"os"

	"sigs.k8s.io/cluster-api/util"

	"github.com/blang/semver"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	"openshift.io/hypershift/hypershift-operator/releaseinfo"
	hypershiftcp "openshift.io/hypershift/hypershift-operator/render/controlplane/hypershift"
	"openshift.io/hypershift/hypershift-operator/render/controlplane/hypershift/pki"
)

const (
	externalOauthPort         = 8443
	APIServerPort             = 6443
	DefaultAPIServerIPAddress = "172.20.0.1"
	oauthBrandingManifest     = "v4-0-config-system-branding.yaml"
)

var (
	excludeManifests = sets.NewString(
		"openshift-apiserver-service.yaml",
		"v4-0-config-system-branding.yaml",
		"oauth-server-service.yaml",
		"kube-apiserver-service.yaml",
	)

	version46 = semver.MustParse("4.6.0")
)

func (r *HostedControlPlaneReconciler) ensureControlPlane(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus, releaseImage *releaseinfo.ReleaseImage) error {
	r.Log.Info("ensuring control plane for cluster", "cluster", hcp.Name)

	name := hcp.Name

	var pullSecret corev1.Secret
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.PullSecret.Name}, &pullSecret)
	if err != nil {
		return fmt.Errorf("failed to get pull secret %s: %w", hcp.Spec.PullSecret.Name, err)
	}
	pullSecretData, hasPullSecretData := pullSecret.Data[".dockerconfigjson"]
	if !hasPullSecretData {
		return fmt.Errorf("pull secret %s is missing the .dockerconfigjson key", hcp.Spec.PullSecret.Name)
	}
	version, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("cannot parse release version (%s): %v", releaseImage.Version(), err)
	}
	controlPlaneOperatorImage, err := r.LookupControlPlaneOperatorImage(r.Client)
	if err != nil {
		return fmt.Errorf("failed to lookup control plane operator image: %w", err)
	}
	var sshKeySecret corev1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.SSHKey.Name}, &sshKeySecret)
	if err != nil {
		return fmt.Errorf("failed to get SSH key secret %s: %w", hcp.Spec.SSHKey.Name, err)
	}
	sshKeyData, hasSSHKeyData := sshKeySecret.Data["id_rsa.pub"]
	if !hasSSHKeyData {
		return fmt.Errorf("SSH key secret secret %s is missing the id_rsa.pub key", hcp.Spec.SSHKey.Name)
	}
	baseDomain, err := ClusterBaseDomain(r.Client, ctx, hcp.Name)
	if err != nil {
		return fmt.Errorf("couldn't determine cluster base domain  name: %w", err)
	}

	params := hypershiftcp.NewClusterParams()
	params.Namespace = name
	params.ExternalAPIDNSName = infraStatus.APIAddress
	params.ExternalAPIPort = APIServerPort
	params.ExternalAPIAddress = DefaultAPIServerIPAddress
	params.ExternalOpenVPNAddress = infraStatus.VPNAddress
	params.ExternalOpenVPNPort = 1194
	params.ExternalOauthDNSName = infraStatus.OAuthAddress
	params.ExternalOauthPort = externalOauthPort
	params.ServiceCIDR = hcp.Spec.ServiceCIDR
	params.PodCIDR = hcp.Spec.PodCIDR
	params.ReleaseImage = hcp.Spec.ReleaseImage
	params.IngressSubdomain = fmt.Sprintf("apps.%s", baseDomain)
	params.OpenShiftAPIClusterIP = infraStatus.OpenShiftAPIAddress
	params.BaseDomain = baseDomain
	params.MachineConfigServerAddress = infraStatus.IgnitionProviderAddress
	params.CloudProvider = string(r.Infra.Status.PlatformStatus.Type)
	params.PlatformType = string(r.Infra.Status.PlatformStatus.Type)
	params.InternalAPIPort = APIServerPort
	params.EtcdClientName = "etcd-client"
	params.NetworkType = "OpenShiftSDN"
	params.ImageRegistryHTTPSecret = generateImageRegistrySecret()
	params.Replicas = "1"
	params.SSHKey = string(sshKeyData)
	params.ControlPlaneOperatorImage = controlPlaneOperatorImage
	params.HypershiftOperatorControllers = []string{"route-sync", "auto-approver", "kubeadmin-password", "node"}

	// Generate PKI data just once and store it in a secret. PKI generation isn't
	// deterministic and shouldn't be performed with every reconcile, otherwise
	// we're effectively doing an uncontrolled cert rotation each generation.
	pkiSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: name,
			Name:      "pki",
		},
		Data: map[string][]byte{},
	}
	needsPkiSecret := false
	if err := r.Get(ctx, client.ObjectKeyFromObject(pkiSecret), pkiSecret); err != nil {
		if apierrors.IsNotFound(err) {
			needsPkiSecret = true
		} else {
			return fmt.Errorf("failed to get pki secret: %w", err)
		}
	} else {
		r.Log.Info("using existing pki secret")
	}
	if needsPkiSecret {
		pkiParams := &hypershiftcp.PKIParams{
			ExternalAPIAddress:         infraStatus.APIAddress,
			NodeInternalAPIServerIP:    DefaultAPIServerIPAddress,
			ExternalAPIPort:            APIServerPort,
			InternalAPIPort:            APIServerPort,
			ServiceCIDR:                hcp.Spec.ServiceCIDR,
			ExternalOauthAddress:       infraStatus.OAuthAddress,
			IngressSubdomain:           "apps." + baseDomain,
			MachineConfigServerAddress: infraStatus.IgnitionProviderAddress,
			ExternalOpenVPNAddress:     infraStatus.VPNAddress,
			Namespace:                  name,
		}
		log.Info("generating PKI secret data")
		data, err := pki.GeneratePKI(pkiParams)
		if err != nil {
			return fmt.Errorf("failed to generate PKI data: %w", err)
		}
		pkiSecret.Data = data
		if err := r.Create(ctx, pkiSecret); err != nil {
			return fmt.Errorf("failed to create pki secret: %w", err)
		}
		r.Log.Info("created pki secret")
	}

	caBytes := pkiSecret.Data["combined-ca.crt"]
	if err != nil {
		return fmt.Errorf("failed to read combined CA: %w", err)
	}
	params.OpenshiftAPIServerCABundle = base64.StdEncoding.EncodeToString(caBytes)

	manifests, err := hypershiftcp.RenderClusterManifests(params, releaseImage, pullSecretData, pkiSecret.Data)
	if err != nil {
		return fmt.Errorf("failed to render hypershift manifests for cluster: %w", err)
	}

	// Create oauth branding manifest because it cannot be applied
	//manifestBytes := manifests[oauthBrandingManifest]
	//manifestObj := &unstructured.Unstructured{}
	//if err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(manifestBytes)), 100).Decode(manifestObj); err != nil {
	//	return fmt.Errorf("failed to decode manifest %s: %w", oauthBrandingManifest, err)
	//}
	//manifestObj.SetNamespace(name)
	//if err = r.Create(context.TODO(), manifestObj); err != nil {
	//	if !apierrors.IsAlreadyExists(err) {
	//		return fmt.Errorf("failed to apply manifest %s: %w", oauthBrandingManifest, err)
	//	}
	//}

	// Use server side apply for manifestss
	applyErrors := []error{}
	for manifestName, manifestBytes := range manifests {
		if excludeManifests.Has(manifestName) {
			continue
		}
		obj := &unstructured.Unstructured{}
		if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifestBytes), 100).Decode(obj); err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to decode manifest %s: %w", manifestName, err))
		}
		obj.SetNamespace(name)
		err = r.Patch(ctx, obj, client.RawPatch(types.ApplyPatchType, manifestBytes), client.ForceOwnership, client.FieldOwner("hypershift-operator"))
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to apply manifest %s: %w", manifestName, err))
		} else {
			r.Log.Info("applied manifest", "manifest", manifestName)
		}
	}
	if errs := errors.NewAggregate(applyErrors); errs != nil {
		return fmt.Errorf("failed to apply some manifests: %w", errs)
	}
	r.Log.Info("successfully applied all manifests")

	userDataSecret := generateUserDataSecret(hcp.GetName(), hcp.GetNamespace(), infraStatus.IgnitionProviderAddress, version)
	if err := r.Create(ctx, userDataSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate user data secret: %w", err)
	}
	userDataSecret.OwnerReferences = util.EnsureOwnerRef(userDataSecret.OwnerReferences, metav1.OwnerReference{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "HostedControlPlane",
		Name:       hcp.GetName(),
		UID:        hcp.UID,
	})

	kubeadminPassword, err := generateKubeadminPassword()
	if err != nil {
		return fmt.Errorf("failed to generate kubeadmin password: %w", err)
	}

	kubeadminPasswordTargetSecret, err := generateKubeadminPasswordTargetSecret(r.Scheme(), kubeadminPassword, name)
	if err != nil {
		return fmt.Errorf("failed to create kubeadmin secret manifest for target cluster: %w", err)
	}
	if err := r.Create(ctx, kubeadminPasswordTargetSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate kubeadminPasswordTargetSecret: %w", err)
	}

	kubeadminPasswordSecret := generateKubeadminPasswordSecret(name, kubeadminPassword)
	if err := r.Create(ctx, kubeadminPasswordSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate kubeadminPasswordSecret: %w", err)
	}

	kubeconfigSecret, err := generateKubeconfigSecret(hcp.GetName(), hcp.GetNamespace(), pkiSecret.Data["admin.kubeconfig"])
	if err != nil {
		return fmt.Errorf("failed to create kubeconfig secret manifest for management cluster: %w", err)
	}
	if err := r.Create(ctx, kubeconfigSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate kubeconfigSecret: %w", err)
	}
	kubeconfigSecret.OwnerReferences = util.EnsureOwnerRef(kubeconfigSecret.OwnerReferences, metav1.OwnerReference{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "HostedControlPlane",
		Name:       hcp.GetName(),
		UID:        hcp.UID,
	})

	targetPullSecret, err := generateTargetPullSecret(r.Scheme(), pullSecretData, name)
	if err != nil {
		return fmt.Errorf("failed to create pull secret manifest for target cluster: %w", err)
	}
	if err := r.Create(ctx, targetPullSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate targetPullSecret: %v", err)
	}

	log.Infof("Cluster API URL: %s", fmt.Sprintf("https://%s:%d", infraStatus.APIAddress, APIServerPort))
	log.Infof("Kubeconfig is available in secret %q in the %s namespace", fmt.Sprintf("%s-kubeconfig", name), hcp.GetNamespace())
	log.Infof("Console URL:  %s", fmt.Sprintf("https://console-openshift-console.%s", params.IngressSubdomain))
	log.Infof("kubeadmin password is available in secret %q in the %s namespace", "kubeadmin-password", name)

	return nil
}

func generateTargetPullSecret(scheme *runtime.Scheme, data []byte, namespace string) (*corev1.ConfigMap, error) {
	secret := &corev1.Secret{}
	secret.Name = "pull-secret"
	secret.Namespace = "openshift-config"
	secret.Data = map[string][]byte{".dockerconfigjson": data}
	secret.Type = corev1.SecretTypeDockerConfigJson
	secretBytes, err := runtime.Encode(serializer.NewCodecFactory(scheme).LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return nil, err
	}
	configMap := &corev1.ConfigMap{}
	configMap.Namespace = namespace
	configMap.Name = "user-manifest-pullsecret"
	configMap.Data = map[string]string{"data": string(secretBytes)}
	return configMap, nil
}

func generateUserDataSecret(name, namespace string, ignitionProviderAddr string, version semver.Version) *corev1.Secret {
	secret := &corev1.Secret{}
	secret.Name = fmt.Sprintf("%s-user-data", name)
	secret.Namespace = namespace

	disableTemplatingValue := []byte(base64.StdEncoding.EncodeToString([]byte("true")))
	var userDataValue []byte

	// Clear any version modifiers for this comparison
	version.Pre = nil
	version.Build = nil
	if version.GTE(version46) {
		userDataValue = []byte(fmt.Sprintf(`{"ignition":{"config":{"merge":[{"source":"http://%s/config/master","verification":{}}]},"security":{},"timeouts":{},"version":"3.1.0"},"networkd":{},"passwd":{},"storage":{},"systemd":{}}`, ignitionProviderAddr))
	} else {
		userDataValue = []byte(fmt.Sprintf(`{"ignition":{"config":{"append":[{"source":"http://%s/config/master","verification":{}}]},"security":{},"timeouts":{},"version":"2.2.0"},"networkd":{},"passwd":{},"storage":{},"systemd":{}}`, ignitionProviderAddr))
	}

	secret.Data = map[string][]byte{
		"disableTemplating": disableTemplatingValue,
		"value":             userDataValue,
	}
	return secret
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func generateKubeadminPasswordTargetSecret(scheme *runtime.Scheme, password string, namespace string) (*corev1.ConfigMap, error) {
	secret := &corev1.Secret{}
	secret.APIVersion = "v1"
	secret.Kind = "Secret"
	secret.Name = "kubeadmin"
	secret.Namespace = "kube-system"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	secret.Data = map[string][]byte{"kubeadmin": passwordHash}

	secretBytes, err := runtime.Encode(serializer.NewCodecFactory(scheme).LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return nil, err
	}
	configMap := &corev1.ConfigMap{}
	configMap.Namespace = namespace
	configMap.Name = "user-manifest-kubeadmin-password"
	configMap.Data = map[string]string{"data": string(secretBytes)}
	return configMap, nil
}

func generateKubeadminPasswordSecret(namespace, password string) *corev1.Secret {
	secret := &corev1.Secret{}
	secret.Namespace = namespace
	secret.Name = "kubeadmin-password"
	secret.Data = map[string][]byte{"password": []byte(password)}
	return secret
}

func generateKubeconfigSecret(name, namespace string, kubeconfigBytes []byte) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	secret.Namespace = namespace
	secret.Name = fmt.Sprintf("%s-kubeconfig", name)
	secret.Data = map[string][]byte{"value": kubeconfigBytes}
	return secret, nil
}

func generateImageRegistrySecret() string {
	num := make([]byte, 64)
	rand.Read(num)
	return hex.EncodeToString(num)
}

func generateKubeadminPassword() (string, error) {
	const (
		lowerLetters = "abcdefghijkmnopqrstuvwxyz"
		upperLetters = "ABCDEFGHIJKLMNPQRSTUVWXYZ"
		digits       = "23456789"
		all          = lowerLetters + upperLetters + digits
		length       = 23
	)
	var password string
	for i := 0; i < length; i++ {
		n, err := crand.Int(crand.Reader, big.NewInt(int64(len(all))))
		if err != nil {
			return "", err
		}
		newchar := string(all[n.Int64()])
		if password == "" {
			password = newchar
		}
		if i < length-1 {
			n, err = crand.Int(crand.Reader, big.NewInt(int64(len(password)+1)))
			if err != nil {
				return "", err
			}
			j := n.Int64()
			password = password[0:j] + newchar + password[j:]
		}
	}
	pw := []rune(password)
	for _, replace := range []int{5, 11, 17} {
		pw[replace] = '-'
	}
	return string(pw), nil
}

func generateMachineSetName(infraName, clusterName, suffix string) string {
	return getName(fmt.Sprintf("%s-%s", infraName, clusterName), suffix, 43)
}

// getName returns a name given a base ("deployment-5") and a suffix ("deploy")
// It will first attempt to join them with a dash. If the resulting name is longer
// than maxLength: if the suffix is too long, it will truncate the base name and add
// an 8-character hash of the [base]-[suffix] string.  If the suffix is not too long,
// it will truncate the base, add the hash of the base and return [base]-[hash]-[suffix]
func getName(base, suffix string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) <= maxLength {
		return name
	}

	// length of -hash-
	baseLength := maxLength - 10 - len(suffix)

	// if the suffix is too long, ignore it
	if baseLength < 0 {
		prefix := base[0:min(len(base), max(0, maxLength-9))]
		// Calculate hash on initial base-suffix string
		shortName := fmt.Sprintf("%s-%s", prefix, hash(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, hash(base), suffix)
}

// max returns the greater of its 2 inputs
func max(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// min returns the lesser of its 2 inputs
func min(a, b int) int {
	if b < a {
		return b
	}
	return a
}

// hash calculates the hexadecimal representation (8-chars)
// of the hash of the passed in string using the FNV-a algorithm
func hash(s string) string {
	hash := fnv.New32a()
	hash.Write([]byte(s))
	intHash := hash.Sum32()
	result := fmt.Sprintf("%08x", intHash)
	return result
}
