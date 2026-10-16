package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsscheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func TestWaitForKarpenterCRDs(t *testing.T) {
	g := NewWithT(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(standaloneKarpenterCRD(name)); err != nil {
			t.Errorf("failed to write CRD response: %v", err)
		}
	}))
	defer server.Close()

	err := waitForKarpenterCRDs(t.Context(), crdTestConfig(server.URL))
	g.Expect(err).NotTo(HaveOccurred())
}

func standaloneKarpenterCRD(name string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiextensions.k8s.io/v1",
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{
			Conditions: []apiextensionsv1.CustomResourceDefinitionCondition{
				{Type: apiextensionsv1.Established, Status: apiextensionsv1.ConditionTrue},
			},
		},
	}
}

func crdTestConfig(serverURL string) *rest.Config {
	return &rest.Config{
		Host:    serverURL,
		APIPath: "/apis",
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &schema.GroupVersion{Group: "apiextensions.k8s.io", Version: "v1"},
			NegotiatedSerializer: apiextensionsscheme.Codecs.WithoutConversion(),
		},
	}
}
