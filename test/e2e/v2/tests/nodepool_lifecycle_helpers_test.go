//go:build e2ev2

package tests

import (
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestBuildTestNodePool(t *testing.T) {
	for _, test := range []struct {
		name                                  string
		labels                                map[string]string
		existingTaints, autoscaling, override bool
		zeroTemplate, trustWorker             bool
	}{
		{name: "When labels and taints are absent, it should initialize test defaults"},
		{name: "When a template has labels taints and autoscaling, it should preserve metadata and clear autoscaling", labels: map[string]string{"preserved": "true"}, existingTaints: true, autoscaling: true},
		{name: "When a template has the test label, it should replace it only in the copied pool", labels: map[string]string{"e2e.hypershift.openshift.io/nodepool": "original"}},
		{name: "When caller mutation overrides defaults, it should run after default initialization", labels: map[string]string{"preserved": "true"}, existingTaints: true, autoscaling: true, override: true},
		{name: "When the trust dispatcher starts with zero workers, it should create its own one-worker pool without changing initial intent", zeroTemplate: true, trustWorker: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			template := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "template", Namespace: "clusters", UID: "template-uid", Labels: map[string]string{"template-only": "true"}},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "hosted", Replicas: ptr.To(int32(2)), NodeLabels: test.labels,
					Release:  hyperv1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.23.0-x86_64"},
					Platform: hyperv1.NodePoolPlatform{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSNodePoolPlatform{InstanceType: "m5.xlarge", ResourceTags: []hyperv1.AWSNodePoolResourceTag{{Key: "preserved", Value: "true"}}}},
				},
			}
			if test.existingTaints {
				template.Spec.Taints = []hyperv1.Taint{{Key: "preserved", Value: "true", Effect: corev1.TaintEffectNoSchedule}}
			}
			if test.zeroTemplate {
				template.Spec.Replicas = ptr.To(int32(0))
			}
			template.Spec.Platform.AWS.SecurityGroups = []hyperv1.AWSResourceReference{{ID: ptr.To("sg-source")}}
			if test.autoscaling {
				template.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 4}
			}
			original := template.DeepCopy()
			var mutate func(*hyperv1.NodePool)
			if test.override {
				mutate = func(pool *hyperv1.NodePool) {
					pool.Spec.NodeLabels["e2e.hypershift.openshift.io/nodepool"] = "caller"
					pool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{Min: ptr.To(int32(0)), Max: 8}
					pool.Spec.Replicas = ptr.To(int32(3))
				}
			}
			if test.trustWorker {
				mutate = func(pool *hyperv1.NodePool) { pool.Spec.Replicas = ptr.To(int32(1)) }
			}
			pool := buildTestNodePool(template, "unit", mutate)
			assertTestNodePoolIdentityAndPlatform(t, pool, template)
			if test.trustWorker && (*pool.Spec.Replicas != 1 || *template.Spec.Replicas != 0) {
				t.Fatal("trust worker creation lost the independent zero-root intent")
			}
			if pool.Spec.NodeLabels["preserved"] != template.Spec.NodeLabels["preserved"] {
				t.Fatal("existing node label was lost")
			}
			wantLabel := "unit"
			if test.override {
				wantLabel = "caller"
			}
			if pool.Spec.NodeLabels["e2e.hypershift.openshift.io/nodepool"] != wantLabel {
				t.Fatal("incorrect test label or mutation ordering")
			}
			if test.override {
				if pool.Spec.AutoScaling == nil || pool.Spec.AutoScaling.Max != 8 || *pool.Spec.Replicas != 3 {
					t.Fatal("caller mutation did not override defaults")
				}
			} else if pool.Spec.AutoScaling != nil {
				t.Fatal("copied autoscaling was not cleared")
			}
			if len(pool.Spec.Taints) != len(template.Spec.Taints)+1 {
				t.Fatal("incorrect test taint count")
			}
			if len(template.Spec.Taints) > 0 && !reflect.DeepEqual(pool.Spec.Taints[:len(template.Spec.Taints)], template.Spec.Taints) {
				t.Fatal("existing taints were not preserved")
			}
			addedTaint := pool.Spec.Taints[len(pool.Spec.Taints)-1]
			if addedTaint.Key != "e2e-nodepool" || addedTaint.Value != "validation" || addedTaint.Effect != corev1.TaintEffectPreferNoSchedule {
				t.Fatal("test taint is missing or incorrect")
			}
			pool.Spec.NodeLabels["mutated"] = "true"
			pool.Spec.Platform.AWS.ResourceTags[0].Value = "mutated"
			pool.Spec.Taints[0].Value = "mutated"
			pool.Spec.Release.Image = "mutated"
			if !reflect.DeepEqual(template, original) {
				t.Fatal("building or mutating the result changed the original template")
			}
		})
	}
}

func assertTestNodePoolIdentityAndPlatform(t *testing.T, pool, template *hyperv1.NodePool) {
	t.Helper()
	if pool.Name == template.Name || !strings.HasPrefix(pool.Name, "hosted-unit-") || pool.Namespace != template.Namespace || pool.UID != "" || len(pool.Labels) != 0 {
		t.Fatal("result did not receive an isolated identity")
	}
	wantPlatform := template.DeepCopy().Spec.Platform
	wantPlatform.AWS.SecurityGroups = nil
	if !reflect.DeepEqual(pool.Spec.Platform, wantPlatform) || pool.Spec.Release != template.Spec.Release {
		t.Fatal("platform or release configuration was not preserved")
	}
}
