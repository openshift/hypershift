package konnectivity

import (
	"maps"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
)

func TestReconcileAgentDaemonSet(t *testing.T) {
	t.Parallel()
	const (
		releaseImage  = "quay.io/openshift-release-dev/ocp-release:5.1.0-x86_64"
		restartDate   = "2026-11-10T13:00:00Z"
		manualRestart = "2026-11-01T08:00:00Z"
		existingHash  = "4f2a9c1e"
		newHash       = "b7d03e55"
	)

	newParams := func(annotations map[string]string, agentSecretHash string) *KonnectivityParams {
		return &KonnectivityParams{
			Image:                 "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:7f0d2c8a3b1e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a",
			ExternalAddress:       "konnectivity-clusters-example.apps.mgmt.example.com",
			ExternalPort:          443,
			AdditionalAnnotations: annotations,
			AgentSecretHash:       agentSecretHash,
		}
	}
	daemonSetWithTemplateAnnotations := func(annotations map[string]string) *appsv1.DaemonSet {
		daemonset := &appsv1.DaemonSet{}
		daemonset.Spec.Template.Annotations = annotations
		return daemonset
	}

	testCases := []struct {
		name                string
		daemonset           *appsv1.DaemonSet
		params              *KonnectivityParams
		expectedAnnotations map[string]string
	}{
		{
			name:      "When ReconcileAgentDaemonSet is called, it should include --sync-forever in the agent args",
			daemonset: &appsv1.DaemonSet{},
			params:    newParams(map[string]string{hyperv1.ReleaseImageAnnotation: releaseImage}, ""),
			expectedAnnotations: map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
			},
		},
		{
			name:      "When AgentSecretHash is set, it should set the agent secret hash annotation on the pod template",
			daemonset: &appsv1.DaemonSet{},
			params:    newParams(map[string]string{hyperv1.ReleaseImageAnnotation: releaseImage}, newHash),
			expectedAnnotations: map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
				AgentSecretHashAnnotation:      newHash,
			},
		},
		{
			name: "When AgentSecretHash differs from the existing pod template hash, it should replace the hash annotation",
			daemonset: daemonSetWithTemplateAnnotations(map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
				AgentSecretHashAnnotation:      existingHash,
			}),
			params: newParams(map[string]string{hyperv1.ReleaseImageAnnotation: releaseImage}, newHash),
			expectedAnnotations: map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
				AgentSecretHashAnnotation:      newHash,
			},
		},
		{
			name: "When AgentSecretHash is empty and the pod template has a hash, it should keep the existing hash",
			daemonset: daemonSetWithTemplateAnnotations(map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
				AgentSecretHashAnnotation:      existingHash,
			}),
			params: newParams(map[string]string{hyperv1.ReleaseImageAnnotation: releaseImage}, ""),
			expectedAnnotations: map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
				AgentSecretHashAnnotation:      existingHash,
			},
		},
		{
			name:      "When AgentSecretHash is empty and the pod template has no hash, it should not set the hash annotation",
			daemonset: daemonSetWithTemplateAnnotations(map[string]string{hyperv1.ReleaseImageAnnotation: releaseImage}),
			params:    newParams(map[string]string{hyperv1.ReleaseImageAnnotation: releaseImage}, ""),
			expectedAnnotations: map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
			},
		},
		{
			name:      "When release-image and restart-date annotations are set, it should keep them alongside the hash annotation",
			daemonset: &appsv1.DaemonSet{},
			params: newParams(map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
				hyperv1.RestartDateAnnotation:  restartDate,
			}, newHash),
			expectedAnnotations: map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
				hyperv1.RestartDateAnnotation:  restartDate,
				AgentSecretHashAnnotation:      newHash,
			},
		},
		{
			name: "When the pod template has unmanaged annotations and AgentSecretHash is empty, it should keep the existing hash and drop the unmanaged annotations as before",
			daemonset: daemonSetWithTemplateAnnotations(map[string]string{
				hyperv1.ReleaseImageAnnotation:      releaseImage,
				AgentSecretHashAnnotation:           existingHash,
				"kubectl.kubernetes.io/restartedAt": manualRestart,
			}),
			params: newParams(map[string]string{hyperv1.ReleaseImageAnnotation: releaseImage}, ""),
			expectedAnnotations: map[string]string{
				hyperv1.ReleaseImageAnnotation: releaseImage,
				AgentSecretHashAnnotation:      existingHash,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			originalAdditionalAnnotations := maps.Clone(tc.params.AdditionalAnnotations)

			ReconcileAgentDaemonSet(tc.daemonset, tc.params, hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}, configv1.ProxyStatus{})

			g.Expect(tc.daemonset.Spec.Template.Annotations).To(Equal(tc.expectedAnnotations))
			g.Expect(tc.daemonset.Spec.Template.Spec.Containers).To(HaveLen(1))
			g.Expect(tc.daemonset.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--sync-forever"))

			// Reconciling the resulting DaemonSet again with the same params must not change the pod template.
			firstTemplate := tc.daemonset.Spec.Template.DeepCopy()
			ReconcileAgentDaemonSet(tc.daemonset, tc.params, hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}, configv1.ProxyStatus{})
			g.Expect(tc.daemonset.Spec.Template).To(Equal(*firstTemplate))

			// The pod template must not share or modify the params annotations map.
			g.Expect(tc.params.AdditionalAnnotations).To(Equal(originalAdditionalAnnotations))
			tc.daemonset.Spec.Template.Annotations["example.com/added-after-reconcile"] = "true"
			g.Expect(tc.params.AdditionalAnnotations).ToNot(HaveKey("example.com/added-after-reconcile"))
		})
	}
}
