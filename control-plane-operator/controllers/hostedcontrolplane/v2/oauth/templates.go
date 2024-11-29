package oauth

import (
	"embed"
	"path"

	corev1 "k8s.io/api/core/v1"

	component "github.com/openshift/hypershift/support/controlplane-component"
)

//go:embed templates/*
var templateContent embed.FS

const (
	loginTemplateKey             = "login.html"
	providerSelectionTemplateKey = "providers.html"
	errorsTemplateKey            = "errors.html"
)

func readTemplate(name string) ([]byte, error) {
	return templateContent.ReadFile(path.Join("templates", name))
}

func adaptLoginTemplateSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	data, err := readTemplate(loginTemplateKey)
	secret.Data[loginTemplateKey] = data
	return err
}

func adaptProviderSelectionTemplateSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	data, err := readTemplate(providerSelectionTemplateKey)
	secret.Data[providerSelectionTemplateKey] = data
	return err
}

func adaptErrorTemplateSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	data, err := readTemplate(errorsTemplateKey)
	secret.Data[errorsTemplateKey] = data
	return err
}
