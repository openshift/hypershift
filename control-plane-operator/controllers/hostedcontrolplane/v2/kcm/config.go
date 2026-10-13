package kcm

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	kcpv1 "github.com/openshift/api/kubecontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
)

const (
	KubeControllerManagerConfigKey = "config.json"
	RecyclerPodTemplateKey         = "recycler-pod.yaml"
)

func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	data := cm.Data[KubeControllerManagerConfigKey]
	config := &kcpv1.KubeControllerManagerConfig{}
	if err := util.DeserializeResource(data, config, api.Scheme); err != nil {
		return fmt.Errorf("unable to decode existing KubeControllerManager configuration: %w", err)
	}

	serviceServingCA, err := getServiceServingCA(cpContext)
	if err != nil {
		return err
	}
	if serviceServingCA != nil {
		config.ServiceServingCert.CertFile = "/etc/kubernetes/certs/service-ca/service-ca.crt"
	}

	configStr, err := util.SerializeResource(config, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize KubeControllerManager configuration: %w", err)
	}
	cm.Data[KubeControllerManagerConfigKey] = configStr
	return nil
}

func adaptRecyclerConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	toolsImage := cpContext.ReleaseImageProvider.GetImage("tools")
	cm.Data[RecyclerPodTemplateKey] = strings.Replace(cm.Data[RecyclerPodTemplateKey], "{{.tools_image}}", toolsImage, 1)
	return nil
}
