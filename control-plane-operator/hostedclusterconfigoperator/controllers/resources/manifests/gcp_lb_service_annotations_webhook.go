package manifests

import (
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GCPLBServiceAnnotationsWebhook returns the MutatingWebhookConfiguration that registers the
// GCP load-balancer Service annotations webhook in the hosted cluster. The webhook is
// served by a sidecar co-located with KAS in the management cluster.
func GCPLBServiceAnnotationsWebhook() *admissionregistrationv1.MutatingWebhookConfiguration {
	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gcp-lb-service-annotations-webhook",
		},
	}
}
