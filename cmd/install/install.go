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

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/version"

	cr "sigs.k8s.io/controller-runtime"
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
		Use:   "install",
		Short: "Installs the HyperShift operator",
	}

	var opts Options

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.Flags().StringVar(&opts.HyperShiftImage, "hypershift-image", version.HyperShiftImage, "The HyperShift image to deploy")
	cmd.Flags().BoolVar(&opts.Development, "development", false, "Enable tweaks to facilitate local development")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		switch {
		case opts.Development:
			opts.HyperShiftOperatorReplicas = 0
		default:
			opts.HyperShiftOperatorReplicas = 1
		}

		var objects []crclient.Object

		objects = append(objects, hyperShiftOperatorManifests(opts)...)
		objects = append(objects, clusterAPIManifests()...)

		switch {
		case opts.Render:
			render(objects)
		default:
			err := apply(context.TODO(), objects)
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
	client, err := crclient.New(cr.GetConfigOrDie(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}
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

	return []crclient.Object{
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
