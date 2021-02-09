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

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha3"
	capiv1 "sigs.k8s.io/cluster-api/api/v1alpha4"

	hyperapi "openshift.io/hypershift/api"
	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	"openshift.io/hypershift/internal/cmd/install/assets"
)

var (
	scheme         = runtime.NewScheme()
	yamlSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, scheme, scheme,
		json.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
)

func init() {
	capiaws.AddToScheme(scheme)
	clientgoscheme.AddToScheme(scheme)
	hyperv1.AddToScheme(scheme)
	capiv1.AddToScheme(scheme)
	configv1.AddToScheme(scheme)
	securityv1.AddToScheme(scheme)
	operatorv1.AddToScheme(scheme)
	routev1.AddToScheme(scheme)
	rbacv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	apiextensionsv1.AddToScheme(scheme)
}

type Options struct {
	Namespace       string
	HyperShiftImage string
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Installs the HyperShift operator",
	}

	var opts Options

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.Flags().StringVar(&opts.HyperShiftImage, "hypershift-image", hyperapi.HyperShiftImage, "The HyperShift image to deploy")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		var objects []runtime.Object

		objects = append(objects, hyperShiftOperatorManifests(opts)...)
		objects = append(objects, clusterAPIManifests()...)

		for _, object := range objects {
			err := yamlSerializer.Encode(object, os.Stdout)
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
