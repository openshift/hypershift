package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	QoSClassBurstable  QoSClass = "Burstable"
	QoSClassGuaranteed QoSClass = "Guaranteed"
)

type QoSClass string

// KubevirtCompute contains values associated with the virtual compute hardware requested for the VM.
type KubevirtCompute struct {
	// memory represents how much guest memory the VM should have
	//
	// +optional
	// +kubebuilder:default="8Gi"
	Memory *resource.Quantity `json:"memory,omitempty"`

	// cores is the number of CPU cores for the KubeVirt VM.
	// +kubebuilder:validation:Minimum=1
	// +optional
	Cores *uint32 `json:"cores,omitempty"`

	// qosClass if set to "Guaranteed", requests the scheduler to place the VirtualMachineInstance on a node with
	// limit memory and CPU, equal to be the requested values, to set the VMI as a Guaranteed QoS Class;
	// See here for more details:
	// https://kubevirt.io/user-guide/operations/node_overcommit/#requesting-the-right-qos-class-for-virtualmachineinstances
	//
	// +optional
	// +kubebuilder:validation:Enum=Burstable;Guaranteed
	// +kubebuilder:default=Burstable
	QosClass *QoSClass `json:"qosClass,omitempty"`
}

// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany;ReadOnly;ReadWriteOncePod
type PersistentVolumeAccessMode corev1.PersistentVolumeAccessMode

// KubevirtPersistentVolume contains the values involved with provisioning persistent storage for a KubeVirt VM.
type KubevirtPersistentVolume struct {
	// size is the size of the persistent storage volume
	//
	// +optional
	// +kubebuilder:default="32Gi"
	Size *resource.Quantity `json:"size,omitempty"`
	// storageClass is the storageClass used for the underlying PVC that hosts the volume
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	StorageClass *string `json:"storageClass,omitempty"`
	// accessModes is an array that contains the desired Access Modes the root volume should have.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes
	//
	// +optional
	// +kubebuilder:validation:MaxItems=10
	AccessModes []PersistentVolumeAccessMode `json:"accessModes,omitempty"`
	// volumeMode defines what type of volume is required by the claim.
	// Value of Filesystem is implied when not included in claim spec.
	// +optional
	// +kubebuilder:validation:Enum=Filesystem;Block
	VolumeMode *corev1.PersistentVolumeMode `json:"volumeMode,omitempty"`
}

// KubevirtCachingStrategyType is the type of the boot image caching mechanism for the KubeVirt provider
type KubevirtCachingStrategyType string

const (
	// KubevirtCachingStrategyNone means that hypershift will not cache the boot image
	KubevirtCachingStrategyNone KubevirtCachingStrategyType = "None"

	// KubevirtCachingStrategyPVC means that hypershift will cache the boot image into a PVC; only relevant when using
	// a QCOW boot image, and is ignored when using a container image
	KubevirtCachingStrategyPVC KubevirtCachingStrategyType = "PVC"
)

// KubevirtCachingStrategy defines the boot image caching strategy
type KubevirtCachingStrategy struct {
	// type is the type of the caching strategy
	// +kubebuilder:default=None
	// +kubebuilder:validation:Enum=None;PVC
	// +required
	Type KubevirtCachingStrategyType `json:"type"`
}

// KubevirtRootVolume represents the volume that the rhcos disk will be stored and run from.
type KubevirtRootVolume struct {
	// diskImage represents what rhcos image to use for the node pool
	//
	// +optional
	Image *KubevirtDiskImage `json:"diskImage,omitempty"`

	// kubevirtVolume represents of type of storage to run the image on
	KubevirtVolume `json:",inline"`

	// cacheStrategy defines the boot image caching strategy. Default - no caching
	// +optional
	CacheStrategy *KubevirtCachingStrategy `json:"cacheStrategy,omitempty"`
}

// KubevirtVolumeType is a specific supported KubeVirt volumes
//
// +kubebuilder:validation:Enum=Persistent
type KubevirtVolumeType string

const (
	// KubevirtVolumeTypePersistent represents persistent volume for kubevirt VMs
	KubevirtVolumeTypePersistent KubevirtVolumeType = "Persistent"
)

// KubevirtVolume represents what kind of storage to use for a KubeVirt VM volume
type KubevirtVolume struct {
	// type represents the type of storage to associate with the kubevirt VMs.
	//
	// +optional
	// +unionDiscriminator
	// +kubebuilder:default=Persistent
	Type KubevirtVolumeType `json:"type,omitempty"`

	// persistent volume type means the VM's storage is backed by a PVC
	// VMs that use persistent volumes can survive disruption events like restart and eviction
	// This is the default type used when no storage type is defined.
	//
	// +optional
	Persistent *KubevirtPersistentVolume `json:"persistent,omitempty"`
}

// KubevirtDiskImage contains values representing where the rhcos image is located
type KubevirtDiskImage struct {
	// containerDiskImage is a string representing the container image that holds the root disk
	//
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	ContainerDiskImage *string `json:"containerDiskImage,omitempty"`
}

type MultiQueueSetting string

const (
	MultiQueueEnable  MultiQueueSetting = "Enable"
	MultiQueueDisable MultiQueueSetting = "Disable"
)

// KubevirtNodePoolPlatform specifies the configuration of a NodePool when operating
// on KubeVirt platform.
type KubevirtNodePoolPlatform struct {
	// rootVolume represents values associated with the VM volume that will host rhcos
	// +kubebuilder:default={persistent: {size: "32Gi"}, type: "Persistent"}
	// +required
	RootVolume *KubevirtRootVolume `json:"rootVolume"`

	// compute contains values representing the virtual hardware requested for the VM
	//
	// +optional
	// +kubebuilder:default={memory: "8Gi", cores: 2}
	Compute *KubevirtCompute `json:"compute,omitempty"`

	// networkInterfaceMultiqueue if set to "Enable", virtual network interfaces configured with a virtio bus will also
	// enable the vhost multiqueue feature for network devices. The number of queues created depends on additional
	// factors of the VirtualMachineInstance, like the number of guest CPUs.
	//
	// +optional
	// +kubebuilder:validation:Enum=Enable;Disable
	// +kubebuilder:default=Enable
	NetworkInterfaceMultiQueue *MultiQueueSetting `json:"networkInterfaceMultiqueue,omitempty"`

	// additionalNetworks specify the extra networks attached to the nodes
	//
	// +optional
	// +kubebuilder:validation:MaxItems=20
	AdditionalNetworks []KubevirtNetwork `json:"additionalNetworks,omitempty"`

	// attachDefaultNetwork specify if the default pod network should be attached to the nodes
	// this can only be set to false if AdditionalNetworks are configured
	//
	// +optional
	// +kubebuilder:default=true
	AttachDefaultNetwork *bool `json:"attachDefaultNetwork,omitempty"`

	// nodeSelector is a selector which must be true for the kubevirt VirtualMachine to fit on a node.
	// Selector which must match a node's labels for the VM to be scheduled on that node. More info:
	// https://kubernetes.io/docs/concepts/configuration/assign-pod-node/
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// hostDevices specifies the host devices (e.g. GPU devices) to be passed
	// from the management cluster, to the nodepool nodes
	// +optional
	// +kubebuilder:validation:MaxItems=10
	KubevirtHostDevices []KubevirtHostDevice `json:"hostDevices,omitempty"`
}

// KubevirtNetwork specifies the configuration for a virtual machine
// network interface
type KubevirtNetwork struct {
	// name specify the network attached to the nodes
	// it is a value with the format "[namespace]/[name]" to reference the
	// multus network attachment definition
	// +kubebuilder:validation:MaxLength=255
	// +required
	Name string `json:"name"`
}

type KubevirtHostDevice struct {
	// deviceName is the name of the host device that is desired to be utilized in the HostedCluster's NodePool
	// The device can be any supported PCI device, including GPU, either as a passthrough or a vGPU slice.
	// +kubebuilder:validation:MaxLength=255
	// +required
	DeviceName string `json:"deviceName"`

	// count is the number of instances the specified host device will be attached to each of the
	// NodePool's nodes. Default is 1.
	//
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	Count int `json:"count,omitempty"`
}

// KubeVirtNodePoolStatus contains the KubeVirt platform statuses
type KubeVirtNodePoolStatus struct {
	// cacheName holds the name of the cache DataVolume, if exists
	// +optional
	// +kubebuilder:validation:MaxLength=255
	CacheName string `json:"cacheName,omitempty"`

	// credentials shows the client credentials used when creating KubeVirt virtual machines.
	// This filed is only exists when the KubeVirt virtual machines are being placed
	// on a cluster separate from the one hosting the Hosted Control Plane components.
	//
	// The default behavior when Credentials is not defined is for the KubeVirt VMs to be placed on
	// the same cluster and namespace as the Hosted Control Plane.
	// +optional
	Credentials *KubevirtPlatformCredentials `json:"credentials,omitempty"`
}
type KubevirtPlatformCredentials struct {
	// infraKubeConfigSecret is a reference to the secret containing the kubeconfig
	// of an external infrastructure cluster for kubevirt provider
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="infraKubeConfigSecret is immutable"
	InfraKubeConfigSecret *KubeconfigSecretRef `json:"infraKubeConfigSecret,omitempty"`

	// infraNamespace is the namespace in the external infrastructure cluster
	// where kubevirt resources will be created
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="infraNamespace is immutable"
	// +kubebuilder:validation:MaxLength=255
	InfraNamespace string `json:"infraNamespace"`
}

// KubevirtPlatformSpec specifies configuration for kubevirt guest cluster installations
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.generateID) || has(self.generateID)", message="Kubevirt GenerateID is required once set"
type KubevirtPlatformSpec struct {
	// baseDomainPassthrough toggles whether or not an automatically
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

	// generateID is used to uniquely apply a name suffix to resources associated with
	// kubevirt infrastructure resources
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Kubevirt GenerateID is immutable once set"
	// +kubebuilder:validation:MaxLength=11
	// +optional
	GenerateID string `json:"generateID,omitempty"`
	// credentials defines the client credentials used when creating KubeVirt virtual machines.
	// Defining credentials is only necessary when the KubeVirt virtual machines are being placed
	// on a cluster separate from the one hosting the Hosted Control Plane components.
	//
	// The default behavior when Credentials is not defined is for the KubeVirt VMs to be placed on
	// the same cluster and namespace as the Hosted Control Plane.
	// +optional
	Credentials *KubevirtPlatformCredentials `json:"credentials,omitempty"`

	// storageDriver defines how the KubeVirt CSI driver exposes StorageClasses on
	// the infra cluster (hosting the VMs) to the guest cluster.
	//
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
	// type represents the type of kubevirt csi driver configuration to use
	//
	// +unionDiscriminator
	// +immutable
	// +kubebuilder:default=Default
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="storageDriver.Type is immutable"
	Type KubevirtStorageDriverConfigType `json:"type,omitempty"`

	// manual is used to explicitly define how the infra storageclasses are
	// mapped to guest storageclasses
	//
	// +immutable
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="storageDriver.Manual is immutable"
	Manual *KubevirtManualStorageDriverConfig `json:"manual,omitempty"`
}

type KubevirtManualStorageDriverConfig struct {
	// storageClassMapping maps StorageClasses on the infra cluster hosting
	// the KubeVirt VMs to StorageClasses that are made available within the
	// Guest Cluster.
	//
	// NOTE: It is possible that not all capabilities of an infra cluster's
	// storageclass will be present for the corresponding guest clusters storageclass.
	//
	// +optional
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="storageClassMapping is immutable"
	// +kubebuilder:validation:MaxItems=50
	StorageClassMapping []KubevirtStorageClassMapping `json:"storageClassMapping,omitempty"`

	// volumeSnapshotClassMapping maps VolumeSnapshotClasses on the infra cluster hosting
	// the KubeVirt VMs to VolumeSnapshotClasses that are made available within the
	// Guest Cluster.
	// +optional
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="volumeSnapshotClassMapping is immutable"
	// +kubebuilder:validation:MaxItems=50
	VolumeSnapshotClassMapping []KubevirtVolumeSnapshotClassMapping `json:"volumeSnapshotClassMapping,omitempty"`
}

type KubevirtStorageClassMapping struct {
	// group contains which group this mapping belongs to.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Group string `json:"group,omitempty"`
	// infraStorageClassName is the name of the infra cluster storage class that
	// will be exposed to the guest.
	// +kubebuilder:validation:MaxLength=255
	// +required
	InfraStorageClassName string `json:"infraStorageClassName"`
	// guestStorageClassName is the name that the corresponding storageclass will
	// be called within the guest cluster
	// +kubebuilder:validation:MaxLength=255
	// +required
	GuestStorageClassName string `json:"guestStorageClassName"`
}

type KubevirtVolumeSnapshotClassMapping struct {
	// group contains which group this mapping belongs to.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Group string `json:"group,omitempty"`
	// infraVolumeSnapshotClassName is the name of the infra cluster volume snapshot class that
	// will be exposed to the guest.
	// +kubebuilder:validation:MaxLength=255
	// +required
	InfraVolumeSnapshotClassName string `json:"infraVolumeSnapshotClassName"`
	// guestVolumeSnapshotClassName is the name that the corresponding volumeSnapshotClass will
	// be called within the guest cluster
	// +kubebuilder:validation:MaxLength=255
	// +required
	GuestVolumeSnapshotClassName string `json:"guestVolumeSnapshotClassName"`
}

type InstanceType string
