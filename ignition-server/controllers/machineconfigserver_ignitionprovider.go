package controllers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sutilspointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ IgnitionProvider = (*MCSIgnitionProvider)(nil)

const (
	resourceGenerateName = "machine-config-server-"
)

// MCSIgnitionProvider is an IgnitionProvider that uses
// MachineConfigServer pods to build ignition payload contents.
type MCSIgnitionProvider struct {
	Client          client.Client
	ReleaseProvider releaseinfo.Provider
	Namespace       string
}

func (p *MCSIgnitionProvider) GetPayload(ctx context.Context, releaseImage string) (payload []byte, err error) {
	// TODO(alberto): If the MCS supports binding address
	// https://github.com/openshift/machine-config-operator/pull/2630/files
	// we could bind it to localhost and get the payload by execing
	// https://zhimin-wen.medium.com/programing-exec-into-a-pod-5f2a70bd93bb
	// Otherwise with the current approach the mcs pod is temporary exposed in the pod network every time a payload is generated.
	img, err := p.ReleaseProvider.Lookup(ctx, releaseImage)
	if err != nil {
		return nil, fmt.Errorf("failed to look up release image metadata: %w", err)
	}

	mcsServiceAccount := machineConfigServerServiceAccount(p.Namespace)
	mcsRoleBinding := machineConfigServerRoleBinding(mcsServiceAccount)
	mcsPod := machineConfigServerPod(p.Namespace, img, mcsServiceAccount)

	// Launch the pod and ensure we clean up regardless of outcome
	defer func() {
		var deleteErrors []error
		if err := p.Client.Delete(ctx, mcsServiceAccount); err != nil && !errors.IsNotFound(err) {
			deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete machine config server ServiceAccount: %w", err))
		}
		if err := p.Client.Delete(ctx, mcsRoleBinding); err != nil && !errors.IsNotFound(err) {
			deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete machine config server RoleBinding: %w", err))
		}
		if err := p.Client.Delete(ctx, mcsPod); err != nil && !errors.IsNotFound(err) {
			deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete machine config server pod: %w", err))
		}
		// We return this in the named returned values.
		if deleteErrors != nil {
			err = utilerrors.NewAggregate(deleteErrors)
		}
	}()
	if err := p.Client.Create(ctx, mcsServiceAccount); err != nil {
		return nil, fmt.Errorf("failed to create machine config server ServiceAccount: %w", err)
	}

	mcsRoleBinding = machineConfigServerRoleBinding(mcsServiceAccount)
	if err := p.Client.Create(ctx, mcsRoleBinding); err != nil {
		return nil, fmt.Errorf("failed to create machine config server RoleBinding: %w", err)
	}

	mcsPod = machineConfigServerPod(p.Namespace, img, mcsServiceAccount)
	if err := p.Client.Create(ctx, mcsPod); err != nil {
		return nil, fmt.Errorf("failed to create machine config server Pod: %w", err)
	}

	// Wait for the pod server the payload.
	if err := wait.PollImmediate(1*time.Second, 10*time.Second, func() (bool, error) {
		if err := p.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(mcsPod), mcsPod); err != nil {
			return false, err
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

		// Build proxy request.
		proxyReq, err := http.NewRequest("GET", fmt.Sprintf("http://%s:%s/config/master", mcsPod.Status.PodIP, "8080"), nil)
		if err != nil {
			return false, fmt.Errorf("error building http request for machine config server pod: %w", err)
		}
		// We pass expected Headers to return the right config version.
		// https://www.iana.org/assignments/media-types/application/vnd.coreos.ignition+json
		// https://github.com/coreos/ignition/blob/0cbe33fee45d012515479a88f0fe94ef58d5102b/internal/resource/url.go#L61-L64
		// https://github.com/openshift/machine-config-operator/blob/9c6c2bfd7ed498bfbc296d530d1839bd6a177b0b/pkg/server/api.go#L269
		proxyReq.Header.Add("Accept", "application/vnd.coreos.ignition+json;version=3.1.0, */*;q=0.1")

		// Send proxy request.
		client := &http.Client{
			Timeout: 5 * time.Second,
		}
		res, err := client.Do(proxyReq)
		if err != nil {
			return false, fmt.Errorf("error sending http request for machine config server pod: %w", err)
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

func machineConfigServerPod(namespace string, releaseImage *releaseinfo.ReleaseImage, sa *corev1.ServiceAccount) *corev1.Pod {
	images := releaseImage.ComponentImages()
	bootstrapArgs := fmt.Sprintf(`
mkdir -p /mcc-manifests/bootstrap/manifests
mkdir -p /mcc-manifests/manifests
exec machine-config-operator bootstrap \
--root-ca=/assets/manifests/root-ca.crt \
--kube-ca=/assets/manifests/combined-ca.crt \
--machine-config-operator-image=%s \
--machine-config-oscontent-image=%s \
--infra-image=%s \
--keepalived-image=%s \
--coredns-image=%s \
--mdns-publisher-image=%s \
--haproxy-image=%s \
--baremetal-runtimecfg-image=%s \
--infra-config-file=/assets/manifests/cluster-infrastructure-02-config.yaml \
--network-config-file=/assets/manifests/cluster-network-02-config.yaml \
--proxy-config-file=/assets/manifests/cluster-proxy-01-config.yaml \
--config-file=/assets/manifests/install-config.yaml \
--dns-config-file=/assets/manifests/cluster-dns-02-config.yaml \
--dest-dir=/mcc-manifests \
--pull-secret=/assets/manifests/pull-secret.yaml

# Use our own version of configpools that swap master and workers
mv /mcc-manifests/bootstrap/manifests /mcc-manifests/bootstrap/manifests.tmp
mkdir /mcc-manifests/bootstrap/manifests
cp /mcc-manifests/bootstrap/manifests.tmp/* /mcc-manifests/bootstrap/manifests/
cp /assets/manifests/*.machineconfigpool.yaml /mcc-manifests/bootstrap/manifests/`,
		images["machine-config-operator"],
		images["machine-os-content"],
		images["pod"],
		images["keepalived-ipfailover"],
		images["coredns"],
		images["mdns-publisher"],
		images["haproxy-router"],
		images["baremetal-runtimecfg"],
	)

	customMachineConfigArg := `
cat <<"EOF" > "./copy-ignition-config.sh"
#!/bin/bash
name="${1}"
oc get cm ${name} -n "${NAMESPACE}" -o jsonpath='{ .data.data }' > "/mcc-manifests/bootstrap/manifests/${name/#ignition-config-//}.yaml"
EOF
chmod +x ./copy-ignition-config.sh
oc get cm -l ignition-config="true" -n "${NAMESPACE}" --no-headers | awk '{ print $1 }' | xargs -n1 ./copy-ignition-config.sh`

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: resourceGenerateName,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:            sa.Name,
			TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
			Tolerations: []corev1.Toleration{
				{
					Key:      "multi-az-worker",
					Operator: "Equal",
					Value:    "true",
					Effect:   "NoSchedule",
				},
			},
			InitContainers: []corev1.Container{
				{
					Image: images["machine-config-operator"],
					Name:  "machine-config-operator-bootstrap",
					Command: []string{
						"/bin/bash",
					},
					Args: []string{
						"-c",
						bootstrapArgs,
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
				},
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
						"-c",
						customMachineConfigArg,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "mcc-manifests",
							MountPath: "/mcc-manifests",
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
						"--insecure-port=8080",
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: 8080,
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
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "kubeconfig",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "machine-config-server-kubeconfig",
						},
					},
				},
				{
					Name: "mcs-tls",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "ignition-server-serving-cert",
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
			},
		},
	}
}

func machineConfigServerServiceAccount(namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: resourceGenerateName,
		},
		ImagePullSecrets: []corev1.LocalObjectReference{
			{Name: "pull-secret"},
		},
	}
}

func machineConfigServerRoleBinding(sa *corev1.ServiceAccount) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    sa.Namespace,
			GenerateName: resourceGenerateName,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "edit",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		},
	}
}
