package primaryudn

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	internalAppsDNSNamespace = "internal-apps-dns"
	internalAppsDNSConfigMap = "coredns-config"
	internalAppsDNSDeploy    = "internal-apps-dns"
	internalAppsDNSSvc       = "internal-apps-dns"
	internalAppsDNSPort      = 5353
)

func ensureInternalAppsDNSBase(ctx context.Context, c client.Client, dnsImage string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: internalAppsDNSNamespace}}
	// Namespace is cluster-scoped; we only need to ensure it exists.
	if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: internalAppsDNSNamespace, Name: internalAppsDNSConfigMap}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		// Default Corefile is installed separately once we know the desired host mappings.
		if cm.Data["Corefile"] == "" {
			cm.Data["Corefile"] = ".:5353 {\n    errors\n    reload\n    whoami\n}\n"
		}
		return nil
	}); err != nil {
		return err
	}

	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: internalAppsDNSNamespace, Name: internalAppsDNSDeploy}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, deploy, func() error {
		labels := map[string]string{"app": internalAppsDNSDeploy}
		deploy.Spec.Replicas = ptr.To[int32](1)
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deploy.Spec.Template.ObjectMeta.Labels = labels
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "coredns",
			Image: dnsImage,
			Args:  []string{"-conf", "/etc/coredns/Corefile"},
			Ports: []corev1.ContainerPort{
				{Name: "dns-udp", ContainerPort: internalAppsDNSPort, Protocol: corev1.ProtocolUDP},
				{Name: "dns-tcp", ContainerPort: internalAppsDNSPort, Protocol: corev1.ProtocolTCP},
			},
			VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: "/etc/coredns"}},
		}}
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: internalAppsDNSConfigMap}},
			},
		}}
		return nil
	}); err != nil {
		return err
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: internalAppsDNSNamespace, Name: internalAppsDNSSvc}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, svc, func() error {
		svc.Spec.Selector = map[string]string{"app": internalAppsDNSDeploy}
		svc.Spec.Ports = []corev1.ServicePort{
			{Name: "dns-udp", Port: internalAppsDNSPort, TargetPort: intstr.FromInt(internalAppsDNSPort), Protocol: corev1.ProtocolUDP},
			{Name: "dns-tcp", Port: internalAppsDNSPort, TargetPort: intstr.FromInt(internalAppsDNSPort), Protocol: corev1.ProtocolTCP},
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func internalAppsDNSUpstream(ctx context.Context, c client.Client) (upstream string, ready bool, err error) {
	clusterIP, ready, err := serviceClusterIPAndReadyEndpoints(ctx, c, internalAppsDNSNamespace, internalAppsDNSSvc)
	if err != nil {
		return "", false, err
	}
	if clusterIP == "" || !ready {
		return "", false, nil
	}
	return fmt.Sprintf("%s:%d", clusterIP, internalAppsDNSPort), true, nil
}

func ensureInternalAppsDNSCorefile(ctx context.Context, c client.Client, hosts map[string]string) error {
	corefile := renderInternalAppsDNSCorefile(hosts)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: internalAppsDNSNamespace, Name: internalAppsDNSConfigMap}}
	_, err := controllerutil.CreateOrUpdate(ctx, c, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data["Corefile"] = corefile
		return nil
	})
	return err
}

func renderInternalAppsDNSCorefile(hosts map[string]string) string {
	// This CoreDNS only serves the specific host overrides.
	// No forward plugin: unmatched queries get NXDOMAIN, which is correct because
	// dns-default handles all other zones. Avoiding forward prevents the server from
	// deadlocking on upstream DNS timeouts.
	var b strings.Builder
	b.WriteString(".:5353 {\n")
	b.WriteString("    errors\n")
	b.WriteString("    reload\n")
	b.WriteString("    hosts {\n")
	keys := make([]string, 0, len(hosts))
	for k := range hosts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, host := range keys {
		b.WriteString(fmt.Sprintf("        %s %s\n", hosts[host], host))
	}
	b.WriteString("    }\n")
	b.WriteString("}\n")
	return b.String()
}

func serviceClusterIPAndReadyEndpoints(ctx context.Context, c client.Client, namespace, name string) (clusterIP string, ready bool, err error) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		return "", false, err
	}
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
		return "", false, nil
	}

	ep := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(ep), ep); err != nil {
		if apierrors.IsNotFound(err) {
			return svc.Spec.ClusterIP, false, nil
		}
		return "", false, err
	}
	for i := range ep.Subsets {
		if len(ep.Subsets[i].Addresses) > 0 {
			return svc.Spec.ClusterIP, true, nil
		}
	}
	return svc.Spec.ClusterIP, false, nil
}
