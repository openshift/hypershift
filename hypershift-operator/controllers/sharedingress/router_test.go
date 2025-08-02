package sharedingress

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileRouterDeployment(t *testing.T) {
	type args struct {
		deployment *appsv1.Deployment
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Valid config map and deployment",
			args: args{
				deployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "test-namespace",
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ReconcileRouterDeployment(tt.args.deployment, "test-hypershift-operator-image")
			if (err != nil) != tt.wantErr {
				t.Errorf("ReconcileRouterDeployment() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if *tt.args.deployment.Spec.Replicas != 2 {
					t.Errorf("Expected replicas to be 2, got %d", *tt.args.deployment.Spec.Replicas)
				}

				expectedAffinity := &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
							{
								Weight: 100,
								PodAffinityTerm: corev1.PodAffinityTerm{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "router",
										},
									},
									TopologyKey: "topology.kubernetes.io/zone",
								},
							},
						},
					},
				}
				if !reflect.DeepEqual(tt.args.deployment.Spec.Template.Spec.Affinity, expectedAffinity) {
					t.Errorf("Expected affinity to be %v, got %v", expectedAffinity, tt.args.deployment.Spec.Template.Spec.Affinity)
				}
			}
		})
	}
}
