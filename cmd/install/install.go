/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package install

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
)

const (
	// ExternalDNSImage - This is specifically tag 1.1.0-3 from https://catalog.redhat.com/software/containers/edo/external-dns-rhel8/61d4c35023156829b87a434a?container-tabs=overview&tag=1.1.0-3&push_date=1671131187000
	// TODO this needs to be updated to a multi-arch image including Arm - https://issues.redhat.com/browse/NE-1298
	ExternalDNSImage = "registry.redhat.io/edo/external-dns-rhel8@sha256:638fb6b5fc348f5cf52b9800d3d8e9f5315078fc9b1e57e800cb0a4a50f1b4b9"
)

type Options struct {
	AdditionalTrustBundle                     string
	Namespace                                 string
	HyperShiftImage                           string
	ImageRefsFile                             string
	HyperShiftOperatorReplicas                int32
	Development                               bool
	EnableDefaultingWebhook                   bool
	EnableValidatingWebhook                   bool
	EnableConversionWebhook                   bool
	Template                                  bool
	Format                                    string
	OutputFile                                string
	OutputTypes                               string
	ExcludeEtcdManifests                      bool
	PlatformMonitoring                        metrics.PlatformMonitoring
	EnableCIDebugOutput                       bool
	PrivatePlatform                           string
	AWSPrivateCreds                           string
	AWSPrivateCredentialsSecret               string
	AWSPrivateCredentialsSecretKey            string
	AWSPrivateRegion                          string
	OIDCStorageProviderS3Region               string
	OIDCStorageProviderS3BucketName           string
	OIDCStorageProviderS3Credentials          string
	OIDCStorageProviderS3CredentialsSecret    string
	OIDCStorageProviderS3CredentialsSecretKey string
	ExternalDNSProvider                       string
	ExternalDNSCredentials                    string
	ExternalDNSCredentialsSecret              string
	ExternalDNSDomainFilter                   string
	ExternalDNSTxtOwnerId                     string
	ExternalDNSImage                          string
	EnableAdminRBACGeneration                 bool
	EnableUWMTelemetryRemoteWrite             bool
	EnableCVOManagementClusterMetricsAccess   bool
	MetricsSet                                metrics.MetricsSet
	WaitUntilAvailable                        bool
	WaitUntilEstablished                      bool
	RHOBSMonitoring                           bool
	SLOsAlerts                                bool
	MonitoringDashboards                      bool
	CertRotationScale                         time.Duration
	EnableDedicatedRequestServingIsolation    bool
	PullSecretFile                            string
	ManagedService                            string
	EnableSizeTagging                         bool
	EnableEtcdRecovery                        bool
	EnableCPOOverrides                        bool
	TechPreviewNoUpgrade                      bool
}

func (o *Options) Validate() error {
	var errs []error

	switch hyperv1.PlatformType(o.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		if (len(o.AWSPrivateCreds) == 0 && len(o.AWSPrivateCredentialsSecret) == 0) || len(o.AWSPrivateRegion) == 0 {
			errs = append(errs, fmt.Errorf("--aws-private-region and --aws-private-creds or --aws-private-secret are required with --private-platform=%s", hyperv1.AWSPlatform))
		}
	case hyperv1.NonePlatform:
	default:
		errs = append(errs, fmt.Errorf("--private-platform must be either %s or %s", hyperv1.AWSPlatform, hyperv1.NonePlatform))
	}

	if len(o.OIDCStorageProviderS3CredentialsSecret) > 0 && len(o.OIDCStorageProviderS3Credentials) > 0 {
		errs = append(errs, fmt.Errorf("only one of --oidc-storage-provider-s3-secret or --oidc-storage-provider-s3-credentials is supported"))
	}
	if (len(o.OIDCStorageProviderS3CredentialsSecret) > 0 || len(o.OIDCStorageProviderS3Credentials) > 0) &&
		(len(o.OIDCStorageProviderS3BucketName) == 0 || len(o.OIDCStorageProviderS3Region) == 0 || len(o.OIDCStorageProviderS3CredentialsSecretKey) == 0) {
		errs = append(errs, fmt.Errorf("all required oidc information is not set"))
	}
	if strings.Contains(o.OIDCStorageProviderS3BucketName, ".") {
		errs = append(errs, fmt.Errorf("oidc bucket name must not contain dots (.); see the notes on HTTPS at https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html"))
	}

	if len(o.ExternalDNSProvider) > 0 {
		if len(o.ExternalDNSCredentials) == 0 && len(o.ExternalDNSCredentialsSecret) == 0 {
			errs = append(errs, fmt.Errorf("--external-dns-credentials or --external-dns-credentials-secret are required with --external-dns-provider"))
		}
		if len(o.ExternalDNSCredentials) != 0 && len(o.ExternalDNSCredentialsSecret) != 0 {
			errs = append(errs, fmt.Errorf("only one of --external-dns-credentials or --external-dns-credentials-secret is supported"))
		}
		if len(o.ExternalDNSDomainFilter) == 0 {
			errs = append(errs, fmt.Errorf("--external-dns-domain-filter is required with --external-dns-provider"))
		}
	}
	if o.HyperShiftImage != version.HyperShiftImage && len(o.ImageRefsFile) > 0 {
		errs = append(errs, fmt.Errorf("only one of --hypershift-image or --image-refs-file should be specified"))
	}
	if o.RHOBSMonitoring && os.Getenv(rhobsmonitoring.EnvironmentVariable) != "1" {
		errs = append(errs, fmt.Errorf("when invoking this command with the --rhobs-monitoring flag, the RHOBS_MONITORING environment variable must be set to \"1\""))
	}

	if o.CertRotationScale > 24*time.Hour {
		errs = append(errs, fmt.Errorf("cannot set --cert-rotation-scale longer than 24h, invalid value: %s", o.CertRotationScale.String()))
	}

	if o.RHOBSMonitoring && o.EnableCVOManagementClusterMetricsAccess {
		errs = append(errs, fmt.Errorf("when invoking this command with the --rhobs-monitoring flag, the --enable-cvo-management-cluster-metrics-access flag is not supported "))
	}

	if len(o.ManagedService) > 0 && o.ManagedService != hyperv1.AroHCP {
		errs = append(errs, fmt.Errorf("not a valid managed service type: %s", o.ManagedService))
	}
	return errors.NewAggregate(errs)
}

func (o *Options) ApplyDefaults() {
	switch {
	case o.Development:
		o.HyperShiftOperatorReplicas = 0
	case o.EnableDefaultingWebhook || o.EnableConversionWebhook || o.EnableValidatingWebhook:
		o.HyperShiftOperatorReplicas = 2
	default:
		o.HyperShiftOperatorReplicas = 1
	}

	if o.ExternalDNSProvider == string(assets.AzureExternalDNSProvider) {
		o.ManagedService = hyperv1.AroHCP
	}
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "install",
		Short:        "Installs the HyperShift operator",
		SilenceUsage: true,
	}

	var opts Options
	opts.PrivatePlatform = string(hyperv1.NonePlatform)
	opts.MetricsSet = metrics.DefaultMetricsSet
	opts.EnableConversionWebhook = true // default to enabling the conversion webhook
	opts.ExternalDNSImage = ExternalDNSImage
	opts.CertRotationScale = 24 * time.Hour
	opts.EnableSizeTagging = false
	opts.EnableEtcdRecovery = true

	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.PersistentFlags().StringVar(&opts.HyperShiftImage, "hypershift-image", version.HyperShiftImage, "The HyperShift image to deploy")
	cmd.PersistentFlags().BoolVar(&opts.Development, "development", false, "Enable tweaks to facilitate local development")
	cmd.PersistentFlags().BoolVar(&opts.EnableDefaultingWebhook, "enable-defaulting-webhook", false, "Enable webhook for defaulting hypershift API types")
	cmd.PersistentFlags().BoolVar(&opts.EnableValidatingWebhook, "enable-validating-webhook", false, "Enable webhook for validating hypershift API types")
	cmd.PersistentFlags().BoolVar(&opts.EnableConversionWebhook, "enable-conversion-webhook", true, "Enable webhook for converting hypershift API types")
	cmd.PersistentFlags().BoolVar(&opts.ExcludeEtcdManifests, "exclude-etcd", false, "Leave out etcd manifests")
	cmd.PersistentFlags().Var(&opts.PlatformMonitoring, "platform-monitoring", "Select an option for enabling platform cluster monitoring. Valid values are: None, OperatorOnly, All")
	cmd.PersistentFlags().BoolVar(&opts.EnableCIDebugOutput, "enable-ci-debug-output", opts.EnableCIDebugOutput, "If extra CI debug output should be enabled")
	cmd.PersistentFlags().StringVar(&opts.PrivatePlatform, "private-platform", opts.PrivatePlatform, "Platform on which private clusters are supported by this operator (supports \"AWS\" or \"None\")")
	cmd.PersistentFlags().StringVar(&opts.AWSPrivateCreds, "aws-private-creds", opts.AWSPrivateCreds, "Path to an AWS credentials file with privileges sufficient to manage private cluster resources")
	cmd.PersistentFlags().StringVar(&opts.AWSPrivateCredentialsSecret, "aws-private-secret", "", "Name of an existing secret containing the AWS private link credentials.")
	cmd.PersistentFlags().StringVar(&opts.AWSPrivateCredentialsSecretKey, "aws-private-secret-key", "credentials", "Name of the secret key containing the AWS private link credentials.")
	cmd.PersistentFlags().StringVar(&opts.AWSPrivateRegion, "aws-private-region", opts.AWSPrivateRegion, "AWS region where private clusters are supported by this operator")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3Region, "oidc-storage-provider-s3-region", "", "Region of the OIDC bucket. Required for AWS guest clusters")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3BucketName, "oidc-storage-provider-s3-bucket-name", "", "Name of the bucket in which to store the clusters OIDC discovery information. Required for AWS guest clusters")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3Credentials, "oidc-storage-provider-s3-credentials", opts.OIDCStorageProviderS3Credentials, "Credentials to use for writing the OIDC documents into the S3 bucket. Required for AWS guest clusters")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3CredentialsSecret, "oidc-storage-provider-s3-secret", "", "Name of an existing secret containing the OIDC S3 credentials.")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3CredentialsSecretKey, "oidc-storage-provider-s3-secret-key", "credentials", "Name of the secret key containing the OIDC S3 credentials.")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSProvider, "external-dns-provider", opts.ExternalDNSProvider, "Provider to use for managing DNS records using external-dns")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSCredentials, "external-dns-credentials", opts.OIDCStorageProviderS3Credentials, "Credentials to use for managing DNS records using external-dns")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSCredentialsSecret, "external-dns-secret", "", "Name of an existing secret containing the external-dns credentials.")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSDomainFilter, "external-dns-domain-filter", "", "Restrict external-dns to changes within the specified domain.")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSTxtOwnerId, "external-dns-txt-owner-id", "", "external-dns TXT registry owner ID.")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSImage, "external-dns-image", opts.ExternalDNSImage, "Image to use for external-dns")
	cmd.PersistentFlags().BoolVar(&opts.EnableAdminRBACGeneration, "enable-admin-rbac-generation", false, "Generate RBAC manifests for hosted cluster admins")
	cmd.PersistentFlags().StringVar(&opts.ImageRefsFile, "image-refs", opts.ImageRefsFile, "Image references to user in Hypershift installation")
	cmd.PersistentFlags().StringVar(&opts.AdditionalTrustBundle, "additional-trust-bundle", opts.AdditionalTrustBundle, "Path to a file with user CA bundle")
	cmd.PersistentFlags().Var(&opts.MetricsSet, "metrics-set", "The set of metrics to produce for each HyperShift control plane. Valid values are: Telemetry, SRE, All")
	cmd.PersistentFlags().BoolVar(&opts.EnableUWMTelemetryRemoteWrite, "enable-uwm-telemetry-remote-write", opts.EnableUWMTelemetryRemoteWrite, "If true, HyperShift operator ensures user workload monitoring is enabled and that it is configured to remote write telemetry metrics from control planes")
	cmd.PersistentFlags().BoolVar(&opts.EnableCVOManagementClusterMetricsAccess, "enable-cvo-management-cluster-metrics-access", opts.EnableCVOManagementClusterMetricsAccess, "If true, the hosted CVO will have access to the management cluster metrics server to evaluate conditional updates (supported for OpenShift management clusters)")
	cmd.Flags().BoolVar(&opts.WaitUntilAvailable, "wait-until-available", opts.WaitUntilAvailable, "If true, pauses installation until hypershift operator has been rolled out and its webhook service is available (if installing the webhook)")
	cmd.Flags().BoolVar(&opts.WaitUntilEstablished, "wait-until-crds-established", opts.WaitUntilEstablished, "If true, pauses installation until all custom resource definitions are established before applying other manifests.")
	cmd.PersistentFlags().BoolVar(&opts.RHOBSMonitoring, "rhobs-monitoring", opts.RHOBSMonitoring, "If true, HyperShift will generate and use the RHOBS version of monitoring resources (ServiceMonitors, PodMonitors, etc)")
	cmd.PersistentFlags().BoolVar(&opts.SLOsAlerts, "slos-alerts", opts.SLOsAlerts, "If true, HyperShift will generate and use the prometheus alerts for monitoring HostedCluster and NodePools")
	cmd.PersistentFlags().BoolVar(&opts.MonitoringDashboards, "monitoring-dashboards", opts.MonitoringDashboards, "If true, HyperShift will generate a monitoring dashboard for every HostedCluster that it creates")
	cmd.PersistentFlags().DurationVar(&opts.CertRotationScale, "cert-rotation-scale", opts.CertRotationScale, "The scaling factor for certificate rotation. It is not supported to set this to anything other than 24h.")
	cmd.PersistentFlags().BoolVar(&opts.EnableDedicatedRequestServingIsolation, "enable-dedicated-request-serving-isolation", true, "If true, enables scheduling of request serving components to dedicated nodes")
	cmd.PersistentFlags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "File path to a pull secret.")
	cmd.PersistentFlags().StringVar(&opts.ManagedService, "managed-service", opts.ManagedService, "The type of managed service the HyperShift Operator is installed on; this is used to configure different HostedCluster options depending on the managed service. Examples: ARO-HCP, ROSA-HCP")
	cmd.PersistentFlags().BoolVar(&opts.EnableSizeTagging, "enable-size-tagging", opts.EnableSizeTagging, "If true, HyperShift will tag the HostedCluster with a size label corresponding to the number of worker nodes")
	cmd.PersistentFlags().BoolVar(&opts.EnableEtcdRecovery, "enable-etcd-recovery", opts.EnableEtcdRecovery, "If true, the HyperShift operator checks for failed etcd pods and attempts a recovery if possible")
	cmd.PersistentFlags().BoolVar(&opts.EnableCPOOverrides, "enable-cpo-overrides", opts.EnableCPOOverrides, "If true, the HyperShift operator uses a set of static overrides for the CPO image given specific release versions")
	cmd.PersistentFlags().BoolVar(&opts.TechPreviewNoUpgrade, "tech-preview-no-upgrade", opts.TechPreviewNoUpgrade, "If true, the HyperShift operator runs with TechPreviewNoUpgrade features enabled")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opts.ApplyDefaults()

		if err := opts.Validate(); err != nil {
			return err
		}

		crds, objects, err := hyperShiftOperatorManifests(opts)
		if err != nil {
			return err
		}

		err = apply(cmd.Context(), crds)
		if err != nil {
			return err
		}

		if opts.WaitUntilAvailable || opts.WaitUntilEstablished {
			if err := waitUntilEstablished(cmd.Context(), crds); err != nil {
				return err
			}
		}

		err = apply(cmd.Context(), objects)
		if err != nil {
			return err
		}

		if opts.WaitUntilAvailable {
			if err := waitUntilAvailable(cmd.Context(), opts); err != nil {
				return err
			}
		}

		return nil
	}

	cmd.AddCommand(NewRenderCommand(&opts))

	return cmd
}

func apply(ctx context.Context, objects []crclient.Object) error {
	client, err := util.GetClient()
	if err != nil {
		return err
	}

	var errs []error
	for _, object := range objects {
		var objectBytes bytes.Buffer
		err := hyperapi.YamlSerializer.Encode(object, &objectBytes)
		if err != nil {
			return err
		}
		if object.GetObjectKind().GroupVersionKind().Kind == "PriorityClass" {
			// PriorityClasses can not be patched as the value field is immutable
			if err := client.Create(ctx, object, &crclient.CreateOptions{}); err != nil {
				if apierrors.IsAlreadyExists(err) {
					fmt.Printf("already exists: %s %s/%s\n", object.GetObjectKind().GroupVersionKind().Kind, object.GetNamespace(), object.GetName())
				} else {
					return err
				}
			} else {
				fmt.Printf("created %s %s/%s\n", "PriorityClass", object.GetNamespace(), object.GetName())
			}
		} else {
			if err := client.Patch(ctx, object, crclient.RawPatch(types.ApplyPatchType, objectBytes.Bytes()), crclient.ForceOwnership, crclient.FieldOwner("hypershift")); err != nil {
				errs = append(errs, err)
			}
			fmt.Printf("applied %s %s/%s\n", object.GetObjectKind().GroupVersionKind().Kind, object.GetNamespace(), object.GetName())
		}
	}

	return errors.NewAggregate(errs)
}

func waitUntilEstablished(ctx context.Context, crds []crclient.Object) error {
	client, err := util.GetClient()
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	eg := errgroup.Group{}
	for i := range crds {
		crd := crds[i].(*apiextensionsv1.CustomResourceDefinition)
		eg.Go(func() error {
			fmt.Printf("Waiting for custom resource definition %q to be established...\n", crd.Name)
			return wait.PollUntilContextCancel(waitCtx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
				if err := client.Get(ctx, crclient.ObjectKeyFromObject(crd), crd); err != nil {
					return false, err
				}
				for _, condition := range crd.Status.Conditions {
					if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
						fmt.Printf("Custom resource definition %q is successfully established\n", crd.Name)
						return true, nil
					}
				}
				return false, nil
			})
		})
	}
	return eg.Wait()
}

func waitUntilAvailable(ctx context.Context, opts Options) error {
	client, err := util.GetClient()
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	deployment := operatorDeployment(opts)
	fmt.Printf("Waiting for deployment %q in namespace %q rollout for operator...\n", deployment.Name, deployment.Namespace)
	err = wait.PollImmediateUntilWithContext(waitCtx, 2*time.Second, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(deployment), deployment); err != nil {
			return false, err
		}
		if deployment.Generation <= deployment.Status.ObservedGeneration {
			cond := getDeploymentCondition(deployment.Status, appsv1.DeploymentProgressing)
			if cond != nil && cond.Reason == "ProgressDeadlineExceeded" {
				return false, fmt.Errorf("deployment %q in namespace %q exceeded its progress deadline", deployment.Name, deployment.Namespace)
			}
			if deployment.Spec.Replicas != nil && deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
				fmt.Printf("Waiting for deployment %q in namespace %q rollout to finish: %d out of %d new replicas have been updated...\n", deployment.Name, deployment.Namespace, deployment.Status.UpdatedReplicas, *deployment.Spec.Replicas)
				return false, nil
			}
			if deployment.Status.Replicas > deployment.Status.UpdatedReplicas {
				fmt.Printf("Waiting for deployment %q in namespace %q rollout to finish: %d old replicas are pending termination...\n", deployment.Name, deployment.Namespace, deployment.Status.Replicas-deployment.Status.UpdatedReplicas)
				return false, nil
			}
			if deployment.Status.AvailableReplicas < deployment.Status.UpdatedReplicas {
				fmt.Printf("Waiting for deployment %q in namespace %q rollout to finish: %d of %d updated replicas are available...\n", deployment.Name, deployment.Namespace, deployment.Status.AvailableReplicas, deployment.Status.UpdatedReplicas)
				return false, nil
			}
			fmt.Printf("Deployment %q in namespace %q successfully rolled out\n", deployment.Name, deployment.Namespace)
			return true, nil
		} else {
			fmt.Printf("Waiting for operator deployment to be observed\n")
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("failed to wait for operator deployment: %w", err)
	}

	if opts.Development {
		return nil
	}
	err = wait.PollImmediateUntilWithContext(waitCtx, 2*time.Second, func(ctx context.Context) (bool, error) {
		endpoints := operatorEndpoints(opts)
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(endpoints), endpoints); err != nil {
			if apierrors.IsNotFound(err) {
				fmt.Printf("Operator service endpoints %q in nameppace %q have not been created yet\n", endpoints.Name, endpoints.Namespace)
				return false, nil
			}
			return false, err
		}
		if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
			fmt.Printf("Waiting for endpoints %q in nameppace %q to have addresses populated\n", endpoints.Name, endpoints.Namespace)
			return false, nil
		}
		fmt.Printf("Endpoints available\n")
		return true, nil

	})
	if err != nil {
		return fmt.Errorf("failed to wait for operator service endpoints: %w", err)
	}
	return nil
}

// getDeploymentCondition returns the condition with the provided type.
func getDeploymentCondition(status appsv1.DeploymentStatus, condType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

func operatorDeployment(opts Options) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "operator", Namespace: opts.Namespace},
	}
}

func operatorEndpoints(opts Options) *corev1.Endpoints {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "operator", Namespace: opts.Namespace},
	}
}

func fetchImageRefs(file string) (map[string]string, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read image references file: %w", err)
	}
	imageStream := imageapi.ImageStream{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(content), 100).Decode(&imageStream); err != nil {
		return nil, fmt.Errorf("cannot parse image references file: %w", err)
	}
	result := map[string]string{}
	for _, tag := range imageStream.Spec.Tags {
		result[tag.Name] = tag.From.Name
	}
	return result, nil
}

func hyperShiftOperatorManifests(opts Options) ([]crclient.Object, []crclient.Object, error) {
	var crds []crclient.Object
	var objects []crclient.Object

	var images map[string]string
	if len(opts.ImageRefsFile) > 0 {
		var err error
		images, err = fetchImageRefs(opts.ImageRefsFile)
		if err != nil {
			return nil, nil, err
		}
	}

	objects = append(objects, assets.HyperShiftControlPlanePriorityClass())
	objects = append(objects, assets.HyperShiftEtcdPriorityClass())
	objects = append(objects, assets.HyperShiftAPICriticalPriorityClass())
	objects = append(objects, assets.HypershiftOperatorPriorityClass())

	operatorNamespace := assets.HyperShiftNamespace{
		Name:                       opts.Namespace,
		EnableOCPClusterMonitoring: opts.PlatformMonitoring.IsEnabled(),
	}.Build()
	objects = append(objects, operatorNamespace)

	// Setup RBAC resources
	operatorServiceAccount, rbacObjs := setupRBAC(opts, operatorNamespace)
	objects = append(objects, rbacObjs...)

	if opts.EnableDefaultingWebhook {
		mutatingWebhookConfiguration := assets.HyperShiftMutatingWebhookConfiguration{
			Namespace: operatorNamespace,
		}.Build()
		objects = append(objects, mutatingWebhookConfiguration)
	}

	if opts.EnableValidatingWebhook {
		validatingWebhookConfiguration := assets.HyperShiftValidatingWebhookConfiguration{
			Namespace: operatorNamespace.Name,
		}.Build()
		objects = append(objects, validatingWebhookConfiguration)
	}

	// Setup Secrets
	oidcSecret, operatorCredentialsSecret, secretObjs, err := setupAuth(opts, operatorNamespace)
	if err != nil {
		return nil, nil, err
	}
	objects = append(objects, secretObjs...)

	// Setup CA resources
	userCABundleCM, trustedCABundle, caObjs, err := setupCA(opts, operatorNamespace)
	if err != nil {
		return nil, nil, err
	}
	objects = append(objects, caObjs...)

	// Setup ExternalDNS resources
	if len(opts.ExternalDNSProvider) > 0 {
		extDNSObjs, err := setupExternalDNS(opts, operatorNamespace)
		if err != nil {
			return nil, nil, err
		}
		objects = append(objects, extDNSObjs...)
	}

	// Setup HyperShift Operator Deployment and Service
	operatorService, operatorObjs := setupOperatorResources(
		opts, userCABundleCM, trustedCABundle, operatorNamespace, operatorServiceAccount, operatorCredentialsSecret,
		oidcSecret, images,
	)
	objects = append(objects, operatorObjs...)

	// Setup Monitoring resources
	monitoringObjs := setupMonitoring(opts, operatorNamespace)
	objects = append(objects, monitoringObjs...)

	crds = setupCRDs(opts, operatorNamespace, operatorService)

	// Set the GVK for all the objects
	setGVK := func(objsList ...[]crclient.Object) error {
		for _, objs := range objsList {
			for idx := range objs {
				gvk, err := apiutil.GVKForObject(objs[idx], hyperapi.Scheme)
				if err != nil {
					return fmt.Errorf("failed to look up gvk for %T: %w", objs[idx], err)
				}
				// Everything that embeds metav1.TypeMeta implements this
				objs[idx].(interface {
					SetGroupVersionKind(gvk schema.GroupVersionKind)
				}).SetGroupVersionKind(gvk)
			}
		}
		return nil
	}

	if err := setGVK(objects, crds); err != nil {
		return nil, nil, err
	}

	return crds, objects, nil
}

// setupCRDs returns the CRDs from all the manifests under the assets directory as list of CustomResourceDefinition objects
//
// The CRDs are filtered based on the options provided. If the option ExcludeEtcdManifests is set to true, the CRDs
// related to etcd are excluded from the list. If the option EnableConversionWebhook is set to true, the CRDs related
// to hypershift.openshift.io group are annotated with the necessary annotations to enable the conversion webhook.
func setupCRDs(opts Options, operatorNamespace *corev1.Namespace, operatorService *corev1.Service) []crclient.Object {
	var crds []crclient.Object
	crds = append(
		crds, assets.CustomResourceDefinitions(
			func(path string, crd *apiextensionsv1.CustomResourceDefinition) bool {
				if strings.Contains(path, "etcd") && opts.ExcludeEtcdManifests {
					return false
				}

				// If the feature generated CRD has any featureSet version then it has the format nodepool-<featureSet>.
				if strings.Contains(path, "zz_generated.crd-manifests") {
					if opts.TechPreviewNoUpgrade {
						// Skip all featureSets but TechPreviewNoUpgrade.
						if featureSet, ok := crd.Annotations["release.openshift.io/feature-set"]; ok {
							if featureSet != "TechPreviewNoUpgrade" {
								return false
							}
						}
					} else {
						// Skip all featureSets but Default.
						if featureSet, ok := crd.Annotations["release.openshift.io/feature-set"]; ok {
							if featureSet != "Default" {
								return false
							}
						}
					}
				}
				return true

			}, func(crd *apiextensionsv1.CustomResourceDefinition) {
				if crd.Spec.Group == "hypershift.openshift.io" {
					if !opts.EnableConversionWebhook {
						return
					}
					if crd.Annotations != nil {
						crd.Annotations = map[string]string{}
					}
					crd.Annotations["service.beta.openshift.io/inject-cabundle"] = "true"
					crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
						Strategy: apiextensionsv1.WebhookConverter,
						Webhook: &apiextensionsv1.WebhookConversion{
							ClientConfig: &apiextensionsv1.WebhookClientConfig{
								Service: &apiextensionsv1.ServiceReference{
									Namespace: operatorNamespace.Name,
									Name:      operatorService.Name,
									Port:      ptr.To[int32](443),
									Path:      ptr.To("/convert"),
								},
							},
							ConversionReviewVersions: []string{"v1beta1", "v1alpha1"},
						},
					}
				}
			},
		)...,
	)
	return crds
}

// setupMonitoring creates the Prometheus resources for monitoring
//
// This includes:
// - Role for Prometheus
// - RoleBinding for Prometheus
// - ServiceMonitor for HyperShift
// - RecordingRule for HyperShift
// - AlertingRule for HyperShift (if SLOsAlerts is enabled)
// - MonitoringDashboardTemplate for HC monitoring dashboards (if MonitoringDashboards is enabled)
func setupMonitoring(opts Options, operatorNamespace *corev1.Namespace) []crclient.Object {
	var objects []crclient.Object

	prometheusRole := assets.HyperShiftPrometheusRole{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, prometheusRole)

	prometheusRoleBinding := assets.HyperShiftOperatorPrometheusRoleBinding{
		Namespace:                  operatorNamespace,
		Role:                       prometheusRole,
		EnableOCPClusterMonitoring: opts.PlatformMonitoring.IsEnabled(),
	}.Build()
	objects = append(objects, prometheusRoleBinding)

	serviceMonitor := assets.HyperShiftServiceMonitor{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, serviceMonitor)

	recordingRule := assets.HypershiftRecordingRule{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, recordingRule)

	if opts.SLOsAlerts {
		alertingRule := assets.HypershiftAlertingRule{
			Namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openshift-monitoring"}},
		}.Build()
		objects = append(objects, alertingRule)
	}

	if opts.MonitoringDashboards {
		monitoringDashboardTemplate := assets.MonitoringDashboardTemplate{
			Namespace: opts.Namespace,
		}.Build()
		objects = append(objects, monitoringDashboardTemplate)
	}
	return objects
}

// setupOperatorResources creates the operator Deployment and Service resources.
//
// Returns the Service and a list of resources to apply.
func setupOperatorResources(opts Options, userCABundleCM *corev1.ConfigMap, trustedCABundle *corev1.ConfigMap, operatorNamespace *corev1.Namespace, operatorServiceAccount *corev1.ServiceAccount, operatorCredentialsSecret *corev1.Secret, oidcSecret *corev1.Secret, images map[string]string) (*corev1.Service, []crclient.Object) {
	operatorDeployment := assets.HyperShiftOperatorDeployment{
		AdditionalTrustBundle:                   userCABundleCM,
		OpenShiftTrustBundle:                    trustedCABundle,
		Namespace:                               operatorNamespace,
		OperatorImage:                           opts.HyperShiftImage,
		ServiceAccount:                          operatorServiceAccount,
		Replicas:                                opts.HyperShiftOperatorReplicas,
		EnableOCPClusterMonitoring:              opts.PlatformMonitoring == metrics.PlatformMonitoringAll,
		EnableCIDebugOutput:                     opts.EnableCIDebugOutput,
		EnableWebhook:                           opts.EnableDefaultingWebhook || opts.EnableConversionWebhook || opts.EnableValidatingWebhook,
		EnableValidatingWebhook:                 opts.EnableValidatingWebhook,
		PrivatePlatform:                         opts.PrivatePlatform,
		AWSPrivateRegion:                        opts.AWSPrivateRegion,
		AWSPrivateSecret:                        operatorCredentialsSecret,
		AWSPrivateSecretKey:                     opts.AWSPrivateCredentialsSecretKey,
		OIDCBucketName:                          opts.OIDCStorageProviderS3BucketName,
		OIDCBucketRegion:                        opts.OIDCStorageProviderS3Region,
		OIDCStorageProviderS3Secret:             oidcSecret,
		OIDCStorageProviderS3SecretKey:          opts.OIDCStorageProviderS3CredentialsSecretKey,
		Images:                                  images,
		MetricsSet:                              opts.MetricsSet,
		IncludeVersion:                          !opts.Template,
		UWMTelemetry:                            opts.EnableUWMTelemetryRemoteWrite,
		RHOBSMonitoring:                         opts.RHOBSMonitoring,
		MonitoringDashboards:                    opts.MonitoringDashboards,
		CertRotationScale:                       opts.CertRotationScale,
		EnableCVOManagementClusterMetricsAccess: opts.EnableCVOManagementClusterMetricsAccess,
		EnableDedicatedRequestServingIsolation:  opts.EnableDedicatedRequestServingIsolation,
		ManagedService:                          opts.ManagedService,
		EnableSizeTagging:                       opts.EnableSizeTagging,
		EnableEtcdRecovery:                      opts.EnableEtcdRecovery,
		EnableCPOOverrides:                      opts.EnableCPOOverrides,
		TechPreviewNoUpgrade:                    opts.TechPreviewNoUpgrade,
	}.Build()
	operatorService := assets.HyperShiftOperatorService{
		Namespace: operatorNamespace,
	}.Build()

	return operatorService, []crclient.Object{operatorDeployment, operatorService}
}

// setupExternalDNS creates the resources for external-dns
//
// This includes:
// - ServiceAccount for external-dns
// - ClusterRole for external-dns
// - ClusterRoleBinding for external-dns
// - Secret for external-dns credentials
// - Deployment for external-dns
// - PodMonitor for external-dns
func setupExternalDNS(opts Options, operatorNamespace *corev1.Namespace) ([]crclient.Object, error) {
	var objects []crclient.Object

	// Setting the proxy for external-dns is best-effort, ignore errors
	proxy, _ := func() (*configv1.Proxy, error) {
		proxy := &configv1.Proxy{}
		client, err := util.GetClient()
		if err != nil {
			return nil, err
		}
		if err := client.Get(context.TODO(), crclient.ObjectKey{Name: "cluster"}, proxy); err != nil {
			return nil, err
		}
		return proxy, nil
	}()

	externalDNSServiceAccount := assets.ExternalDNSServiceAccount{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, externalDNSServiceAccount)

	externalDNSClusterRole := assets.ExternalDNSClusterRole{}.Build()
	objects = append(objects, externalDNSClusterRole)

	externalDNSClusterRoleBinding := assets.ExternalDNSClusterRoleBinding{
		ClusterRole:    externalDNSClusterRole,
		ServiceAccount: externalDNSServiceAccount,
	}.Build()
	objects = append(objects, externalDNSClusterRoleBinding)

	var externalDNSSecret *corev1.Secret
	if opts.ExternalDNSCredentials != "" {
		externalDNSCreds, err := os.ReadFile(opts.ExternalDNSCredentials)
		if err != nil {
			return nil, err
		}

		externalDNSSecret = assets.ExternalDNSCredsSecret{
			Namespace:  operatorNamespace,
			CredsBytes: externalDNSCreds,
		}.Build()
		objects = append(objects, externalDNSSecret)
	} else if opts.ExternalDNSCredentialsSecret != "" {
		externalDNSSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: operatorNamespace.Name,
				Name:      opts.ExternalDNSCredentialsSecret,
			},
		}
	}

	externalDNSDeployment := assets.ExternalDNSDeployment{
		Namespace:         operatorNamespace,
		Image:             opts.ExternalDNSImage,
		ServiceAccount:    externalDNSServiceAccount,
		Provider:          assets.ExternalDNSProvider(opts.ExternalDNSProvider),
		DomainFilter:      opts.ExternalDNSDomainFilter,
		CredentialsSecret: externalDNSSecret,
		TxtOwnerId:        opts.ExternalDNSTxtOwnerId,
		Proxy:             proxy,
	}.Build()
	objects = append(objects, externalDNSDeployment)

	podMonitor := assets.ExternalDNSPodMonitor{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, podMonitor)

	return objects, nil
}

// setupCA creates the CA resources for the HyperShift operator.
// This includes:
// - User trusted CA bundle ConfigMap
// - Managed trusted CA bundle ConfigMap
//
// Returns the user trusted CA bundle ConfigMap, managed trusted CA bundle ConfigMap, and a list of resources to apply
func setupCA(opts Options, operatorNamespace *corev1.Namespace) (*corev1.ConfigMap, *corev1.ConfigMap, []crclient.Object, error) {
	objects := make([]crclient.Object, 0, 1)
	var userCABundleCM *corev1.ConfigMap
	if opts.AdditionalTrustBundle != "" {
		userCABundle, err := os.ReadFile(opts.AdditionalTrustBundle)
		if err != nil {
			return nil, nil, nil, err
		}
		userCABundleCM = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-ca-bundle",
				Namespace: operatorNamespace.Name,
			},
			Data: map[string]string{
				"ca-bundle.crt": string(userCABundle),
			},
		}
		objects = append(objects, userCABundleCM)
	}

	trustedCABundle := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: operatorNamespace.Name,
			Name:      "openshift-config-managed-trusted-ca-bundle",
			Labels: map[string]string{
				"config.openshift.io/inject-trusted-cabundle": "true",
			},
		},
	}
	objects = append(objects, trustedCABundle)
	return userCABundleCM, trustedCABundle, objects, nil
}

// setupRBAC creates the RBAC resources for the HyperShift operator.
//
// This includes:
// - HO ServiceAccount
// - HO ClusterRole
// - HO ClusterRoleBinding
// - HO Role (for the HO to manage resources in its own namespace such as the lease resource)
// - HO RoleBinding
// - Platform specific RBAC (e.g. missing RBAC in AKS that are present in OpenShift)
// - Client and Reader RBAC (if admin RBAC is enabled)
//
// Returns the HO ServiceAccount and a list of resources to apply
func setupRBAC(opts Options, operatorNamespace *corev1.Namespace) (*corev1.ServiceAccount, []crclient.Object) {
	objects := []crclient.Object{}
	operatorServiceAccount := assets.HyperShiftOperatorServiceAccount{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, operatorServiceAccount)

	operatorClusterRole := assets.HyperShiftOperatorClusterRole{
		EnableCVOManagementClusterMetricsAccess: opts.EnableCVOManagementClusterMetricsAccess,
		ManagedService:                          opts.ManagedService,
	}.Build()
	objects = append(objects, operatorClusterRole)

	operatorClusterRoleBinding := assets.HyperShiftOperatorClusterRoleBinding{
		ClusterRole:    operatorClusterRole,
		ServiceAccount: operatorServiceAccount,
	}.Build()
	objects = append(objects, operatorClusterRoleBinding)

	operatorRole := assets.HyperShiftOperatorRole{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, operatorRole)

	operatorRoleBinding := assets.HyperShiftOperatorRoleBinding{
		ServiceAccount: operatorServiceAccount,
		Role:           operatorRole,
	}.Build()
	objects = append(objects, operatorRoleBinding)

	// In OpenShift management clusters, this RoleBinding is brought in through openshift/cluster-kube-apiserver-operator.
	// In ARO HCP, Hosted Clusters are running on AKS management clusters, so we need to provide this through the HO.
	if opts.ManagedService == hyperv1.AroHCP {
		roleBinding := assets.KubeSystemRoleBinding{
			Namespace: "kube-system",
		}.Build()
		objects = append(objects, roleBinding)
	}

	if opts.EnableAdminRBACGeneration {
		clientObjs := setupAdminRBAC(operatorNamespace)
		objects = append(objects, clientObjs...)
	}

	return operatorServiceAccount, objects
}

// setupAdminRBAC creates the RBAC resources for the HyperShift client and reader.
//
// This includes:
// - HyperShift client ClusterRole
// - HyperShift client ServiceAccount
// - HyperShift client ClusterRoleBinding
// - HyperShift reader ClusterRole
// - HyperShift reader ClusterRoleBinding
func setupAdminRBAC(operatorNamespace *corev1.Namespace) []crclient.Object {
	var objects []crclient.Object
	// hypershift-client admin persona for hostedclusters and nodepools creation
	clientClusterRole := assets.HyperShiftClientClusterRole{}.Build()
	objects = append(objects, clientClusterRole)

	clientServiceAccount := assets.HyperShiftClientServiceAccount{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, clientServiceAccount)

	clientRoleBinding := assets.HyperShiftClientClusterRoleBinding{
		ClusterRole:    clientClusterRole,
		ServiceAccount: clientServiceAccount,
		GroupName:      "hypershift-client",
	}.Build()
	objects = append(objects, clientRoleBinding)

	// hypershift-reader admin persona for inspecting hosted controlplanes and the operator
	readerClusterRole := assets.HyperShiftReaderClusterRole{}.Build()
	objects = append(objects, readerClusterRole)

	readerRoleBinding := assets.HyperShiftReaderClusterRoleBinding{
		ClusterRole: readerClusterRole,
		GroupName:   "hypershift-readers",
	}.Build()
	objects = append(objects, readerRoleBinding)
	return objects
}

// setupAuth creates the Secret & Config required for the HyperShift operator.
//
// This includes:
// - Pull secret
// - OIDC S3 credentials secret
// - Platform specific secrets (e.g. AWS credentials)
//
// Returns the OIDC S3 credentials secret, operator credentials secret, and a list of resources to apply
func setupAuth(opts Options, operatorNamespace *corev1.Namespace) (*corev1.Secret, *corev1.Secret, []crclient.Object, error) {
	var objects []crclient.Object
	var operatorCredentialsSecret *corev1.Secret
	var oidcSecret *corev1.Secret

	if len(opts.PullSecretFile) > 0 {
		pullSecretBytes, err := os.ReadFile(opts.PullSecretFile)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to read pull secret file: %w", err)
		}

		pullSecret := assets.HyperShiftPullSecret{
			Namespace:       operatorNamespace.Name,
			PullSecretBytes: pullSecretBytes,
		}.Build()
		objects = append(objects, pullSecret)
	}

	if opts.OIDCStorageProviderS3Credentials != "" {
		oidcCreds, err := os.ReadFile(opts.OIDCStorageProviderS3Credentials)
		if err != nil {
			return nil, nil, nil, err
		}

		oidcSecret = assets.HyperShiftOperatorOIDCProviderS3Secret{
			Namespace:                      operatorNamespace,
			OIDCStorageProviderS3CredBytes: oidcCreds,
			CredsKey:                       opts.OIDCStorageProviderS3CredentialsSecretKey,
		}.Build()
		objects = append(objects, oidcSecret)
	} else if opts.OIDCStorageProviderS3CredentialsSecret != "" {
		oidcSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: operatorNamespace.Name,
				Name:      opts.OIDCStorageProviderS3CredentialsSecret,
			},
		}
	}

	switch hyperv1.PlatformType(opts.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		if opts.AWSPrivateCreds != "" {
			credBytes, err := os.ReadFile(opts.AWSPrivateCreds)
			if err != nil {
				return nil, nil, nil, err
			}

			operatorCredentialsSecret = assets.HyperShiftOperatorCredentialsSecret{
				Namespace:  operatorNamespace,
				CredsBytes: credBytes,
				CredsKey:   opts.AWSPrivateCredentialsSecretKey,
			}.Build()
			objects = append(objects, operatorCredentialsSecret)
		} else if opts.AWSPrivateCredentialsSecret != "" {
			operatorCredentialsSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: operatorNamespace.Name,
					Name:      opts.AWSPrivateCredentialsSecret,
				},
			}
		}
	}
	if opts.OIDCStorageProviderS3BucketName != "" {
		objects = append(
			objects, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kube-public",
					Name:      "oidc-storage-provider-s3-config",
				},
				Data: map[string]string{
					"name":   opts.OIDCStorageProviderS3BucketName,
					"region": opts.OIDCStorageProviderS3Region,
				},
			},
		)
	}
	return oidcSecret, operatorCredentialsSecret, objects, nil
}
