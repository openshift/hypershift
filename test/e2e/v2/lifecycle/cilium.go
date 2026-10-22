//go:build e2ev2

package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	ciliumassets "github.com/openshift/hypershift/test/e2e/assets/cilium"

	configv1 "github.com/openshift/api/config/v1"
	securityv1 "github.com/openshift/api/security/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func installCilium(ctx context.Context, management crclient.Client, cluster *hyperv1.HostedCluster, replicas int32) error {
	if replicas <= 0 {
		return fmt.Errorf("cilium requires a positive worker replica count")
	}
	if cluster.Spec.Networking.NetworkType != hyperv1.Other {
		return fmt.Errorf("cilium fixture requires network type Other")
	}
	if cluster.Status.KubeConfig == nil {
		return fmt.Errorf("cilium fixture has no hosted cluster kubeconfig reference")
	}
	secret := &corev1.Secret{}
	if err := management.Get(ctx, crclient.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Status.KubeConfig.Name}, secret); err != nil {
		return err
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
	if err != nil {
		return err
	}
	config.QPS, config.Burst = -1, -1
	client, err := crclient.New(config, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return err
	}
	if err := installCiliumOperator(ctx, client); err != nil {
		return err
	}
	network := &configv1.Network{}
	if err := client.Get(ctx, crclient.ObjectKey{Name: "cluster"}, network); err != nil {
		return err
	}
	networks := network.Status.ClusterNetwork
	if len(networks) == 0 {
		networks = network.Spec.ClusterNetwork
	}
	ciliumConfig, err := buildCiliumConfig(networks)
	if err != nil {
		return err
	}
	if err := applyCiliumObject(ctx, client, ciliumConfig); err != nil {
		return err
	}
	return waitForCiliumReady(ctx, client, ciliumConfig, replicas)
}

func installCiliumOperator(ctx context.Context, client crclient.Client) error {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "cilium", Labels: map[string]string{
		"security.openshift.io/scc.podSecurityLabelSync": "false", "pod-security.kubernetes.io/enforce": "privileged",
		"pod-security.kubernetes.io/audit": "privileged", "pod-security.kubernetes.io/warn": "privileged",
	}}}
	if err := applyCiliumObject(ctx, client, namespace); err != nil {
		return err
	}
	scc := &securityv1.SecurityContextConstraints{
		ObjectMeta:     metav1.ObjectMeta{Name: "cilium-scc"},
		AllowHostPorts: true, AllowHostNetwork: true, AllowHostDirVolumePlugin: true,
		AllowPrivilegeEscalation: ptr.To(true), AllowPrivilegedContainer: true,
		AllowedCapabilities: []corev1.Capability{"CHOWN", "KILL", "NET_ADMIN", "NET_RAW", "IPC_LOCK", "SYS_MODULE", "SYS_ADMIN", "SYS_RESOURCE", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID", "SYS_CHROOT", "SYS_PTRACE"},
		RunAsUser:           securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyRunAsAny},
		SELinuxContext:      securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyRunAsAny},
		Volumes:             []securityv1.FSType{securityv1.FSTypeHostPath, securityv1.FSTypeEmptyDir, securityv1.FSTypeSecret, securityv1.FSTypeConfigMap, securityv1.FSProjected},
		Users:               []string{"system:serviceaccount:cilium:cilium", "system:serviceaccount:cilium:cilium-operator"},
	}
	if err := applyCiliumObject(ctx, client, scc); err != nil {
		return err
	}
	entries, err := ciliumassets.Manifests.ReadDir("v1.15.1")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "cluster-network-") || strings.Contains(entry.Name(), "cilium-namespace") || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		content, err := ciliumassets.Manifests.ReadFile("v1.15.1/" + entry.Name())
		if err != nil {
			return err
		}
		decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(content), 4096)
		for {
			object := &unstructured.Unstructured{}
			if err := decoder.Decode(object); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return err
			}
			if len(object.Object) == 0 {
				continue
			}
			if err := applyCiliumObject(ctx, client, object); err != nil {
				return err
			}
		}
	}
	if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		deployment := &appsv1.Deployment{}
		if err := client.Get(ctx, crclient.ObjectKey{Namespace: "cilium", Name: "cilium-olm"}, deployment); err != nil {
			return false, crclient.IgnoreNotFound(err)
		}
		desired := ptr.Deref(deployment.Spec.Replicas, 1)
		return desired > 0 && deployment.Status.ObservedGeneration >= deployment.Generation && deployment.Status.UpdatedReplicas == desired && deployment.Status.ReadyReplicas == desired && deployment.Status.AvailableReplicas == desired, nil
	}); err != nil {
		return fmt.Errorf("waiting for Cilium operator: %w", err)
	}
	return nil
}

func waitForCiliumReady(ctx context.Context, client crclient.Client, ciliumConfig *unstructured.Unstructured, replicas int32) error {
	if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 20*time.Minute, true, func(ctx context.Context) (bool, error) {
		observedConfig := ciliumConfig.DeepCopy()
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ciliumConfig), observedConfig); err != nil {
			return false, crclient.IgnoreNotFound(err)
		}
		expectedMask, _, _ := unstructured.NestedInt64(ciliumConfig.Object, "spec", "ipam", "operator", "clusterPoolIPv4MaskSize")
		mask, _, err := unstructured.NestedInt64(observedConfig.Object, "spec", "ipam", "operator", "clusterPoolIPv4MaskSize")
		if err != nil || mask != expectedMask {
			if err := applyCiliumObject(ctx, client, ciliumConfig); err != nil {
				return false, err
			}
			return false, nil
		}
		daemonSets := &appsv1.DaemonSetList{}
		if err := client.List(ctx, daemonSets, crclient.InNamespace("cilium")); err != nil {
			return false, err
		}
		for _, daemonSet := range daemonSets.Items {
			if daemonSet.Name == "cilium" || strings.HasPrefix(daemonSet.Name, "cilium-") && !strings.Contains(daemonSet.Name, "operator") {
				return ciliumDaemonSetReady(&daemonSet, replicas), nil
			}
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("waiting for Cilium IPAM and agent rollout: %w", err)
	}
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		nodes := &corev1.NodeList{}
		if err := client.List(ctx, nodes); err != nil {
			return false, err
		}
		if len(nodes.Items) != int(replicas) {
			return false, nil
		}
		for _, node := range nodes.Items {
			ready := false
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
					ready = true
				}
			}
			if !ready {
				return false, nil
			}
		}
		return true, nil
	})
}

func applyCiliumObject(ctx context.Context, client crclient.Client, object runtime.Object) error {
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		return err
	}
	resource := &unstructured.Unstructured{Object: content}
	if resource.GetAPIVersion() == "" || resource.GetKind() == "" {
		kinds, _, err := client.Scheme().ObjectKinds(object)
		if err != nil {
			return fmt.Errorf("resolving Cilium fixture kind for %T: %w", object, err)
		}
		if len(kinds) == 0 {
			return fmt.Errorf("no Cilium fixture kind registered for %T", object)
		}
		resource.SetGroupVersionKind(kinds[0])
	}
	if err := client.Patch(ctx, resource, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("hypershift-e2e-v2-cilium")); err != nil {
		return fmt.Errorf("applying Cilium fixture %s %s/%s: %w", resource.GetKind(), resource.GetNamespace(), resource.GetName(), err)
	}
	return nil
}

func buildCiliumConfig(networks []configv1.ClusterNetworkEntry) (*unstructured.Unstructured, error) {
	if len(networks) == 0 {
		networks = []configv1.ClusterNetworkEntry{{CIDR: "10.132.0.0/14", HostPrefix: 23}}
	}
	for _, network := range networks {
		address, subnet, err := net.ParseCIDR(network.CIDR)
		if err != nil || address.To4() == nil {
			continue
		}
		ones, _ := subnet.Mask.Size()
		if network.HostPrefix == 0 {
			network.HostPrefix = 23
		}
		if network.HostPrefix < uint32(ones) || network.HostPrefix > 32 {
			return nil, fmt.Errorf("cilium IPv4 host prefix is invalid")
		}
		return &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "cilium.io/v1alpha1", "kind": "CiliumConfig",
			"metadata": map[string]interface{}{"name": "cilium", "namespace": "cilium"},
			"spec": map[string]interface{}{
				"debug": map[string]interface{}{"enabled": true}, "k8s": map[string]interface{}{"requireIPv4PodCIDR": true},
				"logSystemLoad": true, "bpf": map[string]interface{}{"preallocateMaps": true}, "etcd": map[string]interface{}{"leaseTTL": "30s"},
				"ipv4": map[string]interface{}{"enabled": true}, "ipv6": map[string]interface{}{"enabled": false}, "identityChangeGracePeriod": "0s",
				"ipam":              map[string]interface{}{"mode": "cluster-pool", "operator": map[string]interface{}{"clusterPoolIPv4PodCIDRList": []interface{}{network.CIDR}, "clusterPoolIPv4MaskSize": int64(network.HostPrefix)}},
				"nativeRoutingCIDR": network.CIDR, "endpointRoutes": map[string]interface{}{"enabled": true}, "clusterHealthPort": int64(9940), "tunnelPort": int64(4789),
				"cni":        map[string]interface{}{"binPath": "/var/lib/cni/bin", "confPath": "/var/run/multus/cni/net.d", "chainingMode": "portmap"},
				"prometheus": map[string]interface{}{"serviceMonitor": map[string]interface{}{"enabled": false}}, "hubble": map[string]interface{}{"tls": map[string]interface{}{"enabled": false}},
				"sessionAffinity": true, "tolerations": []interface{}{map[string]interface{}{"operator": "Exists"}},
			},
		}}, nil
	}
	return nil, fmt.Errorf("cilium fixture requires an observed IPv4 cluster network")
}

func ciliumDaemonSetReady(daemonSet *appsv1.DaemonSet, replicas int32) bool {
	return replicas > 0 && daemonSet.Status.ObservedGeneration >= daemonSet.Generation && daemonSet.Status.DesiredNumberScheduled >= replicas &&
		daemonSet.Status.NumberReady == daemonSet.Status.DesiredNumberScheduled && daemonSet.Status.UpdatedNumberScheduled == daemonSet.Status.DesiredNumberScheduled
}
