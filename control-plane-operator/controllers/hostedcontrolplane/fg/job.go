package fg

import (
	"bytes"
	"context"
	"embed"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

//go:embed assets/*
var content embed.FS

var jobTemplate *batchv1.Job = assets.MustJob(content.ReadFile, "assets/job.yaml")

func ReconcileFeatureGateGenerationJob(ctx context.Context, job *batchv1.Job, hcp *hyperv1.HostedControlPlane, payloadVersion, configAPIImage, cpoImage string, setDefaultSecurityContext bool) error {
	job.Spec = jobTemplate.Spec

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
		c.Image = configAPIImage
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
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
		c.Image = cpoImage
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "PAYLOAD_VERSION",
				Value: payloadVersion,
			},
		)
	})

	dc := config.DeploymentConfig{}
	dc.AdditionalLabels = map[string]string{
		config.NeedManagementKASAccessLabel: "true",
	}
	dc.Scheduling.PriorityClass = config.DefaultPriorityClass
	dc.SetDefaults(hcp, nil, ptr.To(1))
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		dc.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	dc.SetDefaultSecurityContext = setDefaultSecurityContext
	dc.ApplyToJob(job)

	return nil
}

func NeedsUpdate(existing, updated *batchv1.Job) bool {
	existingPayloadVersion, existingFgYAML := getPayloadVersionAndFeatureGateYAML(existing)
	updatedPayloadVersion, updatedFgYAML := getPayloadVersionAndFeatureGateYAML(updated)
	return existingPayloadVersion != updatedPayloadVersion || existingFgYAML != updatedFgYAML
}

func getPayloadVersionAndFeatureGateYAML(job *batchv1.Job) (string, string) {
	if len(job.Spec.Template.Spec.InitContainers) == 0 {
		return "", ""
	}
	var payloadVersion, fgYAML string
	for _, env := range job.Spec.Template.Spec.InitContainers[0].Env {
		if env.Name == "PAYLOAD_VERSION" {
			payloadVersion = env.Value
			continue
		}
		if env.Name == "FEATURE_GATE_YAML" {
			fgYAML = env.Value
			continue
		}
	}
	return payloadVersion, fgYAML
}
