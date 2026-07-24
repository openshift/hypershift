package sharedingress

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/podspec"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func TestReconcileRouterDeployment(t *testing.T) {
	tests := []struct {
		name   string
		assert func(*WithT, *appsv1.Deployment)
	}{
		{
			name: "When reconciling a valid deployment it should set the default scheduling configuration",
			assert: func(g *WithT, deployment *appsv1.Deployment) {
				g.Expect(deployment.Spec.Replicas).ToNot(BeNil())
				g.Expect(*deployment.Spec.Replicas).To(Equal(int32(2)))

				expectedAffinity := &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
							{
								Weight: 100,
								Preference: corev1.NodeSelectorTerm{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      "aro-hcp.azure.com/role",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"infra"},
										},
									},
								},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: hcpRouterLabels(),
								},
								TopologyKey: corev1.LabelTopologyZone,
							},
						},
					},
				}
				g.Expect(deployment.Spec.Template.Spec.Affinity).To(Equal(expectedAffinity))

				expectedTolerations := []corev1.Toleration{
					{
						Key:      "infra",
						Value:    "true",
						Effect:   corev1.TaintEffectNoSchedule,
						Operator: corev1.TolerationOpEqual,
					},
				}
				g.Expect(deployment.Spec.Template.Spec.Tolerations).To(Equal(expectedTolerations))
			},
		},
		{
			name: "When reconciling router deployment it should harden the HAProxy container security context",
			assert: func(g *WithT, deployment *appsv1.Deployment) {
				routerContainer := podspec.FindContainer("private-router", deployment.Spec.Template.Spec.Containers)
				g.Expect(routerContainer).ToNot(BeNil())
				g.Expect(routerContainer.SecurityContext).ToNot(BeNil())
				g.Expect(routerContainer.SecurityContext.AllowPrivilegeEscalation).ToNot(BeNil())
				g.Expect(*routerContainer.SecurityContext.AllowPrivilegeEscalation).To(BeFalse())
				g.Expect(routerContainer.SecurityContext.ReadOnlyRootFilesystem).ToNot(BeNil())
				g.Expect(*routerContainer.SecurityContext.ReadOnlyRootFilesystem).To(BeTrue())
				g.Expect(routerContainer.SecurityContext.SeccompProfile).ToNot(BeNil())
				g.Expect(routerContainer.SecurityContext.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))
				g.Expect(routerContainer.SecurityContext.Capabilities).ToNot(BeNil())
				g.Expect(routerContainer.SecurityContext.Capabilities.Drop).To(Equal([]corev1.Capability{"ALL"}))
				g.Expect(routerContainer.SecurityContext.Capabilities.Add).To(BeEmpty())
			},
		},
		{
			name: "When reconciling router deployment it should harden the config-generator container security context",
			assert: func(g *WithT, deployment *appsv1.Deployment) {
				configGeneratorContainer := podspec.FindContainer("config-generator", deployment.Spec.Template.Spec.Containers)
				g.Expect(configGeneratorContainer).ToNot(BeNil())
				g.Expect(configGeneratorContainer.SecurityContext).ToNot(BeNil())
				g.Expect(configGeneratorContainer.SecurityContext.AllowPrivilegeEscalation).ToNot(BeNil())
				g.Expect(*configGeneratorContainer.SecurityContext.AllowPrivilegeEscalation).To(BeFalse())
				g.Expect(configGeneratorContainer.SecurityContext.Capabilities).ToNot(BeNil())
				g.Expect(configGeneratorContainer.SecurityContext.Capabilities.Drop).To(Equal([]corev1.Capability{"ALL"}))
				g.Expect(configGeneratorContainer.SecurityContext.Capabilities.Add).To(BeEmpty())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deployment",
					Namespace: "test-namespace",
				},
			}

			g.Expect(ReconcileRouterDeployment(deployment, "test-hypershift-operator-image")).To(Succeed())
			tc.assert(g, deployment)
		})
	}
}

func TestReconcileRouterPodDisruptionBudget(t *testing.T) {
	tests := []struct {
		name                              string
		initialUnhealthyPodEvictionPolicy *policyv1.UnhealthyPodEvictionPolicyType
	}{
		{
			name: "When reconciling a new PDB it should set unhealthyPodEvictionPolicy to AlwaysAllow",
		},
		{
			name:                              "When a PDB already has unhealthyPodEvictionPolicy set to IfHealthyBudget it should overwrite it to AlwaysAllow",
			initialUnhealthyPodEvictionPolicy: ptr.To(policyv1.IfHealthyBudget),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			pdb := &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "router",
					Namespace: "test-namespace",
				},
				Spec: policyv1.PodDisruptionBudgetSpec{
					UnhealthyPodEvictionPolicy: tc.initialUnhealthyPodEvictionPolicy,
				},
			}
			ownerRef := config.OwnerRef{}

			ReconcileRouterPodDisruptionBudget(pdb, ownerRef)

			g.Expect(pdb.Spec.MinAvailable).To(Equal(ptr.To(intstr.FromInt32(1))))
			g.Expect(pdb.Spec.UnhealthyPodEvictionPolicy).ToNot(BeNil())
			g.Expect(*pdb.Spec.UnhealthyPodEvictionPolicy).To(Equal(policyv1.AlwaysAllow))
		})
	}
}

func TestReconcileRouteStatus(t *testing.T) {
	tests := []struct {
		name              string
		route             *routev1.Route
		canonicalHostname string
		expectUpdate      bool
		expectedHostname  string
	}{
		{
			name: "When canonical hostname is set, it should update route ingress",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{Host: "api.test.example.com"},
			},
			canonicalHostname: "lb.example.com",
			expectUpdate:      true,
			expectedHostname:  "lb.example.com",
		},
		{
			name: "When canonical hostname matches existing, it should not update",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{Host: "api.test.example.com"},
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{
						{
							RouterCanonicalHostname: "lb.example.com",
							RouterName:              "router",
						},
					},
				},
			},
			canonicalHostname: "lb.example.com",
			expectUpdate:      false,
			expectedHostname:  "lb.example.com",
		},
		{
			name: "When canonical hostname changes, it should update route ingress",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{Host: "api.test.example.com"},
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{
						{
							RouterCanonicalHostname: "old-lb.example.com",
							RouterName:              "router",
						},
					},
				},
			},
			canonicalHostname: "new-lb.example.com",
			expectUpdate:      true,
			expectedHostname:  "new-lb.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			original := tc.route.DeepCopy()
			ReconcileRouteStatus(tc.route, tc.canonicalHostname)
			if tc.expectUpdate {
				g.Expect(tc.route.Status.Ingress).To(HaveLen(1))
				g.Expect(tc.route.Status.Ingress[0].RouterCanonicalHostname).To(Equal(tc.expectedHostname))
				g.Expect(tc.route.Status.Ingress[0].RouterName).To(Equal("router"))
			} else {
				g.Expect(tc.route.Status).To(Equal(original.Status))
			}
		})
	}
}
