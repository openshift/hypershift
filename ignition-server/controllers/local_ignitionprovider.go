package controllers

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
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
	Client          client.Client
	ReleaseProvider releaseinfo.Provider
	CloudProvider   hyperv1.PlatformType
	Namespace       string

	// WorkDir is the base working directory for contents extracted from a
	// release payload. Usually this would map to a volume mount.
	WorkDir string

	// PreserveOutput indicates whether the temporary working directory created
	// under WorkDir should be preserved. If false, the temporary directory is
	// deleted after use.
	PreserveOutput bool

	ImageFileCache *imageFileCache

	lock sync.Mutex
}

var _ IgnitionProvider = (*LocalIgnitionProvider)(nil)

func (p *LocalIgnitionProvider) GetPayload(ctx context.Context, releaseImage string, customConfig string) ([]byte, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	log := ctrl.Log.WithName("get-payload")

	// Fetch the pull secret contents
	pullSecret, err := func() ([]byte, error) {
		secret := &corev1.Secret{}
		if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: pullSecretName}, secret); err != nil {
			return nil, fmt.Errorf("failed to get pull secret: %w", err)
		}
		data, exists := secret.Data[corev1.DockerConfigJsonKey]
		if !exists {
			return nil, fmt.Errorf("pull secret missing %q key", corev1.DockerConfigJsonKey)
		}
		return data, nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}

	// Fetch the bootstrap kubeconfig contents
	bootstrapKubeConfig, err := func() ([]byte, error) {
		secret := &corev1.Secret{}
		if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "bootstrap-kubeconfig"}, secret); err != nil {
			return nil, fmt.Errorf("failed to get bootstrap kubeconfig secret: %w", err)
		}
		data, exists := secret.Data["kubeconfig"]
		if !exists {
			return nil, fmt.Errorf("bootstrap kubeconfig secret missing kubeconfig key")
		}
		return data, nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap kubeconfig: %w", err)
	}

	// Fetch the MCS config
	mcsConfig := &corev1.ConfigMap{}
	if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: "machine-config-server"}, mcsConfig); err != nil {
		return nil, fmt.Errorf("failed to get machine-config-server configmap: %w", err)
	}

	// Look up the release image metadata
	images, err := func() (map[string]string, error) {
		img, err := p.ReleaseProvider.Lookup(ctx, releaseImage, pullSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to look up release image metadata: %w", err)
		}
		return img.ComponentImages(), nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to get component images: %v", err)
	}

	mcoImage, hasMcoImage := images["machine-config-operator"]
	if !hasMcoImage {
		return nil, fmt.Errorf("release image does not contain machine-config-operator (images: %v)", images)
	}
	log.Info("discovered mco image", "image", mcoImage)

	// Set up the base working directory
	workDir, err := ioutil.TempDir(p.WorkDir, "get-payload")
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

	// Prepare all the working subdirectories
	binDir := filepath.Join(workDir, "bin")
	mcoBaseDir := filepath.Join(workDir, "mco")
	mccBaseDir := filepath.Join(workDir, "mcc")
	mcsBaseDir := filepath.Join(workDir, "mcs")
	configDir := filepath.Join(workDir, "config")
	for _, dir := range []string{binDir, mcoBaseDir, mccBaseDir, mcsBaseDir, configDir} {
		if err := os.Mkdir(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to make directory %s: %w", dir, err)
		}
	}

	// Write out the custom config to the MCC directory
	if err := os.WriteFile(filepath.Join(mccBaseDir, "custom.yaml"), []byte(customConfig), 0644); err != nil {
		return nil, fmt.Errorf("failed to write mcc config: %w", err)
	}
	// Write out the bootstrap kubeconfig to the MCS directory
	if err := os.WriteFile(filepath.Join(mcsBaseDir, "kubeconfig"), bootstrapKubeConfig, 0644); err != nil {
		return nil, fmt.Errorf("failed to write bootstrap kubeconfig: %w", err)
	}
	// Extract MCS config files into the config directory
	for name, contents := range mcsConfig.Data {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(contents), 0644); err != nil {
			return nil, fmt.Errorf("failed to write MCS config file %q: %w", name, err)
		}
	}
	// For Azure, extract the cloud provider config file as MCO input
	if p.CloudProvider == hyperv1.AzurePlatform {
		cloudConfigMap := &corev1.ConfigMap{}
		if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: manifests.AzureProviderConfig("").Name}, cloudConfigMap); err != nil {
			return nil, fmt.Errorf("failed to get cloud provider configmap: %w", err)
		}
		cloudConfYaml, err := yaml.Marshal(cloudConfigMap)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal cloud config: %w", err)
		}
		if err := os.WriteFile(filepath.Join(mcoBaseDir, "cloud.conf.configmap.yaml"), cloudConfYaml, 0644); err != nil {
			return nil, fmt.Errorf("failed to write bootstrap kubeconfig: %w", err)
		}
	}

	// Extract template files from the MCO image to the MCC input directory
	err = func() error {
		start := time.Now()
		if err := registryclient.ExtractImageFilesToDir(ctx, mcoImage, pullSecret, "etc/mcc/templates/*", mccBaseDir); err != nil {
			return fmt.Errorf("failed to extract mcc templates: %w", err)
		}
		log.Info("extracted templates", "time", time.Since(start).Round(time.Second).String())
		return nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to extract templates from image: %w", err)
	}

	// Extract binaries from the MCO image into the bin directory
	err = func() error {
		start := time.Now()
		binaries := []string{"machine-config-operator", "machine-config-controller", "machine-config-server"}
		for _, name := range binaries {
			file, err := os.Create(filepath.Join(binDir, name))
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}
			if err := file.Chmod(0777); err != nil {
				return fmt.Errorf("failed to chmod file: %w", err)
			}
			if err := p.ImageFileCache.extractImageFile(ctx, mcoImage, pullSecret, filepath.Join("usr/bin/", name), file); err != nil {
				return fmt.Errorf("failed to extract image file: %w", err)
			}
			if err := file.Close(); err != nil {
				return fmt.Errorf("failed to close file: %w", err)
			}
		}
		log.Info("downloaded binaries", "time", time.Since(start).Round(time.Second).String())
		return nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to download binaries: %w", err)
	}

	// First, run the MCO using templates and image refs as input. This generates
	// output for the MCC.
	err = func() error {
		destDir := filepath.Join(workDir, "mco")
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("failed to make dir: %w", err)
		}

		args := []string{
			"bootstrap",
			fmt.Sprintf("--machine-config-operator-image=%s", images["machine-config-operator"]),
			fmt.Sprintf("--machine-config-oscontent-image=%s", images["machine-os-content"]),
			fmt.Sprintf("--infra-image=%s", images["pod"]),
			fmt.Sprintf("--keepalived-image=%s", images["keepalived-ipfailover"]),
			fmt.Sprintf("--coredns-image=%s", images["codedns"]),
			fmt.Sprintf("--haproxy-image=%s", images["haproxy"]),
			fmt.Sprintf("--baremetal-runtimecfg-image=%s", images["baremetal-runtimecfg"]),
			fmt.Sprintf("--root-ca=%s/root-ca.crt", configDir),
			fmt.Sprintf("--kube-ca=%s/combined-ca.crt", configDir),
			fmt.Sprintf("--infra-config-file=%s/cluster-infrastructure-02-config.yaml", configDir),
			fmt.Sprintf("--network-config-file=%s/cluster-network-02-config.yaml", configDir),
			fmt.Sprintf("--proxy-config-file=%s/cluster-proxy-01-config.yaml", configDir),
			fmt.Sprintf("--config-file=%s/install-config.yaml", configDir),
			fmt.Sprintf("--dns-config-file=%s/cluster-dns-02-config.yaml", configDir),
			fmt.Sprintf("--pull-secret=%s/pull-secret.yaml", configDir),
			fmt.Sprintf("--dest-dir=%s", destDir),
			fmt.Sprintf("--additional-trust-bundle-config-file=%s/user-ca-bundle-config.yaml", configDir),
		}
		if image, exists := images["mdns-publisher"]; exists {
			args = append(args, fmt.Sprintf("--mdns-publisher-image=%s", image))
		}
		if mcsConfig.Data["user-ca-bundle-config.yaml"] != "" {
			args = append(args, fmt.Sprintf("--additional-trust-bundle-config-file=%s/user-ca-bundle-config.yaml", configDir))
		}
		if p.CloudProvider == hyperv1.AzurePlatform {
			args = append(args, fmt.Sprintf("--cloud-config-file=%s/cloud.conf.configmap.yaml", mcoBaseDir))
		}

		start := time.Now()
		cmd := exec.CommandContext(ctx, filepath.Join(binDir, "machine-config-operator"), args...)
		out, err := cmd.CombinedOutput()
		log.Info("machine-config-operator process completed", "time", time.Since(start).Round(time.Second).String(), "output", string(out))
		if err != nil {
			return fmt.Errorf("machine-config-operator process failed: %w", err)
		}

		// Copy output to the MCC base directory
		bootstrapManifestsDir := filepath.Join(destDir, "bootstrap", "manifests")
		manifests, err := ioutil.ReadDir(bootstrapManifestsDir)
		if err != nil {
			return fmt.Errorf("failed to read dir: %w", err)
		}
		for _, fd := range manifests {
			src := path.Join(bootstrapManifestsDir, fd.Name())
			dst := path.Join(mccBaseDir, fd.Name())
			if fd.IsDir() {
				continue
			}
			if err := copyFile(src, dst); err != nil {
				return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
			}
		}

		// Copy machineconfigpool config data to the MCC input directory. This is
		// important to override the pools with the ones generated by the CPO.
		err = func() error {
			matches, err := filepath.Glob(filepath.Join(configDir, "*.machineconfigpool.yaml"))
			if err != nil {
				return fmt.Errorf("failed to list dir %s: %w", configDir, err)
			}
			for _, src := range matches {
				dst := filepath.Join(mccBaseDir, filepath.Base(src))
				if err := copyFile(src, dst); err != nil {
					return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
				}
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("failed to copy mcs config to mcc directory: %w", err)
		}
		return nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to execute machine-config-operator: %w", err)
	}

	// Next, run the MCC using templates and MCO output as input, producing output
	// for the MCS.
	err = func() error {
		start := time.Now()
		cmd := exec.CommandContext(ctx, filepath.Join(binDir, "machine-config-controller"), "bootstrap",
			fmt.Sprintf("--manifest-dir=%s", mccBaseDir),
			fmt.Sprintf("--templates=%s", filepath.Join(mccBaseDir, "etc", "mcc", "templates")),
			fmt.Sprintf("--pull-secret=%s/machineconfigcontroller-pull-secret", mccBaseDir),
			fmt.Sprintf("--dest-dir=%s", mcsBaseDir),
		)
		out, err := cmd.CombinedOutput()
		log.Info("machine-config-controller process completed", "time", time.Since(start).Round(time.Second).String(), "output", string(out))
		if err != nil {
			return fmt.Errorf("machine-config-controller process failed: %w", err)
		}
		return nil
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to execute machine-config-controller: %w", err)
	}

	// Finally, run the MCS to generate a payload.
	payload, err := func() ([]byte, error) {
		start := time.Now()

		// Generate certificates. The MCS is hard-coded to expose a TLS listener
		// and requires both a certificate and a key.
		// TODO: This could be generated once up-front and cached for all processes
		err = func() error {
			cfg := &certs.CertCfg{
				Subject:   pkix.Name{CommonName: "machine-config-server", OrganizationalUnit: []string{"openshift"}},
				KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
				Validity:  certs.ValidityOneDay,
				IsCA:      true,
			}
			key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
			if err != nil {
				return fmt.Errorf("failed to generate cert: %w", err)
			}
			if err := os.WriteFile(filepath.Join(mcsBaseDir, "tls.crt"), certs.CertToPem(crt), 0644); err != nil {
				return fmt.Errorf("failed to write mcs cert: %w", err)
			}
			if err := os.WriteFile(filepath.Join(mcsBaseDir, "tls.key"), certs.PrivateKeyToPem(key), 0644); err != nil {
				return fmt.Errorf("failed to write mcs cert: %w", err)
			}
			return nil
		}()
		if err != nil {
			return nil, fmt.Errorf("failed to generate certificates: %w", err)
		}

		// Spin up the MCS process and ensure it's signaled to terminate when
		// the function returns
		mcsCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		cmd := exec.CommandContext(mcsCtx, filepath.Join(binDir, "machine-config-server"), "bootstrap",
			fmt.Sprintf("--server-basedir=%s", mcsBaseDir),
			fmt.Sprintf("--bootstrap-kubeconfig=%s/kubeconfig", mcsBaseDir),
			fmt.Sprintf("--cert=%s/tls.crt", mcsBaseDir),
			fmt.Sprintf("--key=%s/tls.key", mcsBaseDir),
			"--secure-port=22623",
			"--insecure-port=22624",
		)
		go func() {
			out, err := cmd.CombinedOutput()
			log.Info("machine-config-server process exited", "output", string(out), "error", err)
		}()

		// Try connecting to the server until we get a response or the context is
		// closed
		httpclient := &http.Client{
			Timeout: 5 * time.Second,
		}
		var payload []byte
		err = wait.PollUntilWithContext(ctx, 1*time.Second, func(ctx context.Context) (bool, error) {
			req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost:22624/config/master", nil)
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
			p, err := ioutil.ReadAll(res.Body)
			if err != nil {
				log.Error(err, "failed to read mcs response body")
				return false, nil
			}
			payload = p
			log.Info("got mcs payload", "time", time.Since(start).Round(time.Second).String())
			return true, nil
		})
		return payload, err
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to get payload from mcs: %w", err)
	}

	return payload, nil
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
