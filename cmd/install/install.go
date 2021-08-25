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
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Options struct {
	Namespace                  string
	HyperShiftImage            string
	HyperShiftOperatorReplicas int32
	Development                bool
	Render                     bool
	ExcludeEtcdManifests       bool
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "install",
		Short:        "Installs the HyperShift operator",
		SilenceUsage: true,
	}

	var opts Options

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.Flags().StringVar(&opts.HyperShiftImage, "hypershift-image", version.HyperShiftImage, "The HyperShift image to deploy")
	cmd.Flags().BoolVar(&opts.Development, "development", false, "Enable tweaks to facilitate local development")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying")
	cmd.Flags().BoolVar(&opts.ExcludeEtcdManifests, "exclude-etcd", false, "Leave out etcd manifests")

	cmd.Run = func(cmd *cobra.Command, args []string) {
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

		var objects []crclient.Object

		objects = append(objects, hyperShiftOperatorManifests(opts)...)
		objects = append(objects, clusterAPIManifests()...)
		if !opts.ExcludeEtcdManifests {
			objects = append(objects, etcdManifests()...)
		}

		switch {
		case opts.Render:
			render(objects)
		default:
			err := apply(ctx, objects)
			if err != nil {
				panic(err)
			}
		}
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

func hyperShiftOperatorManifests(opts Options) []crclient.Object {
	hostedClustersCRD := assets.HyperShiftHostedClustersCustomResourceDefinition{}.Build()
	nodePoolsCRD := assets.HyperShiftNodePoolsCustomResourceDefinition{}.Build()
	hostedControlPlanesCRD := assets.HyperShiftHostedControlPlaneCustomResourceDefinition{}.Build()
	externalInfraClustersCRD := assets.HyperShiftExternalInfraClustersCustomResourceDefinition{}.Build()
	machineConfigServersCRD := assets.HyperShiftMachineConfigServersCustomResourceDefinition{}.Build()
	controlPlanePriorityClass := assets.HyperShiftControlPlanePriorityClass{}.Build()
	etcdPriorityClass := assets.HyperShiftEtcdPriorityClass{}.Build()
	apiCriticalPriorityClass := assets.HyperShiftAPICriticalPriorityClass{}.Build()
	operatorNamespace := assets.HyperShiftNamespace{
		Name: opts.Namespace,
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
	operatorDeployment := assets.HyperShiftOperatorDeployment{
		Namespace:      operatorNamespace,
		OperatorImage:  opts.HyperShiftImage,
		ServiceAccount: operatorServiceAccount,
		Replicas:       opts.HyperShiftOperatorReplicas,
	}.Build()
	operatorService := assets.HyperShiftOperatorService{
		Namespace: operatorNamespace,
	}.Build()
	prometheusRole := assets.HyperShiftPrometheusRole{
		Namespace: operatorNamespace,
	}.Build()
	prometheusRoleBinding := assets.HyperShiftOperatorPrometheusRoleBinding{
		Namespace: operatorNamespace,
		Role:      prometheusRole,
	}.Build()
	serviceMonitor := assets.HyperShiftServiceMonitor{
		Namespace: operatorNamespace,
	}.Build()

	return []crclient.Object{
		hostedClustersCRD,
		nodePoolsCRD,
		hostedControlPlanesCRD,
		externalInfraClustersCRD,
		machineConfigServersCRD,
		controlPlanePriorityClass,
		apiCriticalPriorityClass,
		etcdPriorityClass,
		operatorNamespace,
		operatorServiceAccount,
		operatorClusterRole,
		operatorClusterRoleBinding,
		operatorRole,
		operatorRoleBinding,
		operatorDeployment,
		operatorService,
		prometheusRole,
		prometheusRoleBinding,
		serviceMonitor,
	}
}

func clusterAPIManifests() []crclient.Object {
	clusterResourceSetBindingsCRD := assets.ClusterAPIClusterResourceSetBindingsCustomResourceDefinition{}.Build()
	clustersCRD := assets.ClusterAPIClusterResourceSetsCustomResourceDefinition{}.Build()
	clusterResourceSetsCRD := assets.ClusterAPIClustersCustomResourceDefinition{}.Build()
	machineDeploymentsCRD := assets.ClusterAPIMachineDeploymentsCustomResourceDefinition{}.Build()
	machineHealthChecksCRD := assets.ClusterAPIMachineHealthChecksCustomResourceDefinition{}.Build()
	machinesCRD := assets.ClusterAPIMachinesCustomResourceDefinition{}.Build()
	machineSetsCRD := assets.ClusterAPIMachineSetsCustomResourceDefinition{}.Build()
	awsClusterControllerIdentitiesCRD := assets.ClusterAPIAWSClusterControllerIdentitiesCustomResourceDefinition{}.Build()
	awsClusterRoleIdentitiesCRD := assets.ClusterAPIAWSClusterRoleIdentitiesCustomResourceDefinition{}.Build()
	awsClustersCRD := assets.ClusterAPIAWSClustersCustomResourceDefinition{}.Build()
	awsClusterStaticIdentitiesCRD := assets.ClusterAPIAWSClusterStaticIdentitiesCustomResourceDefinition{}.Build()
	awsMachinePoolsCRD := assets.ClusterAPIAWSMachinePoolsCustomResourceDefinition{}.Build()
	awsMachinesCRD := assets.ClusterAPIAWSMachinesCustomResourceDefinition{}.Build()
	awsMachineTemplatesCRD := assets.ClusterAPIAWSMachineTemplatesCustomResourceDefinition{}.Build()
	awsManagedClustersCRD := assets.ClusterAPIAWSManagedClustersCustomResourceDefinition{}.Build()
	awsManagedMachinePoolsCRD := assets.ClusterAPIAWSManagedMachinePoolsCustomResourceDefinition{}.Build()
	ibmCloudClustersCRD := assets.ClusterAPIIBMCloudClustersCustomResourceDefinition{}.Build()

	return []crclient.Object{
		clusterResourceSetBindingsCRD,
		clusterResourceSetsCRD,
		clustersCRD,
		machineDeploymentsCRD,
		machineHealthChecksCRD,
		machinesCRD,
		machineSetsCRD,
		awsClustersCRD,
		awsMachinePoolsCRD,
		awsMachinesCRD,
		awsMachineTemplatesCRD,
		awsManagedClustersCRD,
		awsManagedMachinePoolsCRD,
		awsClusterControllerIdentitiesCRD,
		awsClusterRoleIdentitiesCRD,
		awsClusterStaticIdentitiesCRD,
		ibmCloudClustersCRD,
	}
}

func etcdManifests() []crclient.Object {
	clustersCRD := assets.EtcdClustersCustomResourceDefinition{}.Build()
	backupsCRD := assets.EtcdBackupsCustomResourceDefinition{}.Build()
	restoresCRD := assets.EtcdRestoresCustomResourceDefinition{}.Build()

	return []crclient.Object{
		clustersCRD,
		backupsCRD,
		restoresCRD,
	}
}
