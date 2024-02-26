package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/mattbaird/jsonpatch"
	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
)

func main() {
	http.HandleFunc("/mutate-service", mutateService)
	log.Fatal(http.ListenAndServeTLS(":8443", "/etc/webhook/certs/tls.crt", "/etc/webhook/certs/tls.key", nil))
}

func mutateService(w http.ResponseWriter, r *http.Request) {
	log.Println("Mutating service")
	admissionReview := v1.AdmissionReview{}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("failed to read request body: %v\n", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	if err := json.Unmarshal(data, &admissionReview); err != nil {
		log.Printf("failed to unmarshal admission review: %v\n", err)
		http.Error(w, "Failed to unmarshal admission review", http.StatusBadRequest)
		return
	}

	object := admissionReview.Request.Object
	service := &corev1.Service{}
	if err := json.Unmarshal(object.Raw, service); err != nil {
		log.Printf("failed to unmarshal original Service: %v\n", err)
		http.Error(w, "Failed to unmarshal service", http.StatusBadRequest)
		return
	}

	admissionReview.Response = &v1.AdmissionResponse{
		Allowed: true,
	}

	if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		patch, err := patchAnnotation(service)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		pt := v1.PatchTypeJSONPatch
		admissionReview.Response.Patch = patch
		admissionReview.Response.PatchType = &pt
	}

	responseBytes, err := json.Marshal(admissionReview)
	if err != nil {
		http.Error(w, "Failed to marshal admission review", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responseBytes)
}

func patchAnnotation(service *corev1.Service) ([]byte, error) {
	var updatedTags string
	originalTags, set := service.Annotations["service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags"]
	if set {
		updatedTags = originalTags + ","
	}
	updatedTags = updatedTags + "red-hat-managed=true"

	patched := service.DeepCopy()
	patched.ObjectMeta.Annotations["service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags"] = updatedTags

	originalBytes, err := json.Marshal(service)
	if err != nil {
		log.Printf("failed to marshal original Service: %v\n", err)
		return nil, errors.New("Failed to marshal original Service")
	}
	patchedBytes, err := json.Marshal(patched)
	if err != nil {
		log.Printf("failed to marshal patched Service: %v\n", err)
		return nil, errors.New("Failed to marshal patched Service")
	}
	patches, err := jsonpatch.CreatePatch(originalBytes, patchedBytes)
	if err != nil {
		log.Printf("failed to create JSON patch: %v\n", err)
		return nil, errors.New("Failed to created JSON patch")
	}

	if len(patches) != 1 {
		formatted, err := json.Marshal(patches)
		if err != nil {
			log.Printf("failed to marshal JSON patches: %v\n", err)
		}
		log.Printf("programmer error: expected one patch, got %d: %v", len(patches), string(formatted))
		return nil, errors.New("Invalid JSON patch generated")
	}

	return json.Marshal(patches)
}
