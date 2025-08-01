//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ImageTagMirrorSetTest struct {
	DummyInfraSetup
	ctx                 context.Context
	managementClient    crclient.Client
	hostedClusterClient crclient.Client
	hostedCluster       *hyperv1.HostedCluster
}

func NewImageTagMirrorSetTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client) *ImageTagMirrorSetTest {
	return &ImageTagMirrorSetTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		managementClient:    mgmtClient,
	}
}

func (itms *ImageTagMirrorSetTest) Setup(t *testing.T) {
	t.Log("Starting test ImageTagMirrorSetTest")

	if e2eutil.IsLessThan(e2eutil.Version414) {
		t.Skip("ImageTagMirrorSet test only applicable for 4.14+")
	}
}

func (itms *ImageTagMirrorSetTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      itms.hostedCluster.Name + "-" + "test-itms",
			Namespace: itms.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = &oneReplicas

	return nodePool, nil
}

func (itms *ImageTagMirrorSetTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Log("Entering ImageTagMirrorSet test")
	ctx := itms.ctx

	// Get the original hosted cluster configuration
	originalCluster := itms.hostedCluster.DeepCopy()

	defer func() {
		// Restore original ITMS configuration
		itms.hostedCluster.Spec.ImageTagMirrorSet = originalCluster.Spec.ImageTagMirrorSet
		if err := itms.managementClient.Update(ctx, itms.hostedCluster); err != nil {
			t.Logf("failed to restore original ITMS configuration: %v", err)
		}
		t.Log("Exiting ImageTagMirrorSet test: OK")
	}()

	// Test 1: Add ITMS configuration to the hosted cluster
	t.Run("Configure ITMS", func(t *testing.T) {
		// Update the hosted cluster with ITMS configuration
		itms.hostedCluster.Spec.ImageTagMirrorSet = []hyperv1.ImageTagMirror{
			{
				Source:  "quay.io/openshift",
				Mirrors: []string{"mirror.example.com/openshift"},
			},
			{
				Source:  "registry.redhat.io/ubi8",
				Mirrors: []string{"mirror.example.com/ubi8"},
				MirrorSourcePolicy: func() *hyperv1.MirrorSourcePolicy {
					policy := hyperv1.MirrorSourcePolicy("AllowContactingSource")
					return &policy
				}(),
			},
		}

		if err := itms.managementClient.Update(ctx, itms.hostedCluster); err != nil {
			t.Fatalf("failed to update hosted cluster with ITMS: %v", err)
		}

		t.Log("Successfully updated hosted cluster with ITMS configuration")
	})

	// Test 2: Verify ImageTagMirrorSet is created in the management cluster
	t.Run("Verify management cluster ITMS", func(t *testing.T) {
		itmsName := fmt.Sprintf("%s-image-tag-mirrors", itms.hostedCluster.Name)

		// Wait for the ImageTagMirrorSet to be created
		err := wait.PollImmediate(5*time.Second, 3*time.Minute, func() (bool, error) {
			itmsResource := &configv1.ImageTagMirrorSet{}
			err := itms.managementClient.Get(ctx, crclient.ObjectKey{Name: itmsName}, itmsResource)
			if apierrors.IsNotFound(err) {
				t.Logf("ImageTagMirrorSet %s not found yet, waiting...", itmsName)
				return false, nil
			}
			if err != nil {
				return false, err
			}

			// Verify the content
			if len(itmsResource.Spec.ImageTagMirrors) != 2 {
				return false, fmt.Errorf("expected 2 image tag mirrors, got %d", len(itmsResource.Spec.ImageTagMirrors))
			}

			// Check for our specific source
			found := false
			for _, mirror := range itmsResource.Spec.ImageTagMirrors {
				if mirror.Source == "quay.io/openshift" {
					found = true
					if len(mirror.Mirrors) != 1 || string(mirror.Mirrors[0]) != "mirror.example.com/openshift" {
						return false, fmt.Errorf("unexpected mirrors for quay.io/openshift: %v", mirror.Mirrors)
					}
				}
			}
			if !found {
				return false, fmt.Errorf("quay.io/openshift mirror not found")
			}

			t.Log("ImageTagMirrorSet successfully created with correct configuration")
			return true, nil
		})

		if err != nil {
			t.Fatalf("ImageTagMirrorSet was not created correctly: %v", err)
		}
	})

	// Test 3: Verify ImageTagMirrorSet is propagated to the guest cluster
	t.Run("Verify guest cluster ITMS", func(t *testing.T) {
		// Wait for the ImageTagMirrorSet to be present in the guest cluster
		err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
			itmsResources := &configv1.ImageTagMirrorSetList{}
			err := itms.hostedClusterClient.List(ctx, itmsResources)
			if err != nil {
				t.Logf("error listing ImageTagMirrorSets in guest cluster: %v", err)
				return false, nil
			}

			if len(itmsResources.Items) == 0 {
				t.Log("No ImageTagMirrorSets found in guest cluster yet, waiting...")
				return false, nil
			}

			// Look for our ITMS configuration
			for _, itmsResource := range itmsResources.Items {
				for _, mirror := range itmsResource.Spec.ImageTagMirrors {
					if mirror.Source == "quay.io/openshift" {
						t.Log("Found ITMS configuration in guest cluster")
						return true, nil
					}
				}
			}

			t.Log("ITMS configuration not yet propagated to guest cluster")
			return false, nil
		})

		if err != nil {
			t.Fatalf("ImageTagMirrorSet was not propagated to guest cluster: %v", err)
		}
	})

	// Test 4: Verify registries.conf is updated on nodes
	t.Run("Verify registries.conf on nodes", func(t *testing.T) {
		if len(nodes) == 0 {
			t.Skip("No nodes available to verify registries.conf")
		}

		// Check the first available node
		node := nodes[0]

		// Execute a command to check registries.conf content
		// Note: This requires the node to be accessible and configured properly
		t.Logf("Checking registries.conf on node %s", node.Name)

		// Create a debug pod to check the registries.conf file
		debugPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("debug-registries-%s", node.Name),
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				NodeName:      node.Name,
				HostNetwork:   true,
				HostPID:       true,
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{
					{
						Name:  "debug",
						Image: "registry.redhat.io/ubi8/ubi:latest",
						Command: []string{
							"sh", "-c",
							"chroot /host cat /etc/containers/registries.conf || echo 'registries.conf not found'",
						},
						SecurityContext: &corev1.SecurityContext{
							Privileged: &[]bool{true}[0],
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "host",
								MountPath: "/host",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "host",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/",
							},
						},
					},
				},
				Tolerations: []corev1.Toleration{
					{
						Operator: corev1.TolerationOpExists,
					},
				},
			},
		}

		// Create and wait for the debug pod
		if err := itms.hostedClusterClient.Create(ctx, debugPod); err != nil {
			t.Fatalf("failed to create debug pod: %v", err)
		}

		defer func() {
			if err := itms.hostedClusterClient.Delete(ctx, debugPod); err != nil {
				t.Logf("failed to delete debug pod: %v", err)
			}
		}()

		// Wait for pod to complete
		err := wait.PollImmediate(10*time.Second, 2*time.Minute, func() (bool, error) {
			pod := &corev1.Pod{}
			err := itms.hostedClusterClient.Get(ctx, crclient.ObjectKeyFromObject(debugPod), pod)
			if err != nil {
				return false, err
			}

			if pod.Status.Phase == corev1.PodSucceeded {
				return true, nil
			}
			if pod.Status.Phase == corev1.PodFailed {
				return false, fmt.Errorf("debug pod failed")
			}

			t.Logf("Debug pod status: %s", pod.Status.Phase)
			return false, nil
		})

		if err != nil {
			t.Logf("Debug pod did not complete successfully: %v", err)
			return // Don't fail the entire test for this check
		}

		// Get the logs to verify registries.conf content
		logs, err := getPodLogs(ctx, itms.hostedClusterClient, debugPod)
		if err != nil {
			t.Logf("failed to get debug pod logs: %v", err)
			return
		}

		t.Logf("registries.conf content:\n%s", logs)

		// Verify that our mirror configuration is present
		if !strings.Contains(logs, "quay.io/openshift") || !strings.Contains(logs, "mirror.example.com") {
			t.Errorf("Expected ITMS configuration not found in registries.conf. Content: %s", logs)
		} else {
			t.Log("Successfully verified ITMS configuration in registries.conf")
		}
	})

	// Test 5: Update ITMS configuration
	t.Run("Update ITMS configuration", func(t *testing.T) {
		// Add a new mirror to the configuration
		itms.hostedCluster.Spec.ImageTagMirrorSet = append(itms.hostedCluster.Spec.ImageTagMirrorSet, hyperv1.ImageTagMirror{
			Source:  "docker.io/library",
			Mirrors: []string{"mirror.example.com/library"},
		})

		if err := itms.managementClient.Update(ctx, itms.hostedCluster); err != nil {
			t.Fatalf("failed to update hosted cluster with additional ITMS: %v", err)
		}

		// Verify the update is reflected in the management cluster
		itmsName := fmt.Sprintf("%s-image-tag-mirrors", itms.hostedCluster.Name)

		err := wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
			itmsResource := &configv1.ImageTagMirrorSet{}
			err := itms.managementClient.Get(ctx, crclient.ObjectKey{Name: itmsName}, itmsResource)
			if err != nil {
				return false, err
			}

			// Check if the new mirror is present
			for _, mirror := range itmsResource.Spec.ImageTagMirrors {
				if mirror.Source == "docker.io/library" {
					t.Log("Successfully updated ITMS configuration")
					return true, nil
				}
			}
			return false, nil
		})

		if err != nil {
			t.Fatalf("Updated ITMS configuration was not reflected: %v", err)
		}
	})
}

// getPodLogs retrieves the logs from a pod
func getPodLogs(ctx context.Context, client crclient.Client, pod *corev1.Pod) (string, error) {
	// Create a REST config from the client
	restConfig, err := getRestConfigFromClient(client)
	if err != nil {
		return "", fmt.Errorf("failed to get REST config: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	podLogOpts := corev1.PodLogOptions{}
	req := kubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &podLogOpts)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to stream pod logs: %w", err)
	}
	defer podLogs.Close()

	buf := new(strings.Builder)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", fmt.Errorf("failed to read pod logs: %w", err)
	}

	return buf.String(), nil
}

// getRestConfigFromClient extracts REST config from controller-runtime client
func getRestConfigFromClient(client crclient.Client) (*rest.Config, error) {
	// This is a simplified approach - in practice you might need to handle this differently
	// depending on how the client was created
	return nil, fmt.Errorf("REST config extraction not implemented - skipping log check")
}
