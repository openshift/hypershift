package v1beta1

import (
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/util/ipnet"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&HostedCluster{},
			&HostedClusterList{},
		)
		metav1.AddToGroupVersion(scheme, SchemeGroupVersion)

		return nil
	})
}

const (
	// AuditWebhookKubeconfigKey is the key name in the AuditWebhook secret that stores audit webhook kubeconfig
	AuditWebhookKubeconfigKey          = "webhook-kubeconfig"
	DisablePKIReconciliationAnnotation = "hypershift.openshift.io/disable-pki-reconciliation"
	// SkipReleaseImageValidation skips any release validation that the HO version might dictate for any HC and skip min supported version check for NodePools.
	SkipReleaseImageValidation                = "hypershift.openshift.io/skip-release-image-validation"
	IdentityProviderOverridesAnnotationPrefix = "idpoverrides.hypershift.openshift.io/"
	OauthLoginURLOverrideAnnotation           = "oauth.hypershift.openshift.io/login-url-override"
	// HCDestroyGracePeriodAnnotation is an annotation which will delay the removal of the HostedCluster finalizer to allow consumers to read the status of the HostedCluster
	// before the resource goes away. The format of the annotation is a go duration string with a numeric component and unit.
	// sample: hypershift.openshift.io/destroy-grace-period: "600s"
	HCDestroyGracePeriodAnnotation = "hypershift.openshift.io/destroy-grace-period"
	// ControlPlanePriorityClass is for pods in the HyperShift Control Plane that are not API critical but still need elevated priority. E.g Cluster Version Operator.
	ControlPlanePriorityClass = "hypershift.openshift.io/control-plane-priority-class"
	// APICriticalPriorityClass is for pods that are required for API calls and resource admission to succeed. This includes pods like kube-apiserver, aggregated API servers, and webhooks.
	APICriticalPriorityClass = "hypershift.openshift.io/api-critical-priority-class"
	// EtcdPriorityClass is for etcd pods.
	EtcdPriorityClass = "hypershift.openshift.io/etcd-priority-class"
	// KonnectivityServerImageAnnotation is a temporary annotation that allows the specification of the konnectivity server image.
	// This will be removed when Konnectivity is added to the Openshift release payload
	KonnectivityServerImageAnnotation = "hypershift.openshift.io/konnectivity-server-image"
	// KonnectivityAgentImageAnnotation is a temporary annotation that allows the specification of the konnectivity agent image.
	// This will be removed when Konnectivity is added to the Openshift release payload
	KonnectivityAgentImageAnnotation = "hypershift.openshift.io/konnectivity-agent-image"
	// ControlPlaneOperatorImageAnnotation is an annotation that allows the specification of the control plane operator image.
	// This is used for development and e2e workflows
	ControlPlaneOperatorImageAnnotation = "hypershift.openshift.io/control-plane-operator-image"
	// ControlPlaneOperatorImageLabelsAnnotation is an annotation that allows the specification of the control plane operator image labels.
	// Labels are provided in a comma-delimited format: key=value,key2=value2
	// This is used for development and e2e workflows
	ControlPlaneOperatorImageLabelsAnnotation = "hypershift.openshift.io/control-plane-operator-image-labels"
	// RestartDateAnnotation is a annotation that can be used to trigger a rolling restart of all components managed by hypershift.
	// it is important in some situations like CA rotation where components need to be fully restarted to pick up new CAs. It's also
	// important in some recovery situations where a fresh start of the component helps fix symptoms a user might be experiencing.
	RestartDateAnnotation = "hypershift.openshift.io/restart-date"
	// ReleaseImageAnnotation is an annotation that can be used to see what release image a given deployment is tied to
	ReleaseImageAnnotation = "hypershift.openshift.io/release-image"
	// ClusterAPIManagerImage is an annotation that allows the specification of the cluster api manager image.
	// This is a temporary workaround necessary for compliance reasons on the IBM Cloud side:
	// no images can be pulled from registries outside of IBM Cloud's official regional registries
	ClusterAPIManagerImage = "hypershift.openshift.io/capi-manager-image"
	// ClusterAutoscalerImage is an annotation that allows the specification of the cluster autoscaler image.
	// This is a temporary workaround necessary for compliance reasons on the IBM Cloud side:
	// no images can be pulled from registries outside of IBM Cloud's official regional registries
	ClusterAutoscalerImage = "hypershift.openshift.io/cluster-autoscaler-image"
	// AWSKMSProviderImage is an annotation that allows the specification of the AWS kms provider image.
	// Upstream code located at: https://github.com/kubernetes-sigs/aws-encryption-provider
	AWSKMSProviderImage = "hypershift.openshift.io/aws-kms-provider-image"
	// IBMCloudKMSProviderImage is an annotation that allows the specification of the IBM Cloud kms provider image.
	IBMCloudKMSProviderImage = "hypershift.openshift.io/ibmcloud-kms-provider-image"
	// PortierisImageAnnotation is an annotation that allows the specification of the portieries component
	// (performs container image verification).
	PortierisImageAnnotation = "hypershift.openshift.io/portieris-image"
	// PrivateIngressControllerAnnotation is an annotation that configures ingress controller with endpoint publishing strategy as Private.
	// This overrides any opinionated strategy set by platform in ReconcileDefaultIngressController.
	// It's used by IBM cloud to support ingress endpoint publishing strategy scope
	// NOTE: We'll expose this in the API if the use case gets generalised.
	PrivateIngressControllerAnnotation = "hypershift.openshift.io/private-ingress-controller"
	// IngressControllerLoadBalancerScope is an annotation that allows the specification of the LoadBalancer scope for ingress controller.
	IngressControllerLoadBalancerScope = "hypershift.openshift.io/ingress-controller-load-balancer-scope"

	// CertifiedOperatorsCatalogImageAnnotation, CommunityOperatorsCatalogImageAnnotation, RedHatMarketplaceCatalogImageAnnotation and RedHatOperatorsCatalogImageAnnotation
	// are annotations that can be used to override the address of the images used for the OLM catalogs if in the `management` OLMCatalogPlacement mode.
	// If used, all of them should be set at the same time referring images only by digest (`...@sha256:<id>`).
	// This will disable the imagestream used to keep the catalog images up to date.
	CertifiedOperatorsCatalogImageAnnotation = "hypershift.openshift.io/certified-operators-catalog-image"
	CommunityOperatorsCatalogImageAnnotation = "hypershift.openshift.io/community-operators-catalog-image"
	RedHatMarketplaceCatalogImageAnnotation  = "hypershift.openshift.io/redhat-marketplace-catalog-image"
	RedHatOperatorsCatalogImageAnnotation    = "hypershift.openshift.io/redhat-operators-catalog-image"

	// OLMCatalogsISRegistryOverridesAnnotation overrides the image registries used for the ImageStream used for the OLM catalogs.
	// It contains the source registry string as a key and the destination registry string as value.
	// Images before being applied are scanned for the source registry string and if found the string is replaced with the destination registry string.
	// Format is: "sr1=dr1,sr2=dr2"
	OLMCatalogsISRegistryOverridesAnnotation = "hypershift.openshift.io/olm-catalogs-is-registry-overrides"

	// ClusterAPIProviderAWSImage overrides the CAPI AWS provider image to use for
	// a HostedControlPlane.
	ClusterAPIProviderAWSImage = "hypershift.openshift.io/capi-provider-aws-image"

	// ClusterAPIKubeVirtProviderImage overrides the CAPI KubeVirt provider image to use for
	// a HostedControlPlane.
	ClusterAPIKubeVirtProviderImage = "hypershift.openshift.io/capi-provider-kubevirt-image"

	// ClusterAPIAgentProviderImage overrides the CAPI Agent provider image to use for
	// a HostedControlPlane.
	ClusterAPIAgentProviderImage = "hypershift.openshift.io/capi-provider-agent-image"

	// ClusterAPIAzureProviderImage overrides the CAPI Azure provider image to use for
	// a HostedControlPlane.
	ClusterAPIAzureProviderImage = "hypershift.openshift.io/capi-provider-azure-image"

	// ClusterAPIPowerVSProviderImage overrides the CAPI PowerVS provider image to use for
	// a HostedControlPlane.
	ClusterAPIPowerVSProviderImage = "hypershift.openshift.io/capi-provider-powervs-image"

	// ClusterAPIOpenStackProviderImage overrides the CAPI OpenStack provider image to use for
	// a HostedControlPlane.
	ClusterAPIOpenStackProviderImage = "hypershift.openshift.io/capi-provider-openstack-image"

	// AESCBCKeySecretKey defines the Kubernetes secret key name that contains the aescbc encryption key
	// in the AESCBC secret encryption strategy
	AESCBCKeySecretKey = "key"
	// IBMCloudIAMAPIKeySecretKey defines the Kubernetes secret key name that contains
	// the customer IBMCloud apikey in the unmanaged authentication strategy for IBMCloud KMS secret encryption
	IBMCloudIAMAPIKeySecretKey = "iam_apikey"
	// AWSCredentialsFileSecretKey defines the Kubernetes secret key name that contains
	// the customer AWS credentials in the unmanaged authentication strategy for AWS KMS secret encryption
	AWSCredentialsFileSecretKey = "credentials"
	// ControlPlaneComponentLabel identifies a resource as belonging to a hosted control plane.
	ControlPlaneComponentLabel = "hypershift.openshift.io/control-plane-component"

	// OperatorComponent identifies a component as belonging to the operator.
	OperatorComponent = "hypershift.openshift.io/operator-component"
	// MachineApproverImage is an annotation that allows the specification of the machine approver image.
	// This is a temporary workaround necessary for compliance reasons on the IBM Cloud side:
	// no images can be pulled from registries outside of IBM Cloud's official regional registries
	MachineApproverImage = "hypershift.openshift.io/machine-approver-image"

	// ExternalDNSHostnameAnnotation is the annotation external-dns uses to register DNS name for different HCP services.
	ExternalDNSHostnameAnnotation = "external-dns.alpha.kubernetes.io/hostname"

	// ForceUpgradeToAnnotation is the annotation that forces HostedCluster upgrade even if the underlying ClusterVersion
	// is reporting it is not Upgradeable.  The annotation value must be set to the release image being forced.
	ForceUpgradeToAnnotation = "hypershift.openshift.io/force-upgrade-to"

	// ServiceAccountSigningKeySecretKey is the name of the secret key that should contain the service account signing
	// key if specified.
	ServiceAccountSigningKeySecretKey = "key"

	// DisableProfilingAnnotation is the annotation that allows disabling profiling for control plane components.
	// Any components specified in this list will have profiling disabled. Profiling is disabled by default for etcd and konnectivity.
	// Components this annotation can apply to: kube-scheduler, kube-controller-manager, kube-apiserver.
	DisableProfilingAnnotation = "hypershift.openshift.io/disable-profiling"

	// CleanupCloudResourcesAnnotation is an annotation that indicates whether a guest cluster's resources should be
	// removed when deleting the corresponding HostedCluster. If set to "true", resources created on the cloud provider during the life
	// of the cluster will be removed, including image registry storage, ingress dns records, load balancers, and persistent storage.
	CleanupCloudResourcesAnnotation = "hypershift.openshift.io/cleanup-cloud-resources"

	// CleanupOrcImageResourcesAnnotation is an annotation that indicates whether an OpenStack Image should be
	// removed when deleting the corresponding ORC Image. If set to "true", the OpenStack Image will be removed.
	CleanupOrcImageResourcesAnnotation = "hypershift.openshift.io/cleanup-orc-image-resource"

	// ResourceRequestOverrideAnnotationPrefix is a prefix for an annotation to override resource requests for a particular deployment/container
	// in a hosted control plane. The format of the annotation is:
	// resource-request-override.hypershift.openshift.io/[deployment-name].[container-name]: [resource-type-1]=[value1],[resource-type-2]=[value2],...
	// For example, to override the memory and cpu request for the Kubernetes APIServer:
	// resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver: memory=3Gi,cpu=2000m
	ResourceRequestOverrideAnnotationPrefix = "resource-request-override.hypershift.openshift.io"

	// LimitedSupportLabel is a label that can be used by consumers to indicate
	// a cluster is somehow out of regular support policy.
	// https://docs.openshift.com/rosa/rosa_architecture/rosa_policy_service_definition/rosa-service-definition.html#rosa-limited-support_rosa-service-definition.
	LimitedSupportLabel = "api.openshift.com/limited-support"

	// SilenceClusterAlertsLabel  is a label that can be used by consumers to indicate
	// alerts from a cluster can be silenced or ignored
	SilenceClusterAlertsLabel = "hypershift.openshift.io/silence-cluster-alerts"

	// KubeVirtInfraCredentialsSecretName is a name of the secret in the hosted control plane namespace containing the kubeconfig
	// of an external infrastructure cluster for kubevirt provider
	KubeVirtInfraCredentialsSecretName = "kubevirt-infra-credentials"

	// InfraIDLabel is a label that indicates the hosted cluster's infra id
	// that the resource is associated with.
	InfraIDLabel = "hypershift.openshift.io/infra-id"

	// NodePoolNameLabel is a label that indicates the name of the node pool
	// a resource is associated with
	NodePoolNameLabel = "hypershift.openshift.io/nodepool-name"

	// RouteVisibilityLabel is a label that can be used by external-dns to filter routes
	// it should not consider for name registration
	RouteVisibilityLabel = "hypershift.openshift.io/route-visibility"

	// RouteVisibilityPrivate is a value for RouteVisibilityLabel that will result
	// in the labeled route being ignored by external-dns
	RouteVisibilityPrivate = "private"

	// AllowUnsupportedKubeVirtRHCOSVariantsAnnotation allows a NodePool to use image sources
	// other than the official rhcos kubevirt variant, such as the openstack variant. This
	// allows the creation of guest clusters <= 4.13, which are before the rhcos kubevirt
	// variant was released.
	AllowUnsupportedKubeVirtRHCOSVariantsAnnotation = "hypershift.openshift.io/allow-unsupported-kubevirt-rhcos-variants"

	// ImageOverridesAnnotation is passed as a flag to the CPO to allow overriding release images.
	// The format of the annotation value is a comma-separated list of image=ref pairs like:
	// cluster-network-operator=example.com/cno:latest,ovn-kubernetes=example.com/ovnkube:latest
	ImageOverridesAnnotation = "hypershift.openshift.io/image-overrides"

	// EnsureExistsPullSecretReconciliation enables a reconciliation behavior on in cluster pull secret
	// resources that enables user modifications to the resources while ensuring they do exist. This
	// allows users to execute workflows like disabling insights operator
	EnsureExistsPullSecretReconciliation = "hypershift.openshift.io/ensureexists-pullsecret-reconcile"

	// HostedClusterLabel is used as a label on nodes that are dedicated to a specific hosted cluster
	HostedClusterLabel = "hypershift.openshift.io/cluster"

	// RequestServingComponentLabel is used as a label on pods and nodes for dedicated serving components.
	RequestServingComponentLabel = "hypershift.openshift.io/request-serving-component"

	// TopologyAnnotation indicates the type of topology that should take effect for the
	// hosted cluster's control plane workloads. Currently the only value supported is "dedicated-request-serving-components".
	// We implicitly support shared and dedicated.
	TopologyAnnotation = "hypershift.openshift.io/topology"

	// HostedClusterScheduledAnnotation indicates that a hosted cluster with dedicated request serving components
	// has been assigned dedicated nodes. If not present, the hosted cluster needs scheduling.
	HostedClusterScheduledAnnotation = "hypershift.openshift.io/cluster-scheduled"

	// DedicatedRequestServingComponentsTopology indicates that control plane request serving
	// components should be scheduled on dedicated nodes in the management cluster.
	DedicatedRequestServingComponentsTopology = "dedicated-request-serving-components"

	// RequestServingNodeAdditionalSelectorAnnotation is used to specify an additional node selector for
	// request serving nodes. The value is a comma-separated list of key=value pairs.
	RequestServingNodeAdditionalSelectorAnnotation = "hypershift.openshift.io/request-serving-node-additional-selector"

	// DisableMachineManagement Disable deployments related to machine management that includes cluster-api, cluster-autoscaler, machine-approver.
	DisableMachineManagement = "hypershift.openshift.io/disable-machine-management"

	// AllowGuestWebhooksServiceLabel marks a service deployed in the control plane as a valid target
	// for validating/mutating webhooks running in the guest cluster.
	AllowGuestWebhooksServiceLabel = "hypershift.openshift.io/allow-guest-webhooks"

	// PodSecurityAdmissionLabelOverrideAnnotation allows overriding the pod security admission label on
	// hosted control plane namespacces. The default is 'Restricted'. Valid values are 'Restricted', 'Baseline', or 'Privileged'
	// See https://github.com/openshift/enhancements/blob/master/enhancements/authentication/pod-security-admission.md
	PodSecurityAdmissionLabelOverrideAnnotation = "hypershift.openshift.io/pod-security-admission-label-override"

	// DisableMonitoringServices introduces an option to disable monitor services IBM Cloud do not use.
	DisableMonitoringServices = "hypershift.openshift.io/disable-monitoring-services"

	// JSONPatchAnnotation allow modifying the kubevirt VM template using jsonpatch
	JSONPatchAnnotation = "hypershift.openshift.io/kubevirt-vm-jsonpatch"

	// KubeAPIServerGOGCAnnotation allows modifying the kube-apiserver GOGC environment variable to impact how often
	// the GO garbage collector runs. This can be used to reduce the memory footprint of the kube-apiserver.
	KubeAPIServerGOGCAnnotation = "hypershift.openshift.io/kube-apiserver-gogc"

	// KubeAPIServerGOMemoryLimitAnnotation allows modifying the kube-apiserver GOMEMLIMIT environment variable to increase
	// the frequency of memory collection when memory used rises above a particular threshold. This can be used to reduce
	// the memory footprint of the kube-apiserver during upgrades.
	KubeAPIServerGOMemoryLimitAnnotation = "hypershift.openshift.io/kube-apiserver-gomemlimit"

	// KubeAPIServerMaximumRequestsInFlight allows overriding the default value for the kube-apiserver max-requests-inflight
	// flag. This allows controlling how many concurrent requests can be handled by the Kube API server at any given time.
	KubeAPIServerMaximumRequestsInFlight = "hypershift.openshift.io/kube-apiserver-max-requests-inflight"

	// KubeAPIServerMaximumMutatingRequestsInFlight allows overring the default value for the kube-apiserver max-mutating-requests-inflight
	// flag. This allows controlling how many mutating concurrent requests can be handled by the Kube API server at any given time.
	KubeAPIServerMaximumMutatingRequestsInFlight = "hypershift.openshift.io/kube-apiserver-max-mutating-requests-inflight"

	// AWSLoadBalancerSubnetsAnnotation allows specifying the subnets to use for control plane load balancers
	// in the AWS platform.
	AWSLoadBalancerSubnetsAnnotation = "hypershift.openshift.io/aws-load-balancer-subnets"

	// DisableClusterAutoscalerAnnotation allows disabling the cluster autoscaler for a hosted cluster.
	// This annotation is only set by the hypershift-operator on HosterControlPlanes.
	// It is not set by the end-user.
	DisableClusterAutoscalerAnnotation = "hypershift.openshift.io/disable-cluster-autoscaler"

	// AroHCP represents the ARO HCP managed service offering
	AroHCP = "ARO-HCP"

	// HostedClusterSizeLabel is a label on HostedClusters indicating a size based on the number of nodes.
	HostedClusterSizeLabel = "hypershift.openshift.io/hosted-cluster-size"

	// NodeSizeLabel is a label on nodes used to match cluster size to a node size.
	NodeSizeLabel = "hypershift.openshift.io/cluster-size"

	// ManagementPlatformAnnotation specifies the infrastructure platform of the underlying management cluster
	ManagementPlatformAnnotation = "hypershift.openshift.io/management-platform"

	// MachineHealthCheckTimeoutAnnotation allows overriding the default machine health check timeout for
	// nodepools. The annotation can be set in either the HostedCluster or the NodePool. If set on both, the
	// one on the NodePool takes precedence. The value is a go duration string with a number and a unit (ie. 8m, 1h, etc)
	MachineHealthCheckTimeoutAnnotation = "hypershift.openshift.io/machine-health-check-timeout"

	// MachineHealthCheckNodeStartupTimeoutAnnotation allows overriding the default machine health check timeout for
	// node startup on nodepools. The annotation can be set in either the HostedCluster or the NodePool. If set on both, the
	// one on the NodePool takes precedence. The value is a go duration string with a number and a unit (ie. 8m, 1h, etc)
	MachineHealthCheckNodeStartupTimeoutAnnotation = "hypershift.openshift.io/machine-health-check-node-startup-timeout"

	// MachineHealthCheckMaxUnhealthyAnnotation allows overriding the max unhealthy value of the machine
	// health check created for a NodePool. The annotation can be set in either the HostedCluster or the NodePool.
	// If set on both, the one on the NodePool takes precedence. The value can be a number or a percentage value.
	MachineHealthCheckMaxUnhealthyAnnotation = "hypershift.openshift.io/machine-health-check-max-unhealthy"

	// ClusterSizeOverrideAnnotation allows overriding the value of the size label regardless of the number
	// of workers associated with the HostedCluster. The value should be the desired size label.
	ClusterSizeOverrideAnnotation = "hypershift.openshift.io/cluster-size-override"

	// KubeAPIServerVerbosityLevelAnnotation allows specifying the log verbosity of kube-apiserver.
	KubeAPIServerVerbosityLevelAnnotation = "hypershift.openshift.io/kube-apiserver-verbosity-level"

	// NodePoolSupportsKubevirtTopologySpreadConstraintsAnnotation indicates if the NodePool currently supports
	// using TopologySpreadConstraints on the KubeVirt VMs.
	//
	// Newer versions of the NodePool controller transitioned to spreading VMs across the cluster
	// using TopologySpreadConstraints instead of Pod Anti-Affinity. When the new controller interacts
	// with a older NodePool that was previously using pod anti-affinity, we don't want to immediately
	// start using TopologySpreadConstraints because it will cause the MachineSet controller to update
	// and replace all existing VMs. For example, it would be unexpected for a user to update the
	// NodePool controller and for that to trigger a rolling update of all KubeVirt VMs.
	//
	// This annotation signals to the NodePool controller that it is safe to use TopologySpreadConstraints on a NodePool
	// without triggering an unexpected update of KubeVirt VMs.
	NodePoolSupportsKubevirtTopologySpreadConstraintsAnnotation = "hypershift.openshift.io/nodepool-supports-kubevirt-topology-spread-constraints"

	// IsKubeVirtRHCOSVolumeLabelName labels rhcos DataVolumes and PVCs, to be able to filter them, e.g. for backup
	IsKubeVirtRHCOSVolumeLabelName = "hypershift.openshift.io/is-kubevirt-rhcos"

	// SkipControlPlaneNamespaceDeletionAnnotation tells the the hosted cluster controller not to delete the hosted control plane
	// namespace during hosted cluster deletion when this annotation is set to the value "true".
	SkipControlPlaneNamespaceDeletionAnnotation = "hypershift.openshift.io/skip-delete-hosted-controlplane-namespace"

	// DisableIgnitionServerAnnotation controls skipping of the ignition server deployment.
	DisableIgnitionServerAnnotation = "hypershift.openshift.io/disable-ignition-server"

	// ControlPlaneOperatorV2Annotation tells the hosted cluster to set 'CPO_V2' env variable on the CPO deployment which enables
	// the new manifest based CPO implementation.
	ControlPlaneOperatorV2Annotation = "hypershift.openshift.io/cpo-v2"

	// ControlPlaneOperatorV2EnvVar when set on the CPO deployment, enables the new manifest based CPO implementation.
	ControlPlaneOperatorV2EnvVar = "CPO_V2"

	// AWSMachinePublicIPs, if set to "true", results in an AWS machine template that creates machines with public IPs
	// WARNING: This option is for development and testing purposes only
	AWSMachinePublicIPs = "hypershift.openshift.io/aws-machine-public-ips"
)

// HostedClusterSpec is the desired behavior of a HostedCluster.

// +kubebuilder:validation:XValidation:rule=`self.platform.type != "IBMCloud" ? self.services == oldSelf.services : true`, message="Services is immutable. Changes might result in unpredictable and disruptive behavior."
// +kubebuilder:validation:XValidation:rule=`self.platform.type == "Azure" ? self.services.exists(s, s.service == "APIServer" && s.servicePublishingStrategy.type == "Route" && s.servicePublishingStrategy.route.hostname != "") : true`,message="Azure platform requires APIServer Route service with a hostname to be defined"
// +kubebuilder:validation:XValidation:rule=`self.platform.type == "Azure" ? self.services.exists(s, s.service == "OAuthServer" && s.servicePublishingStrategy.type == "Route" && s.servicePublishingStrategy.route.hostname != "") : true`,message="Azure platform requires OAuthServer Route service with a hostname to be defined"
// +kubebuilder:validation:XValidation:rule=`self.platform.type == "Azure" ? self.services.exists(s, s.service == "Konnectivity" && s.servicePublishingStrategy.type == "Route" && s.servicePublishingStrategy.route.hostname != "") : true`,message="Azure platform requires Konnectivity Route service with a hostname to be defined"
// +kubebuilder:validation:XValidation:rule=`self.platform.type == "Azure" ? self.services.exists(s, s.service == "Ignition" && s.servicePublishingStrategy.type == "Route" && s.servicePublishingStrategy.route.hostname != "") : true`,message="Azure platform requires Ignition Route service with a hostname to be defined"
// +kubebuilder:validation:XValidation:rule=`has(self.issuerURL) || !has(self.serviceAccountSigningKey)`,message="If serviceAccountSigningKey is set, issuerURL must be set"
// TODO(alberto): Use CEL cidr library for all these validation when all management clusters are >= 1.31.
// TODO(alberto): Move this down to the networking section when IBMCloud has finished valid input migration.
// +kubebuilder:validation:XValidation:rule=`(self.platform.type == "IBMCloud" || !has(self.networking.machineNetwork) && self.networking.clusterNetwork.all(c, self.networking.serviceNetwork.all(s, c.cidr != s.cidr)) || (has(self.networking.machineNetwork) && (self.networking.machineNetwork.all(m, self.networking.clusterNetwork.all(c, m.cidr != c.cidr)) && self.networking.machineNetwork.all(m, self.networking.serviceNetwork.all(s, m.cidr != s.cidr)) && self.networking.clusterNetwork.all(c, self.networking.serviceNetwork.all(s, c.cidr != s.cidr)))))`,message="CIDR ranges in machineNetwork, clusterNetwork, and serviceNetwork must be unique and non-overlapping"
type HostedClusterSpec struct {
	// release specifies the desired OCP release payload for all the hosted cluster components.
	// This includes those components running management side like the Kube API Server and the CVO but also the operands which land in the hosted cluster data plane like the ingress controller, ovn agents, etc.
	// The maximum and minimum supported release versions are determined by the running Hypersfhit Operator.
	// Attempting to use an unsupported version will result in the HostedCluster being degraded and the validateReleaseImage condition being false.
	// Attempting to use a release with a skew against a NodePool release bigger than N-2 for the y-stream will result in leaving the NodePool in an unsupported state.
	// Changing this field will trigger a rollout of the control plane components.
	// The behavior of the rollout will be driven by the ControllerAvailabilityPolicy and InfrastructureAvailabilityPolicy for PDBs and maxUnavailable and surce policies.
	// +required
	Release Release `json:"release"`

	// controlPlaneRelease is like spec.release but only for the components running on the management cluster.
	// This excludes any operand which will land in the hosted cluster data plane.
	// It is useful when you need to apply patch management side like a CVE, transparently for the hosted cluster.
	// Version input for this field is free, no validation is performed against spec.release or maximum and minimum is performed.
	// If defined, it will dicate the version of the components running management side, while spec.release will dictate the version of the components landing in the hosted cluster data plane.
	// If not defined, spec.release is used for both.
	// Changing this field will trigger a rollout of the control plane.
	// The behavior of the rollout will be driven by the ControllerAvailabilityPolicy and InfrastructureAvailabilityPolicy for PDBs and maxUnavailable and surce policies.
	// +optional
	ControlPlaneRelease *Release `json:"controlPlaneRelease,omitempty"`

	// clusterID uniquely identifies this cluster. This is expected to be an RFC4122 UUID value (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in hexadecimal digits).
	// As with a Kubernetes metadata.uid, this ID uniquely identifies this cluster in space and time.
	// This value identifies the cluster in metrics pushed to telemetry and metrics produced by the control plane operators.
	// If a value is not specified, a random clusterID will be generated and set by the controller.
	// Once set, this value is immutable.
	// +kubebuilder:validation:XValidation:rule="self.matches('[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}')",message="clusterID must be an RFC4122 UUID value (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in hexadecimal digits)"
	// +kubebuilder:validation:XValidation:rule=`oldSelf == "" || self == oldSelf`, message="clusterID is immutable"
	// +kubebuilder:validation:MaxLength=36
	// +kubebuilder:validation:MinLength=36
	// +optional
	ClusterID string `json:"clusterID,omitempty"`

	// infraID is a globally unique identifier for the cluster.
	// It must consist of lowercase alphanumeric characters and hyphens ('-') only, and start and end with an alphanumeric character.
	// It must be no more than 253 characters in length.
	// This identifier will be used to associate various cloud resources with the HostedCluster and its associated NodePools.
	// infraID is used to compute and tag created resources with "kubernetes.io/cluster/"+hcluster.Spec.InfraID which has contractual meaning for the cloud provider implementations.
	// If a value is not specified, a random infraID will be generated and set by the controller.
	// Once set, this value is immutable.
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="infraID must consist of lowercase alphanumeric characters or '-', start and end with an alphanumeric character, and be between 1 and 253 characters"
	// +kubebuilder:validation:XValidation:rule=`oldSelf == "" || self == oldSelf`, message="infraID is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +optional
	InfraID string `json:"infraID,omitempty"`

	// updateService may be used to specify the preferred upstream update service.
	// If omitted we will use the appropriate update service for the cluster and region.
	// This is used by the control plane operator to determine and signal the appropriate available upgrades in the hostedCluster.status.
	// +kubebuilder:validation:XValidation:rule="isURL(self)",message="updateService must be a valid absolute URL"
	// +optional
	UpdateService configv1.URL `json:"updateService,omitempty"`

	// channel is an identifier for explicitly requesting that a non-default set of updates be applied to this cluster.
	// If omitted no particular upgrades are suggested.
	// TODO(alberto): Consider the backend to use the default channel by default. Default channel will contain stable updates that are appropriate for production clusters.
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:MinLength=1
	// +optional
	Channel string `json:"channel,omitempty"`

	// platform specifies the underlying infrastructure provider for the cluster
	// and is used to configure platform specific behavior.
	// +required
	Platform PlatformSpec `json:"platform"`

	// controllerAvailabilityPolicy specifies the availability policy applied to critical control plane components like the Kube API Server.
	// Possible values are HighlyAvailable and SingleReplica. The default value is HighlyAvailable.
	// +optional
	// +kubebuilder:default:="HighlyAvailable"
	ControllerAvailabilityPolicy AvailabilityPolicy `json:"controllerAvailabilityPolicy,omitempty"`

	// infrastructureAvailabilityPolicy specifies the availability policy applied to infrastructure services which run on the hosted cluster data plane like the ingress controller and image registry controller.
	// Possible values are HighlyAvailable and SingleReplica. The default value is SingleReplica.
	// +optional
	// +kubebuilder:default:="SingleReplica"
	InfrastructureAvailabilityPolicy AvailabilityPolicy `json:"infrastructureAvailabilityPolicy,omitempty"`

	// dns specifies the DNS configuration for the hosted cluster ingress.
	// +optional
	DNS DNSSpec `json:"dns,omitempty"`

	// networking specifies network configuration for the hosted cluster.
	// Defaults to OVNKubernetes with a cluster network of cidr: "10.132.0.0/14" and a service network of cidr: "172.31.0.0/16".
	// +required
	// +kubebuilder:default={networkType: "OVNKubernetes", clusterNetwork: {{cidr: "10.132.0.0/14"}}, serviceNetwork: {{cidr: "172.31.0.0/16"}}}
	Networking ClusterNetworking `json:"networking"`

	// autoscaling specifies auto-scaling behavior that applies to all NodePools
	// associated with this HostedCluster.
	//
	// +optional
	Autoscaling ClusterAutoscaling `json:"autoscaling,omitempty"`

	// autoNode specifies the configuration for the autoNode feature.
	// +openshift:enable:FeatureGate=AutoNodeKarpenter
	AutoNode *AutoNode `json:"autoNode,omitempty"`

	// etcd specifies configuration for the control plane etcd cluster. The
	// default managementType is Managed. Once set, the managementType cannot be
	// changed.
	//
	// +kubebuilder:default={managementType: "Managed", managed: {storage: {type: "PersistentVolume", persistentVolume: {size: "8Gi"}}}}
	// +required
	// +immutable
	Etcd EtcdSpec `json:"etcd"`

	// services specifies how individual control plane services endpoints are published for consumption.
	// This requires APIServer;OAuthServer;Konnectivity;Ignition.
	// This field is immutable for all platforms but IBMCloud.
	// Max is 6 to account for OIDC;OVNSbDb for backward compatibility though they are no-op.
	//
	// +kubebuilder:validation:MaxItems=6
	// +kubebuilder:validation:MinItems=4
	// +kubebuilder:validation:ListType=atomic
	// -kubebuilder:validation:XValidation:rule="self.all(s, !(s.service == 'APIServer' && s.servicePublishingStrategy.type == 'Route') || has(s.servicePublishingStrategy.route.hostname))",message="If serviceType is 'APIServer' and publishing strategy is 'Route', then hostname must be set"
	// -kubebuilder:validation:XValidation:rule="['APIServer', 'OAuthServer', 'Konnectivity', 'Ignition'].all(requiredType, self.exists(s, s.service == requiredType))",message="Services list must contain at least 'APIServer', 'OAuthServer', 'Konnectivity', and 'Ignition' service types"
	// -kubebuilder:validation:XValidation:rule="self.filter(s, s.servicePublishingStrategy.type == 'Route' && has(s.servicePublishingStrategy.route) && has(s.servicePublishingStrategy.route.hostname)).all(x, self.filter(y, y.servicePublishingStrategy.type == 'Route' && (has(y.servicePublishingStrategy.route) && has(y.servicePublishingStrategy.route.hostname) && y.servicePublishingStrategy.route.hostname == x.servicePublishingStrategy.route.hostname)).size() <= 1)",message="Each route publishingStrategy 'hostname' must be unique within the Services list."
	// -kubebuilder:validation:XValidation:rule="self.filter(s, s.servicePublishingStrategy.type == 'NodePort' && has(s.servicePublishingStrategy.nodePort) && has(s.servicePublishingStrategy.nodePort.address) && has(s.servicePublishingStrategy.nodePort.port)).all(x, self.filter(y, y.servicePublishingStrategy.type == 'NodePort' && (has(y.servicePublishingStrategy.nodePort) && has(y.servicePublishingStrategy.nodePort.address) && y.servicePublishingStrategy.nodePort.address == x.servicePublishingStrategy.nodePort.address && has(y.servicePublishingStrategy.nodePort.port) && y.servicePublishingStrategy.nodePort.port == x.servicePublishingStrategy.nodePort.port )).size() <= 1)",message="Each nodePort publishingStrategy 'nodePort' and 'hostname' must be unique within the Services list."
	// TODO(alberto): this breaks the cost budget for < 4.17. We should figure why and enable it back. And If not fixable, consider imposing a minimum version on the management cluster.
	// +required
	// +immutable
	Services []ServicePublishingStrategyMapping `json:"services"`

	// pullSecret is a local reference to a Secret that must have a ".dockerconfigjson" key whose content must be a valid Openshift pull secret JSON.
	// If the reference is set but none of the above requirements are met, the HostedCluster will enter a degraded state.
	// TODO(alberto): Signal this in a condition.
	// This pull secret will be part of every payload generated by the controllers for any NodePool of the HostedCluster
	// and it will be injected into the container runtime of all NodePools.
	// Changing this value will trigger a rollout for all existing NodePools in the cluster.
	// Changing the content of the secret inplace will not trigger a rollout and might result in unpredictable behaviour.
	// +required
	// +rollout
	// TODO(alberto): have our own local reference type to include our opinions and avoid transparent changes.
	PullSecret corev1.LocalObjectReference `json:"pullSecret"`

	// sshKey is a local reference to a Secret that must have a "id_rsa.pub" key whose content must be the public part of 1..N SSH keys.
	// If the reference is set but none of the above requirements are met, the HostedCluster will enter a degraded state.
	// TODO(alberto): Signal this in a condition.
	// When sshKey is set, the controllers will generate a machineConfig with the sshAuthorizedKeys https://coreos.github.io/ignition/configuration-v3_2/ populated with this value.
	// This MachineConfig will be part of every payload generated by the controllers for any NodePool of the HostedCluster.
	// Changing this value will trigger a rollout for all existing NodePools in the cluster.
	// +rollout
	// +optional
	SSHKey corev1.LocalObjectReference `json:"sshKey"`

	// issuerURL is an OIDC issuer URL which will be used as the issuer in all
	// ServiceAccount tokens generated by the control plane API server via --service-account-issuer kube api server flag.
	// https://k8s-docs.netlify.app/en/docs/reference/command-line-tools-reference/kube-apiserver/
	// https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#serviceaccount-token-volume-projection
	// The default value is kubernetes.default.svc, which only works for in-cluster
	// validation.
	// If the platform is AWS and this value is set, the controller will update an s3 object with the appropriate OIDC documents (using the serviceAccountSigningKey info) into that issuerURL.
	// The expectation is for this s3 url to be backed by an OIDC provider in the AWS IAM.
	// +kubebuilder:default:="https://kubernetes.default.svc"
	// +immutable
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="issuerURL is immutable"
	// +kubebuilder:validation:XValidation:rule="isURL(self)",message="issuerURL must be a valid absolute URL"
	IssuerURL string `json:"issuerURL,omitempty"`

	// serviceAccountSigningKey is a local reference to a secret that must have a "key" key whose content must be the private key
	// used by the service account token issuer.
	// If not specified, a service account signing key will
	// be generated automatically for the cluster.
	// When specifying a service account signing key, an IssuerURL must also be specified.
	// If the reference is set but none of the above requirements are met, the HostedCluster will enter a degraded state.
	// TODO(alberto): Signal this in a condition.
	//
	// +immutable
	// +optional
	ServiceAccountSigningKey *corev1.LocalObjectReference `json:"serviceAccountSigningKey,omitempty"`

	// Configuration specifies configuration for individual OCP components in the
	// cluster, represented as embedded resources that correspond to the openshift
	// configuration API.
	//
	// +optional
	Configuration *ClusterConfiguration `json:"configuration,omitempty"`

	// AuditWebhook contains metadata for configuring an audit webhook endpoint
	// for a cluster to process cluster audit events. It references a secret that
	// contains the webhook information for the audit webhook endpoint. It is a
	// secret because if the endpoint has mTLS the kubeconfig will contain client
	// keys. The kubeconfig needs to be stored in the secret with a secret key
	// name that corresponds to the constant AuditWebhookKubeconfigKey.
	//
	// +optional
	// +immutable
	AuditWebhook *corev1.LocalObjectReference `json:"auditWebhook,omitempty"`

	// imageContentSources specifies image mirrors that can be used by cluster
	// nodes to pull content.
	// When imageContentSources is set, the controllers will generate a machineConfig.
	// This MachineConfig will be part of every payload generated by the controllers for any NodePool of the HostedCluster.
	// Changing this value will trigger a rollout for all existing NodePools in the cluster.
	// +optional
	ImageContentSources []ImageContentSource `json:"imageContentSources,omitempty"`

	// additionalTrustBundle is a local reference to a ConfigMap that must have a "ca-bundle.crt" key
	// whose content must be a PEM-encoded X.509 certificate bundle that will be added to the hosted controlplane and nodes
	// If the reference is set but none of the above requirements are met, the HostedCluster will enter a degraded state.
	// TODO(alberto): Signal this in a condition.
	// This will be part of every payload generated by the controllers for any NodePool of the HostedCluster.
	// Changing this value will trigger a rollout for all existing NodePools in the cluster.
	// +optional
	AdditionalTrustBundle *corev1.LocalObjectReference `json:"additionalTrustBundle,omitempty"`

	// secretEncryption specifies a Kubernetes secret encryption strategy for the
	// control plane.
	//
	// +optional
	SecretEncryption *SecretEncryptionSpec `json:"secretEncryption,omitempty"`

	// fips indicates whether this cluster's nodes will be running in FIPS mode.
	// If set to true, the control plane's ignition server will be configured to
	// expect that nodes joining the cluster will be FIPS-enabled.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="fips is immutable"
	// +optional
	// +immutable
	FIPS bool `json:"fips"`

	// pausedUntil is a field that can be used to pause reconciliation on the HostedCluster controller, resulting in any change to the HostedCluster being ignored.
	// Either a date can be provided in RFC3339 format or a boolean as in 'true', 'false', 'True', 'False'. If a date is
	// provided: reconciliation is paused on the resource until that date. If the boolean true is
	// provided: reconciliation is paused on the resource until the field is removed.
	// +kubebuilder:validation:MaxLength=35
	// +kubebuilder:validation:MinLength=4
	// +kubebuilder:validation:XValidation:rule=`self.matches('^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}.*$') || self in ['true', 'false', 'True', 'False']`,message="PausedUntil must be a date in RFC3339 format or 'True', 'true', 'False' or 'false'"
	// +optional
	PausedUntil *string `json:"pausedUntil,omitempty"`

	// OLMCatalogPlacement specifies the placement of OLM catalog components. By default,
	// this is set to management and OLM catalog components are deployed onto the management
	// cluster. If set to guest, the OLM catalog components will be deployed onto the guest
	// cluster.
	//
	// +kubebuilder:default=management
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="OLMCatalogPlacement is immutable"
	// +optional
	// +immutable
	OLMCatalogPlacement OLMCatalogPlacement `json:"olmCatalogPlacement,omitempty"`

	// NodeSelector when specified, is propagated to all control plane Deployments and Stateful sets running management side.
	// It must be satisfied by the management Nodes for the pods to be scheduled. Otherwise the HostedCluster will enter a degraded state.
	// Changes to this field will propagate to existing Deployments and StatefulSets.
	// +kubebuilder:validation:XValidation:rule="size(self) <= 20",message="nodeSelector map can have at most 20 entries"
	// TODO(alberto): add additional validation for the map key/values.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations when specified, define what custom tolerations are added to the hcp pods.
	//
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// labels when specified, define what custom labels are added to the hcp pods.
	// Changing this day 2 will cause a rollout of all hcp pods.
	// Duplicate keys are not supported. If duplicate keys are defined, only the last key/value pair is preserved.
	// Valid values are those in https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	//
	// -kubebuilder:validation:XValidation:rule=`self.all(key, size(key) <= 317 && key.matches('^(([A-Za-z0-9]+(\\.[A-Za-z0-9]+)?)*[A-Za-z0-9]\\/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$'))`, message="label key must have two segments: an optional prefix and name, separated by a slash (/). The name segment is required and must be 63 characters or less, beginning and ending with an alphanumeric character ([a-z0-9A-Z]) with dashes (-), underscores (_), dots (.), and alphanumerics between. The prefix is optional. If specified, the prefix must be a DNS subdomain: a series of DNS labels separated by dots (.), not longer than 253 characters in total, followed by a slash (/)"
	// -kubebuilder:validation:XValidation:rule=`self.all(key, size(self[key]) <= 63 && self[key].matches('^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$'))`, message="label value must be 63 characters or less (can be empty), consist of alphanumeric characters, dashes (-), underscores (_) or dots (.), and begin and end with an alphanumeric character"
	// TODO: key/value validations break cost budget for <=4.17. We should figure why and enable it back.
	// +kubebuilder:validation:MaxProperties=20
	// +optional
	// +openshift:enable:FeatureGate=HCPPodsLabels
	Labels map[string]string `json:"labels,omitempty"`
}

// OLMCatalogPlacement is an enum specifying the placement of OLM catalog components.
// +kubebuilder:validation:Enum=management;guest
type OLMCatalogPlacement string

const (
	// ManagementOLMCatalogPlacement indicates OLM catalog components will be placed in
	// the management cluster.
	ManagementOLMCatalogPlacement OLMCatalogPlacement = "management"

	// GuestOLMCatalogPlacement indicates OLM catalog components will be placed in
	// the guest cluster.
	GuestOLMCatalogPlacement OLMCatalogPlacement = "guest"
)

func (olm *OLMCatalogPlacement) String() string {
	return string(*olm)
}

func (olm *OLMCatalogPlacement) Set(s string) error {
	switch strings.ToLower(s) {
	case "guest":
		*olm = GuestOLMCatalogPlacement
	case "management":
		*olm = ManagementOLMCatalogPlacement
	default:
		return fmt.Errorf("unknown OLMCatalogPlacement type used '%s'", s)
	}
	return nil
}

func (olm *OLMCatalogPlacement) Type() string {
	return "OLMCatalogPlacement"
}

// ImageContentSource specifies image mirrors that can be used by cluster nodes
// to pull content. For cluster workloads, if a container image registry host of
// the pullspec matches Source then one of the Mirrors are substituted as hosts
// in the pullspec and tried in order to fetch the image.
type ImageContentSource struct {
	// Source is the repository that users refer to, e.g. in image pull
	// specifications.
	//
	// +immutable
	Source string `json:"source"`

	// Mirrors are one or more repositories that may also contain the same images.
	//
	// +optional
	// +immutable
	Mirrors []string `json:"mirrors,omitempty"`
}

// ServicePublishingStrategyMapping specifies how individual control plane services endpoints are published for consumption.
// This includes APIServer;OAuthServer;Konnectivity;Ignition.
// If a given service is not present in this list, it will be exposed publicly by default.
type ServicePublishingStrategyMapping struct {
	// service identifies the type of service being published.
	// It can be APIServer;OAuthServer;Konnectivity;Ignition
	// OVNSbDb;OIDC are no-op and kept for backward compatibility.
	// This field is immutable.
	//
	// +kubebuilder:validation:Enum=APIServer;OAuthServer;OIDC;Konnectivity;Ignition;OVNSbDb
	// +immutable
	// +required
	Service ServiceType `json:"service"`

	// servicePublishingStrategy specifies how to publish a service endpoint.
	// +required
	ServicePublishingStrategy `json:"servicePublishingStrategy"`
}

// ServicePublishingStrategy specifies how to publish a ServiceType.
// +kubebuilder:validation:XValidation:rule="self.type == 'NodePort' ? has(self.nodePort) : !has(self.nodePort)",message="nodePort is required when type is NodePort, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'Route' ? !has(self.nodePort) && !has(self.loadBalancer) : !has(self.route)",message="only route is allowed when type is Route, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'LoadBalancer' ? !has(self.nodePort) && !has(self.route) : !has(self.loadBalancer)",message="only loadBalancer is required when type is LoadBalancer, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'None' ? !has(self.nodePort) && !has(self.route) && !has(self.loadBalancer) : true",message="None does not allowed any configuration for loadBalancer, nodePort, or route"
// +kubebuilder:validation:XValidation:rule="self.type == 'S3' ? !has(self.nodePort) && !has(self.route) && !has(self.loadBalancer) : true",message="S3 does not allowed any configuration for loadBalancer, nodePort, or route"

type ServicePublishingStrategy struct {
	// type is the publishing strategy used for the service.
	// It can be LoadBalancer;NodePort;Route;None;S3
	//
	// +kubebuilder:validation:Enum=LoadBalancer;NodePort;Route;None;S3
	// +required
	Type PublishingStrategyType `json:"type"`

	// nodePort configures exposing a service using a NodePort.
	// +optional
	NodePort *NodePortPublishingStrategy `json:"nodePort,omitempty"`

	// loadBalancer configures exposing a service using a dedicated LoadBalancer.
	// +optional
	LoadBalancer *LoadBalancerPublishingStrategy `json:"loadBalancer,omitempty"`

	// route configures exposing a service using a Route through and an ingress controller behind a cloud Load Balancer.
	// The specifics of the setup are platform dependent.
	// +optional
	Route *RoutePublishingStrategy `json:"route,omitempty"`
}

// PublishingStrategyType defines publishing strategies for services.
type PublishingStrategyType string

var (
	// LoadBalancer exposes a service with a LoadBalancer kube service.
	LoadBalancer PublishingStrategyType = "LoadBalancer"
	// NodePort exposes a service with a NodePort kube service.
	NodePort PublishingStrategyType = "NodePort"
	// Route exposes services with a Route + ClusterIP kube service.
	Route PublishingStrategyType = "Route"
	// S3 exposes a service through an S3 bucket
	S3 PublishingStrategyType = "S3"
	// None disables exposing the service
	None PublishingStrategyType = "None"
)

// ServiceType defines what control plane services can be exposed from the
// management control plane.
type ServiceType string

var (
	// APIServer is the control plane API server.
	APIServer ServiceType = "APIServer"

	// Konnectivity is the control plane Konnectivity networking service.
	Konnectivity ServiceType = "Konnectivity"

	// OAuthServer is the control plane OAuth service.
	OAuthServer ServiceType = "OAuthServer"

	// Ignition is the control plane ignition service for nodes.
	Ignition ServiceType = "Ignition"

	// OVNSbDb is the optional control plane ovn southbound database service used by OVNKubernetes CNI.
	// Deprecated: This service is no longer used by OVNKubernetes CNI for >= 4.14.
	OVNSbDb ServiceType = "OVNSbDb"

	// OIDC is the control plane OIDC service.
	// Deprecated: This service is no longer used by the control plane.
	OIDC ServiceType = "OIDC"
)

// NodePortPublishingStrategy specifies a NodePort used to expose a service.
type NodePortPublishingStrategy struct {
	// address is the host/ip that the NodePort service is exposed over.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule=`isIP(self) || self.matches('^(([a-zA-Z0-9][-a-zA-Z0-9]*\\.)+[a-zA-Z]{2,}|localhost)$') || self.matches('^((\\d{1,3}\\.){3}\\d{1,3})$') || self.matches('^([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}$')`, message="address must be a valid hostname, IPv4, or IPv6 address"
	// +required
	Address string `json:"address"`

	// port is the port of the NodePort service. If <=0, the port is dynamically
	// assigned when the service is created.
	Port int32 `json:"port,omitempty"`
}

// LoadBalancerPublishingStrategy specifies setting used to expose a service as a LoadBalancer.
type LoadBalancerPublishingStrategy struct {
	// hostname is the name of the DNS record that will be created pointing to the LoadBalancer and passed through to consumers of the service.
	// If omitted, the value will be inferred from the corev1.Service Load balancer type .status.
	// +kubebuilder:validation:XValidation:rule=`self.matches('^(?:[a-zA-Z0-9-]+\\.)+[a-zA-Z]{2,}$')`,message="hostname must be a valid domain name (e.g., example.com)"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +optional
	Hostname string `json:"hostname,omitempty"`
}

// RoutePublishingStrategy specifies options for exposing a service as a Route.
type RoutePublishingStrategy struct {
	// Hostname is the name of the DNS record that will be created pointing to the Route and passed through to consumers of the service.
	// If omitted, the value will be inferred from management ingress.Spec.Domain.
	// +kubebuilder:validation:XValidation:rule=`self.matches('^(?:[a-zA-Z0-9-]+\\.)+[a-zA-Z]{2,}$')`,message="hostname must be a valid domain name (e.g., example.com)"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +optional
	Hostname string `json:"hostname,omitempty"`
}

// DNSSpec specifies the DNS configuration for the hosted cluster ingress.
type DNSSpec struct {
	// baseDomain is the base domain of the hosted cluster.
	// It will be used to configure ingress in the hosted cluster through the subdomain baseDomainPrefix.baseDomain.
	// If baseDomainPrefix is omitted, the hostedCluster.name will be used as the subdomain.
	// Once set, this field is immutable.
	// When the value is the empty string "", the controller might default to a value depending on the platform.
	// +kubebuilder:validation:XValidation:rule=`self == "" || self.matches('^(?:(?:[a-zA-Z0-9-]+\\.)+[a-zA-Z]{2,}|[a-zA-Z0-9-]+)$')`,message="baseDomain must be a valid domain name (e.g., example, example.com, sub.example.com)"
	// +kubebuilder:validation:XValidation:rule=`oldSelf == "" || self == oldSelf`, message="baseDomain is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +immutable
	// +required
	BaseDomain string `json:"baseDomain"`

	// baseDomainPrefix is the base domain prefix for the hosted cluster ingress.
	// It will be used to configure ingress in the hosted cluster through the subdomain baseDomainPrefix.baseDomain.
	// If baseDomainPrefix is omitted, the hostedCluster.name will be used as the subdomain.
	// Set baseDomainPrefix to an empty string "", if you don't want a prefix at all (not even hostedCluster.name) to be prepended to baseDomain.
	// +kubebuilder:validation:XValidation:rule=`self == "" || self.matches('^(?:(?:[a-zA-Z0-9-]+\\.)+[a-zA-Z]{2,}|[a-zA-Z0-9-]+)$')`,message="baseDomainPrefix must be a valid domain name (e.g., example, example.com, sub.example.com)"
	// This field is immutable.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="baseDomainPrefix is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +optional
	BaseDomainPrefix *string `json:"baseDomainPrefix,omitempty"`

	// publicZoneID is the Hosted Zone ID where all the DNS records that are publicly accessible to the internet exist.
	// This field is optional and mainly leveraged in cloud environments where the DNS records for the .baseDomain are created by controllers in this zone.
	// Once set, this value is immutable.
	// +optional
	// +kubebuilder:validation:XValidation:rule=`oldSelf == "" || self == oldSelf`, message="publicZoneID is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +immutable
	PublicZoneID string `json:"publicZoneID,omitempty"`

	// privateZoneID is the Hosted Zone ID where all the DNS records that are only available internally to the cluster exist.
	// This field is optional and mainly leveraged in cloud environments where the DNS records for the .baseDomain are created by controllers in this zone.
	// Once set, this value is immutable.
	// +optional
	// +kubebuilder:validation:XValidation:rule=`oldSelf == "" || self == oldSelf`, message="privateZoneID is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +immutable
	PrivateZoneID string `json:"privateZoneID,omitempty"`
}

// clusterNetworking specifies network configuration for a cluster.
// All CIDRs must be unique. Additional validation to check for CIDRs overlap and consistent network stack is performed by the controllers.
// Failing that validation will result in the HostedCluster being degraded and the validConfiguration condition being false.
type ClusterNetworking struct {
	// machineNetwork is the list of IP address pools for machines.
	// This might be used among other things to generate appropriate networking security groups in some clouds providers.
	// Currently only one entry or two for dual stack is supported.
	// This field is immutable.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="machineNetwork is immutable and cannot be modified once set."
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:ListType=atomic
	// +immutable
	// +optional
	MachineNetwork []MachineNetworkEntry `json:"machineNetwork,omitempty"`

	// clusterNetwork is the list of IP address pools for pods.
	// Defaults to cidr: "10.132.0.0/14".
	// Currently only one entry is supported.
	// This field is immutable.
	// +immutable
	// +optional
	// +kubebuilder:default:={{cidr: "10.132.0.0/14"}}
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="clusterNetwork is immutable and cannot be modified once set."
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:MinItems=1
	ClusterNetwork []ClusterNetworkEntry `json:"clusterNetwork,omitempty"`

	// serviceNetwork is the list of IP address pools for services.
	// Defaults to cidr: "172.31.0.0/16".
	// Currently only one entry is supported.
	// This field is immutable.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="serviceNetwork is immutable and cannot be modified once set."
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:MinItems=1
	// +optional
	// +kubebuilder:default:={{cidr: "172.31.0.0/16"}}
	ServiceNetwork []ServiceNetworkEntry `json:"serviceNetwork,omitempty"`

	// networkType specifies the SDN provider used for cluster networking.
	// Defaults to OVNKubernetes.
	// This field is required and immutable.
	// kubebuilder:validation:XValidation:rule="self == oldSelf", message="networkType is immutable"
	// +optional
	// +kubebuilder:default:="OVNKubernetes"
	// +immutable
	NetworkType NetworkType `json:"networkType,omitempty"`

	// apiServer contains advanced network settings for the API server that affect
	// how the APIServer is exposed inside a hosted cluster node.
	//
	// +immutable
	APIServer *APIServerNetworking `json:"apiServer,omitempty"`
}

// MachineNetworkEntry is a single IP address block for node IP blocks.
type MachineNetworkEntry struct {
	// CIDR is the IP block address pool for machines within the cluster.
	CIDR ipnet.IPNet `json:"cidr"`
}

// ClusterNetworkEntry is a single IP address block for pod IP blocks. IP blocks
// are allocated with size 2^HostSubnetLength.
type ClusterNetworkEntry struct {
	// cidr is the IP block address pool.
	CIDR ipnet.IPNet `json:"cidr"`

	// hostPrefix is the prefix size to allocate to each node from the CIDR.
	// For example, 24 would allocate 2^(32-24)=2^8=256 addresses to each node. If this
	// field is not used by the plugin, it can be left unset.
	// +optional
	HostPrefix int32 `json:"hostPrefix,omitempty"`
}

// ServiceNetworkEntry is a single IP address block for the service network.
type ServiceNetworkEntry struct {
	// cidr is the IP block address pool for services within the cluster in CIDR format (e.g., 192.168.1.0/24 or 2001:0db8::/64)
	CIDR ipnet.IPNet `json:"cidr"`
}

// +kubebuilder:validation:Pattern:=`^(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])(\/(3[0-2]|[1-2][0-9]|[0-9]))$`
type CIDRBlock string

// APIServerNetworking specifies how the APIServer is exposed inside a cluster
// node.
type APIServerNetworking struct {
	// advertiseAddress is the address that pods within the nodes will use to talk to the API
	// server. This is an address associated with the loopback adapter of each
	// node. If not specified, the controller will take default values.
	// The default values will be set as 172.20.0.1 or fd00::1.
	// This value is immutable.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="advertiseAddress is immutable"
	// +optional
	AdvertiseAddress *string `json:"advertiseAddress,omitempty"`

	// port is the port at which the APIServer is exposed inside a node. Other
	// pods using host networking cannot listen on this port.
	// If omitted 6443 is used.
	// This is useful to choose a port other than the default one which might interfere with customer environments e.g. https://github.com/openshift/hypershift/pull/356.
	// Setting this to 443 is possible only for backward compatibility reasons and it's discouraged.
	// Doing so, it would result in the controller overriding the KAS endpoint in the guest cluster having a discrepancy with the KAS Pod and potentially causing temporarily network failures.
	// This value is immutable.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="port is immutable"
	// +optional
	Port *int32 `json:"port,omitempty"`

	// allowedCIDRBlocks is an allow list of CIDR blocks that can access the APIServer
	// If not specified, traffic is allowed from all addresses.
	// This depends on underlying support by the cloud provider for Service LoadBalancerSourceRanges
	AllowedCIDRBlocks []CIDRBlock `json:"allowedCIDRBlocks,omitempty"`
}

// NetworkType specifies the SDN provider used for cluster networking.
//
// +kubebuilder:validation:Enum=OpenShiftSDN;Calico;OVNKubernetes;Other
type NetworkType string

const (
	// OpenShiftSDN specifies OpenShiftSDN as the SDN provider
	OpenShiftSDN NetworkType = "OpenShiftSDN"

	// Calico specifies Calico as the SDN provider
	Calico NetworkType = "Calico"

	// OVNKubernetes specifies OVN as the SDN provider
	OVNKubernetes NetworkType = "OVNKubernetes"

	// Other specifies an undefined SDN provider
	Other NetworkType = "Other"
)

// PlatformType is a specific supported infrastructure provider.
type PlatformType string

const (
	// AWSPlatform represents Amazon Web Services infrastructure.
	AWSPlatform PlatformType = "AWS"

	// NonePlatform represents user supplied (e.g. bare metal) infrastructure.
	NonePlatform PlatformType = "None"

	// IBMCloudPlatform represents IBM Cloud infrastructure.
	IBMCloudPlatform PlatformType = "IBMCloud"

	// AgentPlatform represents user supplied insfrastructure booted with agents.
	AgentPlatform PlatformType = "Agent"

	// KubevirtPlatform represents Kubevirt infrastructure.
	KubevirtPlatform PlatformType = "KubeVirt"

	// AzurePlatform represents Azure infrastructure.
	AzurePlatform PlatformType = "Azure"

	// PowerVSPlatform represents PowerVS infrastructure.
	PowerVSPlatform PlatformType = "PowerVS"

	// OpenStackPlatform represents OpenStack infrastructure.
	OpenStackPlatform PlatformType = "OpenStack"
)

// List all PlatformType instances
func PlatformTypes() []PlatformType {
	return []PlatformType{
		AWSPlatform,
		NonePlatform,
		IBMCloudPlatform,
		AgentPlatform,
		KubevirtPlatform,
		AzurePlatform,
		PowerVSPlatform,
		OpenStackPlatform,
	}
}

// PlatformSpec specifies the underlying infrastructure provider for the cluster
// and is used to configure platform specific behavior.
type PlatformSpec struct {
	// Type is the type of infrastructure provider for the cluster.
	//
	// +unionDiscriminator
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="Type is immutable"
	// +immutable
	// +openshift:validation:FeatureGateAwareEnum:featureGate="",enum=AWS;Azure;IBMCloud;KubeVirt;Agent;PowerVS;None
	// +openshift:validation:FeatureGateAwareEnum:featureGate=OpenStack,enum=AWS;Azure;IBMCloud;KubeVirt;Agent;PowerVS;None;OpenStack
	Type PlatformType `json:"type"`

	// AWS specifies configuration for clusters running on Amazon Web Services.
	//
	// +optional
	// +immutable
	AWS *AWSPlatformSpec `json:"aws,omitempty"`

	// Agent specifies configuration for agent-based installations.
	//
	// +optional
	// +immutable
	Agent *AgentPlatformSpec `json:"agent,omitempty"`

	// IBMCloud defines IBMCloud specific settings for components
	IBMCloud *IBMCloudPlatformSpec `json:"ibmcloud,omitempty"`

	// Azure defines azure specific settings
	Azure *AzurePlatformSpec `json:"azure,omitempty"`

	// PowerVS specifies configuration for clusters running on IBMCloud Power VS Service.
	// This field is immutable. Once set, It can't be changed.
	//
	// +optional
	// +immutable
	PowerVS *PowerVSPlatformSpec `json:"powervs,omitempty"`

	// KubeVirt defines KubeVirt specific settings for cluster components.
	//
	// +optional
	// +immutable
	Kubevirt *KubevirtPlatformSpec `json:"kubevirt,omitempty"`

	// OpenStack specifies configuration for clusters running on OpenStack.
	// +optional
	// +openshift:enable:FeatureGate=OpenStack
	OpenStack *OpenStackPlatformSpec `json:"openstack,omitempty"`
}

// IBMCloudPlatformSpec defines IBMCloud specific settings for components
type IBMCloudPlatformSpec struct {
	// ProviderType is a specific supported infrastructure provider within IBM Cloud.
	ProviderType configv1.IBMCloudProviderType `json:"providerType,omitempty"`
}

// Release represents the metadata for an OCP release payload image.
type Release struct {
	// Image is the image pullspec of an OCP release payload image.
	// See https://quay.io/repository/openshift-release-dev/ocp-release?tab=tags for a list of available images.
	// +kubebuilder:validation:XValidation:rule=`self.matches('^(\\w+\\S+)$')`,message="Image must start with a word character (letters, digits, or underscores) and contain no white spaces"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +required
	Image string `json:"image"`
}

// We expose here internal configuration knobs that won't be exposed to the service.
type AutoNode struct {
	// provisioner is the implementation used for Node auto provisioning.
	// +required
	Provisioner *ProvisionerConfig `json:"provisionerConfig"`
}

// ProvisionerConfig is a enum specifying the strategy for auto managing Nodes.
type ProvisionerConfig struct {
	// name specifies the name of the provisioner to use.
	// +required
	// +kubebuilder:validation:Enum=Karpenter
	Name Provisioner `json:"name"`
	// karpenter specifies the configuration for the Karpenter provisioner.
	// +optional
	Karpenter *KarpenterConfig `json:"karpenter,omitempty"`
}

type KarpenterConfig struct {
	// platform specifies the platform-specific configuration for Karpenter.
	// +required
	Platform PlatformType `json:"platform"`
	// aws specifies the AWS-specific configuration for Karpenter.
	// +optional
	AWS *KarpenterAWSConfig `json:"aws,omitempty"`
}

type KarpenterAWSConfig struct {
	//arn specifies the ARN of the Karpenter provisioner.
	// +required
	RoleARN string `json:"roleARN"`
}

const (
	ProvisionerKarpeneter Provisioner = "Karpenter"
)

// provisioner is a enum specifying the strategy for auto managing Nodes.
// +kubebuilder:validation:Enum=Karpenter
type Provisioner string

// ClusterAutoscaling specifies auto-scaling behavior that applies to all
// NodePools associated with a control plane.
type ClusterAutoscaling struct {
	// maxNodesTotal is the maximum allowable number of nodes for the Autoscaler scale out to be operational.
	// The autoscaler will not grow the cluster beyond this number.
	// If omitted, the autoscaler will not have a maximum limit.
	// number.
	//
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxNodesTotal *int32 `json:"maxNodesTotal,omitempty"`

	// maxPodGracePeriod is the maximum seconds to wait for graceful pod
	// termination before scaling down a NodePool. The default is 600 seconds.
	//
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxPodGracePeriod *int32 `json:"maxPodGracePeriod,omitempty"`

	// maxNodeProvisionTime is the maximum time to wait for node provisioning
	// before considering the provisioning to be unsuccessful, expressed as a Go
	// duration string. The default is 15 minutes.
	//
	// +kubebuilder:validation:Pattern=^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$
	// +optional
	MaxNodeProvisionTime string `json:"maxNodeProvisionTime,omitempty"`

	// podPriorityThreshold enables users to schedule "best-effort" pods, which
	// shouldn't trigger autoscaler actions, but only run when there are spare
	// resources available. The default is -10.
	//
	// See the following for more details:
	// https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption
	//
	// +optional
	PodPriorityThreshold *int32 `json:"podPriorityThreshold,omitempty"`
}

// EtcdManagementType is a enum specifying the strategy for managing the cluster's etcd instance
// +kubebuilder:validation:Enum=Managed;Unmanaged
type EtcdManagementType string

const (
	// Managed means HyperShift should provision and operator the etcd cluster
	// automatically.
	Managed EtcdManagementType = "Managed"

	// Unmanaged means HyperShift will not provision or manage the etcd cluster,
	// and the user is responsible for doing so.
	Unmanaged EtcdManagementType = "Unmanaged"
)

// EtcdSpec specifies configuration for a control plane etcd cluster.
// +kubebuilder:validation:XValidation:rule="self.managementType == 'Managed' ? has(self.managed) : !has(self.managed)",message="Only managed configuration must be set when managementType is Managed"
// +kubebuilder:validation:XValidation:rule="self.managementType == 'Unmanaged' ? has(self.unmanaged) : !has(self.unmanaged)",message="Only unmanaged configuration must be set when managementType is Unmanaged"
type EtcdSpec struct {
	// managementType defines how the etcd cluster is managed.
	// This can be either Managed or Unmanaged.
	// This field is immutable.
	// +unionDiscriminator
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="managementType is immutable"
	// +required
	// +immutable
	ManagementType EtcdManagementType `json:"managementType"`

	// managed specifies the behavior of an etcd cluster managed by HyperShift.
	//
	// +optional
	// +immutable
	Managed *ManagedEtcdSpec `json:"managed,omitempty"`

	// unmanaged specifies configuration which enables the control plane to
	// integrate with an externally managed etcd cluster.
	//
	// +optional
	// +immutable
	Unmanaged *UnmanagedEtcdSpec `json:"unmanaged,omitempty"`
}

// ManagedEtcdSpec specifies the behavior of an etcd cluster managed by
// HyperShift.
type ManagedEtcdSpec struct {
	// storage specifies how etcd data is persisted.
	//+required
	Storage ManagedEtcdStorageSpec `json:"storage"`
}

// ManagedEtcdStorageType is a storage type for an etcd cluster.
//
// +kubebuilder:validation:Enum=PersistentVolume
type ManagedEtcdStorageType string

const (
	// PersistentVolumeEtcdStorage uses PersistentVolumes for etcd storage.
	PersistentVolumeEtcdStorage ManagedEtcdStorageType = "PersistentVolume"
)

var (
	DefaultPersistentVolumeEtcdStorageSize resource.Quantity = resource.MustParse("8Gi")
)

// ManagedEtcdStorageSpec describes the storage configuration for etcd data.
type ManagedEtcdStorageSpec struct {
	// type is the kind of persistent storage implementation to use for etcd.
	// Only PersistentVolume is supported at the moment.
	// +immutable
	// +required
	// +unionDiscriminator
	Type ManagedEtcdStorageType `json:"type"`

	// persistentVolume is the configuration for PersistentVolume etcd storage.
	// With this implementation, a PersistentVolume will be allocated for every
	// etcd member (either 1 or 3 depending on the HostedCluster control plane
	// availability configuration).
	//
	// +optional
	PersistentVolume *PersistentVolumeEtcdStorageSpec `json:"persistentVolume,omitempty"`

	// restoreSnapshotURL allows an optional URL to be provided where
	// an etcd snapshot can be downloaded, for example a pre-signed URL
	// referencing a storage service.
	// This snapshot will be restored on initial startup, only when the etcd PV
	// is empty.
	//
	// +optional
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self.size() <= 1", message="RestoreSnapshotURL shouldn't contain more than 1 entry"
	RestoreSnapshotURL []string `json:"restoreSnapshotURL,omitempty"`
}

// PersistentVolumeEtcdStorageSpec is the configuration for PersistentVolume
// etcd storage.
type PersistentVolumeEtcdStorageSpec struct {
	// storageClassName is the StorageClass of the data volume for each etcd member.
	// See https://kubernetes.io/docs/concepts/storage/persistent-volumes#class-1.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="storageClassName is immutable"
	// +optional
	// +immutable
	// TODO(alberto): This shouldn't really be a pointer. There's no real different semantic for nil and empty string. Revisit all pointer vs non-pointer choices.
	StorageClassName *string `json:"storageClassName,omitempty"`

	// size is the minimum size of the data volume for each etcd member.
	// Default is 8Gi.
	// This field is immutable
	// +optional
	// +kubebuilder:default="8Gi"
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="Etcd PV storage size is immutable"
	Size *resource.Quantity `json:"size,omitempty"`
}

// UnmanagedEtcdSpec specifies configuration which enables the control plane to
// integrate with an eternally managed etcd cluster.
type UnmanagedEtcdSpec struct {
	// endpoint is the full etcd cluster client endpoint URL. For example:
	//
	//     https://etcd-client:2379
	//
	// If the URL uses an HTTPS scheme, the TLS field is required.
	//
	// +kubebuilder:validation:Pattern=`^https://`
	Endpoint string `json:"endpoint"`

	// tls specifies TLS configuration for HTTPS etcd client endpoints.
	//+required
	TLS EtcdTLSConfig `json:"tls"`
}

// EtcdTLSConfig specifies TLS configuration for HTTPS etcd client endpoints.
type EtcdTLSConfig struct {
	// ClientSecret refers to a secret for client mTLS authentication with the etcd cluster. It
	// may have the following key/value pairs:
	//
	//     etcd-client-ca.crt: Certificate Authority value
	//     etcd-client.crt: Client certificate value
	//     etcd-client.key: Client certificate key value
	ClientSecret corev1.LocalObjectReference `json:"clientSecret"`
}

// SecretEncryptionType defines the type of kube secret encryption being used.
// +kubebuilder:validation:Enum=kms;aescbc
type SecretEncryptionType string

const (
	// KMS integrates with a cloud provider's key management service to do secret encryption
	KMS SecretEncryptionType = "kms"
	// AESCBC uses AES-CBC with PKCS#7 padding to do secret encryption
	AESCBC SecretEncryptionType = "aescbc"
)

// SecretEncryptionSpec contains metadata about the kubernetes secret encryption strategy being used for the
// cluster when applicable.
type SecretEncryptionSpec struct {
	// Type defines the type of kube secret encryption being used
	// +unionDiscriminator
	Type SecretEncryptionType `json:"type"`

	// KMS defines metadata about the kms secret encryption strategy
	// +optional
	KMS *KMSSpec `json:"kms,omitempty"`

	// AESCBC defines metadata about the AESCBC secret encryption strategy
	// +optional
	AESCBC *AESCBCSpec `json:"aescbc,omitempty"`
}

// KMSProvider defines the supported KMS providers
// +kubebuilder:validation:Enum=IBMCloud;AWS;Azure
type KMSProvider string

const (
	IBMCloud KMSProvider = "IBMCloud"
	AWS      KMSProvider = "AWS"
	AZURE    KMSProvider = "Azure"
)

// KMSSpec defines metadata about the kms secret encryption strategy
type KMSSpec struct {
	// Provider defines the KMS provider
	// +unionDiscriminator
	Provider KMSProvider `json:"provider"`
	// IBMCloud defines metadata for the IBM Cloud KMS encryption strategy
	// +optional
	IBMCloud *IBMCloudKMSSpec `json:"ibmcloud,omitempty"`
	// AWS defines metadata about the configuration of the AWS KMS Secret Encryption provider
	// +optional
	AWS *AWSKMSSpec `json:"aws,omitempty"`
	// Azure defines metadata about the configuration of the Azure KMS Secret Encryption provider using Azure key vault
	// +optional
	Azure *AzureKMSSpec `json:"azure,omitempty"`
}

// AESCBCSpec defines metadata about the AESCBC secret encryption strategy
type AESCBCSpec struct {
	// ActiveKey defines the active key used to encrypt new secrets
	ActiveKey corev1.LocalObjectReference `json:"activeKey"`
	// BackupKey defines the old key during the rotation process so previously created
	// secrets can continue to be decrypted until they are all re-encrypted with the active key.
	// +optional
	BackupKey *corev1.LocalObjectReference `json:"backupKey,omitempty"`
}

type PayloadArchType string

const (
	AMD64   PayloadArchType = "AMD64"
	PPC64LE PayloadArchType = "PPC64LE"
	S390X   PayloadArchType = "S390X"
	ARM64   PayloadArchType = "ARM64"
	Multi   PayloadArchType = "Multi"
)

// ToPayloadArch converts a string to payloadArch.
func ToPayloadArch(arch string) PayloadArchType {
	switch arch {
	case "amd64", string(AMD64):
		return AMD64
	case "arm64", string(ARM64):
		return ARM64
	case "ppc64le", string(PPC64LE):
		return PPC64LE
	case "s390x", string(S390X):
		return S390X
	case "multi", string(Multi):
		return Multi
	default:
		return ""
	}
}

// HostedClusterStatus is the latest observed status of a HostedCluster.
type HostedClusterStatus struct {
	// Version is the status of the release version applied to the
	// HostedCluster.
	// +optional
	Version *ClusterVersionStatus `json:"version,omitempty"`

	// KubeConfig is a reference to the secret containing the default kubeconfig
	// for the cluster.
	// +optional
	KubeConfig *corev1.LocalObjectReference `json:"kubeconfig,omitempty"`

	// KubeadminPassword is a reference to the secret that contains the initial
	// kubeadmin user password for the guest cluster.
	// +optional
	KubeadminPassword *corev1.LocalObjectReference `json:"kubeadminPassword,omitempty"`

	// IgnitionEndpoint is the endpoint injected in the ign config userdata.
	// It exposes the config for instances to become kubernetes nodes.
	// +optional
	IgnitionEndpoint string `json:"ignitionEndpoint,omitempty"`

	// ControlPlaneEndpoint contains the endpoint information by which
	// external clients can access the control plane. This is populated
	// after the infrastructure is ready.
	// +kubebuilder:validation:Optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// OAuthCallbackURLTemplate contains a template for the URL to use as a callback
	// for identity providers. The [identity-provider-name] placeholder must be replaced
	// with the name of an identity provider defined on the HostedCluster.
	// This is populated after the infrastructure is ready.
	// +kubebuilder:validation:Optional
	OAuthCallbackURLTemplate string `json:"oauthCallbackURLTemplate,omitempty"`

	// Conditions represents the latest available observations of a control
	// plane's current state.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// payloadArch represents the CPU architecture type of the HostedCluster.Spec.Release.Image. The valid values are:
	// Multi, ARM64, AMD64, S390X, or PPC64LE.
	// +kubebuilder:validation:Enum=Multi;ARM64;AMD64;PPC64LE;S390X
	// +optional
	PayloadArch PayloadArchType `json:"payloadArch,omitempty"`

	// Platform contains platform-specific status of the HostedCluster
	// +optional
	Platform *PlatformStatus `json:"platform,omitempty"`
}

// PlatformStatus contains platform-specific status
type PlatformStatus struct {
	// +optional
	AWS *AWSPlatformStatus `json:"aws,omitempty"`
}

// ClusterVersionStatus reports the status of the cluster versioning,
// including any upgrades that are in progress. The current field will
// be set to whichever version the cluster is reconciling to, and the
// conditions array will report whether the update succeeded, is in
// progress, or is failing.
// +k8s:deepcopy-gen=true
type ClusterVersionStatus struct {
	// desired is the version that the cluster is reconciling towards.
	// If the cluster is not yet fully initialized desired will be set
	// with the information available, which may be an image or a tag.
	Desired configv1.Release `json:"desired"`

	// history contains a list of the most recent versions applied to the cluster.
	// This value may be empty during cluster startup, and then will be updated
	// when a new update is being applied. The newest update is first in the
	// list and it is ordered by recency. Updates in the history have state
	// Completed if the rollout completed - if an update was failing or halfway
	// applied the state will be Partial. Only a limited amount of update history
	// is preserved.
	//
	// +optional
	History []configv1.UpdateHistory `json:"history,omitempty"`

	// observedGeneration reports which version of the spec is being synced.
	// If this value is not equal to metadata.generation, then the desired
	// and conditions fields may represent a previous version.
	ObservedGeneration int64 `json:"observedGeneration"`

	// availableUpdates contains updates recommended for this
	// cluster. Updates which appear in conditionalUpdates but not in
	// availableUpdates may expose this cluster to known issues. This list
	// may be empty if no updates are recommended, if the update service
	// is unavailable, or if an invalid channel has been specified.
	// +nullable
	// +kubebuilder:validation:Required
	// +required
	AvailableUpdates []configv1.Release `json:"availableUpdates"`

	// conditionalUpdates contains the list of updates that may be
	// recommended for this cluster if it meets specific required
	// conditions. Consumers interested in the set of updates that are
	// actually recommended for this cluster should use
	// availableUpdates. This list may be empty if no updates are
	// recommended, if the update service is unavailable, or if an empty
	// or invalid channel has been specified.
	// +listType=atomic
	// +optional
	ConditionalUpdates []configv1.ConditionalUpdate `json:"conditionalUpdates,omitempty"`
}

// ClusterConfiguration specifies configuration for individual OCP components in the
// cluster, represented as embedded resources that correspond to the openshift
// configuration API.
//
// The API for individual configuration items is at:
// https://docs.openshift.com/container-platform/4.7/rest_api/config_apis/config-apis-index.html
type ClusterConfiguration struct {
	// APIServer holds configuration (like serving certificates, client CA and CORS domains)
	// shared by all API servers in the system, among them especially kube-apiserver
	// and openshift-apiserver.
	// +optional
	APIServer *configv1.APIServerSpec `json:"apiServer,omitempty"`

	// Authentication specifies cluster-wide settings for authentication (like OAuth and
	// webhook token authenticators).
	// +optional
	Authentication *configv1.AuthenticationSpec `json:"authentication,omitempty"`

	// FeatureGate holds cluster-wide information about feature gates.
	// +optional
	FeatureGate *configv1.FeatureGateSpec `json:"featureGate,omitempty"`

	// Image governs policies related to imagestream imports and runtime configuration
	// for external registries. It allows cluster admins to configure which registries
	// OpenShift is allowed to import images from, extra CA trust bundles for external
	// registries, and policies to block or allow registry hostnames.
	// When exposing OpenShift's image registry to the public, this also lets cluster
	// admins specify the external hostname.
	// Changing this value will trigger a rollout for all existing NodePools in the cluster.
	// TODO(alberto): elaborate why.
	// +rollout
	// +optional
	Image *configv1.ImageSpec `json:"image,omitempty"`

	// Ingress holds cluster-wide information about ingress, including the default ingress domain
	// used for routes.
	// +optional
	Ingress *configv1.IngressSpec `json:"ingress,omitempty"`

	// Network holds cluster-wide information about the network. It is used to configure the desired network configuration, such as: IP address pools for services/pod IPs, network plugin, etc.
	// Please view network.spec for an explanation on what applies when configuring this resource.
	// TODO (csrwng): Add validation here to exclude changes that conflict with networking settings in the HostedCluster.Spec.Networking field.
	// +optional
	Network *configv1.NetworkSpec `json:"network,omitempty"`

	// OAuth holds cluster-wide information about OAuth.
	// It is used to configure the integrated OAuth server.
	// This configuration is only honored when the top level Authentication config has type set to IntegratedOAuth.
	// +optional
	// +kubebuilder:validation:XValidation:rule="!has(self.tokenConfig) || !has(self.tokenConfig.accessTokenInactivityTimeout) || duration(self.tokenConfig.accessTokenInactivityTimeout).getSeconds() >= 300", message="spec.configuration.oauth.tokenConfig.accessTokenInactivityTimeout minimum acceptable token timeout value is 300 seconds"
	OAuth *configv1.OAuthSpec `json:"oauth,omitempty"`

	// OperatorHub specifies the configuration for the Operator Lifecycle Manager in the HostedCluster. This is only configured at deployment time but the controller are not reconcilling over it.
	// The OperatorHub configuration will be constantly reconciled if catalog placement is management, but only on cluster creation otherwise.
	//
	// +optional
	OperatorHub *configv1.OperatorHubSpec `json:"operatorhub,omitempty"`

	// Scheduler holds cluster-wide config information to run the Kubernetes Scheduler
	// and influence its placement decisions. The canonical name for this config is `cluster`.
	// +optional
	Scheduler *configv1.SchedulerSpec `json:"scheduler,omitempty"`

	// Proxy holds cluster-wide information on how to configure default proxies for the cluster.
	// This affects traffic flowing from the hosted cluster data plane.
	// The controllers will generate a machineConfig with the proxy config for the cluster.
	// This MachineConfig will be part of every payload generated by the controllers for any NodePool of the HostedCluster.
	// Changing this value will trigger a rollout for all existing NodePools in the cluster.

	// +rollout
	// +optional
	Proxy *configv1.ProxySpec `json:"proxy,omitempty"`
}

// +genclient

// HostedCluster is the primary representation of a HyperShift cluster and encapsulates
// the control plane and common data plane configuration. Creating a HostedCluster
// results in a fully functional OpenShift control plane with no attached nodes.
// To support workloads (e.g. pods), a HostedCluster may have one or more associated
// NodePool resources.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=hostedclusters,shortName=hc;hcs,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version.history[?(@.state==\"Completed\")].version",description="Version"
// +kubebuilder:printcolumn:name="KubeConfig",type="string",JSONPath=".status.kubeconfig.name",description="KubeConfig Secret"
// +kubebuilder:printcolumn:name="Progress",type="string",JSONPath=".status.version.history[?(@.state!=\"\")].state",description="Progress"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].status",description="Available"
// +kubebuilder:printcolumn:name="Progressing",type="string",JSONPath=".status.conditions[?(@.type==\"Progressing\")].status",description="Progressing"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].message",description="Message"
type HostedCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired behavior of the HostedCluster.
	Spec HostedClusterSpec `json:"spec,omitempty"`

	// Status is the latest observed status of the HostedCluster.
	Status HostedClusterStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// HostedClusterList contains a list of HostedCluster
type HostedClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedCluster `json:"items"`
}
