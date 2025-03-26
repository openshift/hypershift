package kcm

import (
	"encoding/json"
	"fmt"
	"html/template"
	"path"
	"strings"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"

	kcpv1 "github.com/openshift/api/kubecontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KubeControllerManagerConfigKey = "config.json"
	ServiceServingCAKey            = "service-ca.crt"
	RecyclerPodTemplateKey         = "recycler-pod.yaml"
)

func ReconcileConfig(config, serviceServingCA *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	serializedConfig, err := generateConfig(serviceServingCA)
	if err != nil {
		return fmt.Errorf("failed to create apiserver config: %w", err)
	}
	config.Data[KubeControllerManagerConfigKey] = serializedConfig
	return nil
}

func generateConfig(serviceServingCA *corev1.ConfigMap) (string, error) {
	var serviceServingCAPath string
	if serviceServingCA != nil {
		serviceServingCAPath = path.Join(serviceServingCAMount.Path(kcmContainerMain().Name, kcmVolumeServiceServingCA().Name), ServiceServingCAKey)
	}
	config := kcpv1.KubeControllerManagerConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeControllerManagerConfig",
			APIVersion: kcpv1.GroupVersion.String(),
		},
		ExtendedArguments: map[string]kcpv1.Arguments{
			"leader-elect":                []string{"true"},
			"leader-elect-renew-deadline": []string{config.KCMRecommendedRenewDeadline},
			"leader-elect-retry-period":   []string{config.KCMRecommendedRetryPeriod},
		},
		ServiceServingCert: kcpv1.ServiceServingCert{
			CertFile: serviceServingCAPath,
		},
	}
	b, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func ReconcileRecyclerConfig(config *corev1.ConfigMap, ownerRef config.OwnerRef, releaseImageProvider imageprovider.ReleaseImageProvider) error {
	var result strings.Builder

	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}

	data := map[string]string{
		"rhtoolsImageName": releaseImageProvider.GetImage("tools"),
	}

	// https://github.com/openshift/cluster-kube-controller-manager-operator/blob/64b4c1ba/bindata/assets/kube-controller-manager/recycler-cm.yaml
	templateContent := `apiVersion: v1
kind: Pod
metadata:
  name: recycler-pod
  namespace: openshift-infra
  annotations:
  target.workload.openshift.io/management: '{"effect": "PreferredDuringScheduling"}'
spec:
  activeDeadlineSeconds: 60
  restartPolicy: Never
  serviceAccountName: pv-recycler-controller
  containers:
  - name: recycler-container
    image: {{.rhtoolsImageName}}
    command:
    - "/bin/bash"
    args:
    - "-c"
    - "test -e /scrub && rm -rf /scrub/..?* /scrub/.[!.]* /scrub/*  && test -z \"$(ls -A /scrub)\" || exit 1"
    volumeMounts:
    - mountPath: /scrub
      name: vol
    securityContext:
    runAsUser: 0
    priorityClassName: openshift-user-critical
    resources:
    requests:
      memory: 50Mi
      cpu: 10m
  volumes:
  - name: vol
`

	tmpl, err := template.New("recycler-pod").Parse(templateContent)
	if err != nil {
		return fmt.Errorf("failed to parse recycler pod template: %w", err)
	}

	err = tmpl.Execute(&result, data)
	if err != nil {
		return fmt.Errorf("failed to render the recycler pod template: %w", err)
	}

	config.Data[RecyclerPodTemplateKey] = result.String()

	return nil
}
