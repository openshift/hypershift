package oauth

import (
	"embed"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

//go:embed templates/*
var templateContent embed.FS

const (
	LoginTemplateKey             = "login.html"
	ProviderSelectionTemplateKey = "providers.html"
	ErrorsTemplateKey            = "errors.html"

	LoginTemplateFile             = "templates/" + LoginTemplateKey
	ProviderSelectionTemplateFile = "templates/" + ProviderSelectionTemplateKey
	ErrorsTemplateFile            = "templates/" + ErrorsTemplateKey
)

func MustTemplate(name string) []byte {
	b, err := templateContent.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}

func ReconcileLoginTemplateSecret(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(secret)
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[LoginTemplateKey] = MustTemplate(LoginTemplateFile)
	return nil
}

func ReconcileProviderSelectionTemplateSecret(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(secret)
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[ProviderSelectionTemplateKey] = MustTemplate(ProviderSelectionTemplateFile)
	return nil
}

func ReconcileErrorTemplateSecret(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(secret)
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[ErrorsTemplateKey] = MustTemplate(ErrorsTemplateFile)
	return nil
}
