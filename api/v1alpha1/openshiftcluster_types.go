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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// OpenShiftClusterSpec defines the desired state of OpenShiftCluster
type OpenShiftClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	BaseDomain      string `json:"baseDomain"`
	PullSecret      string `json:"pullSecret"`
	ServiceCIDR     string `json:"serviceCIDR"`
	PodCIDR         string `json:"podCIDR"`
	SSHKey          string `json:"sshKey"`
	CloudProvider   string `json:"cloudProvider"`
	ComputeReplicas int    `json:"computeReplicas"`
	ReleaseImage    string `json:"releaseImage"`
}

// OpenShiftClusterStatus defines the observed state of OpenShiftCluster
type OpenShiftClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Ready bool `json:"ready"`
}

// +kubebuilder:object:root=true

// OpenShiftCluster is the Schema for the openshiftclusters API
type OpenShiftCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OpenShiftClusterSpec   `json:"spec,omitempty"`
	Status OpenShiftClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OpenShiftClusterList contains a list of OpenShiftCluster
type OpenShiftClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenShiftCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OpenShiftCluster{}, &OpenShiftClusterList{})
}
