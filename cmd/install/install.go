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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
)

type Options struct {
	Namespace                                 string
	HyperShiftImage                           string
	HyperShiftOperatorReplicas                int32
	Development                               bool
	Template                                  bool
	Format                                    string
	ExcludeEtcdManifests                      bool
	EnableOCPClusterMonitoring                bool
	EnableCIDebugOutput                       bool
	PrivatePlatform                           string
	AWSPrivateCreds                           string
	AWSPrivateRegion                          string
	OIDCStorageProviderS3Region               string
	OIDCStorageProviderS3BucketName           string
	OIDCStorageProviderS3Credentials          string
	OIDCStorageProviderS3CredentialsSecret    string
	OIDCStorageProviderS3CredentialsSecretKey string
	EnableAdminRBACGeneration                 bool
}

func (o *Options) Validate() error {
	var errs []error

	switch hyperv1.PlatformType(o.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		if o.AWSPrivateCreds == "" || o.AWSPrivateRegion == "" {
			errs = append(errs, fmt.Errorf("--aws-private-region and --aws-private-creds are required with --private-platform=%s", hyperv1.AWSPlatform))
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
	return errors.NewAggregate(errs)
}

func (o *Options) ApplyDefaults() {
	switch {
	case o.Development:
		o.HyperShiftOperatorReplicas = 0
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
		opts.EnableOCPClusterMonitoring = true
		opts.EnableCIDebugOutput = true
	}
	opts.PrivatePlatform = string(hyperv1.NonePlatform)

	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.PersistentFlags().StringVar(&opts.HyperShiftImage, "hypershift-image", version.HyperShiftImage, "The HyperShift image to deploy")
	cmd.PersistentFlags().BoolVar(&opts.Development, "development", false, "Enable tweaks to facilitate local development")
	cmd.PersistentFlags().BoolVar(&opts.ExcludeEtcdManifests, "exclude-etcd", false, "Leave out etcd manifests")
	cmd.PersistentFlags().BoolVar(&opts.EnableOCPClusterMonitoring, "enable-ocp-cluster-monitoring", opts.EnableOCPClusterMonitoring, "Development-only option that will make your OCP cluster unsupported: If the cluster Prometheus should be configured to scrape metrics")
	cmd.PersistentFlags().BoolVar(&opts.EnableCIDebugOutput, "enable-ci-debug-output", opts.EnableCIDebugOutput, "If extra CI debug output should be enabled")
	cmd.PersistentFlags().StringVar(&opts.PrivatePlatform, "private-platform", opts.PrivatePlatform, "Platform on which private clusters are supported by this operator (supports \"AWS\" or \"None\")")
	cmd.PersistentFlags().StringVar(&opts.AWSPrivateCreds, "aws-private-creds", opts.AWSPrivateCreds, "Path to an AWS credentials file with privileges sufficient to manage private cluster resources")
	cmd.PersistentFlags().StringVar(&opts.AWSPrivateRegion, "aws-private-region", opts.AWSPrivateRegion, "AWS region where private clusters are supported by this operator")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3Region, "oidc-storage-provider-s3-region", "", "Region of the OIDC bucket. Required for AWS guest clusters")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3BucketName, "oidc-storage-provider-s3-bucket-name", "", "Name of the bucket in which to store the clusters OIDC discovery information. Required for AWS guest clusters")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3Credentials, "oidc-storage-provider-s3-credentials", opts.OIDCStorageProviderS3Credentials, "Credentials to use for writing the OIDC documents into the S3 bucket. Required for AWS guest clusters")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3CredentialsSecret, "oidc-storage-provider-s3-secret", "", "Name of an existing secret containing the OIDC S3 credentials.")
	cmd.PersistentFlags().StringVar(&opts.OIDCStorageProviderS3CredentialsSecretKey, "oidc-storage-provider-s3-secret-key", "credentials", "Name of the secret key containing the OIDC S3 credentials.")
	cmd.PersistentFlags().BoolVar(&opts.EnableAdminRBACGeneration, "enable-admin-rbac-generation", false, "Generate RBAC manifests for hosted cluster admins")

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

func hyperShiftOperatorManifests(opts Options) ([]crclient.Object, error) {
	var objects []crclient.Object

	controlPlanePriorityClass := assets.HyperShiftControlPlanePriorityClass{}.Build()
	objects = append(objects, controlPlanePriorityClass)

	etcdPriorityClass := assets.HyperShiftEtcdPriorityClass{}.Build()
	objects = append(objects, etcdPriorityClass)

	apiCriticalPriorityClass := assets.HyperShiftAPICriticalPriorityClass{}.Build()
	objects = append(objects, apiCriticalPriorityClass)

	operatorNamespace := assets.HyperShiftNamespace{
		Name:                       opts.Namespace,
		EnableOCPClusterMonitoring: opts.EnableOCPClusterMonitoring,
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

	operatorDeployment := assets.HyperShiftOperatorDeployment{
		Namespace:                      operatorNamespace,
		OperatorImage:                  opts.HyperShiftImage,
		ServiceAccount:                 operatorServiceAccount,
		Replicas:                       opts.HyperShiftOperatorReplicas,
		EnableOCPClusterMonitoring:     opts.EnableOCPClusterMonitoring,
		EnableCIDebugOutput:            opts.EnableCIDebugOutput,
		PrivatePlatform:                opts.PrivatePlatform,
		AWSPrivateRegion:               opts.AWSPrivateRegion,
		AWSPrivateCreds:                opts.AWSPrivateCreds,
		OIDCBucketName:                 opts.OIDCStorageProviderS3BucketName,
		OIDCBucketRegion:               opts.OIDCStorageProviderS3Region,
		OIDCStorageProviderS3Secret:    oidcSecret,
		OIDCStorageProviderS3SecretKey: opts.OIDCStorageProviderS3CredentialsSecretKey,
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
		EnableOCPClusterMonitoring: opts.EnableOCPClusterMonitoring,
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

	var credBytes []byte
	switch hyperv1.PlatformType(opts.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		var err error
		credBytes, err = ioutil.ReadFile(opts.AWSPrivateCreds)
		if err != nil {
			return objects, err
		}
		operatorCredentialsSecret := assets.HyperShiftOperatorCredentialsSecret{
			Namespace:  operatorNamespace,
			CredsBytes: credBytes,
		}.Build()
		objects = append(objects, operatorCredentialsSecret)
	}

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

	return objects, nil
}
