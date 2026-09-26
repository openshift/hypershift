package gcp

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// wifTokenPath is the path where the token minter writes the projected service
// account token for GCP Workload Identity Federation. The token is written after
// the credential secret is created, so its presence is used as a gate before
// attempting any GCP API call.
const wifTokenPath = "/var/run/secrets/openshift/serviceaccount/token"

// firewallClient abstracts the subset of the GCP Compute API used by the
// firewall reconciler. It exists so the reconciler can be unit tested against a
// fake implementation without talking to GCP.
type firewallClient interface {
	// GetFirewall returns the named firewall rule, or a 404 googleapi.Error if it
	// does not exist.
	GetFirewall(ctx context.Context, project, name string) (*compute.Firewall, error)
	// InsertFirewall creates a firewall rule and returns the corresponding global
	// operation.
	InsertFirewall(ctx context.Context, project string, firewall *compute.Firewall) (*compute.Operation, error)
	// PatchFirewall updates the named firewall rule and returns the corresponding
	// global operation.
	PatchFirewall(ctx context.Context, project, name string, firewall *compute.Firewall) (*compute.Operation, error)
	// DeleteFirewall deletes the named firewall rule and returns the corresponding
	// global operation.
	DeleteFirewall(ctx context.Context, project, name string) (*compute.Operation, error)
	// GetNetwork returns the named VPC network, or a 404 googleapi.Error if it does
	// not exist.
	GetNetwork(ctx context.Context, project, name string) (*compute.Network, error)
	// WaitForGlobalOperation blocks until the named global operation completes or
	// the context deadline is reached.
	WaitForGlobalOperation(ctx context.Context, project, opName string) (*compute.Operation, error)
}

// computeFirewallClient is the production firewallClient backed by a real
// *compute.Service.
type computeFirewallClient struct {
	service *compute.Service
}

func (c *computeFirewallClient) GetFirewall(ctx context.Context, project, name string) (*compute.Firewall, error) {
	return c.service.Firewalls.Get(project, name).Context(ctx).Do()
}

func (c *computeFirewallClient) InsertFirewall(ctx context.Context, project string, firewall *compute.Firewall) (*compute.Operation, error) {
	return c.service.Firewalls.Insert(project, firewall).Context(ctx).Do()
}

func (c *computeFirewallClient) PatchFirewall(ctx context.Context, project, name string, firewall *compute.Firewall) (*compute.Operation, error) {
	return c.service.Firewalls.Patch(project, name, firewall).Context(ctx).Do()
}

func (c *computeFirewallClient) DeleteFirewall(ctx context.Context, project, name string) (*compute.Operation, error) {
	return c.service.Firewalls.Delete(project, name).Context(ctx).Do()
}

func (c *computeFirewallClient) GetNetwork(ctx context.Context, project, name string) (*compute.Network, error) {
	return c.service.Networks.Get(project, name).Context(ctx).Do()
}

func (c *computeFirewallClient) WaitForGlobalOperation(ctx context.Context, project, opName string) (*compute.Operation, error) {
	return c.service.GlobalOperations.Wait(project, opName).Context(ctx).Do()
}

// isWIFTokenAccessible reports whether the WIF service account token file exists.
// The Compute client cannot be built before the token minter writes this file,
// so callers treat its absence as an expected, recoverable "waiting for
// credentials" state rather than an error.
//
// It returns (true, nil) when the token is present, (false, nil) when it is
// simply not yet written (os.ErrNotExist), and (false, err) for any other stat
// failure (e.g. a broken mount or permission error) so the caller can surface it
// instead of masking a persistent problem as "waiting for credentials".
func isWIFTokenAccessible() (bool, error) {
	_, err := os.Stat(wifTokenPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// newComputeFirewallClient builds a firewallClient from the mounted WIF
// credentials referenced by GOOGLE_APPLICATION_CREDENTIALS. It mirrors the PSC
// controller's InitCustomerGCPClient. Service account keys are not supported.
func newComputeFirewallClient(ctx context.Context) (firewallClient, error) {
	credentialsFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credentialsFile == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS not set")
	}
	if _, err := os.Stat(credentialsFile); err != nil {
		return nil, fmt.Errorf("credentials file not accessible at %s: %w", credentialsFile, err)
	}

	httpClient, err := google.DefaultClient(ctx, compute.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create Google Cloud client using %s: %w", credentialsFile, err)
	}

	service, err := compute.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create Compute Engine service: %w", err)
	}

	return &computeFirewallClient{service: service}, nil
}
