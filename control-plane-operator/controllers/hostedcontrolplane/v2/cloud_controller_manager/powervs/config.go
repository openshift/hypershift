package powervs

import (
	"bytes"
	"fmt"
	"html/template"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

const (
	configKey = "ccm-config"
)

func adaptConfig(cpContext component.ControlPlaneContext, cm *corev1.ConfigMap) error {
	powervsPlatform := cpContext.HCP.Spec.Platform.PowerVS
	if powervsPlatform == nil {
		return fmt.Errorf(".spec.platform.powervs is not defined")
	}

	config := map[string]string{
		"ClusterID":                cpContext.HCP.Name,
		"AccountID":                powervsPlatform.AccountID,
		"G2workerServiceAccountID": powervsPlatform.AccountID,
		"G2ResourceGroupName":      powervsPlatform.ResourceGroup,
		"G2VpcSubnetNames":         powervsPlatform.VPC.Subnet,
		"G2VpcName":                powervsPlatform.VPC.Name,
		"Region":                   powervsPlatform.VPC.Region,
		"PowerVSCloudInstanceID":   powervsPlatform.ServiceInstanceID,
		"PowerVSRegion":            powervsPlatform.Region,
		"PowerVSZone":              powervsPlatform.Zone,
	}

	templateData := cm.Data[configKey]
	template := template.Must(template.New("ccmConfigMap").Parse(templateData))
	configData := &bytes.Buffer{}
	err := template.Execute(configData, config)
	if err != nil {
		return fmt.Errorf("error while parsing ccm config map template %v", err)
	}

	cm.Data[configKey] = configData.String()
	return nil
}
