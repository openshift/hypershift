package assets

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

type EtcdClustersCustomResourceDefinition struct{}

func (o EtcdClustersCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("etcd/etcd.database.coreos.com_etcdclusters.yaml")
}

type EtcdBackupsCustomResourceDefinition struct{}

func (o EtcdBackupsCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("etcd/etcd.database.coreos.com_etcdbackups.yaml")
}

type EtcdRestoresCustomResourceDefinition struct{}

func (o EtcdRestoresCustomResourceDefinition) Build() *apiextensionsv1.CustomResourceDefinition {
	return getCustomResourceDefinition("etcd/etcd.database.coreos.com_etcdrestores.yaml")
}
