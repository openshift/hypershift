//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/test/integration/framework"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestImageTagMirrorSetIntegration tests that ImageTagMirrorSet configurations
// are properly processed and propagated to the control plane.
func TestImageTagMirrorSetIntegration(t *testing.T) {
	framework.RunHyperShiftOperatorTest(testContext, log, globalOpts, framework.HostedClusterOptions{}, t, func(t *testing.T, ctx *framework.ManagementTestContext) {
		mgmtClient := ctx.MgmtCluster.CRClient
		hostedCluster := ctx.HostedCluster
		
		// Update the HostedCluster with ITMS configuration
		hostedCluster.Spec.ImageTagMirrorSet = []hyperv1.ImageTagMirror{
			{
				Source:  "quay.io/openshift",
				Mirrors: []string{"mirror.example.com/openshift"},
			},
			{
				Source:  "registry.redhat.io/ubi8",
				Mirrors: []string{"mirror.example.com/ubi8", "backup.mirror.com/ubi8"},
				MirrorSourcePolicy: func() *hyperv1.MirrorSourcePolicy {
					policy := hyperv1.MirrorSourcePolicy("AllowContactingSource")
					return &policy
				}(),
			},
		}
		
		if err := mgmtClient.Update(testContext, hostedCluster); err != nil {
			t.Fatalf("failed to update hosted cluster with ITMS: %v", err)
		}

		// Test 1: Verify ImageTagMirrorSet is created in the control plane
		t.Run("ImageTagMirrorSet creation", func(t *testing.T) {
			itmsName := fmt.Sprintf("%s-image-tag-mirrors", hostedCluster.Name)
			itms := &configv1.ImageTagMirrorSet{}
			
			err := wait.PollImmediate(5*time.Second, 60*time.Second, func() (bool, error) {
				err := mgmtClient.Get(testContext, crclient.ObjectKey{Name: itmsName}, itms)
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				return true, nil
			})
			
			if err != nil {
				t.Fatalf("ImageTagMirrorSet was not created: %v", err)
			}
			
			// Verify the content matches what we configured
			if len(itms.Spec.ImageTagMirrors) != 2 {
				t.Errorf("expected 2 image tag mirrors, got %d", len(itms.Spec.ImageTagMirrors))
			}
			
			// Check first mirror
			found := false
			for _, mirror := range itms.Spec.ImageTagMirrors {
				if mirror.Source == "quay.io/openshift" {
					found = true
					if len(mirror.Mirrors) != 1 || string(mirror.Mirrors[0]) != "mirror.example.com/openshift" {
						t.Errorf("unexpected mirrors for quay.io/openshift: %v", mirror.Mirrors)
					}
				}
			}
			if !found {
				t.Error("quay.io/openshift mirror not found")
			}
		})

		// Test 2: Verify ignition ConfigMap is created with ITMS content
		t.Run("Ignition ConfigMap creation", func(t *testing.T) {
			controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
			
			// Look for the ignition ConfigMap that should contain our ITMS configuration
			configMapList := &corev1.ConfigMapList{}
			err := wait.PollImmediate(5*time.Second, 60*time.Second, func() (bool, error) {
				err := mgmtClient.List(testContext, configMapList, 
					crclient.InNamespace(controlPlaneNamespace),
					crclient.MatchingLabels{"hypershift.openshift.io/core-ignition-config": "true"})
				if err != nil {
					return false, err
				}
				
				// Look for a ConfigMap that contains ImageTagMirrorSet content
				for _, cm := range configMapList.Items {
					if config, exists := cm.Data["config"]; exists {
						if contains(config, "ImageTagMirrorSet") && contains(config, "quay.io/openshift") {
							return true, nil
						}
					}
				}
				return false, nil
			})
			
			if err != nil {
				t.Fatalf("ignition ConfigMap with ITMS content was not created: %v", err)
			}
		})

		// Test 3: Test updating ITMS configuration
		t.Run("ITMS update", func(t *testing.T) {
			// Add a new mirror
			hostedCluster.Spec.ImageTagMirrorSet = append(hostedCluster.Spec.ImageTagMirrorSet, hyperv1.ImageTagMirror{
				Source:  "docker.io/library",
				Mirrors: []string{"mirror.example.com/library"},
			})
			
			if err := mgmtClient.Update(testContext, hostedCluster); err != nil {
				t.Fatalf("failed to update cluster with new ITMS: %v", err)
			}
			
			// Verify the update is reflected in the ImageTagMirrorSet
			itmsName := fmt.Sprintf("%s-image-tag-mirrors", hostedCluster.Name)
			itms := &configv1.ImageTagMirrorSet{}
			
			err := wait.PollImmediate(5*time.Second, 60*time.Second, func() (bool, error) {
				err := mgmtClient.Get(testContext, crclient.ObjectKey{Name: itmsName}, itms)
				if err != nil {
					return false, err
				}
				
				// Check if the new mirror is present
				for _, mirror := range itms.Spec.ImageTagMirrors {
					if mirror.Source == "docker.io/library" {
						return true, nil
					}
				}
				return false, nil
			})
			
			if err != nil {
				t.Fatalf("updated ITMS was not reflected in ImageTagMirrorSet: %v", err)
			}
		})
	})
}

// contains checks if a string contains a substring (helper function)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(s) > len(substr) && s[:len(substr)] == substr) ||
		(len(s) > len(substr) && s[len(s)-len(substr):] == substr) ||
		indexOfSubstring(s, substr) >= 0)
}

func indexOfSubstring(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}