package util

import (
	"testing"

	"k8s.io/client-go/rest"
)

func TestGetClientForConfig(t *testing.T) {
	t.Run("When REST config is valid, it should create a Kubernetes client", func(t *testing.T) {
		client, err := GetClientForConfig(&rest.Config{Host: "https://management.example.com:6443"})
		if err != nil {
			t.Fatalf("expected client creation to succeed, got %v", err)
		}
		if client == nil {
			t.Fatal("expected a client")
		}
	})

	t.Run("When REST config is nil, it should return an error", func(t *testing.T) {
		client, err := GetClientForConfig(nil)
		if err == nil {
			t.Fatal("expected an error")
		}
		if client != nil {
			t.Fatal("expected no client when REST config is nil")
		}
	})
}
