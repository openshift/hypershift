package assets

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

type ClusterAPIClustersCustomResourceDefinition struct{}

func (o ClusterAPIClustersCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/cluster.x-k8s.io_clusters.yaml")
}

type ClusterAPIMachineDeploymentsCustomResourceDefinition struct{}

func (o ClusterAPIMachineDeploymentsCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/cluster.x-k8s.io_machinedeployments.yaml")
}

type ClusterAPIMachineHealthChecksCustomResourceDefinition struct{}

func (o ClusterAPIMachineHealthChecksCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/cluster.x-k8s.io_machinehealthchecks.yaml")
}

type ClusterAPIMachinesCustomResourceDefinition struct{}

func (o ClusterAPIMachinesCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/cluster.x-k8s.io_machines.yaml")
}

type ClusterAPIMachineSetsCustomResourceDefinition struct{}

func (o ClusterAPIMachineSetsCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/cluster.x-k8s.io_machinesets.yaml")
}

type ClusterAPIAWSClustersCustomResourceDefinition struct{}

func (o ClusterAPIAWSClustersCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/infrastructure.cluster.x-k8s.io_awsclusters.yaml")
}

type ClusterAPIAWSMachinePoolsCustomResourceDefinition struct{}

func (o ClusterAPIAWSMachinePoolsCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/infrastructure.cluster.x-k8s.io_awsmachinepools.yaml")
}

type ClusterAPIAWSMachinesCustomResourceDefinition struct{}

func (o ClusterAPIAWSMachinesCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/infrastructure.cluster.x-k8s.io_awsmachines.yaml")
}

type ClusterAPIAWSMachineTemplatesCustomResourceDefinition struct{}

func (o ClusterAPIAWSMachineTemplatesCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/infrastructure.cluster.x-k8s.io_awsmachinetemplates.yaml")
}

type ClusterAPIAWSManagedClustersCustomResourceDefinition struct{}

func (o ClusterAPIAWSManagedClustersCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/infrastructure.cluster.x-k8s.io_awsmanagedclusters.yaml")
}

type ClusterAPIAWSManagedMachinePoolsCustomResourceDefinition struct{}

func (o ClusterAPIAWSManagedMachinePoolsCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("cluster-api/infrastructure.cluster.x-k8s.io_awsmanagedmachinepools.yaml")
}
