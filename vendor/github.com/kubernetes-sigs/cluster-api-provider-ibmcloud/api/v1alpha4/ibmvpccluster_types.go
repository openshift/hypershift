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

package v1alpha4

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// ClusterFinalizer allows DockerClusterReconciler to clean up resources associated with DockerCluster before
	// removing it from the apiserver.
	ClusterFinalizer = "ibmvpccluster.infrastructure.cluster.x-k8s.io"
)

// IBMVPCClusterSpec defines the desired state of IBMVPCCluster
type IBMVPCClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// The IBM Cloud Region the cluster lives in.
	Region string `json:"region"`

	// The VPC resources should be created under the resource group
	ResourceGroup string `json:"resourceGroup"`

	// The Name of VPC
	VPC string `json:"vpc,omitempty"`

	// The Name of availability zone
	Zone string `json:"zone,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`
}

// IBMVPCClusterStatus defines the observed state of IBMVPCCluster
type IBMVPCClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	VPC VPC `json:"vpc,omitempty"`
	// Bastion Instance `json:"bastion,omitempty"`
	Ready       bool        `json:"ready"`
	Subnet      Subnet      `json:"subnet,omitempty"`
	APIEndpoint APIEndpoint `json:"apiEndpoint,omitempty"`
}

type VPC struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=ibmvpcclusters,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this IBMVPCCluster belongs"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="Cluster infrastructure is ready for IBM VPC instances"

// IBMVPCCluster is the Schema for the ibmvpcclusters API
type IBMVPCCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IBMVPCClusterSpec   `json:"spec,omitempty"`
	Status IBMVPCClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// IBMVPCClusterList contains a list of IBMVPCCluster
type IBMVPCClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IBMVPCCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IBMVPCCluster{}, &IBMVPCClusterList{})
}
