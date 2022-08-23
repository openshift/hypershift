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
	"io/ioutil"
	"os"
	"strings"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	imageapi "github.com/openshift/api/image/v1"
	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/openshift/hypershift/support/metrics"
)

type Options struct {
	AdditionalTrustBundle                     string
	Namespace                                 string
	HyperShiftImage                           string
	ImageRefsFile                             string
	HyperShiftOperatorReplicas                int32
	Development                               bool
	EnableWebhook                             bool
	Template                                  bool
	Format                                    string
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
	EnableAdminRBACGeneration                 bool
	EnableUWMTelemetryRemoteWrite             bool
	MetricsSet                                metrics.MetricsSet
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
	return errors.NewAggregate(errs)
}

func (o *Options) ApplyDefaults() {
	switch {
	case o.Development:
		o.HyperShiftOperatorReplicas = 0
	case o.EnableWebhook:
		o.HyperShiftOperatorReplicas = 2
	default:
		o.HyperShiftOperatorReplicas = 1
	}
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "install",
		Short:        "Installs the HyperShift operator",
		SilenceUsage: true,
	}

	var opts Options
	if os.Getenv("CI") == "true" {
		opts.PlatformMonitoring = metrics.PlatformMonitoringAll
		opts.EnableCIDebugOutput = true
	}
	opts.PrivatePlatform = string(hyperv1.NonePlatform)
	opts.MetricsSet = metrics.DefaultMetricsSet

	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.PersistentFlags().StringVar(&opts.HyperShiftImage, "hypershift-image", version.HyperShiftImage, "The HyperShift image to deploy")
	cmd.PersistentFlags().BoolVar(&opts.Development, "development", false, "Enable tweaks to facilitate local development")
	cmd.PersistentFlags().BoolVar(&opts.EnableWebhook, "enable-webhook", false, "Enable webhook for hypershift API types")
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
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSProvider, "external-dns-provider", opts.OIDCStorageProviderS3Credentials, "Provider to use for managing DNS records using external-dns")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSCredentials, "external-dns-credentials", opts.OIDCStorageProviderS3Credentials, "Credentials to use for managing DNS records using external-dns")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSCredentialsSecret, "external-dns-secret", "", "Name of an existing secret containing the external-dns credentials.")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSDomainFilter, "external-dns-domain-filter", "", "Restrict external-dns to changes within the specifed domain.")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSTxtOwnerId, "external-dns-txt-owner-id", "", "external-dns TXT registry owner ID.")
	cmd.PersistentFlags().BoolVar(&opts.EnableAdminRBACGeneration, "enable-admin-rbac-generation", false, "Generate RBAC manifests for hosted cluster admins")
	cmd.PersistentFlags().StringVar(&opts.ImageRefsFile, "image-refs", opts.ImageRefsFile, "Image references to user in Hypershift installation")
	cmd.PersistentFlags().StringVar(&opts.AdditionalTrustBundle, "additional-trust-bundle", opts.AdditionalTrustBundle, "Path to a file with user CA bundle")
	cmd.PersistentFlags().Var(&opts.MetricsSet, "metrics-set", "The set of metrics to produce for each HyperShift control plane. Valid values are: Telemetry, SRE, All")
	cmd.PersistentFlags().BoolVar(&opts.EnableUWMTelemetryRemoteWrite, "enable-uwm-telemetry-remote-write", opts.EnableUWMTelemetryRemoteWrite, "If true, HyperShift operator ensures user workload monitoring is enabled and that it is configured to remote write telemetry metrics from control planes")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opts.ApplyDefaults()

		if err := opts.Validate(); err != nil {
			return err
		}

		objects, err := hyperShiftOperatorManifests(opts)
		if err != nil {
			return err
		}

		err = apply(cmd.Context(), objects)
		if err != nil {
			return err
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
				return err
			}
			fmt.Printf("applied %s %s/%s\n", object.GetObjectKind().GroupVersionKind().Kind, object.GetNamespace(), object.GetName())
		}
	}
	return nil
}

func fetchImageRefs(file string) (map[string]string, error) {
	content, err := ioutil.ReadFile(file)
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

func hyperShiftOperatorManifests(opts Options) ([]crclient.Object, error) {
	var objects []crclient.Object

	var images map[string]string
	if len(opts.ImageRefsFile) > 0 {
		var err error
		images, err = fetchImageRefs(opts.ImageRefsFile)
		if err != nil {
			return nil, err
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

	operatorServiceAccount := assets.HyperShiftOperatorServiceAccount{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, operatorServiceAccount)

	operatorClusterRole := assets.HyperShiftOperatorClusterRole{}.Build()
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

	if opts.EnableWebhook {
		validatingWebhookConfiguration := assets.HyperShiftValidatingWebhookConfiguration{
			Namespace: operatorNamespace,
		}.Build()
		objects = append(objects, validatingWebhookConfiguration)
	}

	var oidcSecret *corev1.Secret
	if opts.OIDCStorageProviderS3Credentials != "" {
		oidcCreds, err := ioutil.ReadFile(opts.OIDCStorageProviderS3Credentials)
		if err != nil {
			return nil, err
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

	var operatorCredentialsSecret *corev1.Secret
	switch hyperv1.PlatformType(opts.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		if opts.AWSPrivateCreds != "" {
			credBytes, err := ioutil.ReadFile(opts.AWSPrivateCreds)
			if err != nil {
				return objects, err
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

	var userCABundleCM *corev1.ConfigMap
	if opts.AdditionalTrustBundle != "" {
		userCABundle, err := ioutil.ReadFile(opts.AdditionalTrustBundle)
		if err != nil {
			return nil, err
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

	if len(opts.ExternalDNSProvider) > 0 {
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
			externalDNSCreds, err := ioutil.ReadFile(opts.ExternalDNSCredentials)
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
			Namespace: operatorNamespace,
			// TODO: need to look this up from somewhere
			Image:             "registry.redhat.io/edo/external-dns-rhel8@sha256:e8c50c1c158d08a99b1f388c65860c533209299fd0ff87f5c9fe29d7c9b5a4d1",
			ServiceAccount:    externalDNSServiceAccount,
			Provider:          opts.ExternalDNSProvider,
			DomainFilter:      opts.ExternalDNSDomainFilter,
			CredentialsSecret: externalDNSSecret,
			TxtOwnerId:        opts.ExternalDNSTxtOwnerId,
		}.Build()
		objects = append(objects, externalDNSDeployment)
	}

	operatorDeployment := assets.HyperShiftOperatorDeployment{
		AdditionalTrustBundle:          userCABundleCM,
		Namespace:                      operatorNamespace,
		OperatorImage:                  opts.HyperShiftImage,
		ServiceAccount:                 operatorServiceAccount,
		Replicas:                       opts.HyperShiftOperatorReplicas,
		EnableOCPClusterMonitoring:     opts.PlatformMonitoring == metrics.PlatformMonitoringAll,
		EnableCIDebugOutput:            opts.EnableCIDebugOutput,
		EnableWebhook:                  opts.EnableWebhook,
		PrivatePlatform:                opts.PrivatePlatform,
		AWSPrivateRegion:               opts.AWSPrivateRegion,
		AWSPrivateSecret:               operatorCredentialsSecret,
		AWSPrivateSecretKey:            opts.AWSPrivateCredentialsSecretKey,
		OIDCBucketName:                 opts.OIDCStorageProviderS3BucketName,
		OIDCBucketRegion:               opts.OIDCStorageProviderS3Region,
		OIDCStorageProviderS3Secret:    oidcSecret,
		OIDCStorageProviderS3SecretKey: opts.OIDCStorageProviderS3CredentialsSecretKey,
		Images:                         images,
		MetricsSet:                     opts.MetricsSet,
		IncludeVersion:                 !opts.Template,
		UWMTelemetry:                   opts.EnableUWMTelemetryRemoteWrite,
	}.Build()
	objects = append(objects, operatorDeployment)

	operatorService := assets.HyperShiftOperatorService{
		Namespace: operatorNamespace,
	}.Build()
	objects = append(objects, operatorService)

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

	objects = append(objects, assets.CustomResourceDefinitions(func(path string) bool {
		if strings.Contains(path, "etcd") && opts.ExcludeEtcdManifests {
			return false
		}
		return true
	})...)

	if opts.EnableAdminRBACGeneration {
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
	}

	if opts.OIDCStorageProviderS3BucketName != "" {
		objects = append(objects, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-public",
				Name:      "oidc-storage-provider-s3-config",
			},
			Data: map[string]string{
				"name":   opts.OIDCStorageProviderS3BucketName,
				"region": opts.OIDCStorageProviderS3Region,
			},
		})
	}

	for idx := range objects {
		gvk, err := apiutil.GVKForObject(objects[idx], hyperapi.Scheme)
		if err != nil {
			return nil, fmt.Errorf("failed to look up gvk for %T: %w", objects[idx], err)
		}
		// Everything that embedds metav1.TypeMeta implements this
		objects[idx].(interface {
			SetGroupVersionKind(gvk schema.GroupVersionKind)
		}).SetGroupVersionKind(gvk)
	}

	return objects, nil
}
