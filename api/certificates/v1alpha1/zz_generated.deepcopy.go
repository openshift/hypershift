//go:build !ignore_autogenerated

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

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CertificateRevocationRequest) DeepCopyInto(out *CertificateRevocationRequest) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CertificateRevocationRequest.
func (in *CertificateRevocationRequest) DeepCopy() *CertificateRevocationRequest {
	if in == nil {
		return nil
	}
	out := new(CertificateRevocationRequest)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CertificateRevocationRequest) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CertificateRevocationRequestList) DeepCopyInto(out *CertificateRevocationRequestList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]CertificateRevocationRequest, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CertificateRevocationRequestList.
func (in *CertificateRevocationRequestList) DeepCopy() *CertificateRevocationRequestList {
	if in == nil {
		return nil
	}
	out := new(CertificateRevocationRequestList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CertificateRevocationRequestList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CertificateRevocationRequestSpec) DeepCopyInto(out *CertificateRevocationRequestSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CertificateRevocationRequestSpec.
func (in *CertificateRevocationRequestSpec) DeepCopy() *CertificateRevocationRequestSpec {
	if in == nil {
		return nil
	}
	out := new(CertificateRevocationRequestSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CertificateRevocationRequestStatus) DeepCopyInto(out *CertificateRevocationRequestStatus) {
	*out = *in
	if in.RevocationTimestamp != nil {
		in, out := &in.RevocationTimestamp, &out.RevocationTimestamp
		*out = (*in).DeepCopy()
	}
	if in.PreviousSigner != nil {
		in, out := &in.PreviousSigner, &out.PreviousSigner
		*out = new(v1.LocalObjectReference)
		**out = **in
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CertificateRevocationRequestStatus.
func (in *CertificateRevocationRequestStatus) DeepCopy() *CertificateRevocationRequestStatus {
	if in == nil {
		return nil
	}
	out := new(CertificateRevocationRequestStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CertificateSigningRequestApproval) DeepCopyInto(out *CertificateSigningRequestApproval) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CertificateSigningRequestApproval.
func (in *CertificateSigningRequestApproval) DeepCopy() *CertificateSigningRequestApproval {
	if in == nil {
		return nil
	}
	out := new(CertificateSigningRequestApproval)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CertificateSigningRequestApproval) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CertificateSigningRequestApprovalList) DeepCopyInto(out *CertificateSigningRequestApprovalList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]CertificateSigningRequestApproval, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CertificateSigningRequestApprovalList.
func (in *CertificateSigningRequestApprovalList) DeepCopy() *CertificateSigningRequestApprovalList {
	if in == nil {
		return nil
	}
	out := new(CertificateSigningRequestApprovalList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CertificateSigningRequestApprovalList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CertificateSigningRequestApprovalSpec) DeepCopyInto(out *CertificateSigningRequestApprovalSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CertificateSigningRequestApprovalSpec.
func (in *CertificateSigningRequestApprovalSpec) DeepCopy() *CertificateSigningRequestApprovalSpec {
	if in == nil {
		return nil
	}
	out := new(CertificateSigningRequestApprovalSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CertificateSigningRequestApprovalStatus) DeepCopyInto(out *CertificateSigningRequestApprovalStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CertificateSigningRequestApprovalStatus.
func (in *CertificateSigningRequestApprovalStatus) DeepCopy() *CertificateSigningRequestApprovalStatus {
	if in == nil {
		return nil
	}
	out := new(CertificateSigningRequestApprovalStatus)
	in.DeepCopyInto(out)
	return out
}
