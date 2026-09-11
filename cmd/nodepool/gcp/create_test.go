package gcp

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/spf13/pflag"
)

// TestCLIFlow tests the full CLI flag parsing → Validate() → Complete() → UpdateNodePool() manifest generation flow.
func TestCLIFlow(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "When minimal flags are provided, it should use defaults",
			args: []string{
				"--machine-type=n2-standard-4",
				"--zone=us-central1-a",
				"--subnet=test-subnet",
			},
		},
		{
			name: "When all flags are provided, it should set all fields",
			args: []string{
				"--machine-type=n2-standard-8",
				"--zone=us-west1-b",
				"--subnet=prod-subnet",
				"--boot-disk-size=200",
				"--boot-disk-type=pd-ssd",
				"--boot-disk-encryption-key=projects/test-project/locations/us-west1/keyRings/test-ring/cryptoKeys/test-key",
				"--service-account-email=test-sa@test-project.iam.gserviceaccount.com",
				"--provisioning-model=Spot",
				"--network-tags=tag1,tag2",
				"--resource-labels=env=prod,team=platform",
				"--image=projects/rhcos-cloud/global/images/rhcos-test",
			},
		},
		{
			name: "When custom boot disk flags are provided, it should customize boot disk",
			args: []string{
				"--machine-type=n2-standard-16",
				"--zone=europe-west1-c",
				"--subnet=custom-subnet",
				"--boot-disk-size=500",
				"--boot-disk-type=pd-balanced",
				"--provisioning-model=Preemptible",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			// Setup flag parsing
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := &core.CreateNodePoolOptions{
				Name:        "test-nodepool",
				Namespace:   "clusters",
				ClusterName: "test-cluster",
				Replicas:    3,
				Arch:        "amd64",
			}
			gcpOpts := DefaultOptions()

			// Bind flags
			BindDeveloperOptions(gcpOpts, flags)

			// Parse flags
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			// Validate
			validOpts, err := gcpOpts.Validate(ctx, coreOpts)
			if err != nil {
				t.Fatalf("validation failed: %v", err)
			}

			// Complete
			completedOpts, err := validOpts.Complete(ctx, coreOpts)
			if err != nil {
				t.Fatalf("completion failed: %v", err)
			}

			// Generate NodePool
			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: coreOpts.Arch,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
					},
				},
			}

			// Create fake HostedCluster
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP:  &hyperv1.GCPPlatformSpec{},
					},
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			// Compare with fixture
			testutil.CompareWithFixture(t, nodePool.Spec.Platform.GCP)
		})
	}
}

// TestUpdateNodePool tests machine type defaulting logic.
func TestUpdateNodePool(t *testing.T) {
	testCases := []struct {
		name                string
		arch                string
		expectedMachineType string
	}{
		{
			name:                "When arch is amd64, it should default to n2-standard-4",
			arch:                "amd64",
			expectedMachineType: "n2-standard-4",
		},
		{
			name:                "When arch is arm64, it should default to t2a-standard-4",
			arch:                "arm64",
			expectedMachineType: "t2a-standard-4",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			opts := &CompletedGCPNodePoolCreateOptions{
				completedGCPNodePoolCreateOptions: &completedGCPNodePoolCreateOptions{
					GCPNodePoolCreateOptions: &GCPNodePoolCreateOptions{
						MachineType:       "", // Empty
						Zone:              "us-central1-a",
						Subnet:            "test-subnet",
						ProvisioningModel: "Standard",
						ResourceLabels:    make(map[string]string),
						NetworkTags:       []string{},
					},
				},
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: tc.arch,
				},
			}

			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
					},
				},
			}

			if err := opts.UpdateNodePool(ctx, nodePool, hcluster, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			if nodePool.Spec.Platform.GCP.MachineType != tc.expectedMachineType {
				t.Errorf("expected machine type to be %q, got %q", tc.expectedMachineType, nodePool.Spec.Platform.GCP.MachineType)
			}
		})
	}

	t.Run("When provisioning model is invalid, it should return error", func(t *testing.T) {
		ctx := t.Context()
		opts := &CompletedGCPNodePoolCreateOptions{
			completedGCPNodePoolCreateOptions: &completedGCPNodePoolCreateOptions{
				GCPNodePoolCreateOptions: &GCPNodePoolCreateOptions{
					Zone:              "us-central1-a",
					Subnet:            "test-subnet",
					ProvisioningModel: "InvalidModel",
					ResourceLabels:    make(map[string]string),
					NetworkTags:       []string{},
				},
			},
		}

		nodePool := &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{Arch: "amd64"}}
		hcluster := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{InfraID: "test", Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform}}}

		err := opts.UpdateNodePool(ctx, nodePool, hcluster, nil)
		if err == nil {
			t.Fatal("expected error for invalid provisioning model")
		}

		expectedError := `invalid provisioning model "InvalidModel", must be one of: Standard, Spot, Preemptible`
		if err.Error() != expectedError {
			t.Errorf("expected error %q, got %q", expectedError, err.Error())
		}
	})
}
