package fg

import (
	"bytes"
	"fmt"

	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func adaptJob(cpContext component.WorkloadContext, job *batchv1.Job) error {
	hcp := cpContext.HCP
	payloadVersion := cpContext.UserReleaseImageProvider.Version()
	clusterFeatureGate := configv1.FeatureGate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "FeatureGate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.FeatureGate != nil {
		clusterFeatureGate.Spec = *hcp.Spec.Configuration.FeatureGate
	}
	featureGateBuffer := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(&clusterFeatureGate, featureGateBuffer); err != nil {
		return fmt.Errorf("failed to encode feature gates: %w", err)
	}
	featureGateYaml := featureGateBuffer.String()

	util.UpdateContainer("render-feature-gates", job.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "PAYLOAD_VERSION",
				Value: payloadVersion,
			},
			corev1.EnvVar{
				Name:  "FEATURE_GATE_YAML",
				Value: featureGateYaml,
			},
		)
	})
	util.UpdateContainer("apply", job.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "PAYLOAD_VERSION",
				Value: payloadVersion,
			},
		)
	})
	return nil
}
