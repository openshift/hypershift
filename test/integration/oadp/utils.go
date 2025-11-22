//go:build integration
// +build integration

package oadp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// downloadBackupCRD downloads the Velero Backup CRD with timeout protection
func downloadBackupCRD() (*apiextensionsv1.CustomResourceDefinition, error) {
	return downloadCRDWithTimeout("https://raw.githubusercontent.com/vmware-tanzu/velero/v1.14.1/config/crd/v1/bases/velero.io_backups.yaml")
}

// downloadRestoreCRD downloads the Velero Restore CRD with timeout protection
func downloadRestoreCRD() (*apiextensionsv1.CustomResourceDefinition, error) {
	return downloadCRDWithTimeout("https://raw.githubusercontent.com/vmware-tanzu/velero/v1.14.1/config/crd/v1/bases/velero.io_restores.yaml")
}

// downloadCRDWithTimeout downloads a CRD from the given URL with timeout protection
// Network timeout for CRD downloads is set to 10 seconds
func downloadCRDWithTimeout(url string) (*apiextensionsv1.CustomResourceDefinition, error) {
	const downloadTimeout = 10 * time.Second
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: downloadTimeout,
	}

	// Create request with context for additional timeout control
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download CRD from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download CRD from %s: HTTP %d", url, resp.StatusCode)
	}

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse CRD
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(bodyBytes, &crd); err != nil {
		return nil, fmt.Errorf("failed to unmarshal CRD YAML: %w", err)
	}

	return &crd, nil
}
