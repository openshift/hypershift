package v1beta1

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/hypershift/api/util/ipnet"
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

	// AESCBCKeySecretKey defines the Kubernetes secret key name that contains the aescbc encryption key
	// in the AESCBC secret encryption strategy
	AESCBCKeySecretKey = "key"
	// IBMCloudIAMAPIKeySecretKey defines the Kubernetes secret key name that contains
	// the customer IBMCloud apikey in the unmanaged authentication strategy for IBMCloud KMS secret encryption
	IBMCloudIAMAPIKeySecretKey = "iam_apikey"
	// AWSCredentialsFileSecretKey defines the Kubernetes secret key name that contains
	// the customer AWS credentials in the unmanaged authentication strategy for AWS KMS secret encryption
	AWSCredentialsFileSecretKey = "credentials"
	// ControlPlaneComponent identifies a resource as belonging to a hosted control plane.
	ControlPlaneComponent = "hypershift.openshift.io/control-plane-component"

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
	// The format of the annotation value is a commma-separated list of image=ref pairs like:
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
	// the frequency of memory collection when memory used rises above a particular threshhold. This can be used to reduce
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

	// RosaHCP represents the ROSA HCP managed service offering
	RosaHCP = "ROSA-HCP"

	// HostedClusterSizeLabel is a label on HostedClusters indicating a size based on the number of nodes.
	HostedClusterSizeLabel = "hypershift.openshift.io/hosted-cluster-size"

	// NodeSizeLabel is a label on nodes used to match cluster size to a node size.
	NodeSizeLabel = "hypershift.openshift.io/cluster-size"

	// ManagementPlatformAnnotation specifies the infrastructure platform of the underlying management cluster
	ManagementPlatformAnnotation = "hypershift.openshift.io/management-platform"

	// KubeAPIServerVerbosityLevelAnnotation allows specifing the log verbosity of kube-apiserver.
	KubeAPIServerVerbosityLevelAnnotation = "hypershift.openshift.io/kube-apiserver-verbosity-level"

	// SkipControlPlaneNamespaceDeletionAnnotation tells the the hosted cluster controller not to delete the hosted control plane
	// namespace during hosted cluster deletion when this annotation is set to the value "true".
	SkipControlPlaneNamespaceDeletionAnnotation = "hypershift.openshift.io/skip-delete-hosted-controlplane-namespace"
)

// HostedClusterSpec is the desired behavior of a HostedCluster.
type HostedClusterSpec struct {
	// Release specifies the desired OCP release payload for the hosted cluster.
	//
	// Updating this field will trigger a rollout of the control plane. The
	// behavior of the rollout will be driven by the ControllerAvailabilityPolicy
	// and InfrastructureAvailabilityPolicy.
	Release Release `json:"release"`

	// ControlPlaneRelease specifies the desired OCP release payload for
	// control plane components running on the management cluster.
	// Updating this field will trigger a rollout of the control plane. The
	// behavior of the rollout will be driven by the ControllerAvailabilityPolicy
	// and InfrastructureAvailabilityPolicy.
	// If not defined, Release is used
	// +optional
	ControlPlaneRelease *Release `json:"controlPlaneRelease,omitempty"`

	// ClusterID uniquely identifies this cluster. This is expected to be
	// an RFC4122 UUID value (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in
	// hexadecimal values).
	// As with a Kubernetes metadata.uid, this ID uniquely identifies this
	// cluster in space and time.
	// This value identifies the cluster in metrics pushed to telemetry and
	// metrics produced by the control plane operators. If a value is not
	// specified, an ID is generated. After initial creation, the value is
	// immutable.
	// +kubebuilder:validation:Pattern:="[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}"
	// +optional
	ClusterID string `json:"clusterID,omitempty"`

	// updateService may be used to specify the preferred upstream update service.
	// By default it will use the appropriate update service for the cluster and region.
	//
	// +optional
	UpdateService configv1.URL `json:"updateService,omitempty"`

	// channel is an identifier for explicitly requesting that a non-default
	// set of updates be applied to this cluster. The default channel will be
	// contain stable updates that are appropriate for production clusters.
	//
	// +optional
	Channel string `json:"channel,omitempty"`

	// InfraID is a globally unique identifier for the cluster. This identifier
	// will be used to associate various cloud resources with the HostedCluster
	// and its associated NodePools.
	//
	// +optional
	// +immutable
	InfraID string `json:"infraID,omitempty"`

	// Platform specifies the underlying infrastructure provider for the cluster
	// and is used to configure platform specific behavior.
	//
	// +immutable
	Platform PlatformSpec `json:"platform"`

	// ControllerAvailabilityPolicy specifies the availability policy applied to
	// critical control plane components. The default value is HighlyAvailable.
	//
	// +optional
	// +kubebuilder:default:="HighlyAvailable"
	// +immutable
	ControllerAvailabilityPolicy AvailabilityPolicy `json:"controllerAvailabilityPolicy,omitempty"`

	// InfrastructureAvailabilityPolicy specifies the availability policy applied
	// to infrastructure services which run on cluster nodes. The default value is
	// SingleReplica.
	//
	// +optional
	// +kubebuilder:default:="SingleReplica"
	// +immutable
	InfrastructureAvailabilityPolicy AvailabilityPolicy `json:"infrastructureAvailabilityPolicy,omitempty"`

	// DNS specifies DNS configuration for the cluster.
	//
	// +immutable
	DNS DNSSpec `json:"dns,omitempty"`

	// Networking specifies network configuration for the cluster.
	//
	// +immutable
	// +kubebuilder:default={networkType: "OVNKubernetes", clusterNetwork: {{cidr: "10.132.0.0/14"}}, serviceNetwork: {{cidr: "172.31.0.0/16"}}}
	Networking ClusterNetworking `json:"networking"`

	// Autoscaling specifies auto-scaling behavior that applies to all NodePools
	// associated with the control plane.
	//
	// +optional
	Autoscaling ClusterAutoscaling `json:"autoscaling,omitempty"`

	// Etcd specifies configuration for the control plane etcd cluster. The
	// default ManagementType is Managed. Once set, the ManagementType cannot be
	// changed.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={managementType: "Managed", managed: {storage: {type: "PersistentVolume", persistentVolume: {size: "8Gi"}}}}
	// +immutable
	Etcd EtcdSpec `json:"etcd"`

	// Services specifies how individual control plane services are published from
	// the hosting cluster of the control plane.
	//
	// If a given service is not present in this list, it will be exposed publicly
	// by default.
	Services []ServicePublishingStrategyMapping `json:"services"`

	// PullSecret references a pull secret to be injected into the container
	// runtime of all cluster nodes. The secret must have a key named
	// ".dockerconfigjson" whose value is the pull secret JSON.
	PullSecret corev1.LocalObjectReference `json:"pullSecret"`

	// SSHKey references an SSH key to be injected into all cluster node sshd
	// servers. The secret must have a single key "id_rsa.pub" whose value is the
	// public part of an SSH key.
	//
	// +immutable
	SSHKey corev1.LocalObjectReference `json:"sshKey"`

	// IssuerURL is an OIDC issuer URL which is used as the issuer in all
	// ServiceAccount tokens generated by the control plane API server. The
	// default value is kubernetes.default.svc, which only works for in-cluster
	// validation.
	//
	// +kubebuilder:default:="https://kubernetes.default.svc"
	// +immutable
	// +optional
	// +kubebuilder:validation:Format=uri
	IssuerURL string `json:"issuerURL,omitempty"`

	// ServiceAccountSigningKey is a reference to a secret containing the private key
	// used by the service account token issuer. The secret is expected to contain
	// a single key named "key". If not specified, a service account signing key will
	// be generated automatically for the cluster. When specifying a service account
	// signing key, a IssuerURL must also be specified.
	//
	// +immutable
	// +kubebuilder:validation:Optional
	// +optional
	ServiceAccountSigningKey *corev1.LocalObjectReference `json:"serviceAccountSigningKey,omitempty"`

	// Configuration specifies configuration for individual OCP components in the
	// cluster, represented as embedded resources that correspond to the openshift
	// configuration API.
	//
	// +kubebuilder:validation:Optional
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

	// ImageContentSources specifies image mirrors that can be used by cluster
	// nodes to pull content.
	//
	// +optional
	// +immutable
	ImageContentSources []ImageContentSource `json:"imageContentSources,omitempty"`

	// AdditionalTrustBundle is a reference to a ConfigMap containing a
	// PEM-encoded X.509 certificate bundle that will be added to the hosted controlplane and nodes
	//
	// +optional
	AdditionalTrustBundle *corev1.LocalObjectReference `json:"additionalTrustBundle,omitempty"`

	// SecretEncryption specifies a Kubernetes secret encryption strategy for the
	// control plane.
	//
	// +optional
	SecretEncryption *SecretEncryptionSpec `json:"secretEncryption,omitempty"`

	// FIPS indicates whether this cluster's nodes will be running in FIPS mode.
	// If set to true, the control plane's ignition server will be configured to
	// expect that nodes joining the cluster will be FIPS-enabled.
	//
	// +optional
	// +immutable
	FIPS bool `json:"fips"`

	// PausedUntil is a field that can be used to pause reconciliation on a resource.
	// Either a date can be provided in RFC3339 format or a boolean. If a date is
	// provided: reconciliation is paused on the resource until that date. If the boolean true is
	// provided: reconciliation is paused on the resource until the field is removed.
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

	// NodeSelector when specified, must be true for the pods managed by the HostedCluster to be scheduled.
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
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

// ServicePublishingStrategyMapping specifies how individual control plane
// services are published from the hosting cluster of a control plane.
type ServicePublishingStrategyMapping struct {
	// Service identifies the type of service being published.
	//
	// +kubebuilder:validation:Enum=APIServer;OAuthServer;OIDC;Konnectivity;Ignition;OVNSbDb
	// +immutable
	Service ServiceType `json:"service"`

	// ServicePublishingStrategy specifies how to publish Service.
	ServicePublishingStrategy `json:"servicePublishingStrategy"`
}

// ServicePublishingStrategy specfies how to publish a ServiceType.
type ServicePublishingStrategy struct {
	// Type is the publishing strategy used for the service.
	//
	// +kubebuilder:validation:Enum=LoadBalancer;NodePort;Route;None;S3
	// +immutable
	Type PublishingStrategyType `json:"type"`

	// NodePort configures exposing a service using a NodePort.
	NodePort *NodePortPublishingStrategy `json:"nodePort,omitempty"`

	// LoadBalancer configures exposing a service using a LoadBalancer.
	LoadBalancer *LoadBalancerPublishingStrategy `json:"loadBalancer,omitempty"`

	// Route configures exposing a service using a Route.
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

	// OIDC is the control plane OIDC service.
	OIDC ServiceType = "OIDC"

	// Ignition is the control plane ignition service for nodes.
	Ignition ServiceType = "Ignition"

	// OVNSbDb is the optional control plane ovn southbound database service used by OVNKubernetes CNI.
	OVNSbDb ServiceType = "OVNSbDb"
)

// NodePortPublishingStrategy specifies a NodePort used to expose a service.
type NodePortPublishingStrategy struct {
	// Address is the host/ip that the NodePort service is exposed over.
	Address string `json:"address"`

	// Port is the port of the NodePort service. If <=0, the port is dynamically
	// assigned when the service is created.
	Port int32 `json:"port,omitempty"`
}

// LoadBalancerPublishingStrategy specifies setting used to expose a service as a LoadBalancer.
type LoadBalancerPublishingStrategy struct {
	// Hostname is the name of the DNS record that will be created pointing to the LoadBalancer.
	// +optional
	Hostname string `json:"hostname,omitempty"`
}

// RoutePublishingStrategy specifies options for exposing a service as a Route.
type RoutePublishingStrategy struct {
	// Hostname is the name of the DNS record that will be created pointing to the Route.
	// +optional
	Hostname string `json:"hostname,omitempty"`
}

// DNSSpec specifies the DNS configuration in the cluster.
type DNSSpec struct {
	// BaseDomain is the base domain of the cluster.
	//
	// +immutable
	BaseDomain string `json:"baseDomain"`

	// BaseDomainPrefix is the base domain prefix of the cluster.
	// defaults to clusterName if not set. Set it to "" if you don't want a prefix to be prepended to BaseDomain.
	//
	// +optional
	// +immutable
	BaseDomainPrefix *string `json:"baseDomainPrefix,omitempty"`

	// PublicZoneID is the Hosted Zone ID where all the DNS records that are
	// publicly accessible to the internet exist.
	//
	// +optional
	// +immutable
	PublicZoneID string `json:"publicZoneID,omitempty"`

	// PrivateZoneID is the Hosted Zone ID where all the DNS records that are only
	// available internally to the cluster exist.
	//
	// +optional
	// +immutable
	PrivateZoneID string `json:"privateZoneID,omitempty"`
}

// ClusterNetworking specifies network configuration for a cluster.
type ClusterNetworking struct {
	// MachineNetwork is the list of IP address pools for machines.
	//
	// +immutable
	// +optional
	MachineNetwork []MachineNetworkEntry `json:"machineNetwork,omitempty"`

	// ClusterNetwork is the list of IP address pools for pods.
	//
	// +immutable
	// +kubebuilder:default:={{cidr: "10.132.0.0/14"}}
	ClusterNetwork []ClusterNetworkEntry `json:"clusterNetwork"`

	// ServiceNetwork is the list of IP address pools for services.
	// NOTE: currently only one entry is supported.
	//
	// +optional
	// +kubebuilder:default:={{cidr: "172.31.0.0/16"}}
	ServiceNetwork []ServiceNetworkEntry `json:"serviceNetwork"`

	// NetworkType specifies the SDN provider used for cluster networking.
	//
	// +kubebuilder:default:="OVNKubernetes"
	// +immutable
	NetworkType NetworkType `json:"networkType"`

	// APIServer contains advanced network settings for the API server that affect
	// how the APIServer is exposed inside a cluster node.
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
	// CIDR is the IP block address pool.
	CIDR ipnet.IPNet `json:"cidr"`

	// HostPrefix is the prefix size to allocate to each node from the CIDR.
	// For example, 24 would allocate 2^8=256 adresses to each node. If this
	// field is not used by the plugin, it can be left unset.
	// +optional
	HostPrefix int32 `json:"hostPrefix,omitempty"`
}

// ServiceNetworkEntry is a single IP address block for the service network.
type ServiceNetworkEntry struct {
	// CIDR is the IP block address pool for services within the cluster.
	CIDR ipnet.IPNet `json:"cidr"`
}

// +kubebuilder:validation:Pattern:=`^(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])(\/(3[0-2]|[1-2][0-9]|[0-9]))$`
type CIDRBlock string

// APIServerNetworking specifies how the APIServer is exposed inside a cluster
// node.
type APIServerNetworking struct {
	// AdvertiseAddress is the address that nodes will use to talk to the API
	// server. This is an address associated with the loopback adapter of each
	// node. If not specified, the controller will take default values.
	// The default values will be set as 172.20.0.1 or fd00::1.
	AdvertiseAddress *string `json:"advertiseAddress,omitempty"`

	// Port is the port at which the APIServer is exposed inside a node. Other
	// pods using host networking cannot listen on this port.
	// If unset 6443 is used.
	// This is useful to choose a port other than the default one which might interfere with customer environments e.g. https://github.com/openshift/hypershift/pull/356.
	// Setting this to 443 is possible only for backward compatibility reasons and it's discouraged.
	// Doing so, it would result in the controller overriding the KAS endpoint in the guest cluster having a discrepancy with the KAS Pod and potentially causing temporarily network failures.
	Port *int32 `json:"port,omitempty"`

	// AllowedCIDRBlocks is an allow list of CIDR blocks that can access the APIServer
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
//
// +kubebuilder:validation:Enum=AWS;None;IBMCloud;Agent;KubeVirt;Azure;PowerVS
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
	}
}

// PlatformSpec specifies the underlying infrastructure provider for the cluster
// and is used to configure platform specific behavior.
type PlatformSpec struct {
	// Type is the type of infrastructure provider for the cluster.
	//
	// +unionDiscriminator
	// +immutable
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
}

type KubevirtPlatformCredentials struct {
	// InfraKubeConfigSecret is a reference to a secret that contains the kubeconfig for the external infra cluster
	// that will be used to host the KubeVirt virtual machines for this cluster.
	//
	// +immutable
	// +kubebuilder:validation:Required
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="infraKubeConfigSecret is immutable"
	InfraKubeConfigSecret *KubeconfigSecretRef `json:"infraKubeConfigSecret,omitempty"`

	// InfraNamespace defines the namespace on the external infra cluster that is used to host the KubeVirt
	// virtual machines. This namespace must already exist before creating the HostedCluster and the kubeconfig
	// referenced in the InfraKubeConfigSecret must have access to manage the required resources within this
	// namespace.
	//
	// +immutable
	// +kubebuilder:validation:Required
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="infraNamespace is immutable"
	InfraNamespace string `json:"infraNamespace"`
}

// KubevirtPlatformSpec specifies configuration for kubevirt guest cluster installations
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.generateID) || has(self.generateID)", message="Kubevirt GenerateID is required once set"
type KubevirtPlatformSpec struct {
	// BaseDomainPassthrough toggles whether or not an automatically
	// generated base domain for the guest cluster should be used that
	// is a subdomain of the management cluster's *.apps DNS.
	//
	// For the KubeVirt platform, the basedomain can be autogenerated using
	// the *.apps domain of the management/infra hosting cluster
	// This makes the guest cluster's base domain a subdomain of the
	// hypershift infra/mgmt cluster's base domain.
	//
	// Example:
	//   Infra/Mgmt cluster's DNS
	//     Base: example.com
	//     Cluster: mgmt-cluster.example.com
	//     Apps:    *.apps.mgmt-cluster.example.com
	//   KubeVirt Guest cluster's DNS
	//     Base: apps.mgmt-cluster.example.com
	//     Cluster: guest.apps.mgmt-cluster.example.com
	//     Apps: *.apps.guest.apps.mgmt-cluster.example.com
	//
	// This is possible using OCP wildcard routes
	//
	// +optional
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="baseDomainPassthrough is immutable"
	BaseDomainPassthrough *bool `json:"baseDomainPassthrough,omitempty"`

	// GenerateID is used to uniquely apply a name suffix to resources associated with
	// kubevirt infrastructure resources
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Kubevirt GenerateID is immutable once set"
	// +kubebuilder:validation:MaxLength=11
	// +optional
	GenerateID string `json:"generateID,omitempty"`
	// Credentials defines the client credentials used when creating KubeVirt virtual machines.
	// Defining credentials is only necessary when the KubeVirt virtual machines are being placed
	// on a cluster separate from the one hosting the Hosted Control Plane components.
	//
	// The default behavior when Credentials is not defined is for the KubeVirt VMs to be placed on
	// the same cluster and namespace as the Hosted Control Plane.
	// +optional
	Credentials *KubevirtPlatformCredentials `json:"credentials,omitempty"`

	// StorageDriver defines how the KubeVirt CSI driver exposes StorageClasses on
	// the infra cluster (hosting the VMs) to the guest cluster.
	//
	// +kubebuilder:validation:Optional
	// +optional
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="storageDriver is immutable"
	StorageDriver *KubevirtStorageDriverSpec `json:"storageDriver,omitempty"`
}

// KubevirtStorageDriverConfigType defines how the kubevirt storage driver is configured.
//
// +kubebuilder:validation:Enum=None;Default;Manual
type KubevirtStorageDriverConfigType string

const (
	// NoneKubevirtStorageDriverConfigType means no kubevirt storage driver is used
	NoneKubevirtStorageDriverConfigType KubevirtStorageDriverConfigType = "None"

	// DefaultKubevirtStorageDriverConfigType means the kubevirt storage driver maps to the
	// underlying infra cluster's default storageclass
	DefaultKubevirtStorageDriverConfigType KubevirtStorageDriverConfigType = "Default"

	// ManualKubevirtStorageDriverConfigType means the kubevirt storage driver mapping is
	// explicitly defined.
	ManualKubevirtStorageDriverConfigType KubevirtStorageDriverConfigType = "Manual"
)

type KubevirtStorageDriverSpec struct {
	// Type represents the type of kubevirt csi driver configuration to use
	//
	// +unionDiscriminator
	// +immutable
	// +kubebuilder:default=Default
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="storageDriver.Type is immutable"
	Type KubevirtStorageDriverConfigType `json:"type,omitempty"`

	// Manual is used to explicilty define how the infra storageclasses are
	// mapped to guest storageclasses
	//
	// +immutable
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="storageDriver.Manual is immutable"
	Manual *KubevirtManualStorageDriverConfig `json:"manual,omitempty"`
}

type KubevirtManualStorageDriverConfig struct {
	// StorageClassMapping maps StorageClasses on the infra cluster hosting
	// the KubeVirt VMs to StorageClasses that are made available within the
	// Guest Cluster.
	//
	// NOTE: It is possible that not all capablities of an infra cluster's
	// storageclass will be present for the corresponding guest clusters storageclass.
	//
	// +optional
	// +immutable
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="storageClassMapping is immutable"
	StorageClassMapping []KubevirtStorageClassMapping `json:"storageClassMapping,omitempty"`

	// +optional
	// +immutable
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="volumeSnapshotClassMapping is immutable"
	VolumeSnapshotClassMapping []KubevirtVolumeSnapshotClassMapping `json:"volumeSnapshotClassMapping,omitempty"`
}

type KubevirtStorageClassMapping struct {
	// Group contains which group this mapping belongs to.
	Group string `json:"group,omitempty"`
	// InfraStorageClassName is the name of the infra cluster storage class that
	// will be exposed to the guest.
	InfraStorageClassName string `json:"infraStorageClassName"`
	// GuestStorageClassName is the name that the corresponding storageclass will
	// be called within the guest cluster
	GuestStorageClassName string `json:"guestStorageClassName"`
}

type KubevirtVolumeSnapshotClassMapping struct {
	// Group contains which group this mapping belongs to.
	Group string `json:"group,omitempty"`
	// InfraStorageClassName is the name of the infra cluster volume snapshot class that
	// will be exposed to the guest.
	InfraVolumeSnapshotClassName string `json:"infraVolumeSnapshotClassName"`
	// GuestVolumeSnapshotClassName is the name that the corresponding volumeSnapshotClass will
	// be called within the guest cluster
	GuestVolumeSnapshotClassName string `json:"guestVolumeSnapshotClassName"`
}

// AgentPlatformSpec specifies configuration for agent-based installations.
type AgentPlatformSpec struct {
	// AgentNamespace is the namespace where to search for Agents for this cluster
	AgentNamespace string `json:"agentNamespace"`
}

// IBMCloudPlatformSpec defines IBMCloud specific settings for components
type IBMCloudPlatformSpec struct {
	// ProviderType is a specific supported infrastructure provider within IBM Cloud.
	ProviderType configv1.IBMCloudProviderType `json:"providerType,omitempty"`
}

// PowerVSPlatformSpec defines IBMCloud PowerVS specific settings for components
type PowerVSPlatformSpec struct {
	// AccountID is the IBMCloud account id.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	AccountID string `json:"accountID"`

	// CISInstanceCRN is the IBMCloud CIS Service Instance's Cloud Resource Name
	// This field is immutable. Once set, It can't be changed.
	//
	// +kubebuilder:validation:Pattern=`^crn:`
	// +immutable
	CISInstanceCRN string `json:"cisInstanceCRN"`

	// ResourceGroup is the IBMCloud Resource Group in which the cluster resides.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	ResourceGroup string `json:"resourceGroup"`

	// Region is the IBMCloud region in which the cluster resides. This configures the
	// OCP control plane cloud integrations, and is used by NodePool to resolve
	// the correct boot image for a given release.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	Region string `json:"region"`

	// Zone is the availability zone where control plane cloud resources are
	// created.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	Zone string `json:"zone"`

	// Subnet is the subnet to use for control plane cloud resources.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	Subnet *PowerVSResourceReference `json:"subnet"`

	// ServiceInstance is the reference to the Power VS service on which the server instance(VM) will be created.
	// Power VS service is a container for all Power VS instances at a specific geographic region.
	// serviceInstance can be created via IBM Cloud catalog or CLI.
	// ServiceInstanceID is the unique identifier that can be obtained from IBM Cloud UI or IBM Cloud cli.
	//
	// More detail about Power VS service instance.
	// https://cloud.ibm.com/docs/power-iaas?topic=power-iaas-creating-power-virtual-server
	//
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	ServiceInstanceID string `json:"serviceInstanceID"`

	// VPC specifies IBM Cloud PowerVS Load Balancing configuration for the control
	// plane.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	VPC *PowerVSVPC `json:"vpc"`

	// KubeCloudControllerCreds is a reference to a secret containing cloud
	// credentials with permissions matching the cloud controller policy.
	// This field is immutable. Once set, It can't be changed.
	//
	// TODO(dan): document the "cloud controller policy"
	//
	// +immutable
	KubeCloudControllerCreds corev1.LocalObjectReference `json:"kubeCloudControllerCreds"`

	// NodePoolManagementCreds is a reference to a secret containing cloud
	// credentials with permissions matching the node pool management policy.
	// This field is immutable. Once set, It can't be changed.
	//
	// TODO(dan): document the "node pool management policy"
	//
	// +immutable
	NodePoolManagementCreds corev1.LocalObjectReference `json:"nodePoolManagementCreds"`

	// IngressOperatorCloudCreds is a reference to a secret containing ibm cloud
	// credentials for ingress operator to get authenticated with ibm cloud.
	//
	// +immutable
	IngressOperatorCloudCreds corev1.LocalObjectReference `json:"ingressOperatorCloudCreds"`

	// StorageOperatorCloudCreds is a reference to a secret containing ibm cloud
	// credentials for storage operator to get authenticated with ibm cloud.
	//
	// +immutable
	StorageOperatorCloudCreds corev1.LocalObjectReference `json:"storageOperatorCloudCreds"`

	// ImageRegistryOperatorCloudCreds is a reference to a secret containing ibm cloud
	// credentials for image registry operator to get authenticated with ibm cloud.
	//
	// +immutable
	ImageRegistryOperatorCloudCreds corev1.LocalObjectReference `json:"imageRegistryOperatorCloudCreds"`
}

// PowerVSVPC specifies IBM Cloud PowerVS LoadBalancer configuration for the control
// plane.
type PowerVSVPC struct {
	// Name for VPC to used for all the service load balancer.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	Name string `json:"name"`

	// Region is the IBMCloud region in which VPC gets created, this VPC used for all the ingress traffic
	// into the OCP cluster.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	Region string `json:"region"`

	// Zone is the availability zone where load balancer cloud resources are
	// created.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +optional
	Zone string `json:"zone,omitempty"`

	// Subnet is the subnet to use for load balancer.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +optional
	Subnet string `json:"subnet,omitempty"`
}

// PowerVSResourceReference is a reference to a specific IBMCloud PowerVS resource by ID, or Name.
// Only one of ID, or Name may be specified. Specifying more than one will result in
// a validation error.
type PowerVSResourceReference struct {
	// ID of resource
	// +optional
	ID *string `json:"id,omitempty"`

	// Name of resource
	// +optional
	Name *string `json:"name,omitempty"`
}

// AWSCloudProviderConfig specifies AWS networking configuration.
type AWSCloudProviderConfig struct {
	// Subnet is the subnet to use for control plane cloud resources.
	//
	// +optional
	Subnet *AWSResourceReference `json:"subnet,omitempty"`

	// Zone is the availability zone where control plane cloud resources are
	// created.
	//
	// +optional
	Zone string `json:"zone,omitempty"`

	// VPC is the VPC to use for control plane cloud resources.
	VPC string `json:"vpc"`
}

// AWSEndpointAccessType specifies the publishing scope of cluster endpoints.
type AWSEndpointAccessType string

const (
	// Public endpoint access allows public API server access and public node
	// communication with the control plane.
	Public AWSEndpointAccessType = "Public"

	// PublicAndPrivate endpoint access allows public API server access and
	// private node communication with the control plane.
	PublicAndPrivate AWSEndpointAccessType = "PublicAndPrivate"

	// Private endpoint access allows only private API server access and private
	// node communication with the control plane.
	Private AWSEndpointAccessType = "Private"
)

// AWSPlatformSpec specifies configuration for clusters running on Amazon Web Services.
type AWSPlatformSpec struct {
	// Region is the AWS region in which the cluster resides. This configures the
	// OCP control plane cloud integrations, and is used by NodePool to resolve
	// the correct boot AMI for a given release.
	//
	// +immutable
	Region string `json:"region"`

	// CloudProviderConfig specifies AWS networking configuration for the control
	// plane.
	// This is mainly used for cloud provider controller config:
	// https://github.com/kubernetes/kubernetes/blob/f5be5052e3d0808abb904aebd3218fe4a5c2dd82/staging/src/k8s.io/legacy-cloud-providers/aws/aws.go#L1347-L1364
	// TODO(dan): should this be named AWSNetworkConfig?
	//
	// +optional
	// +immutable
	CloudProviderConfig *AWSCloudProviderConfig `json:"cloudProviderConfig,omitempty"`

	// ServiceEndpoints specifies optional custom endpoints which will override
	// the default service endpoint of specific AWS Services.
	//
	// There must be only one ServiceEndpoint for a given service name.
	//
	// +optional
	// +immutable
	ServiceEndpoints []AWSServiceEndpoint `json:"serviceEndpoints,omitempty"`

	// RolesRef contains references to various AWS IAM roles required to enable
	// integrations such as OIDC.
	//
	// +immutable
	RolesRef AWSRolesRef `json:"rolesRef"`

	// ResourceTags is a list of additional tags to apply to AWS resources created
	// for the cluster. See
	// https://docs.aws.amazon.com/general/latest/gr/aws_tagging.html for
	// information on tagging AWS resources. AWS supports a maximum of 50 tags per
	// resource. OpenShift reserves 25 tags for its use, leaving 25 tags available
	// for the user.
	//
	// +kubebuilder:validation:MaxItems=25
	// +optional
	ResourceTags []AWSResourceTag `json:"resourceTags,omitempty"`

	// EndpointAccess specifies the publishing scope of cluster endpoints. The
	// default is Public.
	//
	// +kubebuilder:validation:Enum=Public;PublicAndPrivate;Private
	// +kubebuilder:default=Public
	// +optional
	EndpointAccess AWSEndpointAccessType `json:"endpointAccess,omitempty"`

	// AdditionalAllowedPrincipals specifies a list of additional allowed principal ARNs
	// to be added to the hosted control plane's VPC Endpoint Service to enable additional
	// VPC Endpoint connection requests to be automatically accepted.
	// See https://docs.aws.amazon.com/vpc/latest/privatelink/configure-endpoint-service.html
	// for more details around VPC Endpoint Service allowed principals.
	//
	// +optional
	AdditionalAllowedPrincipals []string `json:"additionalAllowedPrincipals,omitempty"`

	// MultiArch specifies whether the Hosted Cluster will be expected to support NodePools with different
	// CPU architectures, i.e., supporting arm64 NodePools and supporting amd64 NodePools on the same Hosted Cluster.
	// +kubebuilder:default=false
	// +optional
	MultiArch bool `json:"multiArch"`
}

type AWSRoleCredentials struct {
	ARN       string `json:"arn"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// AWSResourceTag is a tag to apply to AWS resources created for the cluster.
type AWSResourceTag struct {
	// Key is the key of the tag.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z_.:/=+-@]+$`
	Key string `json:"key"`
	// Value is the value of the tag.
	//
	// Some AWS service do not support empty values. Since tags are added to
	// resources in many services, the length of the tag value must meet the
	// requirements of all services.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z_.:/=+-@]+$`
	Value string `json:"value"`
}

// AWSRolesRef contains references to various AWS IAM roles required for operators to make calls against the AWS API.
type AWSRolesRef struct {
	// The referenced role must have a trust relationship that allows it to be assumed via web identity.
	// https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_oidc.html.
	// Example:
	// {
	//		"Version": "2012-10-17",
	//		"Statement": [
	//			{
	//				"Effect": "Allow",
	//				"Principal": {
	//					"Federated": "{{ .ProviderARN }}"
	//				},
	//					"Action": "sts:AssumeRoleWithWebIdentity",
	//				"Condition": {
	//					"StringEquals": {
	//						"{{ .ProviderName }}:sub": {{ .ServiceAccounts }}
	//					}
	//				}
	//			}
	//		]
	//	}
	//
	// IngressARN is an ARN value referencing a role appropriate for the Ingress Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"elasticloadbalancing:DescribeLoadBalancers",
	//				"tag:GetResources",
	//				"route53:ListHostedZones"
	//			],
	//			"Resource": "*"
	//		},
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"route53:ChangeResourceRecordSets"
	//			],
	//			"Resource": [
	//				"arn:aws:route53:::PUBLIC_ZONE_ID",
	//				"arn:aws:route53:::PRIVATE_ZONE_ID"
	//			]
	//		}
	//	]
	// }
	IngressARN string `json:"ingressARN"`

	// ImageRegistryARN is an ARN value referencing a role appropriate for the Image Registry Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"s3:CreateBucket",
	//				"s3:DeleteBucket",
	//				"s3:PutBucketTagging",
	//				"s3:GetBucketTagging",
	//				"s3:PutBucketPublicAccessBlock",
	//				"s3:GetBucketPublicAccessBlock",
	//				"s3:PutEncryptionConfiguration",
	//				"s3:GetEncryptionConfiguration",
	//				"s3:PutLifecycleConfiguration",
	//				"s3:GetLifecycleConfiguration",
	//				"s3:GetBucketLocation",
	//				"s3:ListBucket",
	//				"s3:GetObject",
	//				"s3:PutObject",
	//				"s3:DeleteObject",
	//				"s3:ListBucketMultipartUploads",
	//				"s3:AbortMultipartUpload",
	//				"s3:ListMultipartUploadParts"
	//			],
	//			"Resource": "*"
	//		}
	//	]
	// }
	ImageRegistryARN string `json:"imageRegistryARN"`

	// StorageARN is an ARN value referencing a role appropriate for the Storage Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"ec2:AttachVolume",
	//				"ec2:CreateSnapshot",
	//				"ec2:CreateTags",
	//				"ec2:CreateVolume",
	//				"ec2:DeleteSnapshot",
	//				"ec2:DeleteTags",
	//				"ec2:DeleteVolume",
	//				"ec2:DescribeInstances",
	//				"ec2:DescribeSnapshots",
	//				"ec2:DescribeTags",
	//				"ec2:DescribeVolumes",
	//				"ec2:DescribeVolumesModifications",
	//				"ec2:DetachVolume",
	//				"ec2:ModifyVolume"
	//			],
	//			"Resource": "*"
	//		}
	//	]
	// }
	StorageARN string `json:"storageARN"`

	// NetworkARN is an ARN value referencing a role appropriate for the Network Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"ec2:DescribeInstances",
	//        "ec2:DescribeInstanceStatus",
	//        "ec2:DescribeInstanceTypes",
	//        "ec2:UnassignPrivateIpAddresses",
	//        "ec2:AssignPrivateIpAddresses",
	//        "ec2:UnassignIpv6Addresses",
	//        "ec2:AssignIpv6Addresses",
	//        "ec2:DescribeSubnets",
	//        "ec2:DescribeNetworkInterfaces"
	//			],
	//			"Resource": "*"
	//		}
	//	]
	// }
	NetworkARN string `json:"networkARN"`

	// KubeCloudControllerARN is an ARN value referencing a role appropriate for the KCM/KCC.
	// Source: https://cloud-provider-aws.sigs.k8s.io/prerequisites/#iam-policies
	//
	// The following is an example of a valid policy document:
	//
	//  {
	//  "Version": "2012-10-17",
	//  "Statement": [
	//    {
	//      "Action": [
	//        "autoscaling:DescribeAutoScalingGroups",
	//        "autoscaling:DescribeLaunchConfigurations",
	//        "autoscaling:DescribeTags",
	//        "ec2:DescribeAvailabilityZones",
	//        "ec2:DescribeInstances",
	//        "ec2:DescribeImages",
	//        "ec2:DescribeRegions",
	//        "ec2:DescribeRouteTables",
	//        "ec2:DescribeSecurityGroups",
	//        "ec2:DescribeSubnets",
	//        "ec2:DescribeVolumes",
	//        "ec2:CreateSecurityGroup",
	//        "ec2:CreateTags",
	//        "ec2:CreateVolume",
	//        "ec2:ModifyInstanceAttribute",
	//        "ec2:ModifyVolume",
	//        "ec2:AttachVolume",
	//        "ec2:AuthorizeSecurityGroupIngress",
	//        "ec2:CreateRoute",
	//        "ec2:DeleteRoute",
	//        "ec2:DeleteSecurityGroup",
	//        "ec2:DeleteVolume",
	//        "ec2:DetachVolume",
	//        "ec2:RevokeSecurityGroupIngress",
	//        "ec2:DescribeVpcs",
	//        "elasticloadbalancing:AddTags",
	//        "elasticloadbalancing:AttachLoadBalancerToSubnets",
	//        "elasticloadbalancing:ApplySecurityGroupsToLoadBalancer",
	//        "elasticloadbalancing:CreateLoadBalancer",
	//        "elasticloadbalancing:CreateLoadBalancerPolicy",
	//        "elasticloadbalancing:CreateLoadBalancerListeners",
	//        "elasticloadbalancing:ConfigureHealthCheck",
	//        "elasticloadbalancing:DeleteLoadBalancer",
	//        "elasticloadbalancing:DeleteLoadBalancerListeners",
	//        "elasticloadbalancing:DescribeLoadBalancers",
	//        "elasticloadbalancing:DescribeLoadBalancerAttributes",
	//        "elasticloadbalancing:DetachLoadBalancerFromSubnets",
	//        "elasticloadbalancing:DeregisterInstancesFromLoadBalancer",
	//        "elasticloadbalancing:ModifyLoadBalancerAttributes",
	//        "elasticloadbalancing:RegisterInstancesWithLoadBalancer",
	//        "elasticloadbalancing:SetLoadBalancerPoliciesForBackendServer",
	//        "elasticloadbalancing:AddTags",
	//        "elasticloadbalancing:CreateListener",
	//        "elasticloadbalancing:CreateTargetGroup",
	//        "elasticloadbalancing:DeleteListener",
	//        "elasticloadbalancing:DeleteTargetGroup",
	//        "elasticloadbalancing:DeregisterTargets",
	//        "elasticloadbalancing:DescribeListeners",
	//        "elasticloadbalancing:DescribeLoadBalancerPolicies",
	//        "elasticloadbalancing:DescribeTargetGroups",
	//        "elasticloadbalancing:DescribeTargetHealth",
	//        "elasticloadbalancing:ModifyListener",
	//        "elasticloadbalancing:ModifyTargetGroup",
	//        "elasticloadbalancing:RegisterTargets",
	//        "elasticloadbalancing:SetLoadBalancerPoliciesOfListener",
	//        "iam:CreateServiceLinkedRole",
	//        "kms:DescribeKey"
	//      ],
	//      "Resource": [
	//        "*"
	//      ],
	//      "Effect": "Allow"
	//    }
	//  ]
	// }
	// +immutable
	KubeCloudControllerARN string `json:"kubeCloudControllerARN"`

	// NodePoolManagementARN is an ARN value referencing a role appropriate for the CAPI Controller.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//   "Version": "2012-10-17",
	//  "Statement": [
	//    {
	//      "Action": [
	//        "ec2:AssociateRouteTable",
	//        "ec2:AttachInternetGateway",
	//        "ec2:AuthorizeSecurityGroupIngress",
	//        "ec2:CreateInternetGateway",
	//        "ec2:CreateNatGateway",
	//        "ec2:CreateRoute",
	//        "ec2:CreateRouteTable",
	//        "ec2:CreateSecurityGroup",
	//        "ec2:CreateSubnet",
	//        "ec2:CreateTags",
	//        "ec2:DeleteInternetGateway",
	//        "ec2:DeleteNatGateway",
	//        "ec2:DeleteRouteTable",
	//        "ec2:DeleteSecurityGroup",
	//        "ec2:DeleteSubnet",
	//        "ec2:DeleteTags",
	//        "ec2:DescribeAccountAttributes",
	//        "ec2:DescribeAddresses",
	//        "ec2:DescribeAvailabilityZones",
	//        "ec2:DescribeImages",
	//        "ec2:DescribeInstances",
	//        "ec2:DescribeInternetGateways",
	//        "ec2:DescribeNatGateways",
	//        "ec2:DescribeNetworkInterfaces",
	//        "ec2:DescribeNetworkInterfaceAttribute",
	//        "ec2:DescribeRouteTables",
	//        "ec2:DescribeSecurityGroups",
	//        "ec2:DescribeSubnets",
	//        "ec2:DescribeVpcs",
	//        "ec2:DescribeVpcAttribute",
	//        "ec2:DescribeVolumes",
	//        "ec2:DetachInternetGateway",
	//        "ec2:DisassociateRouteTable",
	//        "ec2:DisassociateAddress",
	//        "ec2:ModifyInstanceAttribute",
	//        "ec2:ModifyNetworkInterfaceAttribute",
	//        "ec2:ModifySubnetAttribute",
	//        "ec2:RevokeSecurityGroupIngress",
	//        "ec2:RunInstances",
	//        "ec2:TerminateInstances",
	//        "tag:GetResources",
	//        "ec2:CreateLaunchTemplate",
	//        "ec2:CreateLaunchTemplateVersion",
	//        "ec2:DescribeLaunchTemplates",
	//        "ec2:DescribeLaunchTemplateVersions",
	//        "ec2:DeleteLaunchTemplate",
	//        "ec2:DeleteLaunchTemplateVersions"
	//      ],
	//      "Resource": [
	//        "*"
	//      ],
	//      "Effect": "Allow"
	//    },
	//    {
	//      "Condition": {
	//        "StringLike": {
	//          "iam:AWSServiceName": "elasticloadbalancing.amazonaws.com"
	//        }
	//      },
	//      "Action": [
	//        "iam:CreateServiceLinkedRole"
	//      ],
	//      "Resource": [
	//        "arn:*:iam::*:role/aws-service-role/elasticloadbalancing.amazonaws.com/AWSServiceRoleForElasticLoadBalancing"
	//      ],
	//      "Effect": "Allow"
	//    },
	//    {
	//      "Action": [
	//        "iam:PassRole"
	//      ],
	//      "Resource": [
	//        "arn:*:iam::*:role/*-worker-role"
	//      ],
	//      "Effect": "Allow"
	//    },
	// 	  {
	// 	  	"Effect": "Allow",
	// 	  	"Action": [
	// 	  		"kms:Decrypt",
	// 	  		"kms:ReEncrypt",
	// 	  		"kms:GenerateDataKeyWithoutPlainText",
	// 	  		"kms:DescribeKey"
	// 	  	],
	// 	  	"Resource": "*"
	// 	  },
	// 	  {
	// 	  	"Effect": "Allow",
	// 	  	"Action": [
	// 	  		"kms:CreateGrant"
	// 	  	],
	// 	  	"Resource": "*",
	// 	  	"Condition": {
	// 	  		"Bool": {
	// 	  			"kms:GrantIsForAWSResource": true
	// 	  		}
	// 	  	}
	// 	  }
	//  ]
	// }
	//
	// +immutable
	NodePoolManagementARN string `json:"nodePoolManagementARN"`

	// ControlPlaneOperatorARN  is an ARN value referencing a role appropriate for the Control Plane Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"ec2:CreateVpcEndpoint",
	//				"ec2:DescribeVpcEndpoints",
	//				"ec2:ModifyVpcEndpoint",
	//				"ec2:DeleteVpcEndpoints",
	//				"ec2:CreateTags",
	//				"route53:ListHostedZones",
	//				"ec2:CreateSecurityGroup",
	//				"ec2:AuthorizeSecurityGroupIngress",
	//				"ec2:AuthorizeSecurityGroupEgress",
	//				"ec2:DeleteSecurityGroup",
	//				"ec2:RevokeSecurityGroupIngress",
	//				"ec2:RevokeSecurityGroupEgress",
	//				"ec2:DescribeSecurityGroups",
	//				"ec2:DescribeVpcs",
	//			],
	//			"Resource": "*"
	//		},
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"route53:ChangeResourceRecordSets",
	//				"route53:ListResourceRecordSets"
	//			],
	//			"Resource": "arn:aws:route53:::%s"
	//		}
	//	]
	// }
	// +immutable
	ControlPlaneOperatorARN string `json:"controlPlaneOperatorARN"`
}

// AWSServiceEndpoint stores the configuration for services to
// override existing defaults of AWS Services.
type AWSServiceEndpoint struct {
	// Name is the name of the AWS service.
	// This must be provided and cannot be empty.
	Name string `json:"name"`

	// URL is fully qualified URI with scheme https, that overrides the default generated
	// endpoint for a client.
	// This must be provided and cannot be empty.
	//
	// +kubebuilder:validation:Pattern=`^https://`
	URL string `json:"url"`
}

// AzurePlatformSpec specifies configuration for clusters running on Azure. Generally, the HyperShift API assumes bring
// your own (BYO) cloud infrastructure resources. For example, resources like a resource group, a subnet, or a vnet
// would be pre-created and then their names would be used respectively in the ResourceGroupName, SubnetName, VnetName
// fields of the Hosted Cluster CR. An existing cloud resource is expected to exist under the same SubscriptionID.
type AzurePlatformSpec struct {
	// Credentials is the object containing existing Azure credentials needed for creating and managing cloud
	// infrastructure resources.
	//
	// +kubebuilder:validation:Required
	// +required
	Credentials corev1.LocalObjectReference `json:"credentials"`

	// Cloud is the cloud environment identifier, valid values could be found here: https://github.com/Azure/go-autorest/blob/4c0e21ca2bbb3251fe7853e6f9df6397f53dd419/autorest/azure/environments.go#L33
	//
	// +kubebuilder:validation:Enum=AzurePublicCloud;AzureUSGovernmentCloud;AzureChinaCloud;AzureGermanCloud;AzureStackCloud
	// +kubebuilder:default="AzurePublicCloud"
	Cloud string `json:"cloud,omitempty"`

	// Location is the Azure region in where all the cloud infrastructure resources will be created.
	//
	// Example: eastus
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Location is immutable"
	// +immutable
	// +required
	Location string `json:"location"`

	// ResourceGroupName is the name of an existing resource group where all cloud resources created by the Hosted
	// Cluster are to be placed. The resource group is expected to exist under the same subscription as SubscriptionID.
	//
	// In ARO HCP, this will be the managed resource group where customer cloud resources will be created.
	//
	// Resource group naming requirements can be found here: https://azure.github.io/PSRule.Rules.Azure/en/rules/Azure.ResourceGroup.Name/.
	//
	//Example: if your resource group ID is /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>, your
	//          ResourceGroupName is <resourceGroupName>.
	//
	// +kubebuilder:default:=default
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_()\-\.]{1,89}[a-zA-Z0-9_()\-]$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ResourceGroupName is immutable"
	// +immutable
	// +required
	ResourceGroupName string `json:"resourceGroup"`

	// VnetID is the ID of an existing VNET to use in creating VMs. The VNET can exist in a different resource group
	// other than the one specified in ResourceGroupName, but it must exist under the same subscription as
	// SubscriptionID.
	//
	// In ARO HCP, this will be the ID of the customer provided VNET.
	//
	// Example: /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Network/virtualNetworks/<vnetName>
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="VnetID is immutable"
	// +immutable
	// +required
	VnetID string `json:"vnetID,omitempty"`

	// SubnetID is the subnet ID of an existing subnet where the load balancer for node egress will be created. This
	// subnet is expected to be a subnet within the VNET specified in VnetID. This subnet is expected to exist under the
	// same subscription as SubscriptionID.
	//
	// In ARO HCP, managed services will create the aforementioned load balancer in ResourceGroupName.
	//
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="SubnetID is immutable"
	// +kubebuilder:validation:Required
	// +immutable
	// +required
	SubnetID string `json:"subnetID"`

	// SubscriptionID is a unique identifier for an Azure subscription used to manage resources.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="SubscriptionID is immutable"
	// +immutable
	// +required
	SubscriptionID string `json:"subscriptionID"`

	// MachineIdentityID is used as the user-assigned identity to be assigned to the VMs
	//
	// +optional
	MachineIdentityID string `json:"machineIdentityID,omitempty"`

	// SecurityGroupID is the ID of an existing security group on the SubnetID. This field is provided as part of the
	// configuration for the Azure cloud provider, aka Azure cloud controller manager (CCM). This security group is
	// expected to exist under the same subscription as SubscriptionID.
	//
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="SecurityGroupID is immutable"
	// +kubebuilder:validation:Required
	// +immutable
	// +required
	SecurityGroupID string `json:"securityGroupID,omitempty"`
}

// Release represents the metadata for an OCP release payload image.
type Release struct {
	// Image is the image pullspec of an OCP release payload image.
	//
	// +kubebuilder:validation:Pattern=^(\w+\S+)$
	Image string `json:"image"`
}

// ClusterAutoscaling specifies auto-scaling behavior that applies to all
// NodePools associated with a control plane.
type ClusterAutoscaling struct {
	// MaxNodesTotal is the maximum allowable number of nodes across all NodePools
	// for a HostedCluster. The autoscaler will not grow the cluster beyond this
	// number.
	//
	// +kubebuilder:validation:Minimum=0
	MaxNodesTotal *int32 `json:"maxNodesTotal,omitempty"`

	// MaxPodGracePeriod is the maximum seconds to wait for graceful pod
	// termination before scaling down a NodePool. The default is 600 seconds.
	//
	// +kubebuilder:validation:Minimum=0
	MaxPodGracePeriod *int32 `json:"maxPodGracePeriod,omitempty"`

	// MaxNodeProvisionTime is the maximum time to wait for node provisioning
	// before considering the provisioning to be unsuccessful, expressed as a Go
	// duration string. The default is 15 minutes.
	//
	// +kubebuilder:validation:Pattern=^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$
	MaxNodeProvisionTime string `json:"maxNodeProvisionTime,omitempty"`

	// PodPriorityThreshold enables users to schedule "best-effort" pods, which
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
type EtcdSpec struct {
	// ManagementType defines how the etcd cluster is managed.
	//
	// +unionDiscriminator
	// +immutable
	ManagementType EtcdManagementType `json:"managementType"`

	// Managed specifies the behavior of an etcd cluster managed by HyperShift.
	//
	// +optional
	// +immutable
	Managed *ManagedEtcdSpec `json:"managed,omitempty"`

	// Unmanaged specifies configuration which enables the control plane to
	// integrate with an eternally managed etcd cluster.
	//
	// +optional
	// +immutable
	Unmanaged *UnmanagedEtcdSpec `json:"unmanaged,omitempty"`
}

// ManagedEtcdSpec specifies the behavior of an etcd cluster managed by
// HyperShift.
type ManagedEtcdSpec struct {
	// Storage specifies how etcd data is persisted.
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
	// Type is the kind of persistent storage implementation to use for etcd.
	//
	// +immutable
	// +unionDiscriminator
	Type ManagedEtcdStorageType `json:"type"`

	// PersistentVolume is the configuration for PersistentVolume etcd storage.
	// With this implementation, a PersistentVolume will be allocated for every
	// etcd member (either 1 or 3 depending on the HostedCluster control plane
	// availability configuration).
	//
	// +optional
	PersistentVolume *PersistentVolumeEtcdStorageSpec `json:"persistentVolume,omitempty"`

	// RestoreSnapshotURL allows an optional URL to be provided where
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
	// StorageClassName is the StorageClass of the data volume for each etcd member.
	//
	// See https://kubernetes.io/docs/concepts/storage/persistent-volumes#class-1.
	//
	// +optional
	// +immutable
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Size is the minimum size of the data volume for each etcd member.
	//
	// +optional
	// +kubebuilder:default="8Gi"
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="Etcd PV storage size is immutable"
	Size *resource.Quantity `json:"size,omitempty"`
}

// UnmanagedEtcdSpec specifies configuration which enables the control plane to
// integrate with an eternally managed etcd cluster.
type UnmanagedEtcdSpec struct {
	// Endpoint is the full etcd cluster client endpoint URL. For example:
	//
	//     https://etcd-client:2379
	//
	// If the URL uses an HTTPS scheme, the TLS field is required.
	//
	// +kubebuilder:validation:Pattern=`^https://`
	Endpoint string `json:"endpoint"`

	// TLS specifies TLS configuration for HTTPS etcd client endpoints.
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

// AzureKMSSpec defines metadata about the configuration of the Azure KMS Secret Encryption provider using Azure key vault
type AzureKMSSpec struct {
	// ActiveKey defines the active key used to encrypt new secrets
	//
	// +kubebuilder:validation:Required
	ActiveKey AzureKMSKey `json:"activeKey"`
	// BackupKey defines the old key during the rotation process so previously created
	// secrets can continue to be decrypted until they are all re-encrypted with the active key.
	// +optional
	BackupKey *AzureKMSKey `json:"backupKey,omitempty"`
}

type AzureKMSKey struct {
	// KeyVaultName is the name of the keyvault. Must match criteria specified at https://docs.microsoft.com/en-us/azure/key-vault/general/about-keys-secrets-certificates#vault-name-and-object-name
	// Your Microsoft Entra application used to create the cluster must be authorized to access this keyvault, e.g using the AzureCLI:
	// `az keyvault set-policy -n $KEYVAULT_NAME --key-permissions decrypt encrypt --spn <YOUR APPLICATION CLIENT ID>`
	KeyVaultName string `json:"keyVaultName"`
	// KeyName is the name of the keyvault key used for encrypt/decrypt
	KeyName string `json:"keyName"`
	// KeyVersion contains the version of the key to use
	KeyVersion string `json:"keyVersion"`
}

// IBMCloudKMSSpec defines metadata for the IBM Cloud KMS encryption strategy
type IBMCloudKMSSpec struct {
	// Region is the IBM Cloud region
	Region string `json:"region"`
	// Auth defines metadata for how authentication is done with IBM Cloud KMS
	Auth IBMCloudKMSAuthSpec `json:"auth"`
	// KeyList defines the list of keys used for data encryption
	KeyList []IBMCloudKMSKeyEntry `json:"keyList"`
}

// IBMCloudKMSKeyEntry defines metadata for an IBM Cloud KMS encryption key
type IBMCloudKMSKeyEntry struct {
	// CRKID is the customer rook key id
	CRKID string `json:"crkID"`
	// InstanceID is the id for the key protect instance
	InstanceID string `json:"instanceID"`
	// CorrelationID is an identifier used to track all api call usage from hypershift
	CorrelationID string `json:"correlationID"`
	// URL is the url to call key protect apis over
	// +kubebuilder:validation:Pattern=`^https://`
	URL string `json:"url"`
	// KeyVersion is a unique number associated with the key. The number increments whenever a new
	// key is enabled for data encryption.
	KeyVersion int `json:"keyVersion"`
}

// IBMCloudKMSAuthSpec defines metadata for how authentication is done with IBM Cloud KMS
type IBMCloudKMSAuthSpec struct {
	// Type defines the IBM Cloud KMS authentication strategy
	// +unionDiscriminator
	Type IBMCloudKMSAuthType `json:"type"`
	// Unmanaged defines the auth metadata the customer provides to interact with IBM Cloud KMS
	// +optional
	Unmanaged *IBMCloudKMSUnmanagedAuthSpec `json:"unmanaged,omitempty"`
	// Managed defines metadata around the service to service authentication strategy for the IBM Cloud
	// KMS system (all provider managed).
	// +optional
	Managed *IBMCloudKMSManagedAuthSpec `json:"managed,omitempty"`
}

// IBMCloudKMSAuthType defines the IBM Cloud KMS authentication strategy
// +kubebuilder:validation:Enum=Managed;Unmanaged
type IBMCloudKMSAuthType string

const (
	// IBMCloudKMSManagedAuth defines the KMS authentication strategy where the IKS/ROKS platform uses
	// service to service auth to call IBM Cloud KMS APIs (no customer credentials requried)
	IBMCloudKMSManagedAuth IBMCloudKMSAuthType = "Managed"
	// IBMCloudKMSUnmanagedAuth defines the KMS authentication strategy where a customer supplies IBM Cloud
	// authentication to interact with IBM Cloud KMS APIs
	IBMCloudKMSUnmanagedAuth IBMCloudKMSAuthType = "Unmanaged"
)

// IBMCloudKMSUnmanagedAuthSpec defines the auth metadata the customer provides to interact with IBM Cloud KMS
type IBMCloudKMSUnmanagedAuthSpec struct {
	// Credentials should reference a secret with a key field of IBMCloudIAMAPIKeySecretKey that contains a apikey to
	// call IBM Cloud KMS APIs
	Credentials corev1.LocalObjectReference `json:"credentials"`
}

// IBMCloudKMSManagedAuthSpec defines metadata around the service to service authentication strategy for the IBM Cloud
// KMS system (all provider managed).
type IBMCloudKMSManagedAuthSpec struct {
}

// AWSKMSSpec defines metadata about the configuration of the AWS KMS Secret Encryption provider
type AWSKMSSpec struct {
	// Region contains the AWS region
	Region string `json:"region"`
	// ActiveKey defines the active key used to encrypt new secrets
	ActiveKey AWSKMSKeyEntry `json:"activeKey"`
	// BackupKey defines the old key during the rotation process so previously created
	// secrets can continue to be decrypted until they are all re-encrypted with the active key.
	// +optional
	BackupKey *AWSKMSKeyEntry `json:"backupKey,omitempty"`
	// Auth defines metadata about the management of credentials used to interact with AWS KMS
	Auth AWSKMSAuthSpec `json:"auth"`
}

// AWSKMSAuthSpec defines metadata about the management of credentials used to interact and encrypt data via AWS KMS key.
type AWSKMSAuthSpec struct {
	// The referenced role must have a trust relationship that allows it to be assumed via web identity.
	// https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_oidc.html.
	// Example:
	// {
	//		"Version": "2012-10-17",
	//		"Statement": [
	//			{
	//				"Effect": "Allow",
	//				"Principal": {
	//					"Federated": "{{ .ProviderARN }}"
	//				},
	//					"Action": "sts:AssumeRoleWithWebIdentity",
	//				"Condition": {
	//					"StringEquals": {
	//						"{{ .ProviderName }}:sub": {{ .ServiceAccounts }}
	//					}
	//				}
	//			}
	//		]
	//	}
	//
	// AWSKMSARN is an ARN value referencing a role appropriate for managing the auth via the AWS KMS key.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//    	{
	//			"Effect": "Allow",
	//			"Action": [
	//				"kms:Encrypt",
	//				"kms:Decrypt",
	//				"kms:ReEncrypt*",
	//				"kms:GenerateDataKey*",
	//				"kms:DescribeKey"
	//			],
	//			"Resource": %q
	//		}
	//	]
	// }
	AWSKMSRoleARN string `json:"awsKms"`
}

// AWSKMSKeyEntry defines metadata to locate the encryption key in AWS
type AWSKMSKeyEntry struct {
	// ARN is the Amazon Resource Name for the encryption key
	// +kubebuilder:validation:Pattern=`^arn:`
	ARN string `json:"arn"`
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

	// Platform contains platform-specific status of the HostedCluster
	// +optional
	Platform *PlatformStatus `json:"platform,omitempty"`
}

// PlatformStatus contains platform-specific status
type PlatformStatus struct {
	// +optional
	AWS *AWSPlatformStatus `json:"aws,omitempty"`
}

// AWSPlatformStatus contains status specific to the AWS platform
type AWSPlatformStatus struct {
	// DefaultWorkerSecurityGroupID is the ID of a security group created by
	// the control plane operator. It is always added to worker machines in
	// addition to any security groups specified in the NodePool.
	// +optional
	DefaultWorkerSecurityGroupID string `json:"defaultWorkerSecurityGroupID,omitempty"`
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

// +kubebuilder:object:root=true
// HostedClusterList contains a list of HostedCluster
type HostedClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedCluster `json:"items"`
}
