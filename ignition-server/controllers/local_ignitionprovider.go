package controllers

import (
	"bytes"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/blang/semver"
)

// LocalIgnitionProvider is an IgnitionProvider that executes MCO binaries
// directly to build ignition payload contents out of a given release image and
// a config string containing 0..N MachineConfig YAML definitions.
//
// To do this, MCO binaries and other static input files are extracted from a
// release image into WorkDir. These contents are cleaned up after each
// execution and are not currently cached between executions for a given release
// image because the effort of managing the cache is not yet justified by any
// performance measurements.
//
// Currently, all GetPayload executions are performed serially, enforced by a
// mutex. Enabling concurrent executions requires more work because of the of
// MCS, which is an HTTP server process, implying work to allocate
// non-conflicting ports. This effort is not yet justified by any performance
// measurements.
type LocalIgnitionProvider struct {
	Client           client.Client
	HypershiftClient hypershiftclient.Interface
	ReleaseProvider  releaseinfo.ProviderWithOpenShiftImageRegistryOverrides
	CloudProvider    hyperv1.PlatformType
	Namespace        string

	// WorkDir is the base working directory for contents extracted from a
	// release payload. Usually this would map to a volume mount.
	WorkDir string

	// PreserveOutput indicates whether the temporary working directory created
	// under WorkDir should be preserved. If false, the temporary directory is
	// deleted after use.
	PreserveOutput bool

	// FeatureGateManifest is the path to a rendered feature gate manifest.
	// This must be copied into the MCC directory as it is required
	// to render the ignition payload.
	FeatureGateManifest string

	// ImageMetaDataProvider is used to get the image metadata for the images
	// used in the ignition payload.
	ImageMetadataProvider *util.RegistryClientImageMetadataProvider

	ImageFileCache *imageFileCache

	lock sync.Mutex

	// mcsCertPEM and mcsKeyPEM cache the PEM-encoded MCS TLS certificate and key.
	// mcsCertExpiry tracks when the cached certificate expires so we can regenerate
	// before it becomes invalid. These fields are protected by the lock mutex above.
	mcsCertPEM    []byte
	mcsKeyPEM     []byte
	mcsCertExpiry time.Time
}

var _ IgnitionProvider = (*LocalIgnitionProvider)(nil)

// mcsCertRefreshMargin is the safety margin before the certificate's actual expiry
// at which we regenerate a new certificate. This ensures we never serve an expired cert.
const mcsCertRefreshMargin = 1 * time.Hour

// getOrGenerateMCSCert returns cached PEM-encoded MCS TLS certificate and key,
// generating a new self-signed certificate if the cache is empty, partially
// populated, or the cached certificate is about to expire. This avoids redundant
// RSA key generation on every GetPayload call. The caller must hold p.lock.
func (p *LocalIgnitionProvider) getOrGenerateMCSCert() (certPEM []byte, keyPEM []byte, err error) {
	if p.mcsCertPEM != nil && p.mcsKeyPEM != nil && time.Now().Add(mcsCertRefreshMargin).Before(p.mcsCertExpiry) {
		return p.mcsCertPEM, p.mcsKeyPEM, nil
	}

	cfg := &certs.CertCfg{
		Subject:   pkix.Name{CommonName: "machine-config-server", OrganizationalUnit: []string{"openshift"}},
		KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		Validity:  certs.ValidityOneDay,
		IsCA:      true,
	}
	key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate cert: %w", err)
	}

	p.mcsCertPEM = certs.CertToPem(crt)
	p.mcsKeyPEM = certs.PrivateKeyToPem(key)
	p.mcsCertExpiry = crt.NotAfter

	ctrl.Log.WithName("get-payload").Info("regenerated MCS TLS certificate", "expiry", crt.NotAfter)

	return p.mcsCertPEM, p.mcsKeyPEM, nil
}

const (
	pullSecretName            = "pull-secret"
	additionalTrustBundleName = "user-ca-bundle"
	managedTrustBundleName    = "trusted-ca-bundle-managed"
)

type payloadDirs struct {
	workDir   string
	binDir    string
	mcoDir    string
	mccDir    string
	mcsDir    string
	configDir string
}

func (p *LocalIgnitionProvider) fetchPullSecret(ctx context.Context) ([]byte, error) {
	secret := &corev1.Secret{}
	if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: pullSecretName}, secret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}
	data, exists := secret.Data[corev1.DockerConfigJsonKey]
	if !exists {
		return nil, fmt.Errorf("pull secret missing %q key", corev1.DockerConfigJsonKey)
	}
	return data, nil
}

func (p *LocalIgnitionProvider) fetchAdditionalTrustBundle(ctx context.Context) (string, error) {
	atbCM := &corev1.ConfigMap{}
	if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: additionalTrustBundleName}, atbCM); err != nil {
		if errors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get additionalTrustBundle configmap: %w", err)
	}
	data, exists := atbCM.Data["ca-bundle.crt"]
	if !exists {
		return "", fmt.Errorf("additionalTrustBundle configmap missing %q key", "ca-bundle.crt")
	}
	return data, nil
}

func (p *LocalIgnitionProvider) fetchBootstrapKubeConfig(ctx context.Context) ([]byte, error) {
	secret := &corev1.Secret{}
	if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "bootstrap-kubeconfig"}, secret); err != nil {
		return nil, fmt.Errorf("failed to get bootstrap kubeconfig secret: %w", err)
	}
	data, exists := secret.Data["kubeconfig"]
	if !exists {
		return nil, fmt.Errorf("bootstrap kubeconfig secret missing kubeconfig key")
	}
	return data, nil
}

func (p *LocalIgnitionProvider) fetchAndValidateMCSConfig(ctx context.Context, hcConfigurationHash string) (*corev1.ConfigMap, error) {
	mcsConfig := &corev1.ConfigMap{}
	if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "machine-config-server"}, mcsConfig); err != nil {
		return nil, fmt.Errorf("failed to get machine-config-server configmap: %w", err)
	}

	if hcConfigurationHash != "" && mcsConfig.Data["configuration-hash"] != hcConfigurationHash {
		return nil, fmt.Errorf("machine-config-server configmap is out of date, waiting for update %s != %s", mcsConfig.Data["configuration-hash"], hcConfigurationHash)
	}

	userCaBundleConfigCM := &corev1.ConfigMap{}
	if err := yaml.Unmarshal([]byte(mcsConfig.Data["user-ca-bundle-config.yaml"]), &userCaBundleConfigCM); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user-ca-bundle-config.yaml: %w", err)
	}

	managedTrustedBundle := &corev1.ConfigMap{}
	if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: managedTrustBundleName}, managedTrustedBundle); err != nil {
		if !errors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get %s configmap: %w", managedTrustBundleName, err)
		}
	}

	// NOTE: Here we compare the contents of the MCS user-ca-bundle-config.yaml and the trusted-ca-bundle-managed
	// ConfigMap. Both should contain the aggregate of hc.spec.additionalTrustBundle and hc.spec.configuration.proxy.trustedCA
	// and should match.
	if managedTrustedBundle.Data != nil {
		if managedTrustedBundle.Data["ca-bundle.crt"] != userCaBundleConfigCM.Data["ca-bundle.crt"] {
			return nil, fmt.Errorf("user-ca-bundle-config.yaml in machine-config-server configmap does not contain the same ca-bundle.crt value as in trusted-ca-bundle-managed")
		}
	}

	return mcsConfig, nil
}

func (p *LocalIgnitionProvider) resolveMCOImage(ctx context.Context, imageProvider *imageprovider.SimpleReleaseImageProvider, pullSecret []byte) (string, error) {
	log := ctrl.Log.WithName("get-payload")
	component := "machine-config-operator"
	mcoImage, hasMcoImage := imageProvider.ImageExist(component)
	if !hasMcoImage {
		return "", fmt.Errorf("release image does not contain machine-config-operator (images: %v)", imageProvider.ComponentImages())
	}

	mcoImage, err := registryclient.GetCorrectArchImage(ctx, component, mcoImage, pullSecret, p.ImageMetadataProvider)
	if err != nil {
		return "", err
	}

	log.Info(fmt.Sprintf("discovered image %s image %v", component, mcoImage))

	checkedMcoImage, err := p.ImageMetadataProvider.GetOverride(ctx, mcoImage, pullSecret)
	if err != nil {
		return "", err
	}

	// Making sure image uses the registry override for disconnected environments
	mcoComposedImage := checkedMcoImage.String()
	if mcoComposedImage != mcoImage {
		mcoImage = mcoComposedImage
		log.Info(fmt.Sprintf("using mirrored %s image %v", component, mcoImage))
	}

	return mcoImage, nil
}

func (p *LocalIgnitionProvider) setupWorkDirs(workDir string) (*payloadDirs, error) {
	dirs := &payloadDirs{
		workDir:   workDir,
		binDir:    filepath.Join(workDir, "bin"),
		mcoDir:    filepath.Join(workDir, "mco"),
		mccDir:    filepath.Join(workDir, "mcc"),
		mcsDir:    filepath.Join(workDir, "mcs"),
		configDir: filepath.Join(workDir, "config"),
	}
	for _, dir := range []string{dirs.binDir, dirs.mcoDir, dirs.mccDir, dirs.mcsDir, dirs.configDir} {
		if err := os.Mkdir(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to make directory %s: %w", dir, err)
		}
	}
	return dirs, nil
}

func (p *LocalIgnitionProvider) writeInitialFiles(dirs *payloadDirs, customConfig string, bootstrapKubeConfig []byte, mcsConfig *corev1.ConfigMap) error {
	if err := os.WriteFile(filepath.Join(dirs.mccDir, "custom.yaml"), []byte(customConfig), 0644); err != nil {
		return fmt.Errorf("failed to write mcc config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dirs.mcsDir, "kubeconfig"), bootstrapKubeConfig, 0644); err != nil {
		return fmt.Errorf("failed to write bootstrap kubeconfig: %w", err)
	}
	for name, contents := range mcsConfig.Data {
		if name == "configuration-hash" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dirs.configDir, name), []byte(contents), 0644); err != nil {
			return fmt.Errorf("failed to write MCS config file %q: %w", name, err)
		}
	}
	return nil
}

func (p *LocalIgnitionProvider) extractImageReferences(ctx context.Context, releaseImage string, pullSecret []byte, configDir string) (string, error) {
	log := ctrl.Log.WithName("get-payload")
	start := time.Now()

	_, err := p.ReleaseProvider.Lookup(ctx, releaseImage, pullSecret)
	if err != nil {
		return "", fmt.Errorf("failed to look up release image metadata: %w", err)
	}
	// Replace the release image with the mirrored release image in disconnected environment cases.
	if p.ReleaseProvider.GetMirroredReleaseImage() != "" {
		releaseImage = p.ReleaseProvider.GetMirroredReleaseImage()
		log.Info("using mirrored release image", "releaseImage", releaseImage)
	}

	if err := registryclient.ExtractImageFilesToDir(ctx, releaseImage, pullSecret, "release-manifests/image-references", configDir); err != nil {
		return "", fmt.Errorf("failed to extract image-references: %w", err)
	}
	log.Info("extracted image-references", "time", time.Since(start).Round(time.Second).String())
	return releaseImage, nil
}

// For Azure and OpenStack, extract the cloud provider config file as MCO input
func (p *LocalIgnitionProvider) writeCloudProviderConfig(ctx context.Context, mcoDir string) error {
	if p.CloudProvider != hyperv1.AzurePlatform && p.CloudProvider != hyperv1.OpenStackPlatform {
		return nil
	}
	cloudConfigMap := &corev1.ConfigMap{}
	switch p.CloudProvider {
	case hyperv1.AzurePlatform:
		if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: manifests.AzureProviderConfig("").Name}, cloudConfigMap); err != nil {
			return fmt.Errorf("failed to get cloud provider configmap: %w", err)
		}
	case hyperv1.OpenStackPlatform:
		if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: manifests.OpenStackProviderConfig("").Name}, cloudConfigMap); err != nil {
			return fmt.Errorf("failed to get cloud provider configmap: %w", err)
		}
	}
	cloudConfYaml, err := yaml.Marshal(cloudConfigMap)
	if err != nil {
		return fmt.Errorf("failed to marshal cloud config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(mcoDir, "cloud.conf.configmap.yaml"), cloudConfYaml, 0644); err != nil {
		return fmt.Errorf("failed to write bootstrap kubeconfig: %w", err)
	}
	return nil
}

func (p *LocalIgnitionProvider) extractClusterConfigBinary(ctx context.Context, imageProvider *imageprovider.SimpleReleaseImageProvider, pullSecret []byte, binDir, clusterConfigComponent, clusterConfigFile string) error {
	log := ctrl.Log.WithName("get-payload")
	start := time.Now()
	clusterConfigImage, ok := imageProvider.ImageExist(clusterConfigComponent)
	if !ok {
		return fmt.Errorf("release image does not contain $%s (images: %v)", clusterConfigComponent, imageProvider.ComponentImages())
	}

	var err error
	clusterConfigImage, err = registryclient.GetCorrectArchImage(ctx, clusterConfigComponent, clusterConfigImage, pullSecret, p.ImageMetadataProvider)
	if err != nil {
		return err
	}

	log.Info(fmt.Sprintf("discovered image %s image %v", clusterConfigComponent, clusterConfigImage))

	checkedClusterConfigImage, err := p.ImageMetadataProvider.GetOverride(ctx, clusterConfigImage, pullSecret)
	if err != nil {
		return err
	}

	// Making sure image uses the registry override for disconnected environments
	ccaComposedImage := checkedClusterConfigImage.String()
	if ccaComposedImage != clusterConfigImage {
		clusterConfigImage = ccaComposedImage
		log.Info(fmt.Sprintf("using mirrored %s image %v", clusterConfigComponent, ccaComposedImage))
	}

	file, err := os.Create(filepath.Join(binDir, clusterConfigComponent))
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	if err := file.Chmod(0777); err != nil {
		return fmt.Errorf("failed to chmod file: %w", err)
	}
	if err := p.ImageFileCache.extractImageFile(ctx, clusterConfigImage, pullSecret, clusterConfigFile, file); err != nil {
		return fmt.Errorf("failed to extract image file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	log.Info("downloaded binaries", "time", time.Since(start).Round(time.Second).String())
	return nil
}

func (p *LocalIgnitionProvider) runFeatureGateRender(ctx context.Context, binDir, workDir, mccDir, clusterConfigComponent, clusterConfigComponentShort string, payloadVersion semver.Version, featureGateBytes []byte) error {
	log := ctrl.Log.WithName("get-payload")
	start := time.Now()

	args := []string{
		"-c",
		invokeFeatureGateRenderScript(filepath.Join(binDir, clusterConfigComponent), filepath.Join(workDir, clusterConfigComponentShort), mccDir, payloadVersion, string(featureGateBytes)),
	}

	cmd := exec.CommandContext(ctx, "/bin/bash", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s process failed: %s: %w", clusterConfigComponent, string(out), err)
	}
	log.Info(fmt.Sprintf("%s process completed", clusterConfigComponent), "time", time.Since(start).Round(time.Second).String(), "output", string(out))
	return nil
}

func (p *LocalIgnitionProvider) runMCO(ctx context.Context, dirs *payloadDirs, releaseImage string, pullSecret []byte, mcsConfig *corev1.ConfigMap, imageProvider *imageprovider.SimpleReleaseImageProvider, payloadVersion semver.Version) error {
	log := ctrl.Log.WithName("get-payload")
	destDir := dirs.mcoDir
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to make dir: %w", err)
	}

	pullSecretObject := common.PullSecret("default")
	pullSecretObject.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: pullSecret,
	}
	pullSecretObject.Type = corev1.SecretTypeDockerConfigJson
	serializedPullSecret, err := k8sutil.SerializeResource(pullSecretObject, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize pull-secret.yaml: %w", err)
	}
	// NOTE: This overwrites the one in the machine-config-server configmap to ensure it's the one that matches the hash used in the token secret.
	if err = os.WriteFile(fmt.Sprintf("%s/pull-secret.yaml", dirs.configDir), []byte(serializedPullSecret), 0644); err != nil {
		return fmt.Errorf("failed to write pull secret to config dir: %w", err)
	}

	// args contains the base args that have not changed over time.
	args := []string{
		"bootstrap",
		fmt.Sprintf("--root-ca=%s/root-ca.crt", dirs.configDir),
		fmt.Sprintf("--infra-config-file=%s/cluster-infrastructure-02-config.yaml", dirs.configDir),
		fmt.Sprintf("--network-config-file=%s/cluster-network-02-config.yaml", dirs.configDir),
		fmt.Sprintf("--proxy-config-file=%s/cluster-proxy-01-config.yaml", dirs.configDir),
		fmt.Sprintf("--config-file=%s/install-config.yaml", dirs.configDir),
		fmt.Sprintf("--dns-config-file=%s/cluster-dns-02-config.yaml", dirs.configDir),
		fmt.Sprintf("--pull-secret=%s/pull-secret.yaml", dirs.configDir),
		fmt.Sprintf("--dest-dir=%s", destDir),
		fmt.Sprintf("--additional-trust-bundle-config-file=%s/user-ca-bundle-config.yaml", dirs.configDir),
		fmt.Sprintf("--release-image=%s", releaseImage),
	}

	args = append(args, buildMCOVersionArgs(payloadVersion, imageProvider.Version(), imageProvider.GetImage, dirs.configDir)...)

	if image, exists := imageProvider.ImageExist("mdns-publisher"); exists {
		args = append(args, fmt.Sprintf("--mdns-publisher-image=%s", image))
	}
	if mcsConfig.Data["user-ca-bundle-config.yaml"] != "" {
		args = append(args, fmt.Sprintf("--additional-trust-bundle-config-file=%s/user-ca-bundle-config.yaml", dirs.configDir))
	}
	if p.CloudProvider == hyperv1.AzurePlatform || p.CloudProvider == hyperv1.OpenStackPlatform {
		args = append(args, fmt.Sprintf("--cloud-config-file=%s/cloud.conf.configmap.yaml", dirs.mcoDir))
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, filepath.Join(dirs.binDir, "machine-config-operator"), args...)
	out, err := cmd.CombinedOutput()
	log.Info("machine-config-operator process completed", "time", time.Since(start).Round(time.Second).String(), "output", string(out))
	if err != nil {
		return fmt.Errorf("machine-config-operator process failed: %w", err)
	}

	// set missing images condition on the HCP
	if err := p.reconcileValidReleaseInfoCondition(ctx, imageProvider); err != nil {
		log.Error(err, "failed to reconcile IgnitionValidReleaseInfo condition")
	}

	if err := p.copyMCOOutputToMCC(destDir, dirs.mccDir, dirs.configDir); err != nil {
		return err
	}
	return nil
}

func (p *LocalIgnitionProvider) copyMCOOutputToMCC(destDir, mccDir, configDir string) error {
	bootstrapManifestsDir := filepath.Join(destDir, "bootstrap", "manifests")
	manifestEntries, err := os.ReadDir(bootstrapManifestsDir)
	if err != nil {
		return fmt.Errorf("failed to read dir: %w", err)
	}
	for _, fd := range manifestEntries {
		src := path.Join(bootstrapManifestsDir, fd.Name())
		dst := path.Join(mccDir, fd.Name())
		if fd.IsDir() {
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
		}
	}

	// Copy machineconfigpool config data to the MCC input directory. This is
	// important to override the pools with the ones generated by the CPO.
	matches, err := filepath.Glob(filepath.Join(configDir, "*.machineconfigpool.yaml"))
	if err != nil {
		return fmt.Errorf("failed to list dir %s: %w", configDir, err)
	}
	for _, src := range matches {
		dst := filepath.Join(mccDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
		}
	}
	return nil
}

func (p *LocalIgnitionProvider) runMCC(ctx context.Context, dirs *payloadDirs, imageProvider *imageprovider.SimpleReleaseImageProvider, payloadVersion semver.Version) error {
	log := ctrl.Log.WithName("get-payload")
	start := time.Now()

	// copy the image config out of the configDir and into the mccBaseDir
	if err := copyFile(filepath.Join(dirs.configDir, "image-config.yaml"), filepath.Join(dirs.mccDir, "image-config.yaml")); err != nil {
		return fmt.Errorf("failed to copy image-config.yaml: %w", err)
	}

	// args contains the base args that have not changed over time.
	args := []string{
		"bootstrap",
		fmt.Sprintf("--manifest-dir=%s", dirs.mccDir),
		fmt.Sprintf("--templates=%s", filepath.Join(dirs.mccDir, "etc", "mcc", "templates")),
		fmt.Sprintf("--pull-secret=%s/machineconfigcontroller-pull-secret", dirs.mccDir),
		fmt.Sprintf("--dest-dir=%s", dirs.mcsDir),
	}

	// For 4.14 onwards there's a requirement to include the payload version flag.
	if payloadVersion.GTE(semver.Version{Major: 4, Minor: 14}) {
		args = append(args,
			fmt.Sprintf("--payload-version=%s", imageProvider.Version()),
		)
	}

	cmd := exec.CommandContext(ctx, filepath.Join(dirs.binDir, "machine-config-controller"), args...)
	out, err := cmd.CombinedOutput()
	log.Info("machine-config-controller process completed", "time", time.Since(start).Round(time.Second).String(), "output", string(out))
	if err != nil {
		return fmt.Errorf("machine-config-controller process failed: %w", err)
	}
	return nil
}

func (p *LocalIgnitionProvider) runMCSAndFetchPayload(ctx context.Context, dirs *payloadDirs, imageProvider *imageprovider.SimpleReleaseImageProvider, payloadVersion semver.Version) ([]byte, error) {
	log := ctrl.Log.WithName("get-payload")
	start := time.Now()

	// The certificate is cached across calls to avoid redundant RSA key generation on every payload request.
	certPEM, keyPEM, err := p.getOrGenerateMCSCert()
	if err != nil {
		return nil, fmt.Errorf("failed to generate certificates: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dirs.mcsDir, "tls.crt"), certPEM, 0644); err != nil {
		return nil, fmt.Errorf("failed to write mcs cert: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dirs.mcsDir, "tls.key"), keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("failed to write mcs key: %w", err)
	}

	args := []string{
		"bootstrap",
		fmt.Sprintf("--server-basedir=%s", dirs.mcsDir),
		fmt.Sprintf("--bootstrap-kubeconfig=%s/kubeconfig", dirs.mcsDir),
		fmt.Sprintf("--cert=%s/tls.crt", dirs.mcsDir),
		fmt.Sprintf("--key=%s/tls.key", dirs.mcsDir),
		"--secure-port=22625",
		"--insecure-port=22626",
	}

	// For 4.14 onwards there's a requirement to include the payload version flag.
	if payloadVersion.GTE(semver.Version{Major: 4, Minor: 14}) {
		args = append(args,
			fmt.Sprintf("--payload-version=%s", imageProvider.Version()),
		)
	}

	// Spin up the MCS process and ensure it's signaled to terminate when the function returns
	mcsCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(mcsCtx, filepath.Join(dirs.binDir, "machine-config-server"), args...)
	go func() {
		out, err := cmd.CombinedOutput()
		log.Info("machine-config-server process exited", "output", string(out), "error", err)
	}()

	httpclient := &http.Client{
		Timeout: 5 * time.Second,
	}
	var payload []byte
	// Try connecting to the server until we get a response or the context is closed
	err = wait.PollUntilContextCancel(ctx, 1*time.Second, true, func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost:22626/config/master", nil)
		if err != nil {
			return false, fmt.Errorf("error building http request: %w", err)
		}
		// We pass expected Headers to return the right config version.
		// https://www.iana.org/assignments/media-types/application/vnd.coreos.ignition+json
		// https://github.com/coreos/ignition/blob/0cbe33fee45d012515479a88f0fe94ef58d5102b/internal/resource/url.go#L61-L64
		// https://github.com/openshift/machine-config-operator/blob/9c6c2bfd7ed498bfbc296d530d1839bd6a177b0b/pkg/server/api.go#L269
		req.Header.Add("Accept", "application/vnd.coreos.ignition+json;version=3.2.0, */*;q=0.1")
		res, err := httpclient.Do(req)
		if err != nil {
			log.Error(err, "mcs request failed")
			return false, nil
		}
		if res.StatusCode != http.StatusOK {
			log.Error(err, "mcs returned unexpected response code", "code", res.StatusCode)
			return false, nil
		}

		defer func() {
			if err := res.Body.Close(); err != nil {
				log.Error(err, "failed to close mcs response body")
			}
		}()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			log.Error(err, "failed to read mcs response body")
			return false, nil
		}
		payload = body
		log.Info("got mcs payload", "time", time.Since(start).Round(time.Second).String())
		return true, nil
	})
	return payload, err
}

func (p *LocalIgnitionProvider) GetPayload(ctx context.Context, releaseImage, customConfig, pullSecretHash, additionalTrustBundleHash, hcConfigurationHash string) ([]byte, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	log := ctrl.Log.WithName("get-payload")

	pullSecret, err := p.fetchPullSecret(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}

	if pullSecretHash != "" && util.HashSimple(pullSecret) != pullSecretHash {
		return nil, fmt.Errorf("pull secret does not match hash")
	}

	additionalTrustBundle, err := p.fetchAdditionalTrustBundle(ctx)
	if err != nil {
		return nil, err
	}
	// NOTE: the additionalTrustBundle only contains the bundle in hc.spec.additionalTrustBundle. The hash is generated
	// by the nodepool controller only based on that bundle, so we test a match here. However, the bundle that we want
	// to pass to the MCO is the aggregate of the hc.spec.additionalTrustBundle and hc.spec.configuration.proxy.trustedCA
	// That is contained in the trusted-ca-bundle-managed configmap.
	if additionalTrustBundleHash != "" && util.HashSimple(additionalTrustBundle) != additionalTrustBundleHash {
		return nil, fmt.Errorf("additionalTrustBundle does not match hash")
	}

	bootstrapKubeConfig, err := p.fetchBootstrapKubeConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap kubeconfig: %w", err)
	}

	mcsConfig, err := p.fetchAndValidateMCSConfig(ctx, hcConfigurationHash)
	if err != nil {
		return nil, err
	}

	imageProvider, err := func() (*imageprovider.SimpleReleaseImageProvider, error) {
		img, err := p.ReleaseProvider.Lookup(ctx, releaseImage, pullSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to look up release image metadata: %w", err)
		}
		return imageprovider.New(img), nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to get component images: %v", err)
	}

	mcoImage, err := p.resolveMCOImage(ctx, imageProvider, pullSecret)
	if err != nil {
		return nil, err
	}

	workDir, err := os.MkdirTemp(p.WorkDir, "get-payload")
	if err != nil {
		return nil, fmt.Errorf("failed to create working directory: %w", err)
	}
	if !p.PreserveOutput {
		defer func() {
			if err := os.RemoveAll(workDir); err != nil {
				log.Error(err, "failed to delete working directory", "dir", workDir)
			}
		}()
	}
	log.Info("created working directory", "dir", workDir)

	dirs, err := p.setupWorkDirs(workDir)
	if err != nil {
		return nil, err
	}

	if err := p.writeInitialFiles(dirs, customConfig, bootstrapKubeConfig, mcsConfig); err != nil {
		return nil, err
	}

	releaseImage, err = p.extractImageReferences(ctx, releaseImage, pullSecret, dirs.configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to extract image-references from image: %w", err)
	}

	if err := p.writeCloudProviderConfig(ctx, dirs.mcoDir); err != nil {
		return nil, err
	}

	err = func() error {
		start := time.Now()
		if err := registryclient.ExtractImageFilesToDir(ctx, mcoImage, pullSecret, "etc/mcc/templates/*", dirs.mccDir); err != nil {
			return fmt.Errorf("failed to extract mcc templates: %w", err)
		}
		log.Info("extracted templates", "time", time.Since(start).Round(time.Second).String())
		return nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to extract templates from image: %w", err)
	}

	payloadVersion, err := semver.Parse(imageProvider.Version())
	if err != nil {
		return nil, fmt.Errorf("failed to parse payload version: %w", err)
	}

	// set the component to the correct binary name and file path based on the payload version
	clusterConfigComponent := "cluster-config-api"
	clusterConfigComponentShort := "cca"
	clusterConfigFile := "usr/bin/render"

	if payloadVersion.LT(semver.Version{Major: 4, Minor: 15}) {
		clusterConfigComponent = "cluster-config-operator"
		clusterConfigComponentShort = "cco"
		clusterConfigFile = "usr/bin/cluster-config-operator"
	}

	if err := p.extractMCOBinaries(ctx, "/usr/lib/os-release", mcoImage, pullSecret, dirs.binDir); err != nil {
		return nil, fmt.Errorf("failed to download MCO binaries: %w", err)
	}

	if err := p.extractClusterConfigBinary(ctx, imageProvider, pullSecret, dirs.binDir, clusterConfigComponent, clusterConfigFile); err != nil {
		return nil, fmt.Errorf("failed to download binaries: %w", err)
	}

	featureGateBytes, err := os.ReadFile(p.FeatureGateManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to read feature gate: %w", err)
	}

	if err := p.runFeatureGateRender(ctx, dirs.binDir, dirs.workDir, dirs.mccDir, clusterConfigComponent, clusterConfigComponentShort, payloadVersion, featureGateBytes); err != nil {
		return nil, fmt.Errorf("failed to execute %s: %w", clusterConfigComponent, err)
	}

	// First, run the MCO using templates and image refs as input. This generates output for the MCC.
	if err := p.runMCO(ctx, dirs, releaseImage, pullSecret, mcsConfig, imageProvider, payloadVersion); err != nil {
		return nil, fmt.Errorf("failed to execute machine-config-operator: %w", err)
	}

	// Next, run the MCC using templates and MCO output as input, producing output for the MCS.
	if err := p.runMCC(ctx, dirs, imageProvider, payloadVersion); err != nil {
		return nil, fmt.Errorf("failed to execute machine-config-controller: %w", err)
	}

	// Finally, run the MCS to generate a payload.
	payload, err := p.runMCSAndFetchPayload(ctx, dirs, imageProvider, payloadVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to get payload from mcs: %w", err)
	}

	return payload, nil
}

func (r *LocalIgnitionProvider) reconcileValidReleaseInfoCondition(ctx context.Context, releaseImageProvider *imageprovider.SimpleReleaseImageProvider) error {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.Client.List(ctx, hcpList, client.InNamespace(r.Namespace)); err != nil {
		return err
	}
	if len(hcpList.Items) == 0 {
		return fmt.Errorf("failed to find HostedControlPlane in namespace %s", r.Namespace)
	}

	hostedControlPlane := hcpList.Items[0]

	managedTypes := sets.New[string](string(hyperv1.IgnitionServerValidReleaseInfo))

	var condition *metav1applyconfigurations.ConditionApplyConfiguration
	if len(releaseImageProvider.GetMissingImages()) == 0 {
		condition = metav1applyconfigurations.Condition().
			WithType(string(hyperv1.IgnitionServerValidReleaseInfo)).
			WithStatus(metav1.ConditionTrue).
			WithReason(hyperv1.AsExpectedReason).
			WithMessage(hyperv1.AllIsWellMessage).
			WithObservedGeneration(hostedControlPlane.Generation).
			WithLastTransitionTime(metav1.Now())
	} else {
		condition = metav1applyconfigurations.Condition().
			WithType(string(hyperv1.IgnitionServerValidReleaseInfo)).
			WithStatus(metav1.ConditionFalse).
			WithReason(hyperv1.MissingReleaseImagesReason).
			WithMessage(strings.Join(releaseImageProvider.GetMissingImages(), ", ")).
			WithObservedGeneration(hostedControlPlane.Generation).
			WithLastTransitionTime(metav1.Now())
	}

	cfg := hypershiftv1beta1applyconfigurations.HostedControlPlane(hostedControlPlane.Name, hostedControlPlane.Namespace).
		WithStatus(hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().
			WithConditions(conditions.SSAConditions(hostedControlPlane.Status.Conditions, managedTypes, condition)...))
	_, err := r.HypershiftClient.HypershiftV1beta1().HostedControlPlanes(hostedControlPlane.Namespace).ApplyStatus(
		ctx, cfg, metav1.ApplyOptions{FieldManager: "ignition-server", Force: true})
	return err
}

// copyFile copies a file named src to dst, preserving attributes.
func copyFile(src, dst string) error {
	srcfd, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcfd.Close()

	dstfd, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstfd.Close()

	if _, err = io.Copy(dstfd, srcfd); err != nil {
		return err
	}
	srcinfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcinfo.Mode())
}

func invokeFeatureGateRenderScript(binary, workDir, outputDir string, payloadVersion semver.Version, featureGateYAML string) string {
	var script = `#!/bin/bash
set -e
mkdir -p %[2]s

cd %[2]s
mkdir -p input output manifests

touch %[2]s/manifests/99_feature-gate.yaml
cat <<EOF >%[2]s/manifests/99_feature-gate.yaml
%[5]s
EOF

%[1]s \
   --asset-output-dir %[2]s/output \
   --image-manifests=input \
   --rendered-manifest-dir=%[2]s/manifests \
   --cluster-profile=ibm-cloud-managed \
   --payload-version=%[4]s
cp %[2]s/manifests/99_feature-gate.yaml %[3]s/99_feature-gate.yaml
`

	// Depending on the version, we need different args.
	if payloadVersion.LT(semver.Version{Major: 4, Minor: 15}) {
		script = `#!/bin/bash
set -e
mkdir -p %[2]s
cd %[2]s
mkdir -p input output manifests
touch %[2]s/manifests/99_feature-gate.yaml
cat <<EOF >%[2]s/manifests/99_feature-gate.yaml
%[5]s
EOF
%[1]s render \
   --config-output-file config \
   --asset-input-dir %[2]s/input \
   --asset-output-dir %[2]s/output \
   --rendered-manifest-files=%[2]s/manifests \
   --payload-version=%[4]s
cp %[2]s/manifests/99_feature-gate.yaml %[3]s/99_feature-gate.yaml
`
	}

	// Depending on the version, we need different args.
	if payloadVersion.LT(semver.Version{Major: 4, Minor: 14}) {
		script = `#!/bin/bash
set -e
mkdir -p %[2]s
cd %[2]s
mkdir -p input output manifests
touch %[2]s/manifests/99_feature-gate.yaml
cat <<EOF >%[2]s/manifests/99_feature-gate.yaml
%[5]s
EOF
%[1]s render \
   --config-output-file config \
   --asset-input-dir %[2]s/input \
   --asset-output-dir %[2]s/output
cp %[2]s/manifests/99_feature-gate.yaml %[3]s/99_feature-gate.yaml
`
	}

	return fmt.Sprintf(script, binary, workDir, outputDir, payloadVersion, featureGateYAML)
}

// buildMCOVersionArgs returns the version-dependent arguments for the MCO
// bootstrap command. The args vary based on the payload version:
//   - 4.14+: --payload-version, --image-references, --kube-ca (signer)
//   - 4.13:  --image-references, --kube-ca (signer)
//   - <4.13: legacy per-image flags, --kube-ca (root)
func buildMCOVersionArgs(payloadVersion semver.Version, payloadVersionStr string, getImage func(string) string, configDir string) []string {
	var args []string
	switch {
	case payloadVersion.GTE(semver.Version{Major: 4, Minor: 14}):
		args = append(args,
			fmt.Sprintf("--payload-version=%s", payloadVersionStr),
		)
		// We need to include 4.13 plus args here too.
		fallthrough
	case payloadVersion.GTE(semver.Version{Major: 4, Minor: 13}):
		args = append(args,
			fmt.Sprintf("--image-references=%s", path.Join(configDir, "release-manifests", "image-references")),
			fmt.Sprintf("--kube-ca=%s/signer-ca.crt", configDir),
		)
	default:
		// when the CPO is at N and the NodePool.spec.release at N-1
		// we fail to render ignition payload because https://github.com/openshift/machine-config-operator/pull/3286
		// broke backward compatibility.
		args = append(args,
			fmt.Sprintf("--machine-config-operator-image=%s", getImage("machine-config-operator")),
			fmt.Sprintf("--machine-config-oscontent-image=%s", getImage("machine-os-content")),
			fmt.Sprintf("--infra-image=%s", getImage("pod")),
			fmt.Sprintf("--keepalived-image=%s", getImage("keepalived-ipfailover")),
			fmt.Sprintf("--coredns-image=%s", getImage("coredns")),
			fmt.Sprintf("--haproxy-image=%s", getImage("haproxy")),
			fmt.Sprintf("--baremetal-runtimecfg-image=%s", getImage("baremetal-runtimecfg")),
			fmt.Sprintf("--kube-ca=%s/root-ca.crt", configDir),
		)
	}
	return args
}

func (p *LocalIgnitionProvider) extractMCOBinaries(ctx context.Context, cpoOSReleaseFile string, mcoImage string, pullSecret []byte, binDir string) error {
	start := time.Now()
	binaries := []string{"machine-config-operator", "machine-config-controller", "machine-config-server"}
	suffix := ""

	mcoOSReleaseBuf := &bytes.Buffer{}
	if err := p.ImageFileCache.extractImageFile(ctx, mcoImage, pullSecret, "usr/lib/os-release", mcoOSReleaseBuf); err != nil {
		return fmt.Errorf("failed to extract image os-release file: %w", err)
	}
	mcoOSRelease := mcoOSReleaseBuf.String()

	// read /etc/os-release file from disk to cpoOSRelease
	cpoOSRelease, err := os.ReadFile(cpoOSReleaseFile)
	if err != nil {
		return fmt.Errorf("failed to read cpo os-release file: %w", err)
	}

	// extract RHEL major version from both os-release files
	extractMajorVersion := func(osRelease string) (string, error) {
		for _, line := range strings.Split(osRelease, "\n") {
			if strings.HasPrefix(line, "VERSION_ID=") {
				return strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "VERSION_ID=\""), "\""), ".")[0], nil
			}
		}
		return "", fmt.Errorf("failed to find VERSION_ID in os-release file")
	}
	mcoRHELMajorVersion, err := extractMajorVersion(mcoOSRelease)
	if err != nil {
		return fmt.Errorf("failed to extract major version from MCO os-release: %w", err)
	}
	cpoRHELMajorVersion, err := extractMajorVersion(string(cpoOSRelease))
	if err != nil {
		return fmt.Errorf("failed to extract major version from CPO os-release: %w", err)
	}
	log.Info("read os-release", "mcoRHELMajorVersion", mcoRHELMajorVersion, "cpoRHELMajorVersion", cpoRHELMajorVersion)

	if mcoRHELMajorVersion == "8" && cpoRHELMajorVersion == "9" {
		// NodePool MCO RHEL major version is older than the CPO, need to add suffix to the binaries
		suffix = ".rhel9"
	}

	for _, name := range binaries {
		srcPath := filepath.Join("usr/bin/", name+suffix)
		destPath := filepath.Join(binDir, name)
		file, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		if err := file.Chmod(0777); err != nil {
			return fmt.Errorf("failed to chmod file: %w", err)
		}
		log.Info("copying file", "src", srcPath, "dest", destPath)
		if err := p.ImageFileCache.extractImageFile(ctx, mcoImage, pullSecret, srcPath, file); err != nil {
			if suffix == "" {
				return fmt.Errorf("failed to extract image file: %w", err)
			}
			// The MCO image in the NodePool release image does not contain the suffixed binary, try to extract the unsuffixed binary
			srcPath = filepath.Join("usr/bin/", name)
			log.Info("suffixed binary not found, copying file", "src", srcPath, "dest", destPath)
			if err := p.ImageFileCache.extractImageFile(ctx, mcoImage, pullSecret, filepath.Join("usr/bin/", name), file); err != nil {
				return fmt.Errorf("failed to extract image file: %w", err)
			}
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("failed to close file: %w", err)
		}
	}

	log.Info("downloaded binaries", "time", time.Since(start).Round(time.Second).String())
	return nil
}
