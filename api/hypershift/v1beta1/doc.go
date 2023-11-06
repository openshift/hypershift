/*
Package v1beta1 contains the HyperShift API.

The HyperShift API enables creating and managing lightweight, flexible, heterogeneous
OpenShift clusters at scale.

HyperShift clusters are deployed in a topology which isolates the "control plane"
(e.g. etcd, the API server, controller manager, etc.) from the "data plane" (e.g.
worker nodes and their kubelets, and the infrastructure on which they run). This
enables "hosted control plane as a service" use cases.
*/
// +kubebuilder:object:generate=true
// +groupName=hypershift.openshift.io
package v1beta1
