package aws

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	Provider          = "aws"
	ProviderConfigKey = "aws.conf"
)

const configTemplate = `[Global]
Zone = %s
VPC = %s
KubernetesClusterID = %s
SubnetID = %s`

func (p *AWSParams) ReconcileCloudConfig(cm *corev1.ConfigMap) error {
	util.EnsureOwnerRef(cm, p.OwnerRef)
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[ProviderConfigKey] = fmt.Sprintf(configTemplate, p.Zone, p.VPC, p.ClusterID, p.SubnetID)
	return nil
}
