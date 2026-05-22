package hostedcluster

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/k8sutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var platformProviderOverrideAnnotations = []struct {
	name       string
	annotation string
}{
	{name: "When the AWS provider image override changes, it should return true", annotation: hyperv1.ClusterAPIProviderAWSImage},
	{name: "When the Azure provider image override changes, it should return true", annotation: hyperv1.ClusterAPIAzureProviderImage},
	{name: "When the GCP provider image override changes, it should return true", annotation: hyperv1.ClusterAPIGCPProviderImage},
	{name: "When the agent provider image override changes, it should return true", annotation: hyperv1.ClusterAPIAgentProviderImage},
	{name: "When the KubeVirt provider image override changes, it should return true", annotation: hyperv1.ClusterAPIKubeVirtProviderImage},
	{name: "When the PowerVS provider image override changes, it should return true", annotation: hyperv1.ClusterAPIPowerVSProviderImage},
	{name: "When the OpenStack provider image override changes, it should return true", annotation: hyperv1.ClusterAPIOpenStackProviderImage},
	{name: "When the OpenStack resource controller image override changes, it should return true", annotation: hyperv1.OpenStackResourceControllerImage},
}

func TestHostedClusterActionableAnnotationChanged_WhenAnnotationsChangeItShouldReturnExpectedResult(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		oldAnnotations map[string]string
		newAnnotations map[string]string
		expected       bool
	}{
		{
			name: "When a mirrored annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.CleanupCloudResourcesAnnotation: "false",
			},
			newAnnotations: map[string]string{
				hyperv1.CleanupCloudResourcesAnnotation: "true",
			},
			expected: true,
		},
		{
			name: "When a prefixed annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-example": "one",
			},
			newAnnotations: map[string]string{
				hyperv1.IdentityProviderOverridesAnnotationPrefix + "-example": "two",
			},
			expected: true,
		},
		{
			name: "When the restart annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation: "2026-05-05T10:00:00Z",
			},
			newAnnotations: map[string]string{
				hyperv1.RestartDateAnnotation: "2026-05-05T10:05:00Z",
			},
			expected: true,
		},
		{
			name: "When a direct reconciliation annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.ForceUpgradeToAnnotation: "quay.io/openshift-release-dev/ocp-release:4.19.0-x86_64",
			},
			newAnnotations: map[string]string{
				hyperv1.ForceUpgradeToAnnotation: "quay.io/openshift-release-dev/ocp-release:4.19.1-x86_64",
			},
			expected: true,
		},
		{
			name: "When the kubevirt escape hatch annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "false",
			},
			newAnnotations: map[string]string{
				hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation: "true",
			},
			expected: true,
		},
		{
			name: "When the destroy grace period annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.HCDestroyGracePeriodAnnotation: "5m",
			},
			newAnnotations: map[string]string{
				hyperv1.HCDestroyGracePeriodAnnotation: "10m",
			},
			expected: true,
		},
		{
			name: "When the pod security override annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.PodSecurityAdmissionLabelOverrideAnnotation: "privileged",
			},
			newAnnotations: map[string]string{
				hyperv1.PodSecurityAdmissionLabelOverrideAnnotation: "restricted",
			},
			expected: true,
		},
		{
			name: "When the cluster API manager image annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.ClusterAPIManagerImage: "quay.io/example/capi:v1",
			},
			newAnnotations: map[string]string{
				hyperv1.ClusterAPIManagerImage: "quay.io/example/capi:v2",
			},
			expected: true,
		},
		{
			name: "When the skip release validation annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.SkipReleaseImageValidation: "true",
			},
			newAnnotations: map[string]string{},
			expected:       true,
		},
		{
			name: "When the skip KAS conflict SAN validation annotation changes, it should return true",
			oldAnnotations: map[string]string{
				hyperv1.SkipKASConflicSANValidation: "true",
			},
			newAnnotations: map[string]string{},
			expected:       true,
		},
		{
			name: "When the scope annotation changes, it should return true",
			oldAnnotations: map[string]string{
				k8sutil.HostedClustersScopeAnnotation: "one",
			},
			newAnnotations: map[string]string{
				k8sutil.HostedClustersScopeAnnotation: "two",
			},
			expected: true,
		},
		{
			name: "When a non action annotation changes, it should return false",
			oldAnnotations: map[string]string{
				"example.com/ignored": "old",
			},
			newAnnotations: map[string]string{
				"example.com/ignored": "new",
			},
			expected: false,
		},
		{
			name: "When actionable annotations do not change, it should return false",
			oldAnnotations: map[string]string{
				hyperv1.CleanupCloudResourcesAnnotation: "true",
			},
			newAnnotations: map[string]string{
				hyperv1.CleanupCloudResourcesAnnotation: "true",
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if actual := hostedClusterActionableAnnotationChanged(tc.oldAnnotations, tc.newAnnotations); actual != tc.expected {
				t.Fatalf("expected actionable annotation change to be %t, got %t", tc.expected, actual)
			}
		})
	}
}

func TestHostedClusterActionableAnnotationChanged_WhenAPlatformProviderOverrideChangesItShouldReturnTrue(t *testing.T) {
	t.Parallel()

	for _, tc := range platformProviderOverrideAnnotations {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if actual := hostedClusterActionableAnnotationChanged(
				map[string]string{tc.annotation: "old"},
				map[string]string{tc.annotation: "new"},
			); !actual {
				t.Fatalf("expected annotation %s to be actionable", tc.annotation)
			}
		})
	}
}

func TestHostedClusterPrimaryPredicate_WhenHostedClusterUpdatesItShouldFilterMeaningfulChanges(t *testing.T) {
	t.Setenv(k8sutil.EnableHostedClustersAnnotationScopingEnv, "")
	t.Setenv(k8sutil.HostedClustersScopeAnnotationEnv, "")

	pred := hostedClusterPrimaryPredicate(fake.NewClientBuilder().WithScheme(api.Scheme).Build())

	testCases := []struct {
		name     string
		oldHC    *hyperv1.HostedCluster
		newHC    *hyperv1.HostedCluster
		expected bool
	}{
		{
			name:     "When the generation changes, it should allow the update",
			oldHC:    newHostedClusterForPredicateTests(1, nil),
			newHC:    newHostedClusterForPredicateTests(2, nil),
			expected: true,
		},
		{
			name:  "When only status changes, it should skip the update",
			oldHC: newHostedClusterForPredicateTests(1, nil),
			newHC: func() *hyperv1.HostedCluster {
				hc := newHostedClusterForPredicateTests(1, nil)
				hc.Status.Conditions = []metav1.Condition{{
					Type:   string(hyperv1.ReconciliationSucceeded),
					Status: metav1.ConditionTrue,
				}}
				return hc
			}(),
			expected: false,
		},
		{
			name: "When a mirrored annotation changes, it should allow the update",
			oldHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.CleanupCloudResourcesAnnotation: "false",
			}),
			newHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.CleanupCloudResourcesAnnotation: "true",
			}),
			expected: true,
		},
		{
			name: "When a prefixed annotation changes, it should allow the update",
			oldHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.ResourceRequestOverrideAnnotationPrefix + "-kas": "old",
			}),
			newHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.ResourceRequestOverrideAnnotationPrefix + "-kas": "new",
			}),
			expected: true,
		},
		{
			name: "When a direct reconciliation annotation changes, it should allow the update",
			oldHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.ForceUpgradeToAnnotation: "quay.io/openshift-release-dev/ocp-release:4.19.0-x86_64",
			}),
			newHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.ForceUpgradeToAnnotation: "quay.io/openshift-release-dev/ocp-release:4.19.1-x86_64",
			}),
			expected: true,
		},
		{
			name: "When a platform provider override annotation changes, it should allow the update",
			oldHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.ClusterAPIProviderAWSImage: "quay.io/example/aws:v1",
			}),
			newHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.ClusterAPIProviderAWSImage: "quay.io/example/aws:v2",
			}),
			expected: true,
		},
		{
			name: "When the KAS SAN validation skip annotation changes, it should allow the update",
			oldHC: newHostedClusterForPredicateTests(1, map[string]string{
				hyperv1.SkipKASConflicSANValidation: "true",
			}),
			newHC:    newHostedClusterForPredicateTests(1, map[string]string{}),
			expected: true,
		},
		{
			name: "When the scope annotation changes, it should allow the update",
			oldHC: newHostedClusterForPredicateTests(1, map[string]string{
				k8sutil.HostedClustersScopeAnnotation: "one",
			}),
			newHC: newHostedClusterForPredicateTests(1, map[string]string{
				k8sutil.HostedClustersScopeAnnotation: "two",
			}),
			expected: true,
		},
		{
			name:  "When the deletion timestamp changes, it should allow the update",
			oldHC: newHostedClusterForPredicateTests(1, nil),
			newHC: func() *hyperv1.HostedCluster {
				hc := newHostedClusterForPredicateTests(1, nil)
				hc.DeletionTimestamp = ptr.To(metav1.Now())
				return hc
			}(),
			expected: true,
		},
		{
			name: "When an actionable label changes, it should allow the update",
			oldHC: func() *hyperv1.HostedCluster {
				hc := newHostedClusterForPredicateTests(1, nil)
				hc.Labels = map[string]string{"api.openshift.com/id": "old"}
				return hc
			}(),
			newHC: func() *hyperv1.HostedCluster {
				hc := newHostedClusterForPredicateTests(1, nil)
				hc.Labels = map[string]string{"api.openshift.com/id": "new"}
				return hc
			}(),
			expected: true,
		},
		{
			name: "When an actionable label is removed, it should allow the update",
			oldHC: func() *hyperv1.HostedCluster {
				hc := newHostedClusterForPredicateTests(1, nil)
				hc.Labels = map[string]string{"api.openshift.com/id": "old"}
				return hc
			}(),
			newHC: func() *hyperv1.HostedCluster {
				hc := newHostedClusterForPredicateTests(1, nil)
				hc.Labels = map[string]string{}
				return hc
			}(),
			expected: true,
		},
		{
			name: "When a non action annotation changes, it should skip the update",
			oldHC: newHostedClusterForPredicateTests(1, map[string]string{
				"example.com/ignored": "old",
			}),
			newHC: newHostedClusterForPredicateTests(1, map[string]string{
				"example.com/ignored": "new",
			}),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			actual := pred.Update(event.UpdateEvent{
				ObjectOld: tc.oldHC,
				ObjectNew: tc.newHC,
			})
			if actual != tc.expected {
				t.Fatalf("expected predicate result %t, got %t", tc.expected, actual)
			}
		})
	}
}

func TestHostedClusterPrimaryPredicate_WhenAHostedClusterBecomesInScopeItShouldAllowTheUpdate(t *testing.T) {
	t.Setenv(k8sutil.EnableHostedClustersAnnotationScopingEnv, "true")
	t.Setenv(k8sutil.HostedClustersScopeAnnotationEnv, "team-a")

	pred := hostedClusterPrimaryPredicate(fake.NewClientBuilder().WithScheme(api.Scheme).Build())

	oldHC := newHostedClusterForPredicateTests(1, map[string]string{
		k8sutil.HostedClustersScopeAnnotation: "team-b",
	})
	newHC := newHostedClusterForPredicateTests(1, map[string]string{
		k8sutil.HostedClustersScopeAnnotation: "team-a",
	})

	if !pred.Update(event.UpdateEvent{
		ObjectOld: oldHC,
		ObjectNew: newHC,
	}) {
		t.Fatal("expected scope transition into the configured scope to enqueue a reconcile")
	}
}

func newHostedClusterForPredicateTests(generation int64, annotations map[string]string) *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "example",
			Namespace:   "clusters",
			Generation:  generation,
			Annotations: annotations,
		},
	}
}
