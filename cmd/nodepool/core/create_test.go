package core

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestValidateHostedClusterPayloadSupportsNodePoolCPUArch(t *testing.T) {
	for _, testCase := range []struct {
		name                     string
		hc                       *hyperv1.HostedCluster
		nodePoolCPUArch          string
		buildHostedClusterObject bool
		expectedErr              bool
	}{
		{
			name: "when a valid HC exists and the payload type is Multi, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.Multi,
				},
			},
			buildHostedClusterObject: true,
			expectedErr:              false,
		},
		{
			name: "when a valid HC exists and the payload type is AMD64 and the NodePool CPU arch is AMD64, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			nodePoolCPUArch:          hyperv1.ArchitectureAMD64,
			buildHostedClusterObject: true,
			expectedErr:              false,
		},
		{
			name: "when a valid HC exists and the payload type is AMD64 and the NodePool CPU arch is ARM64, then there is an error",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			nodePoolCPUArch:          hyperv1.ArchitectureARM64,
			buildHostedClusterObject: true,
			expectedErr:              true,
		},
		{
			name: "when a valid HC does not exist, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			buildHostedClusterObject: false,
			expectedErr:              false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewWithT(t)

			var objs []client.Object

			if testCase.buildHostedClusterObject {
				objs = append(objs, testCase.hc)
			}

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			err := validateHostedClusterPayloadSupportsNodePoolCPUArch(context.TODO(), c, testCase.hc.Name, testCase.hc.Namespace, testCase.nodePoolCPUArch)
			if testCase.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestValidMinorVersionCompatibility(t *testing.T) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	tests := []struct {
		name                 string
		controlPlaneVersion  string
		nodePoolReleaseImage string
		expectedError        string
	}{
		{
			name:                 "same version",
			controlPlaneVersion:  "4.18.5",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
			expectedError:        "",
		},
		{
			name:                 "nodePool version higher than control plane",
			controlPlaneVersion:  "4.17.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64",
			expectedError:        "NodePool minor version 4.18 cannot be higher than the HostedCluster minor version 4.17",
		},
		{
			name:                 "nodePool version lower than control plane by 1 (odd version)",
			controlPlaneVersion:  "4.17.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.37-x86_64",
			expectedError:        "",
		},
		{
			name:                 "nodePool version lower than control plane by 2 (odd version)",
			controlPlaneVersion:  "4.17.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.15.47-x86_64",
			expectedError:        "NodePool minor version 4.15 is not compatible with the HostedCluster minor version 4.17 (max allowed difference: 1)",
		},
		{
			name:                 "nodePool version lower than control plane by 2 (even version)",
			controlPlaneVersion:  "4.18.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64",
			expectedError:        "",
		},
		{
			name:                 "nodePool version lower than control plane by 3 (even version)",
			controlPlaneVersion:  "4.18.0",
			nodePoolReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.15.0-x86_64",
			expectedError:        "NodePool minor version 4.15 is not compatible with the HostedCluster minor version 4.18 (max allowed difference: 2)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create a HostedCluster with the specified version
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:   configv1.CompletedUpdate,
								Version: test.controlPlaneVersion,
							},
						},
					},
				},
			}

			// Create a pull secret
			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":{"quay.io":{"auth":"","email":""}}}`),
				},
			}

			// Create the resources in the fake client
			objs := []client.Object{hc, pullSecret}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			// Run the test
			err := validMinorVersionCompatibility(context.TODO(), c, "test-cluster", "test-namespace", test.nodePoolReleaseImage)

			// Check the results
			if test.expectedError == "" {
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(test.expectedError))
			}
		})
	}
}
