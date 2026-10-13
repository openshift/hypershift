package openstack

import (
	"os"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	capo "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"github.com/blang/semver"
	"github.com/google/go-cmp/cmp"
)

func TestReconcileOpenStackCluster(t *testing.T) {
	const externalNetworkID = "a42211a2-4d2c-426f-9413-830e4b4abbbc"
	const networkID = "803084c1-70a2-44d3-a484-3b9c08dedee0"
	const subnetID = "e08dd45e-1bce-42c7-a5a9-3f7e1e55640e"
	apiEndpoint := hyperv1.APIEndpoint{
		Host: "api-endpoint",
		Port: 6443,
	}
	testCases := []struct {
		name                         string
		hostedCluster                *hyperv1.HostedCluster
		expectedOpenStackClusterSpec capo.OpenStackClusterSpec
		wantErr                      bool
	}{
		{
			name: "CAPO provisioned network and subnet",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "cluster-123",
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.0.0.0/24")}},
					},
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "openstack-credentials",
								CloudName: "openstack",
							},
							ManagedSubnets: []hyperv1.SubnetSpec{{
								DNSNameservers: []string{"1.1.1.1"},
								AllocationPools: []hyperv1.AllocationPool{{
									Start: "10.0.0.1",
									End:   "10.0.0.10",
								}}}},
							NetworkMTU: ptr.To(1500),
						}},
				},
			},
			expectedOpenStackClusterSpec: capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
				ManagedSubnets: []capo.SubnetSpec{{
					CIDR:           "10.0.0.0/24",
					DNSNameservers: []string{"1.1.1.1"},
					AllocationPools: []capo.AllocationPool{{
						Start: "10.0.0.1",
						End:   "10.0.0.10",
					}}},
				},
				NetworkMTU: ptr.To(1500),
				ControlPlaneEndpoint: &capiv1.APIEndpoint{
					Host: "api-endpoint",
					Port: 6443,
				},
				DisableAPIServerFloatingIP: ptr.To(true),
				ManagedSecurityGroups: &capo.ManagedSecurityGroups{
					AllNodesSecurityGroupRules: defaultWorkerSecurityGroupRules([]string{"10.0.0.0/24"}),
				},
				Tags: []string{"openshiftClusterID=cluster-123"},
			},
			wantErr: false,
		},
		{
			name: "User provided network and subnet by ID on hosted cluster",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "cluster-123",
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "openstack-credentials",
								CloudName: "openstack",
							},
							ExternalNetwork: &hyperv1.NetworkParam{
								ID: ptr.To(externalNetworkID),
							},
							Network: &hyperv1.NetworkParam{
								ID: ptr.To(networkID),
							},
							Subnets: []hyperv1.SubnetParam{
								{ID: ptr.To(subnetID)},
							},
						}}}},
			expectedOpenStackClusterSpec: capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
				ExternalNetwork: &capo.NetworkParam{
					ID: ptr.To(externalNetworkID),
				},
				Subnets: []capo.SubnetParam{{ID: ptr.To(subnetID)}},
				Network: &capo.NetworkParam{ID: ptr.To(networkID)},
				ControlPlaneEndpoint: &capiv1.APIEndpoint{
					Host: "api-endpoint",
					Port: 6443,
				},
				DisableAPIServerFloatingIP: ptr.To(true),
				ManagedSecurityGroups: &capo.ManagedSecurityGroups{
					AllNodesSecurityGroupRules: defaultWorkerSecurityGroupRules([]string{"192.168.1.0/24"}),
				},
				Tags: []string{"openshiftClusterID=cluster-123"},
			},
			wantErr: false,
		},
		{
			name: "User provided network and subnet by tag on hosted cluster",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "cluster-123",
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "openstack-credentials",
								CloudName: "openstack",
							},
							Network: &hyperv1.NetworkParam{
								Filter: &hyperv1.NetworkFilter{
									FilterByNeutronTags: hyperv1.FilterByNeutronTags{
										Tags: []hyperv1.NeutronTag{"test"},
									}},
							},
							Subnets: []hyperv1.SubnetParam{
								{Filter: &hyperv1.SubnetFilter{
									FilterByNeutronTags: hyperv1.FilterByNeutronTags{
										Tags: []hyperv1.NeutronTag{"test"},
									},
								}},
							},
							Tags: []string{"hcp-id=123"},
						}}}},
			expectedOpenStackClusterSpec: capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
				Subnets: []capo.SubnetParam{{
					Filter: &capo.SubnetFilter{
						FilterByNeutronTags: capo.FilterByNeutronTags{
							Tags: []capo.NeutronTag{"test"},
						},
					},
				}},
				Network: &capo.NetworkParam{
					Filter: &capo.NetworkFilter{
						FilterByNeutronTags: capo.FilterByNeutronTags{
							Tags: []capo.NeutronTag{"test"},
						}},
				},
				ControlPlaneEndpoint: &capiv1.APIEndpoint{
					Host: "api-endpoint",
					Port: 6443,
				},
				DisableAPIServerFloatingIP: ptr.To(true),
				ManagedSecurityGroups: &capo.ManagedSecurityGroups{
					AllNodesSecurityGroupRules: defaultWorkerSecurityGroupRules([]string{"192.168.1.0/24"}),
				},
				Tags: []string{"openshiftClusterID=cluster-123", "hcp-id=123"},
			},
			wantErr: false,
		},
		{
			name: "Missing machine networks",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name:      "openstack-credentials",
								CloudName: "openstack",
							},
							Network: &hyperv1.NetworkParam{
								Filter: &hyperv1.NetworkFilter{
									FilterByNeutronTags: hyperv1.FilterByNeutronTags{
										Tags: []hyperv1.NeutronTag{"test"},
									}},
							},
							Subnets: []hyperv1.SubnetParam{
								{Filter: &hyperv1.SubnetFilter{
									FilterByNeutronTags: hyperv1.FilterByNeutronTags{
										Tags: []hyperv1.NeutronTag{"test"},
									},
								}},
							},
						}}}},
			expectedOpenStackClusterSpec: capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			initialOpenStackClusterSpec := capo.OpenStackClusterSpec{
				IdentityRef: capo.OpenStackIdentityReference{
					Name:      "openstack-credentials",
					CloudName: "openstack",
				},
			}
			err := reconcileOpenStackClusterSpec(tc.hostedCluster, &initialOpenStackClusterSpec, apiEndpoint)
			if (err != nil) != tc.wantErr {
				t.Fatalf("reconcileOpenStackClusterSpec() error = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(initialOpenStackClusterSpec, tc.expectedOpenStackClusterSpec); diff != "" {
				t.Errorf("reconciled OpenStack cluster spec differs from expected OpenStack cluster spec: %s", diff)
			}
		})
	}
}

func TestCAPIProviderDeploymentSpec(t *testing.T) {
	testCases := []struct {
		name           string
		hcluster       *hyperv1.HostedCluster
		payloadVersion *semver.Version
		expectedSpec   *appsv1.DeploymentSpec
		expectedErr    bool
		envVars        map[string]string
	}{
		{
			name: "deployment spec on 4.18.0",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cluster",
					Namespace:   "clusters",
					Annotations: map[string]string{},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{},
					},
					InfraID: "123",
				},
			},
			payloadVersion: ptr.To(semver.MustParse("4.18.0")),
			expectedSpec: &appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](1),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						TerminationGracePeriodSeconds: ptr.To[int64](10),
						Volumes: []corev1.Volume{
							{
								Name: "capi-webhooks-tls",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										DefaultMode: ptr.To[int32](0640),
										SecretName:  "capi-webhooks-tls",
									},
								},
							},
							{
								Name: "svc-kubeconfig",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										DefaultMode: ptr.To[int32](0640),
										SecretName:  "service-network-admin-kubeconfig",
									},
								},
							},
						},
						Containers: []corev1.Container{{
							Name:            "manager",
							Image:           "capi-provider-image",
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/manager"},
							Args: []string{
								"--namespace=$(MY_NAMESPACE)",
								"--leader-elect",
								"--v=2",
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("10Mi"),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "healthz",
									ContainerPort: 9440,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("healthz"),
									},
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromString("healthz"),
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "capi-webhooks-tls",
									ReadOnly:  true,
									MountPath: "/tmp/k8s-webhook-server/serving-certs",
								},
								{
									Name:      "svc-kubeconfig",
									MountPath: "/etc/kubernetes",
								},
							},
							Env: []corev1.EnvVar{
								{
									Name: "MY_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
						}},
					},
				},
			},
			expectedErr: false,
			envVars:     map[string]string{},
		},
		{
			name: "deployment spec on 4.19.0 (with ORC)",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cluster",
					Namespace:   "clusters",
					Annotations: map[string]string{},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{},
					},
					InfraID: "123",
				},
			},
			payloadVersion: ptr.To(semver.MustParse("4.19.0")),
			expectedSpec: &appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](1),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						TerminationGracePeriodSeconds: ptr.To[int64](10),
						Volumes: []corev1.Volume{
							{
								Name: "capi-webhooks-tls",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										DefaultMode: ptr.To[int32](0640),
										SecretName:  "capi-webhooks-tls",
									},
								},
							},
							{
								Name: "svc-kubeconfig",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										DefaultMode: ptr.To[int32](0640),
										SecretName:  "service-network-admin-kubeconfig",
									},
								},
							},
						},
						Containers: []corev1.Container{
							{
								Name:            "manager",
								Image:           "capi-provider-image",
								ImagePullPolicy: corev1.PullIfNotPresent,
								Command:         []string{"/manager"},
								Args: []string{
									"--namespace=$(MY_NAMESPACE)",
									"--leader-elect",
									"--v=2",
								},
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("10m"),
										corev1.ResourceMemory: resource.MustParse("10Mi"),
									},
								},
								Ports: []corev1.ContainerPort{
									{
										Name:          "healthz",
										ContainerPort: 9440,
										Protocol:      corev1.ProtocolTCP,
									},
								},
								LivenessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										HTTPGet: &corev1.HTTPGetAction{
											Path: "/healthz",
											Port: intstr.FromString("healthz"),
										},
									},
								},
								ReadinessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										HTTPGet: &corev1.HTTPGetAction{
											Path: "/readyz",
											Port: intstr.FromString("healthz"),
										},
									},
								},
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "capi-webhooks-tls",
										ReadOnly:  true,
										MountPath: "/tmp/k8s-webhook-server/serving-certs",
									},
									{
										Name:      "svc-kubeconfig",
										MountPath: "/etc/kubernetes",
									},
								},
								Env: []corev1.EnvVar{
									{
										Name: "MY_NAMESPACE",
										ValueFrom: &corev1.EnvVarSource{
											FieldRef: &corev1.ObjectFieldSelector{
												FieldPath: "metadata.namespace",
											},
										},
									},
								},
							},
							{
								Name:            "orc-manager",
								Image:           "orc-image",
								ImagePullPolicy: corev1.PullIfNotPresent,
								Command:         []string{"/manager"},
								Args: []string{
									"--namespace=$(MY_NAMESPACE)",
									"--leader-elect",
									"--health-probe-bind-address=:8081",
									"--zap-log-level=4",
								},
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: ptr.To(false),
									Capabilities: &corev1.Capabilities{
										Drop: []corev1.Capability{
											"ALL",
										},
									},
								},
								LivenessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										HTTPGet: &corev1.HTTPGetAction{
											Path: "/healthz",
											Port: intstr.FromInt(8081),
										},
									},
									InitialDelaySeconds: 15,
									PeriodSeconds:       20,
								},
								ReadinessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										HTTPGet: &corev1.HTTPGetAction{
											Path: "/readyz",
											Port: intstr.FromInt(8081),
										},
									},
									InitialDelaySeconds: 5,
									PeriodSeconds:       10,
								},
								Resources: corev1.ResourceRequirements{
									Limits: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("500m"),
										corev1.ResourceMemory: resource.MustParse("128Mi"),
									},
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("10m"),
										corev1.ResourceMemory: resource.MustParse("64Mi"),
									},
								},
								Env: []corev1.EnvVar{
									{
										Name: "MY_NAMESPACE",
										ValueFrom: &corev1.EnvVarSource{
											FieldRef: &corev1.ObjectFieldSelector{
												FieldPath: "metadata.namespace",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErr: false,
			envVars:     map[string]string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			openStack := OpenStack{
				capiProviderImage: "capi-provider-image",
				orcImage:          "orc-image",
				payloadVersion:    tc.payloadVersion,
			}
			result, err := openStack.CAPIProviderDeploymentSpec(tc.hcluster, nil)
			if tc.expectedErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if diff := cmp.Diff(tc.expectedSpec, result); diff != "" {
					t.Errorf("reconciled CAPO Provider DeploymentSpec differs from expected spec: %s", diff)
				}
			}
		})
	}
}
