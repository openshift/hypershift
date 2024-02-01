package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

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
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	if err := json.Unmarshal(data, &admissionReview); err != nil {
		http.Error(w, "Failed to unmarshal admission review", http.StatusBadRequest)
		return
	}

	object := admissionReview.Request.Object
	service := &corev1.Service{}
	if err := json.Unmarshal(object.Raw, service); err != nil {
		http.Error(w, "Failed to unmarshal service", http.StatusBadRequest)
		return
	}

	admissionReview.Response = &v1.AdmissionResponse{
		Allowed: true,
	}

	if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		patch := `[{"op": "add", "path": "/metadata/annotations", "value": {"service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags": "red-hat-managed=true"}}]`
		pt := v1.PatchTypeJSONPatch
		admissionReview.Response.Patch = []byte(patch)
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
