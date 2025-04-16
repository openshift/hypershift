package azure

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	hyperapi "github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcile(t *testing.T) {
	sizingConfig := &schedulingv1alpha1.ClusterSizingConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
			Sizes: []schedulingv1alpha1.SizeConfiguration{
				{
					Name: "small",
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 0,
						To:   ptr.To(uint32(1)),
					},
					Effects: &schedulingv1alpha1.Effects{
						KASGoMemLimit: ptr.To("1GiB"),
					},
				},
				{
					Name: "medium",
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 2,
						To:   ptr.To(uint32(2)),
					},
					Effects: &schedulingv1alpha1.Effects{
						KASGoMemLimit: ptr.To("2GiB"),
					},
				},
				{
					Name: "large",
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 3,
						To:   nil,
					},
					Effects: &schedulingv1alpha1.Effects{
						KASGoMemLimit: ptr.To("3GiB"),
					},
				},
			},
		},
		Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   schedulingv1alpha1.ClusterSizingConfigurationValidType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	hostedcluster := func(mods ...func(*hyperv1.HostedCluster)) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-namespace",
				Name:      "test",
				Labels: map[string]string{
					hyperv1.HostedClusterSizeLabel: "small",
				},
			},
		}
		for _, m := range mods {
			m(hc)
		}
		return hc
	}

	tests := []struct {
		name              string
		hc                *hyperv1.HostedCluster
		sizingConfig      *schedulingv1alpha1.ClusterSizingConfiguration
		expectError       bool
		expectErrText     string
		expectRequeue     bool
		expectAnnotations map[string]string
	}{
		{
			name:          "hosted cluster not found",
			hc:            nil,
			sizingConfig:  sizingConfig,
			expectError:   false,
			expectRequeue: false,
		},
		{
			name: "hosted cluster paused",
			hc: hostedcluster(func(hc *hyperv1.HostedCluster) {
				hc.Spec.PausedUntil = ptr.To(time.Now().Add(time.Hour).Format(time.RFC3339Nano))
			}),
			sizingConfig:  sizingConfig,
			expectError:   false,
			expectRequeue: true,
		},
		{
			name: "hosted cluster without size label",
			hc: hostedcluster(func(hc *hyperv1.HostedCluster) {
				delete(hc.Labels, hyperv1.HostedClusterSizeLabel)
			}),
			sizingConfig:  sizingConfig,
			expectError:   false,
			expectRequeue: false,
		},
		{
			name: "invalid cluster sizing configuration",
			hc:   hostedcluster(),
			sizingConfig: &schedulingv1alpha1.ClusterSizingConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
					Conditions: []metav1.Condition{
						{
							Type:   schedulingv1alpha1.ClusterSizingConfigurationValidType,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expectError:   false,
			expectRequeue: false,
		},
		{
			name: "size configuration not found",
			hc: hostedcluster(func(hc *hyperv1.HostedCluster) {
				hc.Labels[hyperv1.HostedClusterSizeLabel] = "extra-large"
			}),
			sizingConfig:  sizingConfig,
			expectError:   true,
			expectErrText: "could not find size configuration for size",
			expectRequeue: false,
		},
		{
			name:          "valid hosted cluster",
			hc:            hostedcluster(),
			sizingConfig:  sizingConfig,
			expectError:   false,
			expectRequeue: false,
			expectAnnotations: map[string]string{
				hyperv1.KubeAPIServerGOMemoryLimitAnnotation: "1GiB",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.sizingConfig).Build()
			if test.hc != nil {
				err := c.Create(context.Background(), test.hc)
				g.Expect(err).ToNot(HaveOccurred())
			}
			r := &Scheduler{
				Client: c,
			}
			req := reconcile.Request{}
			if test.hc != nil {
				req.Name = test.hc.Name
				req.Namespace = test.hc.Namespace
			} else {
				req.Name = "non-existent-hosted-cluster"
				req.Namespace = "non-existent-namespace"
			}
			result, err := r.Reconcile(context.Background(), req)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(test.expectErrText))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			if test.expectRequeue {
				g.Expect(result.RequeueAfter).ToNot(BeZero())
			} else {
				g.Expect(result.RequeueAfter).To(BeZero())
			}
			if test.expectAnnotations != nil {
				actual := &hyperv1.HostedCluster{}
				err := c.Get(context.Background(), client.ObjectKeyFromObject(test.hc), actual)
				g.Expect(err).ToNot(HaveOccurred())
				for key, value := range test.expectAnnotations {
					g.Expect(actual.Annotations).To(HaveKeyWithValue(key, value))
				}
			}
		})
	}
}
