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
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"

	hyperapi "openshift.io/hypershift/api"
	"openshift.io/hypershift/cmd/install/assets"
)

type Options struct {
	Namespace                  string
	HyperShiftImage            string
	HyperShiftOperatorReplicas int32
	Development                bool
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Installs the HyperShift operator",
	}

	var opts Options

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.Flags().StringVar(&opts.HyperShiftImage, "hypershift-image", hyperapi.HyperShiftImage, "The HyperShift image to deploy")
	cmd.Flags().BoolVar(&opts.Development, "development", false, "Enable tweaks to facilitate local development")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		switch {
		case opts.Development:
			opts.HyperShiftOperatorReplicas = 0
		default:
			opts.HyperShiftOperatorReplicas = 1
		}

		var objects []runtime.Object

		objects = append(objects, hyperShiftOperatorManifests(opts)...)
		objects = append(objects, clusterAPIManifests()...)

		for _, object := range objects {
			err := hyperapi.YamlSerializer.Encode(object, os.Stdout)
			if err != nil {
				panic(err)
			}
			fmt.Println("---")
		}
	}

	return cmd
}

func hyperShiftOperatorManifests(opts Options) []runtime.Object {
	hostedClustersCRD := assets.HyperShiftHostedClustersCustomResourceDefinition{}.Build()
	nodePoolsCRD := assets.HyperShiftNodePoolsCustomResourceDefinition{}.Build()
	hostedControlPlanesCRD := assets.HyperShiftHostedControlPlaneCustomResourceDefinition{}.Build()
	externalInfraClustersCRD := assets.HyperShiftExternalInfraClustersCustomResourceDefinition{}.Build()
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

	return []runtime.Object{
		hostedClustersCRD,
		nodePoolsCRD,
		hostedControlPlanesCRD,
		externalInfraClustersCRD,
		operatorNamespace,
		operatorServiceAccount,
		operatorClusterRole,
		operatorClusterRoleBinding,
		operatorDeployment,
	}
}

func clusterAPIManifests() []runtime.Object {
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

	return []runtime.Object{
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
