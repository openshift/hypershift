package controllers

import (
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io"
	"io/ioutil"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/blang/semver"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	"openshift.io/hypershift/hypershift-operator/releaseinfo"
	hypershiftcp "openshift.io/hypershift/hypershift-operator/render/controlplane/hypershift"
	"openshift.io/hypershift/hypershift-operator/render/controlplane/hypershift/pki"
	rokscp "openshift.io/hypershift/hypershift-operator/render/controlplane/roks"
)

const (
	externalOauthPort         = 8443
	DefaultAPIServerIPAddress = "172.20.0.1"
)

var (
	excludeManifests = []string{
		"openshift-apiserver-service.yaml",
		"v4-0-config-system-branding.yaml",
		"oauth-server-service.yaml",
		"kube-apiserver-service.yaml",
	}

	version46 = semver.MustParse("4.6.0")
)

type CreateClusterOpts struct {
	Directory                 string
	Config                    hyperv1.OpenShiftCluster
	ReleaseImageInfo          *releaseinfo.ReleaseImageInfo
	ControlPlaneOperatorImage string

	Client client.Client
}

func (r *OpenShiftClusterReconciler) ensureControlPlane(ctx context.Context, cluster *hyperv1.OpenShiftCluster, infraStatus InfrastructureStatus, releaseInfo *releaseinfo.ReleaseImageInfo) error {
	workingDir, err := ioutil.TempDir("", "hypershift")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		if os.IsExist(err) {
			if err = os.RemoveAll(workingDir); err != nil {
				return err
			}
			if err = os.MkdirAll(workingDir, 0755); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	r.Log.Info("ensuring control plane for cluster", "cluster", cluster.Name, "workingDir", workingDir)

	name := cluster.Name

	pullSecretFile := filepath.Join(workingDir, "pull-secret")
	if err := ioutil.WriteFile(pullSecretFile, []byte(cluster.Spec.PullSecret), 0644); err != nil {
		return fmt.Errorf("failed to create temporary pull secret file: %v", err)
	}
	releaseVersion, err := releaseInfo.ReleaseVersion()
	if err != nil {
		return fmt.Errorf("cannot obtain release version: %v", err)
	}
	version, err := semver.Parse(releaseVersion)
	if err != nil {
		return fmt.Errorf("cannot parse release version (%s): %v", releaseVersion, err)
	}

	params := hypershiftcp.NewClusterParams()
	params.Namespace = name
	params.ExternalAPIDNSName = infraStatus.APIAddress
	params.ExternalAPIPort = 6443
	params.ExternalAPIAddress = DefaultAPIServerIPAddress
	params.ExternalOpenVPNAddress = infraStatus.VPNAddress
	params.ExternalOpenVPNPort = 1194
	params.ExternalOauthDNSName = infraStatus.OAuthAddress
	params.ExternalOauthPort = externalOauthPort
	params.ServiceCIDR = cluster.Spec.ServiceCIDR
	params.PodCIDR = cluster.Spec.PodCIDR
	params.ReleaseImage = releaseInfo.Image
	params.IngressSubdomain = fmt.Sprintf("apps.%s", cluster.Spec.BaseDomain)
	params.OpenShiftAPIClusterIP = DefaultAPIServerIPAddress
	params.BaseDomain = cluster.Spec.BaseDomain
	params.MachineConfigServerAddress = infraStatus.IgnitionProviderAddress
	params.CloudProvider = cluster.Spec.CloudProvider
	params.InternalAPIPort = 6443
	params.EtcdClientName = "etcd-client"
	params.NetworkType = "OpenShiftSDN"
	params.ImageRegistryHTTPSecret = generateImageRegistrySecret()
	params.Replicas = "1"
	params.SSHKey = cluster.Spec.SSHKey
	params.ControlPlaneOperatorImage = r.ControlPlaneOperatorImage
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
		log.Info("generating PKI secret data")
		data, err := generatePKIData(params)
		if err != nil {
			return fmt.Errorf("failed to generate PKI data: %w", err)
		}
		pkiSecret.Data = data
		if err := r.Create(ctx, pkiSecret); err != nil {
			return fmt.Errorf("failed to create pki secret: %w", err)
		}
		r.Log.Info("created pki secret")
	}

	pkiDir := filepath.Join(workingDir, "pki")
	if err = os.MkdirAll(pkiDir, 0755); err != nil {
		return fmt.Errorf("failed to create temporary PKI directory: %w", err)
	}
	for file, data := range pkiSecret.Data {
		pkiFile := filepath.Join(pkiDir, file)
		if err := ioutil.WriteFile(pkiFile, data, 0644); err != nil {
			return fmt.Errorf("failed to write pki file %s: %w", pkiFile, err)
		}
	}

	manifestsDir := filepath.Join(workingDir, "manifests")
	if err = os.MkdirAll(manifestsDir, 0755); err != nil {
		return fmt.Errorf("cannot create temporary manifests directory: %w", err)
	}

	log.Info("Rendering Manifests")
	hypershiftcp.RenderPKISecrets(pkiDir, manifestsDir, true, true, true)
	caBytes, err := ioutil.ReadFile(filepath.Join(pkiDir, "combined-ca.crt"))
	if err != nil {
		return fmt.Errorf("failed to render PKI secrets: %w", err)
	}
	params.OpenshiftAPIServerCABundle = base64.StdEncoding.EncodeToString(caBytes)

	if err = rokscp.RenderClusterManifests(&rokscp.ClusterParams{ClusterParams: *params}, releaseInfo, pullSecretFile, manifestsDir, false, false); err != nil {
		return fmt.Errorf("failed to render roks manifests for cluster: %w", err)
	}

	if err = hypershiftcp.RenderClusterManifests(params, releaseInfo, pullSecretFile, pkiDir, manifestsDir, true, true, true, true); err != nil {
		return fmt.Errorf("failed to render hypershift manifests for cluster: %w", err)
	}

	for _, name := range excludeManifests {
		f := filepath.Join(manifestsDir, name)
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if err := os.Remove(f); err != nil {
			r.Log.Error(err, "failed to remove manifest", "manifest", f)
		} else {
			r.Log.Info("removed excluded manifest", "manifest", f)
		}
	}

	// Use server side apply for manifestss
	manifests, err := filepath.Glob(filepath.Join(manifestsDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("failed to read manifest dir: %w", err)
	}
	applyErrors := []error{}
	for _, manifest := range manifests {
		bytes, err := ioutil.ReadFile(manifest)
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to read manifest %s: %w", manifest, err))
		}
		obj := &unstructured.Unstructured{}
		if err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(bytes)), 100).Decode(obj); err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to decode manifest %s: %w", manifest, err))
		}
		obj.SetNamespace(name)
		err = r.Patch(ctx, obj, client.RawPatch(types.ApplyPatchType, bytes), client.ForceOwnership, client.FieldOwner("hypershift-operator"))
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to apply manifest %s: %w", manifest, err))
		} else {
			r.Log.Info("applied manifest", "manifest", manifest)
		}
	}
	if errs := errors.NewAggregate(applyErrors); errs != nil {
		return fmt.Errorf("failed to apply some manifests: %w", errs)
	}
	r.Log.Info("successfully applied all manifests")

	var infra configv1.Infrastructure
	if err := r.Get(ctx, client.ObjectKey{Name: "cluster"}, &infra); err != nil {
		return fmt.Errorf("failed to get cluster infra: %w", err)
	}
	// Create a machineset for the new cluster's worker nodes
	machineSet, err := r.generateWorkerMachineset(ctx, infra.Status.InfrastructureName, name, cluster.Spec.ComputeReplicas)
	if err != nil {
		return fmt.Errorf("failed to generate worker machineset: %w", err)
	}
	if err := r.Create(ctx, machineSet); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create machineset: %w", err)
	}

	userDataSecret := generateUserDataSecret(name, infraStatus.IgnitionProviderAddress, version)
	if err := r.Create(ctx, userDataSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate user data secret: %w", err)
	}

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

	kubeconfigSecret, err := generateKubeconfigSecret(filepath.Join(pkiDir, "admin.kubeconfig"), name)
	if err != nil {
		return fmt.Errorf("failed to create kubeconfig secret manifest for management cluster: %w", err)
	}
	if err := r.Create(ctx, kubeconfigSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate kubeconfigSecret: %w", err)
	}

	targetPullSecret, err := generateTargetPullSecret(r.Scheme(), []byte(cluster.Spec.PullSecret), name)
	if err != nil {
		return fmt.Errorf("failed to create pull secret manifest for target cluster: %w", err)
	}
	if err := r.Create(ctx, targetPullSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to generate targetPullSecret: %v", err)
	}

	log.Infof("Cluster API URL: %s", fmt.Sprintf("https://%s:6443", infraStatus.APIAddress))
	log.Infof("Kubeconfig is available in secret %q in the %s namespace", "admin-kubeconfig", name)
	log.Infof("Console URL:  %s", fmt.Sprintf("https://console-openshift-console.%s", params.IngressSubdomain))
	log.Infof("kubeadmin password is available in secret %q in the %s namespace", "kubeadmin-password", name)

	cluster = cluster.DeepCopy()
	cluster.Status.Ready = true
	if err := r.Update(ctx, cluster); err != nil {
		return fmt.Errorf("failed to update cluster status: %w", err)
	}
	return nil
}

func generatePKIData(params *hypershiftcp.ClusterParams) (map[string][]byte, error) {
	pkiDir, err := ioutil.TempDir("", "pki")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary pki directory: %w", err)
	}
	dhParamsFile := os.Getenv("DH_PARAMS")
	if len(dhParamsFile) > 0 {
		if err = copyFile(dhParamsFile, filepath.Join(pkiDir, "openvpn-dh.pem")); err != nil {
			return nil, fmt.Errorf("failed to copy dh parameters file %s: %w", dhParamsFile, err)
		}
	}
	if err := pki.GeneratePKI(params, pkiDir); err != nil {
		return nil, fmt.Errorf("failed to generate PKI assets: %w", err)
	}
	files, err := ioutil.ReadDir(pkiDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read PKI directory: %w", err)
	}
	data := map[string][]byte{}
	for _, file := range files {
		baseName := filepath.Base(file.Name())
		bytes, err := ioutil.ReadFile(filepath.Join(pkiDir, baseName))
		if err != nil {
			return nil, fmt.Errorf("failed to read PKI file %q: %w", file.Name(), err)
		}
		data[baseName] = bytes
	}
	return data, nil
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

func (r *OpenShiftClusterReconciler) generateWorkerMachineset(ctx context.Context, infraName string, namespace string, workerCount int) (*unstructured.Unstructured, error) {
	machineSets := &unstructured.UnstructuredList{}
	machineSets.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "machine.openshift.io",
		Version: "v1beta1",
		Kind:    "MachineSet",
	})
	if err := r.List(ctx, machineSets, client.InNamespace("openshift-machine-api")); err != nil {
		return nil, fmt.Errorf("failed to list machinesets: %w", err)
	}
	if len(machineSets.Items) == 0 {
		return nil, fmt.Errorf("no machinesets found")
	}
	obj := machineSets.Items[0]

	workerName := generateMachineSetName(infraName, namespace, "worker")
	object := obj.Object

	unstructured.RemoveNestedField(object, "status")
	unstructured.RemoveNestedField(object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(object, "metadata", "generation")
	unstructured.RemoveNestedField(object, "metadata", "resourceVersion")
	unstructured.RemoveNestedField(object, "metadata", "selfLink")
	unstructured.RemoveNestedField(object, "metadata", "uid")
	unstructured.RemoveNestedField(object, "spec", "template", "spec", "metadata")
	unstructured.RemoveNestedField(object, "spec", "template", "spec", "providerSpec", "value", "publicIp")
	unstructured.SetNestedField(object, int64(workerCount), "spec", "replicas")
	unstructured.SetNestedField(object, workerName, "metadata", "name")
	unstructured.SetNestedField(object, workerName, "spec", "selector", "matchLabels", "machine.openshift.io/cluster-api-machineset")
	unstructured.SetNestedField(object, workerName, "spec", "template", "metadata", "labels", "machine.openshift.io/cluster-api-machineset")
	unstructured.SetNestedField(object, fmt.Sprintf("%s-user-data", namespace), "spec", "template", "spec", "providerSpec", "value", "userDataSecret", "name")

	return &obj, nil
}

func generateUserDataSecret(namespace string, ignitionProviderAddr string, version semver.Version) *corev1.Secret {
	secret := &corev1.Secret{}
	secret.Name = fmt.Sprintf("%s-user-data", namespace)
	secret.Namespace = "openshift-machine-api"

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
		"userData":          userDataValue,
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

func generateKubeconfigSecret(kubeconfigFile, namespace string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	secret.Namespace = namespace
	secret.Name = "admin-kubeconfig"
	kubeconfigBytes, err := ioutil.ReadFile(kubeconfigFile)
	if err != nil {
		return nil, err
	}
	secret.Data = map[string][]byte{"kubeconfig": kubeconfigBytes}
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
