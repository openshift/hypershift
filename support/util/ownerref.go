package util

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func EnsureOwnerRef(resource client.Object, ownerRef *metav1.OwnerReference) {
	if ownerRef == nil {
		return
	}
	ownerRefs := resource.GetOwnerReferences()
	i := getOwnerRefIndex(ownerRefs, ownerRef)
	if i == -1 {
		ownerRefs = append(ownerRefs, *ownerRef)
	} else {
		ownerRefs[i] = *ownerRef
	}
	resource.SetOwnerReferences(ownerRefs)
}

func getOwnerRefIndex(list []metav1.OwnerReference, ref *metav1.OwnerReference) int {
	for i := range list {
		// NOTE: The APIVersion may have changed with a new API Version, however the UID should remain the
		// same. Use either to identify the owner reference.
		if list[i].Kind == ref.Kind && (list[i].APIVersion == ref.APIVersion || list[i].UID == ref.UID) && list[i].Name == ref.Name {
			return i
		}
	}
	return -1
}
