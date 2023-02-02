package aws

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
)

func TestReconcileAWSCluster(t *testing.T) {
	testCases := []struct {
		name              string
		initialAWSCluster *capiaws.AWSCluster
		hostedCluster     *hyperv1.HostedCluster

		expectedAWSCluster *capiaws.AWSCluster
	}{
		{
			name:              "Tags get copied over",
			initialAWSCluster: &capiaws.AWSCluster{},
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "foo", Value: "bar"},
				},
			}}}},

			expectedAWSCluster: &capiaws.AWSCluster{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					"cluster.x-k8s.io/managed-by": "external",
				}},
				Spec: capiaws.AWSClusterSpec{
					AdditionalTags: capiaws.Tags{"foo": "bar"},
				},
				Status: capiaws.AWSClusterStatus{
					Ready: true,
				},
			},
		},
		{
			name: "Existing tags get removed",
			initialAWSCluster: &capiaws.AWSCluster{Spec: capiaws.AWSClusterSpec{AdditionalTags: capiaws.Tags{
				"to-be-removed": "value",
			}}},
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "foo", Value: "bar"},
				},
			}}}},

			expectedAWSCluster: &capiaws.AWSCluster{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					"cluster.x-k8s.io/managed-by": "external",
				}},
				Spec: capiaws.AWSClusterSpec{
					AdditionalTags: capiaws.Tags{"foo": "bar"},
				},
				Status: capiaws.AWSClusterStatus{
					Ready: true,
				},
			},
		},
		{
			name: "No tags on hostedcluster clears existing awscluster tags",
			initialAWSCluster: &capiaws.AWSCluster{Spec: capiaws.AWSClusterSpec{AdditionalTags: capiaws.Tags{
				"to-be-removed": "value",
			}}},
			hostedCluster: &hyperv1.HostedCluster{},

			expectedAWSCluster: &capiaws.AWSCluster{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					"cluster.x-k8s.io/managed-by": "external",
				}},
				Status: capiaws.AWSClusterStatus{
					Ready: true,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := reconcileAWSCluster(tc.initialAWSCluster, tc.hostedCluster, hyperv1.APIEndpoint{}); err != nil {
				t.Fatalf("reconcileAWSCluster failed: %v", err)
			}
			if diff := cmp.Diff(tc.initialAWSCluster, tc.expectedAWSCluster); diff != "" {
				t.Errorf("reconciled AWS cluster differs from expcted AWS cluster: %s", diff)
			}
		})
	}
}
