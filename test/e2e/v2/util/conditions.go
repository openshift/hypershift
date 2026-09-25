//go:build e2ev2

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

package util

import (
	"fmt"
	"reflect"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// Predicate evaluates an object. Return whether the object in question matches your predicate, the reasons
// why or why not, and whether an error occurred. If determining that an object does not match a predicate,
// a message is required. Returning an error is fatal to the asynchronous assertion using this predicate.
type Predicate[T any] func(T) (done bool, reasons string, err error)

// Condition is a generic structure required to adapt all the different concrete condition types into one.
type Condition struct {
	Type    string
	Status  metav1.ConditionStatus
	Reason  string
	Message string
}

// String formats a condition in the canonical way.
func (c Condition) String() string {
	msg := fmt.Sprintf("%s=%s", c.Type, c.Status)
	if c.Reason != "" {
		msg += ": " + c.Reason
	}
	if c.Message != "" {
		msg += "(" + c.Message + ")"
	}
	return msg
}

// Conditions extracts conditions from the item and adapts them to the generic wrapper.
func Conditions(item client.Object) ([]Condition, error) {
	if reflect.ValueOf(item).IsZero() || reflect.ValueOf(item).Elem().IsZero() {
		panic(fmt.Sprintf("programmer error: got a nil value under client.Object: %#v", item))
	}
	switch obj := item.(type) {
	case *corev1.Node:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for i := range obj.Status.Conditions {
			conditions[i] = Condition{
				Type:    string(obj.Status.Conditions[i].Type),
				Status:  metav1.ConditionStatus(obj.Status.Conditions[i].Status),
				Reason:  obj.Status.Conditions[i].Reason,
				Message: obj.Status.Conditions[i].Message,
			}
		}
		return conditions, nil
	case *corev1.Pod:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for _, condition := range obj.Status.Conditions {
			conditions = append(conditions, Condition{
				Type:    string(condition.Type),
				Status:  metav1.ConditionStatus(condition.Status),
				Reason:  condition.Reason,
				Message: condition.Message,
			})
		}
		return conditions, nil
	case *hyperv1.NodePool:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for i := range obj.Status.Conditions {
			conditions[i] = Condition{
				Type:    obj.Status.Conditions[i].Type,
				Status:  metav1.ConditionStatus(obj.Status.Conditions[i].Status),
				Reason:  obj.Status.Conditions[i].Reason,
				Message: obj.Status.Conditions[i].Message,
			}
		}
		return conditions, nil
	case *hyperv1.HostedCluster:
		return adaptConditions(obj.Status.Conditions), nil
	case *hyperv1.HostedControlPlane:
		return adaptConditions(obj.Status.Conditions), nil
	case *certificatesv1alpha1.CertificateRevocationRequest:
		return adaptConditions(obj.Status.Conditions), nil
	case *hyperv1.ControlPlaneComponent:
		return adaptConditions(obj.Status.Conditions), nil
	case *certificatesv1.CertificateSigningRequest:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for i := range obj.Status.Conditions {
			conditions[i] = Condition{
				Type:    string(obj.Status.Conditions[i].Type),
				Status:  metav1.ConditionStatus(obj.Status.Conditions[i].Status),
				Reason:  obj.Status.Conditions[i].Reason,
				Message: obj.Status.Conditions[i].Message,
			}
		}
		return conditions, nil
	case *karpenterv1.NodeClaim:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for i := range obj.Status.Conditions {
			conditions[i] = Condition{
				Type:    obj.Status.Conditions[i].Type,
				Status:  obj.Status.Conditions[i].Status,
				Reason:  obj.Status.Conditions[i].Reason,
				Message: obj.Status.Conditions[i].Message,
			}
		}
		return conditions, nil
	case *hyperkarpenterv1.OpenshiftEC2NodeClass:
		return adaptConditions(obj.Status.Conditions), nil
	default:
		return nil, fmt.Errorf("object %T unknown", item)
	}
}

func adaptConditions(in []metav1.Condition) []Condition {
	conditions := make([]Condition, len(in))
	for i := range in {
		conditions[i] = Condition{
			Type:    in[i].Type,
			Status:  in[i].Status,
			Reason:  in[i].Reason,
			Message: in[i].Message,
		}
	}
	return conditions
}

func (needle Condition) Matches(condition Condition) bool {
	return (needle.Type == "" || needle.Type == condition.Type) &&
		(needle.Status == "" || needle.Status == condition.Status) &&
		(needle.Reason == "" || needle.Reason == condition.Reason) &&
		(needle.Message == "" || needle.Message == condition.Message)
}

// ConditionPredicate returns a predicate that validates that a particular condition type exists and has the requisite status, reason and/or message.
func ConditionPredicate[T client.Object](needle Condition) Predicate[T] {
	return func(item T) (bool, string, error) {
		haystack, err := Conditions(item)
		if err != nil {
			return false, "", err
		}
		for _, condition := range haystack {
			if needle.Type == condition.Type {
				valid := needle.Matches(condition)
				prefix := ""
				if !valid {
					prefix = "in"
				}
				return valid, fmt.Sprintf("%scorrect condition: wanted %s, got %s", prefix, needle.String(), condition.String()), nil
			}
		}

		return false, fmt.Sprintf("missing condition: wanted %s, did not find condition of this type", needle.String()), nil
	}
}
