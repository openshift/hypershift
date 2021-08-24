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
	clusterv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// ClusterFinalizer allows system to clean up resources associated with cluster before
	// removing it from the apiserver.
	ClusterFinalizer = "ibmcluster.infrastructure.cluster.x-k8s.io"
)

// IBMClusterSpec defines the desired state of IBMCluster
type IBMClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`
}

// IBMClusterStatus defines the observed state of IBMCluster
type IBMClusterStatus struct {
	// Ready signifies control plane ready
	Ready bool `json:"ready"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=ibmclusters,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this IBMCluster belongs"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="Cluster infrastructure is ready for IBM machine instances"

// IBMCluster is the Schema for the ibmclusters API
type IBMCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IBMClusterSpec   `json:"spec,omitempty"`
	Status IBMClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// IBMClusterList contains a list of IBMCluster
type IBMClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IBMCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IBMCluster{}, &IBMClusterList{})
}
