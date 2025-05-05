package v1beta1

import (
	"fmt"

	"github.com/openshift/hypershift/api/ibmcapi"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// PowerVSNodePoolProcType defines processor type to be used for PowerVSNodePoolPlatform
type PowerVSNodePoolProcType string

func (p *PowerVSNodePoolProcType) String() string {
	return string(*p)
}

func (p *PowerVSNodePoolProcType) Set(s string) error {
	switch s {
	case string(PowerVSNodePoolSharedProcType), string(PowerVSNodePoolCappedProcType), string(PowerVSNodePoolDedicatedProcType):
		*p = PowerVSNodePoolProcType(s)
		return nil
	default:
		return fmt.Errorf("unknown processor type used %s", s)
	}
}

func (p *PowerVSNodePoolProcType) Type() string {
	return "PowerVSNodePoolProcType"
}

const (
	// PowerVSNodePoolDedicatedProcType defines dedicated processor type
	PowerVSNodePoolDedicatedProcType = PowerVSNodePoolProcType("dedicated")

	// PowerVSNodePoolSharedProcType defines shared processor type
	PowerVSNodePoolSharedProcType = PowerVSNodePoolProcType("shared")

	// PowerVSNodePoolCappedProcType defines capped processor type
	PowerVSNodePoolCappedProcType = PowerVSNodePoolProcType("capped")
)

func (p *PowerVSNodePoolProcType) CastToCAPIPowerVSProcessorType() ibmcapi.PowerVSProcessorType {
	switch *p {
	case PowerVSNodePoolDedicatedProcType:
		return ibmcapi.PowerVSProcessorTypeDedicated
	case PowerVSNodePoolCappedProcType:
		return ibmcapi.PowerVSProcessorTypeCapped
	default:
		return ibmcapi.PowerVSProcessorTypeShared
	}
}

// PowerVSNodePoolStorageType defines storage type to be used for PowerVSNodePoolPlatform
type PowerVSNodePoolStorageType string

// PowerVSNodePoolImageDeletePolicy defines image delete policy to be used for PowerVSNodePoolPlatform
type PowerVSNodePoolImageDeletePolicy string

// PowerVSNodePoolPlatform specifies the configuration of a NodePool when operating
// on IBMCloud PowerVS platform.
type PowerVSNodePoolPlatform struct {
	// systemType is the System type used to host the instance.
	// systemType determines the number of cores and memory that is available.
	// Few of the supported SystemTypes are s922,e880,e980.
	// e880 systemType available only in Dallas Datacenters.
	// e980 systemType available in Datacenters except Dallas and Washington.
	// When omitted, this means that the user has no opinion and the platform is left to choose a
	// reasonable default. The current default is s922 which is generally available.
	//
	// +optional
	// +kubebuilder:default=s922
	// +kubebuilder:validation:MaxLength=255
	SystemType string `json:"systemType,omitempty"`

	// processorType is the VM instance processor type.
	// It must be set to one of the following values: Dedicated, Capped or Shared.
	//
	// Dedicated: resources are allocated for a specific client, The hypervisor makes a 1:1 binding of a partition's processor to a physical processor core.
	// Shared: Shared among other clients.
	// Capped: Shared, but resources do not expand beyond those that are requested, the amount of CPU time is Capped to the value specified for the entitlement.
	//
	// if the processorType is selected as Dedicated, then Processors value cannot be fractional.
	// When omitted, this means that the user has no opinion and the platform is left to choose a
	// reasonable default. The current default is shared.
	//
	// +kubebuilder:default=shared
	// +kubebuilder:validation:Enum=dedicated;shared;capped
	// +optional
	ProcessorType PowerVSNodePoolProcType `json:"processorType,omitempty"`

	// processors is the number of virtual processors in a virtual machine.
	// when the processorType is selected as Dedicated the processors value cannot be fractional.
	// maximum value for the Processors depends on the selected SystemType.
	// when SystemType is set to e880 or e980 maximum Processors value is 143.
	// when SystemType is set to s922 maximum Processors value is 15.
	// minimum value for Processors depends on the selected ProcessorType.
	// when ProcessorType is set as Shared or Capped, The minimum processors is 0.5.
	// when ProcessorType is set as Dedicated, The minimum processors is 1.
	// When omitted, this means that the user has no opinion and the platform is left to choose a
	// reasonable default. The default is set based on the selected ProcessorType.
	// when ProcessorType selected as Dedicated, the default is set to 1.
	// when ProcessorType selected as Shared or Capped, the default is set to 0.5.
	//
	// +optional
	// +kubebuilder:default="0.5"
	Processors intstr.IntOrString `json:"processors,omitempty"`

	// memoryGiB is the size of a virtual machine's memory, in GiB.
	// maximum value for the MemoryGiB depends on the selected SystemType.
	// when SystemType is set to e880 maximum MemoryGiB value is 7463 GiB.
	// when SystemType is set to e980 maximum MemoryGiB value is 15307 GiB.
	// when SystemType is set to s922 maximum MemoryGiB value is 942 GiB.
	// The minimum memory is 32 GiB.
	//
	// When omitted, this means the user has no opinion and the platform is left to choose a reasonable
	// default. The current default is 32.
	//
	// +optional
	// +kubebuilder:default=32
	MemoryGiB int32 `json:"memoryGiB,omitempty"`

	// image used for deploying the nodes. If unspecified, the default
	// is chosen based on the NodePool release payload image.
	//
	// +optional
	Image *PowerVSResourceReference `json:"image,omitempty"`

	// storageType for the image and nodes, this will be ignored if Image is specified.
	// The storage tiers in PowerVS are based on I/O operations per second (IOPS).
	// It means that the performance of your storage volumes is limited to the maximum number of IOPS based on volume size and storage tier.
	// Although, the exact numbers might change over time, the Tier 3 storage is currently set to 3 IOPS/GB, and the Tier 1 storage is currently set to 10 IOPS/GB.
	//
	// The default is tier1
	//
	// +kubebuilder:default=tier1
	// +kubebuilder:validation:Enum=tier1;tier3
	// +optional
	StorageType PowerVSNodePoolStorageType `json:"storageType,omitempty"`

	// imageDeletePolicy is policy for the image deletion.
	//
	// delete: delete the image from the infrastructure.
	// retain: delete the image from the openshift but retain in the infrastructure.
	//
	// The default is delete
	//
	// +kubebuilder:default=delete
	// +kubebuilder:validation:Enum=delete;retain
	// +optional
	ImageDeletePolicy PowerVSNodePoolImageDeletePolicy `json:"imageDeletePolicy,omitempty"`
}

// PowerVSPlatformSpec defines IBMCloud PowerVS specific settings for components
type PowerVSPlatformSpec struct {
	// accountID is the IBMCloud account id.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +kubebuilder:validation:MaxLength=255
	// +required
	AccountID string `json:"accountID"`

	// cisInstanceCRN is the IBMCloud CIS Service Instance's Cloud Resource Name
	// This field is immutable. Once set, It can't be changed.
	//
	// +kubebuilder:validation:Pattern=`^crn:`
	// +kubebuilder:validation:MaxLength=255
	// +immutable
	// +required
	CISInstanceCRN string `json:"cisInstanceCRN"`

	// resourceGroup is the IBMCloud Resource Group in which the cluster resides.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +kubebuilder:validation:MaxLength=255
	// +required
	ResourceGroup string `json:"resourceGroup"`

	// region is the IBMCloud region in which the cluster resides. This configures the
	// OCP control plane cloud integrations, and is used by NodePool to resolve
	// the correct boot image for a given release.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +kubebuilder:validation:MaxLength=255
	// +required
	Region string `json:"region"`

	// zone is the availability zone where control plane cloud resources are
	// created.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +kubebuilder:validation:MaxLength=255
	// +required
	Zone string `json:"zone"`

	// subnet is the subnet to use for control plane cloud resources.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +required
	Subnet *PowerVSResourceReference `json:"subnet"`

	// serviceInstanceID is the reference to the Power VS service on which the server instance(VM) will be created.
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
	// +kubebuilder:validation:MaxLength=255
	// +required
	ServiceInstanceID string `json:"serviceInstanceID"`

	// vpc specifies IBM Cloud PowerVS Load Balancing configuration for the control
	// plane.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +required
	VPC *PowerVSVPC `json:"vpc"`

	// kubeCloudControllerCreds is a reference to a secret containing cloud
	// credentials with permissions matching the cloud controller policy.
	// This field is immutable. Once set, It can't be changed.
	//
	// TODO(dan): document the "cloud controller policy"
	//
	// +immutable
	// +required
	KubeCloudControllerCreds corev1.LocalObjectReference `json:"kubeCloudControllerCreds"`

	// nodePoolManagementCreds is a reference to a secret containing cloud
	// credentials with permissions matching the node pool management policy.
	// This field is immutable. Once set, It can't be changed.
	//
	// TODO(dan): document the "node pool management policy"
	//
	// +immutable
	// +required
	NodePoolManagementCreds corev1.LocalObjectReference `json:"nodePoolManagementCreds"`

	// ingressOperatorCloudCreds is a reference to a secret containing ibm cloud
	// credentials for ingress operator to get authenticated with ibm cloud.
	//
	// +immutable
	// +required
	IngressOperatorCloudCreds corev1.LocalObjectReference `json:"ingressOperatorCloudCreds"`

	// storageOperatorCloudCreds is a reference to a secret containing ibm cloud
	// credentials for storage operator to get authenticated with ibm cloud.
	//
	// +immutable
	// +required
	StorageOperatorCloudCreds corev1.LocalObjectReference `json:"storageOperatorCloudCreds"`

	// imageRegistryOperatorCloudCreds is a reference to a secret containing ibm cloud
	// credentials for image registry operator to get authenticated with ibm cloud.
	//
	// +immutable
	// +required
	ImageRegistryOperatorCloudCreds corev1.LocalObjectReference `json:"imageRegistryOperatorCloudCreds"`
}

// PowerVSVPC specifies IBM Cloud PowerVS LoadBalancer configuration for the control
// plane.
type PowerVSVPC struct {
	// name for VPC to used for all the service load balancer.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +kubebuilder:validation:MaxLength=255
	// +required
	Name string `json:"name"`

	// region is the IBMCloud region in which VPC gets created, this VPC used for all the ingress traffic
	// into the OCP cluster.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +kubebuilder:validation:MaxLength=255
	// +required
	Region string `json:"region"`

	// zone is the availability zone where load balancer cloud resources are
	// created.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Zone string `json:"zone,omitempty"`

	// subnet is the subnet to use for load balancer.
	// This field is immutable. Once set, It can't be changed.
	//
	// +immutable
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Subnet string `json:"subnet,omitempty"`
}

// PowerVSResourceReference is a reference to a specific IBMCloud PowerVS resource by ID, or Name.
// Only one of ID, or Name may be specified. Specifying more than one will result in
// a validation error.
type PowerVSResourceReference struct {
	// id of resource
	// +optional
	// +kubebuilder:validation:MaxLength=255
	ID *string `json:"id,omitempty"`

	// name of resource
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Name *string `json:"name,omitempty"`
}
