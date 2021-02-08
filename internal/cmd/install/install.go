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
	Namespace         string
	HyperShiftImage   string
	CAPIManagerImage  string
	CAPIProviderImage string

	Output string
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Installs the HyperShift operator",
	}

	var opts Options

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "hypershift", "The namespace in which to install HyperShift")
	cmd.Flags().StringVar(&opts.HyperShiftImage, "hypershift-image", "registry.ci.openshift.org/hypershift/hypershift:latest", "The HyperShift image to deploy")
	cmd.Flags().StringVar(&opts.CAPIManagerImage, "capi-manager-image", "quay.io/hypershift/cluster-api:hypershift", "The CAPI Manager image to deploy")
	cmd.Flags().StringVar(&opts.CAPIProviderImage, "capi-provider-image", "quay.io/hypershift/cluster-api-provider-aws:master", "The CAPI Provider image to deploy")
	cmd.Flags().StringVar(&opts.Output, "output", "", "Render the resources to be installed as YAML instead of applying them")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		var objects []runtime.Object

		objects = append(objects, buildHyperShiftOperatorManifests(opts)...)
		objects = append(objects, buildClusterAPIManifests()...)

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

func buildHyperShiftOperatorManifests(opts Options) []runtime.Object {
	var objects []runtime.Object

	hostedClustersCRD := assets.HyperShiftHostedClustersCustomResourceDefinition{}.Build()
	objects = append(objects, hostedClustersCRD)

	nodePoolsCRD := assets.HyperShiftNodePoolsCustomResourceDefinition{}.Build()
	objects = append(objects, nodePoolsCRD)

	hostedControlPlanesCRD := assets.HyperShiftHostedControlPlaneCustomResourceDefinition{}.Build()
	objects = append(objects, hostedControlPlanesCRD)

	externalInfraClustersCRD := assets.HyperShiftExternalInfraClustersCustomResourceDefinition{}.Build()
	objects = append(objects, externalInfraClustersCRD)

	namespace := assets.HyperShiftNamespace{
		Name: opts.Namespace,
	}.Build()
	objects = append(objects, namespace)

	operatorServiceAccount := assets.HyperShiftOperatorServiceAccount{
		Namespace: namespace.Name,
	}.Build()
	objects = append(objects, operatorServiceAccount)

	operatorClusterRole := assets.HyperShiftOperatorClusterRole{}.Build()
	objects = append(objects, operatorClusterRole)

	operatorClusterRoleBinding := assets.HyperShiftOperatorClusterRoleBinding{
		ClusterRole:    operatorClusterRole,
		ServiceAccount: operatorServiceAccount,
	}.Build()
	objects = append(objects, operatorClusterRoleBinding)

	deployment := assets.HyperShiftOperatorDeployment{
		Namespace:     namespace.Name,
		OperatorImage: opts.HyperShiftImage,
	}.Build()
	objects = append(objects, deployment)

	return objects
}

func buildClusterAPIManifests() []runtime.Object {
	var objects []runtime.Object

	clustersCRD := assets.ClusterAPIClustersCustomResourceDefinition{}.Build()
	objects = append(objects, clustersCRD)

	machineDeploymentsCRD := assets.ClusterAPIMachineDeploymentsCustomResourceDefinition{}.Build()
	objects = append(objects, machineDeploymentsCRD)

	machineHealthChecksCRD := assets.ClusterAPIMachineHealthChecksCustomResourceDefinition{}.Build()
	objects = append(objects, machineHealthChecksCRD)

	machinesCRD := assets.ClusterAPIMachinesCustomResourceDefinition{}.Build()
	objects = append(objects, machinesCRD)

	machineSetsCRD := assets.ClusterAPIMachineSetsCustomResourceDefinition{}.Build()
	objects = append(objects, machineSetsCRD)

	awsClustersCRD := assets.ClusterAPIAWSClustersCustomResourceDefinition{}.Build()
	objects = append(objects, awsClustersCRD)

	awsMachinePoolsCRD := assets.ClusterAPIAWSMachinePoolsCustomResourceDefinition{}.Build()
	objects = append(objects, awsMachinePoolsCRD)

	awsMachinesCRD := assets.ClusterAPIAWSMachinesCustomResourceDefinition{}.Build()
	objects = append(objects, awsMachinesCRD)

	awsMachineTemplatesCRD := assets.ClusterAPIAWSMachineTemplatesCustomResourceDefinition{}.Build()
	objects = append(objects, awsMachineTemplatesCRD)

	awsManagedClustersCRD := assets.ClusterAPIAWSManagedClustersCustomResourceDefinition{}.Build()
	objects = append(objects, awsManagedClustersCRD)

	awsManagedMachinePoolsCRD := assets.ClusterAPIAWSManagedMachinePoolsCustomResourceDefinition{}.Build()
	objects = append(objects, awsManagedMachinePoolsCRD)

	return objects
}
