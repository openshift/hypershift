package proxy

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift/library-go/pkg/crypto"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ProxyCAConfigMapKey = "ca-bundle.crt"
)

// LoadCABundle loads the CA bundle from a ConfigMap.
func LoadCABundle(configMap corev1.ConfigMap) ([]*x509.Certificate, error) {
	if _, ok := configMap.Data[ProxyCAConfigMapKey]; !ok {
		return nil, fmt.Errorf("ConfigMap %q is missing %q", configMap.Name, ProxyCAConfigMapKey)
	}
	trustBundleData := []byte(configMap.Data[ProxyCAConfigMapKey])
	if len(trustBundleData) == 0 {
		return nil, fmt.Errorf("data key %q is empty from ConfigMap %q", ProxyCAConfigMapKey, configMap.Name)
	}
	certBundle, err := crypto.CertsFromPEM(trustBundleData)
	if err != nil {
		return nil, fmt.Errorf("failed parsing certificate data from ConfigMap %q: %v", configMap.Name, err)
	}
	return certBundle, nil
}

// ValidateProxyCAValidity loads the CA bundle for the hosted cluster and verifies the contained certificates are still valid.
// Returns nil if valid, error if invalid.
func ValidateProxyCAValidity(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster) error {
	if hcluster.Spec.Configuration == nil || hcluster.Spec.Configuration.Proxy == nil || hcluster.Spec.Configuration.Proxy.TrustedCA.Name == "" {
		return nil
	}

	cmName := hcluster.Spec.Configuration.Proxy.TrustedCA.Name
	caConfigMap := corev1.ConfigMap{}
	err := c.Get(ctx, types.NamespacedName{
		Namespace: hcluster.Namespace,
		Name:      cmName,
	}, &caConfigMap)
	if err != nil {
		return err
	}
	certBundle, err := LoadCABundle(caConfigMap)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, cert := range certBundle {
		if cert.NotBefore.UTC().After(now) {
			return fmt.Errorf("a configured certificate in the ca bundle is not yet valid: %s", cert.Subject.CommonName)
		}
		if cert.NotAfter.UTC().Before(now) {
			return fmt.Errorf("a configured certificate in the ca bundle was no longer valid: %s", cert.Subject.CommonName)
		}
	}
	return nil
}

// ExpiryTimeProxyCA loads the CA bundle for the hosted cluster and finds the earliest expiring certificate time.
// Returns the time.Time in UTC format.
func ExpiryTimeProxyCA(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster) (*time.Time, error) {
	if hcluster.Spec.Configuration == nil || hcluster.Spec.Configuration.Proxy == nil || hcluster.Spec.Configuration.Proxy.TrustedCA.Name == "" {
		return nil, nil
	}

	cmName := hcluster.Spec.Configuration.Proxy.TrustedCA.Name
	caConfigMap := corev1.ConfigMap{}
	err := c.Get(ctx, types.NamespacedName{
		Namespace: hcluster.Namespace,
		Name:      cmName,
	}, &caConfigMap)
	if err != nil {
		return nil, err
	}
	certBundle, err := LoadCABundle(caConfigMap)
	if err != nil {
		return nil, err
	}
	if len(certBundle) == 0 {
		return nil, fmt.Errorf("no certificates found in CA bundle from ConfigMap %q", cmName)
	}
	var earliest time.Time
	for i, cert := range certBundle {
		// First cert to initiate our variable instead of constructing an artificially big time.Time
		if i == 0 {
			earliest = cert.NotAfter.UTC()
		}
		if cert.NotAfter.UTC().Before(earliest) {
			earliest = cert.NotAfter.UTC()
		}
	}
	return &earliest, nil
}
