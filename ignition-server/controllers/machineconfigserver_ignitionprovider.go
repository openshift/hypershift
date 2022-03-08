package controllers

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"text/template"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sutilspointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ IgnitionProvider = (*MCSIgnitionProvider)(nil)

const (
	resourceGenerateName = "machine-config-server-"
	mcsPodSubdomain      = "machine-config-server"
	pullSecretName       = "pull-secret"
)

// MCSIgnitionProvider is an IgnitionProvider that uses
// MachineConfigServer pods to build ignition payload contents
// out of a given releaseImage and a config string containing 0..N MachineConfig yaml definitions.
type MCSIgnitionProvider struct {
	Client          client.Client
	ReleaseProvider releaseinfo.Provider
	CloudProvider   hyperv1.PlatformType
	Namespace       string
}

func (p *MCSIgnitionProvider) GetPayload(ctx context.Context, releaseImage string, config string) (payload []byte, err error) {
	pullSecret := &corev1.Secret{}
	if err := p.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: pullSecretName}, pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}
	if _, hasKey := pullSecret.Data[corev1.DockerConfigJsonKey]; !hasKey {
		return nil, fmt.Errorf("pull secret %s/%s missing %q key", pullSecret.Namespace, pullSecret.Name, corev1.DockerConfigJsonKey)
	}

	// TODO(alberto): If the MCS supports binding address
	// https://github.com/openshift/machine-config-operator/pull/2630/files
	// we could bind it to localhost and get the payload by execing
	// https://zhimin-wen.medium.com/programing-exec-into-a-pod-5f2a70bd93bb
	// Otherwise with the current approach the mcs pod is temporary exposed in the pod network every time a payload is generated.
	img, err := p.ReleaseProvider.Lookup(ctx, releaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return nil, fmt.Errorf("failed to look up release image metadata: %w", err)
	}

	compressedConfig, err := compress([]byte(config))
	if err != nil {
		return nil, fmt.Errorf("failed to compress config: %w", err)
	}

	// The ConfigMap requires data stored to be a string.
	// By base64ing the compressed data we ensure all bytes are decodable back.
	// Otherwise if we'd just string() the bytes, some might not be a valid UTF-8 sequence
	// and we might lose data.
	base64CompressedConfig := base64.StdEncoding.EncodeToString(compressedConfig)
	mcsConfigConfigMap := machineConfigServerConfigConfigMap(p.Namespace, base64CompressedConfig)
	mcsPod := machineConfigServerPod(p.Namespace, img, mcsConfigConfigMap, p.CloudProvider)

	// Launch the pod and ensure we clean up regardless of outcome
	defer func() {
		var deleteErrors []error
		if err := p.Client.Delete(ctx, mcsConfigConfigMap); err != nil && !errors.IsNotFound(err) {
			deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete machine config server config ConfigMap: %w", err))
		}
		if err := p.Client.Delete(ctx, mcsPod); err != nil && !errors.IsNotFound(err) {
			deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete machine config server pod: %w", err))
		}
		// We return this in the named returned values.
		if deleteErrors != nil {
			err = utilerrors.NewAggregate(deleteErrors)
		}
	}()

	if err := p.Client.Create(ctx, mcsConfigConfigMap); err != nil {
		return nil, fmt.Errorf("failed to create machine config server RoleBinding: %w", err)
	}

	mcsPod = machineConfigServerPod(p.Namespace, img, mcsConfigConfigMap, p.CloudProvider)
	if err := p.Client.Create(ctx, mcsPod); err != nil {
		return nil, fmt.Errorf("failed to create machine config server Pod: %w", err)
	}

	// Wait for the pod server the payload.
	if err := wait.PollImmediate(10*time.Second, 300*time.Second, func() (bool, error) {
		if err := p.Client.Get(ctx, client.ObjectKeyFromObject(mcsPod), mcsPod); err != nil {
			// We don't return the error here so we want to keep retrying.
			return false, nil
		}

		// If the machine config server is not ready we return and wait for an update event to reconcile.
		mcsReady := false
		for _, cond := range mcsPod.Status.Conditions {
			if cond.Type == corev1.ContainersReady && cond.Status == corev1.ConditionTrue {
				mcsReady = true
				break
			}
		}
		if mcsPod.Status.PodIP == "" || !mcsReady {
			return false, nil
		}

		// Get  Machine config certs
		var caCert []byte
		var ok bool
		machineConfigServerCert := manifests.MachineConfigServerCert(p.Namespace)
		if err := p.Client.Get(ctx, client.ObjectKeyFromObject(machineConfigServerCert), machineConfigServerCert); err != nil {
			return false, fmt.Errorf("failed to get machine config server secret: %w", err)
		}
		if caCert, ok = machineConfigServerCert.Data["ca.crt"]; !ok {
			return false, fmt.Errorf("failed to get Certificate from mcs-crt: %w", err)
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		if err != nil {
			return false, fmt.Errorf("failed to load cert: %w", err)
		}
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: caCertPool,
				},
			},
			Timeout: 5 * time.Second,
		}
		// Build proxy request.
		mcsPodHeadlessDomain := fmt.Sprintf("%s.machine-config-server.%s.svc.cluster.local", mcsPod.Name, p.Namespace)
		proxyReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://%s:8443/config/master", mcsPodHeadlessDomain), nil)
		if err != nil {
			return false, fmt.Errorf("error building https request for machine config server pod: %w", err)
		}
		// We pass expected Headers to return the right config version.
		// https://www.iana.org/assignments/media-types/application/vnd.coreos.ignition+json
		// https://github.com/coreos/ignition/blob/0cbe33fee45d012515479a88f0fe94ef58d5102b/internal/resource/url.go#L61-L64
		// https://github.com/openshift/machine-config-operator/blob/9c6c2bfd7ed498bfbc296d530d1839bd6a177b0b/pkg/server/api.go#L269
		proxyReq.Header.Add("Accept", "application/vnd.coreos.ignition+json;version=3.2.0, */*;q=0.1")
		res, err := client.Do(proxyReq)
		if err != nil {
			return false, fmt.Errorf("error sending https request for machine config server pod: %w", err)
		}
		if res.StatusCode != http.StatusOK {
			return false, fmt.Errorf("request to the machine config server did not returned a 200, this is unexpected")
		}

		defer res.Body.Close()
		payload, err = ioutil.ReadAll(res.Body)
		if err != nil {
			return false, fmt.Errorf("error reading http request body for machine config server pod: %w", err)
		}
		if payload == nil {
			return false, nil
		}

		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to get payload from machine config server Pod: %w", err)
	}

	// Return the named values if everything went ok
	// so if any deletion in the defer call fails, the func returns an error.
	return
}

const mcoBootstrapScript = `#!/bin/bash
mkdir -p /mcc-manifests/bootstrap/manifests
mkdir -p /mcc-manifests/manifests
{{ if .isAzure }}
cat <<EOF >/tmp/cloud.conf.configmap.yaml
kind: ConfigMap
apiVersion: v1
data:
  cloud.conf: |
$(sed 's/^/    /g' /etc/cloudconfig/cloud.conf)
{{ end }}
EOF
machine-config-operator bootstrap \
--root-ca=/assets/manifests/root-ca.crt \
--kube-ca=/assets/manifests/combined-ca.crt \
--machine-config-operator-image={{ .mcoImage }} \
--machine-config-oscontent-image={{ .osContentImage }} \
--infra-image={{ .podImage }} \
--keepalived-image={{ .keepAlivedImage }} \
--coredns-image={{ .coreDNSImage }} \
{{ if .mdnsImage -}}
--mdns-publisher-image={{ .mdnsImage }} \
{{ end -}}
--haproxy-image={{ .haproxyImage }} \
--baremetal-runtimecfg-image={{ .baremetalRuntimeCfgImage }} \
--infra-config-file=/assets/manifests/cluster-infrastructure-02-config.yaml \
--network-config-file=/assets/manifests/cluster-network-02-config.yaml \
--proxy-config-file=/assets/manifests/cluster-proxy-01-config.yaml \
--config-file=/assets/manifests/install-config.yaml \
--dns-config-file=/assets/manifests/cluster-dns-02-config.yaml \
--dest-dir=/mcc-manifests \
{{ if .isAzure -}}
--cloud-config-file=/tmp/cloud.conf.configmap.yaml \
{{ end -}}
--pull-secret=/assets/manifests/pull-secret.yaml
# Use our own version of configpools that swap master and workers
mv /mcc-manifests/bootstrap/manifests /mcc-manifests/bootstrap/manifests.tmp
mkdir /mcc-manifests/bootstrap/manifests
cp /mcc-manifests/bootstrap/manifests.tmp/* /mcc-manifests/bootstrap/manifests/
cp /assets/manifests/*.machineconfigpool.yaml /mcc-manifests/bootstrap/manifests/`

var mcoBootstrapScriptTemplate = template.Must(template.New("mcoBootstrap").Parse(mcoBootstrapScript))

func machineConfigServerPod(namespace string, releaseImage *releaseinfo.ReleaseImage, config *corev1.ConfigMap, provider hyperv1.PlatformType) *corev1.Pod {
	images := releaseImage.ComponentImages()
	scriptArgs := map[string]interface{}{
		"mcoImage":                 images["machine-config-operator"],
		"osContentImage":           images["machine-os-content"],
		"podImage":                 images["pod"],
		"keepAlivedImage":          images["keepalived-ipfailover"],
		"coreDNSImage":             images["coredns"],
		"mdnsImage":                images["mdns-publisher"],
		"haproxyImage":             images["haproxy-router"],
		"baremetalRuntimeCfgImage": images["baremetal-runtimecfg"],
		"isAzure":                  provider == hyperv1.AzurePlatform,
	}
	bootstrapScriptBytes := &bytes.Buffer{}
	if err := mcoBootstrapScriptTemplate.Execute(bootstrapScriptBytes, scriptArgs); err != nil {
		panic(fmt.Sprintf("unexpected error executing bootstrap script: %v", err))
	}
	customMachineConfigArg := `
cat /tmp/custom-config/base64CompressedConfig | base64 -d | gunzip --force --stdout > /mcc-manifests/bootstrap/manifests/custom.yaml`

	podName := fmt.Sprintf("%s%d", resourceGenerateName, rand.Int31())
	bootstrapContainer := corev1.Container{
		Image: images["machine-config-operator"],
		Name:  "machine-config-operator-bootstrap",
		Command: []string{
			"/bin/bash",
		},
		Args: []string{
			"-c",
			bootstrapScriptBytes.String(),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "mcc-manifests",
				MountPath: "/mcc-manifests",
			},
			{
				Name:      "config",
				MountPath: "/assets/manifests",
			},
		},
	}
	if provider == hyperv1.AzurePlatform {
		bootstrapContainer.VolumeMounts = append(bootstrapContainer.VolumeMounts, corev1.VolumeMount{Name: "cloudconf", MountPath: "/etc/cloudconfig"})
	}
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      podName,
			Labels: map[string]string{
				"app":                         "machine-config-server",
				hyperv1.ControlPlaneComponent: "machine-config-server",
			},
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
			EnableServiceLinks:            k8sutilspointer.BoolPtr(true),
			Subdomain:                     mcsPodSubdomain,
			Hostname:                      podName,
			Tolerations: []corev1.Toleration{
				{
					Key:      "hypershift.openshift.io/cluster",
					Operator: corev1.TolerationOpExists,
				},
			},
			ImagePullSecrets: []corev1.LocalObjectReference{
				{
					Name: pullSecretName,
				},
			},
			InitContainers: []corev1.Container{
				bootstrapContainer,
				{
					Image:           images["cli"],
					ImagePullPolicy: corev1.PullIfNotPresent,
					Name:            "inject-custom-machine-configs",
					Env: []corev1.EnvVar{
						{
							Name: "NAMESPACE",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.namespace",
								},
							},
						},
					},
					WorkingDir: "/tmp",
					Command: []string{
						"/usr/bin/bash",
					},
					Args: []string{
						"-e",
						"-o",
						"pipefail",
						"-c",
						customMachineConfigArg,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "mcc-manifests",
							MountPath: "/mcc-manifests",
						},
						{
							Name:      "ign-custom-config",
							MountPath: "/tmp/custom-config",
						},
					},
				},
				{
					Image:           images["machine-config-operator"],
					ImagePullPolicy: corev1.PullIfNotPresent,
					Name:            "machine-config-controller-bootstrap",
					Command: []string{
						"/usr/bin/machine-config-controller",
					},
					Args: []string{
						"bootstrap",
						"--manifest-dir=/mcc-manifests/bootstrap/manifests",
						"--pull-secret=/mcc-manifests/bootstrap/manifests/machineconfigcontroller-pull-secret",
						"--dest-dir=/mcs-manifests",
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "mcc-manifests",
							MountPath: "/mcc-manifests",
						},
						{
							Name:      "mcs-manifests",
							MountPath: "/mcs-manifests",
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Image:           images["machine-config-operator"],
					ImagePullPolicy: corev1.PullIfNotPresent,
					Name:            "machine-config-server",
					Command: []string{
						"/usr/bin/machine-config-server",
					},
					Args: []string{
						"bootstrap",
						"--bootstrap-kubeconfig=/etc/openshift/kubeconfig",
						"--secure-port=8443",
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "https",
							ContainerPort: 8443,
							Protocol:      corev1.ProtocolTCP,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "kubeconfig",
							ReadOnly:  true,
							MountPath: "/etc/openshift",
						},
						{
							Name:      "mcs-manifests",
							MountPath: "/etc/mcs/bootstrap",
						},
						{
							Name:      "mcc-manifests",
							MountPath: "/etc/mcc/bootstrap",
						},
						{
							Name:      "mcs-tls",
							MountPath: "/etc/ssl/mcs",
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("50Mi"),
							corev1.ResourceCPU:    resource.MustParse("10m"),
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "kubeconfig",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "bootstrap-kubeconfig",
						},
					},
				},
				{
					Name: "mcs-tls",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "mcs-crt",
						},
					},
				},
				{
					Name: "mcs-manifests",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "mcc-manifests",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "machine-config-server",
							},
						},
					},
				},
				{
					Name: "ign-custom-config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: config.Name,
							},
							Items: []corev1.KeyToPath{
								{
									Key:  TokenSecretConfigKey,
									Path: "base64CompressedConfig",
								},
							},
						},
					},
				},
			},
		},
	}
	if provider == hyperv1.AzurePlatform {
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "cloudconf", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: manifests.AzureProviderConfig("").Name}}}})
	}
	return p
}

func machineConfigServerConfigConfigMap(namespace, config string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: resourceGenerateName,
		},
		Immutable: k8sutilspointer.BoolPtr(true),
		Data: map[string]string{
			TokenSecretConfigKey: config,
		},
	}
}

func compress(content []byte) ([]byte, error) {
	if len(content) == 0 {
		return nil, nil
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write(content); err != nil {
		return nil, fmt.Errorf("failed to compress content: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("compress closure failure %w", err)
	}
	return b.Bytes(), nil
}
