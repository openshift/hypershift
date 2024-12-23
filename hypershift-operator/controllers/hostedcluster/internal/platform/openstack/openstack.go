package openstack

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/openstackutil"
	"github.com/openshift/hypershift/support/upsert"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/utils/ptr"

	capo "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/openstack"
)

const (
	sgRuleDescription = "Managed by the Hypershift Control Plane Operator"
)

type OpenStack struct {
	capiProviderImage string
}

func New(capiProviderImage string) *OpenStack {
	return &OpenStack{
		capiProviderImage: capiProviderImage,
	}
}

func (a OpenStack) ReconcileCAPIInfraCR(ctx context.Context, client client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {
	openStackCluster := &capo.OpenStackCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hcluster.Name,
			Namespace: controlPlaneNamespace,
		},
	}
	openStackPlatform := hcluster.Spec.Platform.OpenStack
	if openStackPlatform == nil {
		return nil, fmt.Errorf("failed to reconcile OpenStack CAPI cluster, empty OpenStack platform spec")
	}

	openStackCluster.Spec.IdentityRef = capo.OpenStackIdentityReference{
		Name:      openStackPlatform.IdentityRef.Name,
		CloudName: openStackPlatform.IdentityRef.CloudName,
	}
	if _, err := createOrUpdate(ctx, client, openStackCluster, func() error {
		err := reconcileOpenStackClusterSpec(hcluster, &openStackCluster.Spec, apiEndpoint)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return openStackCluster, nil
}

func reconcileOpenStackClusterSpec(hcluster *hyperv1.HostedCluster, openStackClusterSpec *capo.OpenStackClusterSpec, apiEndpoint hyperv1.APIEndpoint) error {
	machineNetworks := hcluster.Spec.Networking.MachineNetwork
	if len(machineNetworks) == 0 {
		return fmt.Errorf("failed to reconcile OpenStackClusterSpec, empty machine networks")
	}

	openStackPlatform := hcluster.Spec.Platform.OpenStack

	openStackClusterSpec.ControlPlaneEndpoint = &capiv1.APIEndpoint{
		Host: apiEndpoint.Host,
		Port: apiEndpoint.Port,
	}

	if len(openStackPlatform.Subnets) > 0 {
		openStackClusterSpec.Subnets = make([]capo.SubnetParam, len(openStackPlatform.Subnets))
		for i := range openStackPlatform.Subnets {
			subnet := openStackPlatform.Subnets[i]
			openStackClusterSpec.Subnets[i] = capo.SubnetParam{ID: subnet.ID}
			subnetFilter := subnet.Filter
			if subnetFilter != nil {
				openStackClusterSpec.Subnets[i].Filter = &capo.SubnetFilter{
					Name:                subnetFilter.Name,
					Description:         subnetFilter.Description,
					ProjectID:           subnetFilter.ProjectID,
					IPVersion:           subnetFilter.IPVersion,
					GatewayIP:           subnetFilter.GatewayIP,
					CIDR:                subnetFilter.CIDR,
					IPv6AddressMode:     subnetFilter.IPv6AddressMode,
					IPv6RAMode:          subnetFilter.IPv6RAMode,
					FilterByNeutronTags: openstackutil.CreateCAPOFilterTags(subnetFilter.Tags, subnetFilter.TagsAny, subnetFilter.NotTags, subnetFilter.NotTagsAny),
				}
			}
		}
	} else {
		openStackClusterSpec.ManagedSubnets = make([]capo.SubnetSpec, len(machineNetworks))
		// Only one Subnet is supported in CAPO
		openStackClusterSpec.ManagedSubnets[0] = capo.SubnetSpec{
			CIDR: machineNetworks[0].CIDR.String(),
		}
		for i := range openStackPlatform.ManagedSubnets {
			openStackClusterSpec.ManagedSubnets[i].DNSNameservers = openStackPlatform.ManagedSubnets[i].DNSNameservers
			allocationPools := openStackPlatform.ManagedSubnets[i].AllocationPools
			openStackClusterSpec.ManagedSubnets[i].AllocationPools = make([]capo.AllocationPool, len(allocationPools))
			for j := range allocationPools {
				openStackClusterSpec.ManagedSubnets[i].AllocationPools[j] = capo.AllocationPool{
					Start: allocationPools[j].Start,
					End:   allocationPools[j].End,
				}
			}
		}
	}
	if openStackPlatform.Router != nil {
		openStackClusterSpec.Router = &capo.RouterParam{ID: openStackPlatform.Router.ID}
		if openStackPlatform.Router.Filter != nil {
			routerFilter := openStackPlatform.Router.Filter
			openStackClusterSpec.Router.Filter = &capo.RouterFilter{
				Name:                routerFilter.Name,
				Description:         routerFilter.Description,
				ProjectID:           routerFilter.ProjectID,
				FilterByNeutronTags: openstackutil.CreateCAPOFilterTags(routerFilter.Tags, routerFilter.TagsAny, routerFilter.NotTags, routerFilter.NotTagsAny),
			}

		}
	}
	if openStackPlatform.Network != nil {
		openStackClusterSpec.Network = &capo.NetworkParam{ID: openStackPlatform.Network.ID}
		if openStackPlatform.Network.Filter != nil {
			openStackClusterSpec.Network.Filter = openstackutil.CreateCAPONetworkFilter(openStackPlatform.Network.Filter)
		}
	}
	if openStackPlatform.NetworkMTU != nil {
		openStackClusterSpec.NetworkMTU = openStackPlatform.NetworkMTU
	}
	if openStackPlatform.ExternalNetwork != nil {
		openStackClusterSpec.ExternalNetwork = &capo.NetworkParam{ID: openStackPlatform.ExternalNetwork.ID}
		if openStackPlatform.ExternalNetwork.Filter != nil {
			openStackClusterSpec.ExternalNetwork.Filter = openstackutil.CreateCAPONetworkFilter(openStackPlatform.ExternalNetwork.Filter)
		}
	}
	if openStackPlatform.DisableExternalNetwork != nil {
		openStackClusterSpec.DisableExternalNetwork = openStackPlatform.DisableExternalNetwork
	}
	openStackClusterSpec.DisableAPIServerFloatingIP = ptr.To(true)
	openStackClusterSpec.ManagedSecurityGroups = &capo.ManagedSecurityGroups{
		AllNodesSecurityGroupRules: defaultWorkerSecurityGroupRules(machineNetworksToStrings(machineNetworks)),
	}

	// Users are permitted to specify additional tags to be applied to the OpenStack resources
	// but the default tag will be compliant with the OpenShift Cluster ID.
	openStackClusterSpec.Tags = []string{"openshiftClusterID=" + hcluster.Spec.InfraID}
	openStackClusterSpec.Tags = append(openStackClusterSpec.Tags, openStackPlatform.Tags...)

	return nil
}

func (a OpenStack) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	image := a.capiProviderImage
	if envImage := os.Getenv(images.OpenStackCAPIProviderEnvVar); len(envImage) > 0 {
		image = envImage
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIOpenStackProviderImage]; ok {
		image = override
	}
	defaultMode := int32(0640)
	return &appsv1.DeploymentSpec{
		Replicas: ptr.To[int32](1),
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					{
						Name: "capi-webhooks-tls",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &defaultMode,
								SecretName:  "capi-webhooks-tls",
							},
						},
					},
				},
				Containers: []corev1.Container{{
					Name:            "manager",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"/manager"},
					Args: []string{
						"--namespace=$(MY_NAMESPACE)",
						"--leader-elect",
						"--metrics-bind-addr=127.0.0.1:8080",
						// We need to set the log level to 3 to get the logs from ORC.
						// Once ORC follows logging guidelines, we should use V(2) again.
						"--v=3",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("10Mi"),
						},
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "healthz",
							ContainerPort: 9440,
							Protocol:      corev1.ProtocolTCP,
						},
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/healthz",
								Port: intstr.FromString("healthz"),
							},
						},
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/readyz",
								Port: intstr.FromString("healthz"),
							},
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "capi-webhooks-tls",
							ReadOnly:  true,
							MountPath: "/tmp/k8s-webhook-server/serving-certs",
						},
					},
					Env: []corev1.EnvVar{
						{
							Name: "MY_NAMESPACE",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.namespace",
								},
							},
						},
					},
				}},
			}}}, nil
}

func (a OpenStack) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	if err := a.reconcileCloudsYaml(ctx, c, createOrUpdate, controlPlaneNamespace, hcluster.Namespace, hcluster.Spec.Platform.OpenStack.IdentityRef.Name); err != nil {
		return fmt.Errorf("failed to reconcile OpenStack clouds.yaml: %w", err)
	}

	// Sync CNCC secret
	if err := a.reconcileOpenStackCredentialsSecret(ctx, c, createOrUpdate, hcluster, controlPlaneNamespace, "cloud-network-config-controller-creds"); err != nil {
		return err
	}
	// Sync Cinder CSI driver secret
	if err := a.reconcileOpenStackCredentialsSecret(ctx, c, createOrUpdate, hcluster, controlPlaneNamespace, "openstack-cloud-credentials"); err != nil {
		return err
	}

	// Sync Manila CSI driver secret
	if err := a.reconcileOpenStackCredentialsSecret(ctx, c, createOrUpdate, hcluster, controlPlaneNamespace, "manila-cloud-credentials"); err != nil {
		return err
	}

	return nil
}

// reconcileOpenStackCredentialsSecret is a wrapper used to reconcile the OpenStack cloud config secret.
func (a OpenStack) reconcileOpenStackCredentialsSecret(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace, name string) error {
	credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcluster.Namespace, Name: hcluster.Spec.Platform.OpenStack.IdentityRef.Name}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
		return fmt.Errorf("failed to get OpenStack credentials secret: %w", err)
	}

	caCertData := openstack.GetCACertFromCredentialsSecret(credentialsSecret)
	credsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: name},
		Data:       map[string][]byte{},
	}
	credsSecret.Data[openstack.CloudsSecretKey] = credentialsSecret.Data[openstack.CloudsSecretKey]
	if caCertData != nil {
		credsSecret.Data[openstack.CABundleKey] = caCertData
	}

	if _, err := createOrUpdate(ctx, c, credsSecret, func() error {
		return openstack.ReconcileCloudConfigSecret(hcluster.Spec.Platform.OpenStack, credsSecret, credentialsSecret, caCertData, hcluster.Spec.Networking.MachineNetwork)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OpenStack cloud config for %s: %w", name, err)
	}

	return nil
}

func (a OpenStack) reconcileCloudsYaml(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, controlPlaneNamespace string, clusterNamespace string, identityRefName string) error {
	var source corev1.Secret

	// Sync user clouds.yaml secret
	clusterCloudsSecret := client.ObjectKey{Namespace: clusterNamespace, Name: identityRefName}
	if err := c.Get(ctx, clusterCloudsSecret, &source); err != nil {
		return fmt.Errorf("failed to get secret %s: %w", clusterCloudsSecret, err)
	}

	userCloudsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: identityRefName}}
	_, err := createOrUpdate(ctx, c, userCloudsSecret, func() error {
		if userCloudsSecret.Data == nil {
			userCloudsSecret.Data = map[string][]byte{}
		}
		if _, ok := source.Data[openstack.CASecretKey]; ok {
			userCloudsSecret.Data[openstack.CASecretKey] = source.Data[openstack.CASecretKey]
		}
		userCloudsSecret.Data[openstack.CloudsSecretKey] = source.Data[openstack.CloudsSecretKey]
		return nil
	})

	return err
}

func (a OpenStack) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}

func (a OpenStack) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{"ipam.cluster.x-k8s.io"},
			Resources: []string{"ipaddressclaims", "ipaddressclaims/status"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{"ipam.cluster.x-k8s.io"},
			Resources: []string{"ipaddresses", "ipaddresses/status"},
			Verbs:     []string{"create", "delete", "get", "list", "update", "watch"},
		},
		// The following rule is required for CAPO to reconcile the Images resources created by ORC,
		// which is a dependency since CAPO v0.11.0.
		// This rule is also defined in the Hypershift Operator and the Hypershift CLI when creating
		// the cluster.
		{
			APIGroups: []string{"openstack.k-orc.cloud"},
			Resources: []string{"images", "images/status"},
			Verbs:     []string{rbacv1.VerbAll},
		},
	}
}

func (a OpenStack) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}

func machineNetworksToStrings(machineNetworks []hyperv1.MachineNetworkEntry) []string {
	var machineNetworksStr []string
	for _, machineNetwork := range machineNetworks {
		machineNetworksStr = append(machineNetworksStr, machineNetwork.CIDR.String())
	}
	return machineNetworksStr
}

func defaultWorkerSecurityGroupRules(machineCIDRs []string) []capo.SecurityGroupRuleSpec {
	ingressRules := []capo.SecurityGroupRuleSpec{}

	// Rules for worker security group with the remote IP prefix set to the machine CIDRs
	for _, machineCIDR := range machineCIDRs {
		machineCIDRInboundRules := []capo.SecurityGroupRuleSpec{
			{
				Name:     "esp-ingress",
				Protocol: ptr.To("esp"),
			},
			{
				Name:     "icmp-ingress",
				Protocol: ptr.To("icmp"),
			},
			{
				Name:         "router-ingress",
				Protocol:     ptr.To("tcp"),
				PortRangeMin: ptr.To(1936),
				PortRangeMax: ptr.To(1936),
			},
			{
				Name:         "ssh-ingress",
				Protocol:     ptr.To("tcp"),
				PortRangeMin: ptr.To(22),
				PortRangeMax: ptr.To(22),
			},
			{
				Name:     "vrrp-ingress",
				Protocol: ptr.To("vrrp"),
			},
		}

		for i, rule := range machineCIDRInboundRules {
			rule.RemoteIPPrefix = ptr.To(machineCIDR)
			machineCIDRInboundRules[i] = rule
		}

		ingressRules = append(ingressRules, machineCIDRInboundRules...)
	}

	// Rules open to all
	allIngressRules := []capo.SecurityGroupRuleSpec{
		{
			Name:         "http-ingress",
			Protocol:     ptr.To("tcp"),
			PortRangeMin: ptr.To(80),
			PortRangeMax: ptr.To(80),
		},
		{
			Name:         "https-ingress",
			Protocol:     ptr.To("tcp"),
			PortRangeMin: ptr.To(443),
			PortRangeMax: ptr.To(443),
		},
		{
			Name:         "geneve-ingress",
			Protocol:     ptr.To("udp"),
			PortRangeMin: ptr.To(6081),
			PortRangeMax: ptr.To(6081),
		},
		{
			Name:         "ike-ingress",
			Protocol:     ptr.To("udp"),
			PortRangeMin: ptr.To(500),
			PortRangeMax: ptr.To(500),
		},
		{
			Name:         "ike-nat-ingress",
			Protocol:     ptr.To("udp"),
			PortRangeMin: ptr.To(4500),
			PortRangeMax: ptr.To(4500),
		},
		{
			Name:         "internal-ingress-tcp",
			Protocol:     ptr.To("tcp"),
			PortRangeMin: ptr.To(9000),
			PortRangeMax: ptr.To(9999),
		},
		{
			Name:         "internal-ingress-udp",
			Protocol:     ptr.To("udp"),
			PortRangeMin: ptr.To(9000),
			PortRangeMax: ptr.To(9999),
		},
		{
			Name:         "vxlan-ingress",
			Protocol:     ptr.To("udp"),
			PortRangeMin: ptr.To(4789),
			PortRangeMax: ptr.To(4789),
		},
	}
	for i, rule := range allIngressRules {
		rule.RemoteIPPrefix = ptr.To("0.0.0.0/0")
		allIngressRules[i] = rule
	}
	ingressRules = append(ingressRules, allIngressRules...)

	// Common attributes for all rules
	for i, rule := range ingressRules {
		rule.Description = ptr.To(sgRuleDescription)
		rule.Direction = "ingress"
		rule.EtherType = ptr.To("IPv4")
		ingressRules[i] = rule
	}

	return ingressRules
}
