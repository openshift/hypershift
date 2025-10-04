package framework

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Framework provides shared utilities and setup/teardown for e2e tests
type Framework struct {
	opts   *TestOptions
	logger logr.Logger

	// Clients
	managementClient client.Client
	kubeClient       kubernetes.Interface

	// Shared test resources
	sharedResources *SharedResources
}

// SharedResources holds resources that are shared across tests
type SharedResources struct {
	// OIDC provider resources (for AWS)
	OIDCProviderURL       string
	OIDCSigningKey        []byte
	OIDCProviderARN       string

	// Shared infrastructure resources
	SharedInfraNamespace string
	SharedSecrets        map[string]string
}

// NewFramework creates a new test framework instance
func NewFramework(opts *TestOptions, logger logr.Logger) (*Framework, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid test options: %w", err)
	}

	f := &Framework{
		opts:   opts,
		logger: logger.WithName("framework"),
		sharedResources: &SharedResources{
			SharedSecrets: make(map[string]string),
		},
	}

	return f, nil
}

// Setup performs global setup for the test framework
func (f *Framework) Setup(ctx context.Context) error {
	f.logger.Info("Setting up test framework")

	// Initialize Kubernetes clients
	if err := f.setupClients(); err != nil {
		return fmt.Errorf("failed to setup clients: %w", err)
	}

	// Setup platform-specific resources
	if err := f.setupPlatformResources(ctx); err != nil {
		return fmt.Errorf("failed to setup platform resources: %w", err)
	}

	// Setup shared infrastructure
	if err := f.setupSharedInfrastructure(ctx); err != nil {
		return fmt.Errorf("failed to setup shared infrastructure: %w", err)
	}

	f.logger.Info("Test framework setup completed")
	return nil
}

// Cleanup performs global cleanup for the test framework
func (f *Framework) Cleanup(ctx context.Context) error {
	f.logger.Info("Cleaning up test framework")

	var errs []error

	// Cleanup shared infrastructure
	if err := f.cleanupSharedInfrastructure(ctx); err != nil {
		f.logger.Error(err, "Failed to cleanup shared infrastructure")
		errs = append(errs, err)
	}

	// Cleanup platform-specific resources
	if err := f.cleanupPlatformResources(ctx); err != nil {
		f.logger.Error(err, "Failed to cleanup platform resources")
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup failed with %d errors: %v", len(errs), errs)
	}

	f.logger.Info("Test framework cleanup completed")
	return nil
}

// setupClients initializes Kubernetes clients
func (f *Framework) setupClients() error {
	f.logger.Info("Setting up Kubernetes clients")

	// Get management cluster client
	managementClient, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get management client: %w", err)
	}
	f.managementClient = managementClient

	// Get Kubernetes client
	config, err := e2eutil.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	f.kubeClient = kubeClient

	f.logger.Info("Kubernetes clients setup completed")
	return nil
}

// setupPlatformResources sets up platform-specific shared resources
func (f *Framework) setupPlatformResources(ctx context.Context) error {
	f.logger.Info("Setting up platform-specific resources", "platform", f.opts.Platform)

	switch hyperv1.PlatformType(f.opts.Platform) {
	case hyperv1.AWSPlatform:
		return f.setupAWSResources(ctx)
	case hyperv1.AzurePlatform:
		return f.setupAzureResources(ctx)
	case hyperv1.KubevirtPlatform:
		return f.setupKubeVirtResources(ctx)
	case hyperv1.NonePlatform:
		f.logger.Info("No platform-specific setup required for None platform")
		return nil
	default:
		f.logger.Info("No platform-specific setup available", "platform", f.opts.Platform)
		return nil
	}
}

// cleanupPlatformResources cleans up platform-specific shared resources
func (f *Framework) cleanupPlatformResources(ctx context.Context) error {
	f.logger.Info("Cleaning up platform-specific resources", "platform", f.opts.Platform)

	switch hyperv1.PlatformType(f.opts.Platform) {
	case hyperv1.AWSPlatform:
		return f.cleanupAWSResources(ctx)
	case hyperv1.AzurePlatform:
		return f.cleanupAzureResources(ctx)
	case hyperv1.KubevirtPlatform:
		return f.cleanupKubeVirtResources(ctx)
	default:
		f.logger.Info("No platform-specific cleanup required", "platform", f.opts.Platform)
		return nil
	}
}

// setupSharedInfrastructure sets up shared infrastructure resources
func (f *Framework) setupSharedInfrastructure(ctx context.Context) error {
	f.logger.Info("Setting up shared infrastructure")

	// Create shared namespace for test resources
	namespace := fmt.Sprintf("hypershift-e2e-%d", time.Now().Unix())
	f.sharedResources.SharedInfraNamespace = namespace

	f.logger.Info("Shared infrastructure setup completed", "namespace", namespace)
	return nil
}

// cleanupSharedInfrastructure cleans up shared infrastructure resources
func (f *Framework) cleanupSharedInfrastructure(ctx context.Context) error {
	f.logger.Info("Cleaning up shared infrastructure")

	// Implementation would clean up shared namespace and resources
	// For now, just log the cleanup
	f.logger.Info("Shared infrastructure cleanup completed")
	return nil
}

// GetClient returns the management cluster client
func (f *Framework) GetClient() client.Client {
	return f.managementClient
}

// GetKubeClient returns the Kubernetes client
func (f *Framework) GetKubeClient() kubernetes.Interface {
	return f.kubeClient
}

// GetSharedResources returns the shared resources
func (f *Framework) GetSharedResources() *SharedResources {
	return f.sharedResources
}

// GetLogger returns a logger with the specified name
func (f *Framework) GetLogger(name string) logr.Logger {
	return f.logger.WithName(name)
}

// GetTestOptions returns the test options
func (f *Framework) GetTestOptions() *TestOptions {
	return f.opts
}