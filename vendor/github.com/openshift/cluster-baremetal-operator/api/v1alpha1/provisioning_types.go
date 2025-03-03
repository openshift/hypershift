/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Generate the README.md input section.
//go:generate go run ../../hack/readme_inputs ../../README.md ./

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
)

// ProvisioningNetwork is the boot mode of the system
// +kubebuilder:validation:Enum=Managed;Unmanaged;Disabled
type ProvisioningNetwork string

// ProvisioningNetwork modes
const (
	ProvisioningNetworkManaged   ProvisioningNetwork = "Managed"
	ProvisioningNetworkUnmanaged ProvisioningNetwork = "Unmanaged"
	ProvisioningNetworkDisabled  ProvisioningNetwork = "Disabled"

	// ProvisioningFinalizer is required for proper handling of deletion
	ProvisioningFinalizer = "provisioning.metal3.io"

	// ProvisioningSingletonName is the name of the provisioning resource
	ProvisioningSingletonName = "provisioning-configuration"
)

// BootIsoSource is the origin of the boot iso image
// +kubebuilder:validation:Enum=local;http
type BootIsoSource string

// BootIsoSource values
const (
	BootIsoSourceLocal BootIsoSource = "local"
	BootIsoSourceHttp  BootIsoSource = "http"
)

// PreProvisioningOSDownloadURLs defines a set of URLs that the cluster
// can use to provision RHCOS Live images
type PreProvisioningOSDownloadURLs struct {
	// IsoURL Image URL to be used for Live ISO deployments
	IsoURL string `json:"isoURL,omitempty"`

	// KernelURL is an Image URL to be used for PXE deployments
	KernelURL string `json:"kernelURL,omitempty"`

	// InitramfsURL Image URL to be used for PXE deployments
	InitramfsURL string `json:"initramfsURL,omitempty"`

	// RootfsURL Image URL to be used for PXE deployments
	RootfsURL string `json:"rootfsURL,omitempty"`
}

// ProvisioningSpec defines the desired state of Provisioning
type ProvisioningSpec struct {
	// ProvisioningInterface is the name of the network interface
	// on a baremetal server to the provisioning network. It can
	// have values like eth1 or ens3.
	ProvisioningInterface string `json:"provisioningInterface,omitempty"`

	// ProvisioningMacAddresses is a list of mac addresses of network interfaces
	// on a baremetal server to the provisioning network.
	// Use this instead of ProvisioningInterface to allow interfaces of different
	// names. If not provided it will be populated by the BMH.Spec.BootMacAddress
	// of each master.
	ProvisioningMacAddresses []string `json:"provisioningMacAddresses,omitempty"`

	// ProvisioningIP is the IP address assigned to the
	// provisioningInterface of the baremetal server. This IP
	// address should be within the provisioning subnet, and
	// outside of the DHCP range.
	ProvisioningIP string `json:"provisioningIP,omitempty"`

	// ProvisioningNetworkCIDR is the network on which the
	// baremetal nodes are provisioned. The provisioningIP and the
	// IPs in the dhcpRange all come from within this network. When using IPv6
	// and in a network managed by the Baremetal IPI solution this cannot be a
	// network larger than a /64.
	ProvisioningNetworkCIDR string `json:"provisioningNetworkCIDR,omitempty"`

	// ProvisioningDHCPExternal indicates whether the DHCP server
	// for IP addresses in the provisioning DHCP range is present
	// within the metal3 cluster or external to it. This field is being
	// deprecated in favor of provisioningNetwork.
	ProvisioningDHCPExternal bool `json:"provisioningDHCPExternal,omitempty"`

	// ProvisioningDHCPRange needs to be interpreted along with
	// ProvisioningDHCPExternal. If the value of
	// provisioningDHCPExternal is set to False, then
	// ProvisioningDHCPRange represents the range of IP addresses
	// that the DHCP server running within the metal3 cluster can
	// use while provisioning baremetal servers. If the value of
	// ProvisioningDHCPExternal is set to True, then the value of
	// ProvisioningDHCPRange will be ignored. When the value of
	// ProvisioningDHCPExternal is set to False, indicating an
	// internal DHCP server and the value of ProvisioningDHCPRange
	// is not set, then the DHCP range is taken to be the default
	// range which goes from .10 to .100 of the
	// ProvisioningNetworkCIDR. This is the only value in all of
	// the Provisioning configuration that can be changed after
	// the installer has created the CR. This value needs to be
	// two comma sererated IP addresses within the
	// ProvisioningNetworkCIDR where the 1st address represents
	// the start of the range and the 2nd address represents the
	// last usable address in the  range.
	ProvisioningDHCPRange string `json:"provisioningDHCPRange,omitempty"`

	// ProvisioningOSDownloadURL is the location from which the OS
	// Image used to boot baremetal host machines can be downloaded
	// by the metal3 cluster.
	ProvisioningOSDownloadURL string `json:"provisioningOSDownloadURL,omitempty"`

	// ProvisioningNetwork provides a way to indicate the state of the
	// underlying network configuration for the provisioning network.
	// This field can have one of the following values -
	// `Managed`- when the provisioning network is completely managed by
	// the Baremetal IPI solution.
	// `Unmanaged`- when the provsioning network is present and used but
	// the user is responsible for managing DHCP. Virtual media provisioning
	// is recommended but PXE is still available if required.
	// `Disabled`- when the provisioning network is fully disabled. User can
	// bring up the baremetal cluster using virtual media or assisted
	// installation. If using metal3 for power management, BMCs must be
	// accessible from the machine networks. User should provide two IPs on
	// the external network that would be used for provisioning services.
	ProvisioningNetwork ProvisioningNetwork `json:"provisioningNetwork,omitempty"`

	// ProvisioningDNS allows sending the DNS information via DHCP on the
	// provisionig network. It is off by default since the Provisioning
	// service itself (Ironic) does not require DNS, but it may be useful
	// for layered products (e.g. ZTP).
	ProvisioningDNS bool `json:"provisioningDNS,omitempty"`

	// AdditionalNTPServers is a list of NTP Servers to be used by the
	// provisioning service
	AdditionalNTPServers []string `json:"additionalNTPServers,omitempty"`

	// WatchAllNamespaces provides a way to explicitly allow use of this
	// Provisioning configuration across all Namespaces. It is an
	// optional configuration which defaults to false and in that state
	// will be used to provision baremetal hosts in only the
	// openshift-machine-api namespace. When set to true, this provisioning
	// configuration would be used for baremetal hosts across all namespaces.
	WatchAllNamespaces bool `json:"watchAllNamespaces,omitempty"`

	// BootIsoSource provides a way to set the location where the iso image
	// to boot the nodes will be served from.
	// By default the boot iso image is cached locally and served from
	// the Provisioning service (Ironic) nodes using an auxiliary httpd server.
	// If the boot iso image is already served by an httpd server, setting
	// this option to http allows to directly provide the image from there;
	// in this case, the network (either internal or external) where the
	// httpd server that hosts the boot iso is needs to be accessible
	// by the metal3 pod.
	BootIsoSource BootIsoSource `json:"bootIsoSource,omitempty"`

	// VirtualMediaViaExternalNetwork flag when set to "true" allows for workers
	// to boot via Virtual Media and contact metal3 over the External Network.
	// When the flag is set to "false" (which is the default), virtual media
	// deployments can still happen based on the configuration specified in the
	// ProvisioningNetwork i.e when in Disabled mode, over the External Network
	// and over Provisioning Network when in Managed mode.
	// PXE deployments will always use the Provisioning Network and will not be
	// affected by this flag.
	VirtualMediaViaExternalNetwork bool `json:"virtualMediaViaExternalNetwork,omitempty"`

	// PreprovisioningOSDownloadURLs is set of CoreOS Live URLs that would be necessary to provision a worker
	// either using virtual media or PXE.
	PreProvisioningOSDownloadURLs PreProvisioningOSDownloadURLs `json:"preProvisioningOSDownloadURLs,omitempty"`

	// DisableVirtualMediaTLS turns off TLS on the virtual media server,
	// which may be required for hardware that cannot accept HTTPS links.
	DisableVirtualMediaTLS bool `json:"disableVirtualMediaTLS,omitempty"`
}

// ProvisioningStatus defines the observed state of Provisioning
type ProvisioningStatus struct {
	operatorv1.OperatorStatus `json:",inline"`
}

// +kubebuilder:resource:path=provisionings,scope=Cluster
// +kubebuilder:subresource:status

// Provisioning contains configuration used by the Provisioning
// service (Ironic) to provision baremetal hosts.
// Provisioning is created by the OpenShift installer using admin or
// user provided information about the provisioning network and the
// NIC on the server that can be used to PXE boot it.
// This CR is a singleton, created by the installer and currently only
// consumed by the cluster-baremetal-operator to bring up and update
// containers in a metal3 cluster.
// +kubebuilder:object:root=true
type Provisioning struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProvisioningSpec   `json:"spec,omitempty"`
	Status ProvisioningStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProvisioningList contains a list of Provisioning
type ProvisioningList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Provisioning `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Provisioning{}, &ProvisioningList{})
}
