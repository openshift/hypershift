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
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Options struct {
	Namespace                        string
	HyperShiftImage                  string
	HyperShiftOperatorReplicas       int32
	Development                      bool
	Render                           bool
	ExcludeEtcdManifests             bool
	EnableOCPClusterMonitoring       bool
	EnableCIDebugOutput              bool
	PrivatePlatform                  string
	AWSPrivateCreds                  string
	AWSPrivateRegion                 string
	OIDCStorageProviderS3Credentials string
	OIDCStorageProviderS3Region      string
	OIDCStorageProviderS3BucketName  string
}

func (o *Options) Validate() error {
	switch hyperv1.PlatformType(o.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		if o.AWSPrivateCreds == "" || o.AWSPrivateRegion == "" {
			return fmt.Errorf("--aws-private-region and --aws-private-creds are required with --private-platform=%s", hyperv1.AWSPlatform)
		}
	case hyperv1.NonePlatform:
	default:
		return fmt.Errorf("--private-platform must be either %s or %s", hyperv1.AWSPlatform, hyperv1.NonePlatform)
	}
	return nil
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

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.Flags().StringVar(&opts.HyperShiftImage, "hypershift-image", version.HyperShiftImage, "The HyperShift image to deploy")
	cmd.Flags().BoolVar(&opts.Development, "development", false, "Enable tweaks to facilitate local development")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying")
	cmd.Flags().BoolVar(&opts.ExcludeEtcdManifests, "exclude-etcd", false, "Leave out etcd manifests")
	cmd.Flags().BoolVar(&opts.EnableOCPClusterMonitoring, "enable-ocp-cluster-monitoring", opts.EnableOCPClusterMonitoring, "Development-only option that will make your OCP cluster unsupported: If the cluster Prometheus should be configured to scrape metrics")
	cmd.Flags().BoolVar(&opts.EnableCIDebugOutput, "enable-ci-debug-output", opts.EnableCIDebugOutput, "If extra CI debug output should be enabled")
	cmd.Flags().StringVar(&opts.PrivatePlatform, "private-platform", opts.PrivatePlatform, "Platform on which private clusters are supported by this operator (supports \"AWS\" or \"None\")")
	cmd.Flags().StringVar(&opts.AWSPrivateCreds, "aws-private-creds", opts.AWSPrivateCreds, "Path to an AWS credentials file with privileges sufficient to manage private cluster resources")
	cmd.Flags().StringVar(&opts.AWSPrivateRegion, "aws-private-region", opts.AWSPrivateRegion, "AWS region where private clusters are supported by this operator")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3Credentials, "oidc-storage-provider-s3-credentials", opts.OIDCStorageProviderS3Credentials, "Credentials to use for writing the OIDC documents into the S3 bucket. Required for AWS guest clusters")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3Region, "oidc-storage-provider-s3-region", "", "Region of the OIDC bucket. Required for AWS guest clusters")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3BucketName, "oidc-storage-provider-s3-bucket-name", "", "Name of the bucket in which to store the clusters OIDC discovery information. Required for AWS guest clusters")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		switch {
		case opts.Development:
			opts.HyperShiftOperatorReplicas = 0
		default:
			opts.HyperShiftOperatorReplicas = 1
		}

		objects, err := hyperShiftOperatorManifests(opts)
		if err != nil {
			return err
		}

		switch {
		case opts.Render:
			render(objects)
		default:
			err := apply(ctx, objects)
			if err != nil {
				return err
			}
		}
		return nil
	}

	return cmd
}

func render(objects []crclient.Object) {
	for _, object := range objects {
		err := hyperapi.YamlSerializer.Encode(object, os.Stdout)
		if err != nil {
			panic(err)
		}
		fmt.Println("---")
	}
}

func apply(ctx context.Context, objects []crclient.Object) error {
	client := util.GetClientOrDie()
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
	controlPlanePriorityClass := assets.HyperShiftControlPlanePriorityClass{}.Build()
	etcdPriorityClass := assets.HyperShiftEtcdPriorityClass{}.Build()
	apiCriticalPriorityClass := assets.HyperShiftAPICriticalPriorityClass{}.Build()
	operatorNamespace := assets.HyperShiftNamespace{
		Name:                       opts.Namespace,
		EnableOCPClusterMonitoring: opts.EnableOCPClusterMonitoring,
	}.Build()
	operatorServiceAccount := assets.HyperShiftOperatorServiceAccount{
		Namespace: operatorNamespace,
	}.Build()
	operatorClusterRole := assets.HyperShiftOperatorClusterRole{}.Build()
	operatorClusterRoleBinding := assets.HyperShiftOperatorClusterRoleBinding{
		ClusterRole:    operatorClusterRole,
		ServiceAccount: operatorServiceAccount,
	}.Build()
	operatorRole := assets.HyperShiftOperatorRole{
		Namespace: operatorNamespace,
	}.Build()
	operatorRoleBinding := assets.HyperShiftOperatorRoleBinding{
		ServiceAccount: operatorServiceAccount,
		Role:           operatorRole,
	}.Build()
	var oidcStorageProviderS3CredBytes []byte
	if opts.OIDCStorageProviderS3Credentials != "" {
		var err error
		oidcStorageProviderS3CredBytes, err = ioutil.ReadFile(opts.OIDCStorageProviderS3Credentials)
		if err != nil {
			panic(err)
		}
	}
	var operatorOIDCProviderS3Config *assets.HyperShiftOperatorOIDCProviderS3Secret
	if opts.OIDCStorageProviderS3BucketName != "" {
		operatorOIDCProviderS3Config = &assets.HyperShiftOperatorOIDCProviderS3Secret{
			Namespace:                       operatorNamespace,
			OIDCStorageProviderS3CredBytes:  oidcStorageProviderS3CredBytes,
			OIDCStorageProviderS3BucketName: opts.OIDCStorageProviderS3BucketName,
			OIDCStorageProviderS3Region:     opts.OIDCStorageProviderS3Region,
		}
	}
	operatorDeployment := assets.HyperShiftOperatorDeployment{
		Namespace:                   operatorNamespace,
		OperatorImage:               opts.HyperShiftImage,
		ServiceAccount:              operatorServiceAccount,
		Replicas:                    opts.HyperShiftOperatorReplicas,
		EnableOCPClusterMonitoring:  opts.EnableOCPClusterMonitoring,
		EnableCIDebugOutput:         opts.EnableCIDebugOutput,
		PrivatePlatform:             opts.PrivatePlatform,
		AWSPrivateRegion:            opts.AWSPrivateRegion,
		AWSPrivateCreds:             opts.AWSPrivateCreds,
		OIDCStorageProviderS3Config: operatorOIDCProviderS3Config,
	}.Build()
	operatorService := assets.HyperShiftOperatorService{
		Namespace: operatorNamespace,
	}.Build()
	prometheusRole := assets.HyperShiftPrometheusRole{
		Namespace: operatorNamespace,
	}.Build()
	prometheusRoleBinding := assets.HyperShiftOperatorPrometheusRoleBinding{
		Namespace:                  operatorNamespace,
		Role:                       prometheusRole,
		EnableOCPClusterMonitoring: opts.EnableOCPClusterMonitoring,
	}.Build()
	serviceMonitor := assets.HyperShiftServiceMonitor{
		Namespace: operatorNamespace,
	}.Build()
	recordingRule := assets.HypershiftRecordingRule{
		Namespace: operatorNamespace,
	}.Build()

	var objects []crclient.Object

	var credBytes []byte
	switch hyperv1.PlatformType(opts.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		var err error
		credBytes, err = ioutil.ReadFile(opts.AWSPrivateCreds)
		if err != nil {
			return objects, err
		}
	}
	operatorCredentialsSecret := assets.HyperShiftOperatorCredentialsSecret{
		Namespace:  operatorNamespace,
		CredsBytes: credBytes,
	}.Build()

	objects = append(objects, assets.CustomResourceDefinitions(func(path string) bool {
		if strings.Contains(path, "etcd") && opts.ExcludeEtcdManifests {
			return false
		}
		return true
	})...)

	objects = append(objects, controlPlanePriorityClass)
	objects = append(objects, apiCriticalPriorityClass)
	objects = append(objects, etcdPriorityClass)
	objects = append(objects, operatorNamespace)
	objects = append(objects, operatorServiceAccount)
	objects = append(objects, operatorClusterRole)
	objects = append(objects, operatorClusterRoleBinding)
	objects = append(objects, operatorRole)
	objects = append(objects, operatorRoleBinding)
	switch hyperv1.PlatformType(opts.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		objects = append(objects, operatorCredentialsSecret)
	}
	objects = append(objects, operatorDeployment)
	objects = append(objects, operatorService)
	objects = append(objects, prometheusRole)
	objects = append(objects, prometheusRoleBinding)
	objects = append(objects, serviceMonitor)
	objects = append(objects, recordingRule)
	if opts.OIDCStorageProviderS3BucketName != "" {
		objects = append(objects, operatorOIDCProviderS3Config.Build())
		objects = append(objects, assets.OIDCStorageProviderS3ConfigMap(opts.OIDCStorageProviderS3BucketName, opts.OIDCStorageProviderS3Region))
	}
	return objects, nil
}
