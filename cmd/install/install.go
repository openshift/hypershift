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
	"k8s.io/apimachinery/pkg/types"

	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/version"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Options struct {
	Namespace                  string
	HyperShiftImage            string
	HyperShiftOperatorReplicas int32
	Development                bool
	Render                     bool
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
		objects = append(objects, etcdManifests()...)

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
		err = client.Patch(ctx, object, crclient.RawPatch(types.ApplyPatchType, objectBytes.Bytes()), crclient.ForceOwnership, crclient.FieldOwner("hypershift"))
		if err != nil {
			return err
		}
		fmt.Printf("applied %s %s/%s\n", object.GetObjectKind().GroupVersionKind().Kind, object.GetNamespace(), object.GetName())
	}
	return nil
}

func hyperShiftOperatorManifests(opts Options) []crclient.Object {
	hostedClustersCRD := assets.HyperShiftHostedClustersCustomResourceDefinition{}.Build()
	nodePoolsCRD := assets.HyperShiftNodePoolsCustomResourceDefinition{}.Build()
	hostedControlPlanesCRD := assets.HyperShiftHostedControlPlaneCustomResourceDefinition{}.Build()
	externalInfraClustersCRD := assets.HyperShiftExternalInfraClustersCustomResourceDefinition{}.Build()
	machineConfigServersCRD := assets.HyperShiftMachineConfigServersCustomResourceDefinition{}.Build()
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
		operatorNamespace,
		operatorServiceAccount,
		operatorClusterRole,
		operatorClusterRoleBinding,
		operatorDeployment,
		operatorService,
		prometheusRole,
		prometheusRoleBinding,
		serviceMonitor,
	}
}

func clusterAPIManifests() []crclient.Object {
	clustersCRD := assets.ClusterAPIClustersCustomResourceDefinition{}.Build()
	machineDeploymentsCRD := assets.ClusterAPIMachineDeploymentsCustomResourceDefinition{}.Build()
	machineHealthChecksCRD := assets.ClusterAPIMachineHealthChecksCustomResourceDefinition{}.Build()
	machinesCRD := assets.ClusterAPIMachinesCustomResourceDefinition{}.Build()
	machineSetsCRD := assets.ClusterAPIMachineSetsCustomResourceDefinition{}.Build()
	awsClustersCRD := assets.ClusterAPIAWSClustersCustomResourceDefinition{}.Build()
	awsMachinePoolsCRD := assets.ClusterAPIAWSMachinePoolsCustomResourceDefinition{}.Build()
	awsMachinesCRD := assets.ClusterAPIAWSMachinesCustomResourceDefinition{}.Build()
	awsMachineTemplatesCRD := assets.ClusterAPIAWSMachineTemplatesCustomResourceDefinition{}.Build()
	awsManagedClustersCRD := assets.ClusterAPIAWSManagedClustersCustomResourceDefinition{}.Build()
	awsManagedMachinePoolsCRD := assets.ClusterAPIAWSManagedMachinePoolsCustomResourceDefinition{}.Build()

	return []crclient.Object{
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
