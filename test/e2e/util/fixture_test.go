package util

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	hyperapi "github.com/openshift/hypershift/support/api"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	resourcegroupstaggingapitypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestDestroyAzureCluster(t *testing.T) {
	t.Parallel()
	cleanupErr := errors.New("DNS assignment deletion forbidden")
	destroyErr := errors.New("resource group deletion failed")
	tests := []struct {
		name       string
		clusterRG  string
		nsgID      string
		vnetID     string
		wantNSG    string
		wantVnet   string
		cleanupErr error
		destroyErr error
		invalid    bool
	}{
		{name: "When networking is generated, it should target infraID-suffixed network groups", wantNSG: "cluster-nsg-infra", wantVnet: "cluster-vnet-infra"},
		{name: "When a custom resource group is provided, it should use that group for generated networking", clusterRG: "Custom-RG", wantNSG: "Custom-RG", wantVnet: "Custom-RG"},
		{name: "When both networks are shared, it should retain their original mixed-case scopes", nsgID: "/subscriptions/sub/resourceGroups/Shared-NSG/providers/Microsoft.Network/networkSecurityGroups/nsg", vnetID: "/subscriptions/sub/resourceGroups/Shared-VNet/providers/Microsoft.Network/virtualNetworks/vnet", wantNSG: "Shared-NSG", wantVnet: "Shared-VNet"},
		{name: "When only the VNet is shared, it should retain the generated NSG scope", vnetID: "/subscriptions/sub/resourceGroups/Shared-VNet/providers/Microsoft.Network/virtualNetworks/vnet", wantNSG: "cluster-nsg-infra", wantVnet: "Shared-VNet"},
		{name: "When only the NSG is shared in a custom layout, it should retain the custom VNet scope", clusterRG: "Custom-RG", nsgID: "/subscriptions/sub/resourceGroups/Shared-NSG/providers/Microsoft.Network/networkSecurityGroups/nsg", wantNSG: "Shared-NSG", wantVnet: "Custom-RG"},
		{name: "When cleanup fails but destroy succeeds, it should fail CI without skipping destruction", wantNSG: "cluster-nsg-infra", wantVnet: "cluster-vnet-infra", cleanupErr: cleanupErr},
		{name: "When cleanup and destroy both fail, it should retain both errors in CI", wantNSG: "cluster-nsg-infra", wantVnet: "cluster-vnet-infra", cleanupErr: cleanupErr, destroyErr: destroyErr},
		{name: "When only destroy fails, it should retain the original failure", wantNSG: "cluster-nsg-infra", wantVnet: "cluster-vnet-infra", destroyErr: destroyErr},
		{name: "When cleanup is canceled, it should expose cancellation to CI", wantNSG: "cluster-nsg-infra", wantVnet: "cluster-vnet-infra", cleanupErr: context.Canceled},
		{name: "When destroy is canceled after cleanup fails, it should preserve both error identities", wantNSG: "cluster-nsg-infra", wantVnet: "cluster-vnet-infra", cleanupErr: cleanupErr, destroyErr: context.Canceled},
		{name: "When a VNet ID is invalid, it should fail before calling destroy", vnetID: "invalid", invalid: true},
		{name: "When an NSG ID is invalid, it should fail before calling destroy", nsgID: "invalid", invalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			createOpts := &PlatformAgnosticOptions{RawCreateOptions: core.RawCreateOptions{Name: "cluster", InfraID: "infra"}, AzurePlatform: azure.RawCreateOptions{
				CredentialsFile: "azure-creds", Location: "eastus", DNSZoneRGName: "Shared-DNS", ResourceGroupName: test.clusterRG, VnetID: test.vnetID, NetworkSecurityGroupID: test.nsgID,
			}}
			opts := &core.DestroyOptions{Name: "cluster", InfraID: "infra"}
			called := false
			err := destroyAzureCluster(t.Context(), opts, createOpts, nil, func(ctx context.Context, actual *core.DestroyOptions, client crclient.Client) error {
				called = true
				platform := actual.AzurePlatform
				g.Expect(ctx).To(Equal(t.Context()))
				g.Expect(actual).To(BeIdenticalTo(opts))
				g.Expect(client).To(BeNil())
				g.Expect(actual.InfraID).To(Equal(createOpts.InfraID))
				g.Expect(platform.CredentialsFile).To(Equal("azure-creds"))
				g.Expect(platform.Location).To(Equal("eastus"))
				g.Expect(platform.DNSZoneRGName).To(Equal("Shared-DNS"))
				g.Expect(platform.ResourceGroupName).To(Equal(test.clusterRG))
				g.Expect(platform.NSGResourceGroupName).To(Equal(test.wantNSG))
				g.Expect(platform.VnetResourceGroupName).To(Equal(test.wantVnet))
				if platform.OnRoleAssignmentCleanupError == nil {
					t.Fatal("fixture did not install CI cleanup-error notification")
				}
				if test.cleanupErr != nil {
					platform.OnRoleAssignmentCleanupError(test.cleanupErr)
				}
				return test.destroyErr
			})
			if test.invalid {
				if err == nil || called {
					t.Fatalf("expected invalid input to prevent destroy: called=%t err=%v", called, err)
				}
				return
			}
			if !called {
				t.Fatal("expected fixture to call destroy")
			}
			for _, expected := range []error{test.cleanupErr, test.destroyErr} {
				if expected != nil && !errors.Is(err, expected) {
					t.Fatalf("expected CI error to retain %v, got %v", expected, err)
				}
			}
			if test.cleanupErr == nil && test.destroyErr == nil && err != nil {
				t.Fatalf("unexpected cleanup error: %v", err)
			}
		})
	}
	for _, test := range []struct {
		name          string
		force         bool
		platformErr   error
		wantFinalized bool
	}{
		{"When production continues after a cleanup error, it should finalize normally but fail CI", false, nil, true},
		{"When force suppresses a platform failure, it should finalize normally but retain CI cleanup failure", true, destroyErr, true},
		{"When non-force platform cleanup fails, it should retain the finalizer and both CI errors", false, destroyErr, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cluster := &v1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(cluster).Build()
			opts := &core.DestroyOptions{Name: cluster.Name, Namespace: cluster.Namespace, InfraID: "infra", ForceDestroy: test.force, ClusterGracePeriod: time.Second, Log: logr.Discard()}
			createOpts := &PlatformAgnosticOptions{RawCreateOptions: core.RawCreateOptions{Name: cluster.Name, InfraID: "infra"}}
			var coreErr error
			err := destroyAzureCluster(t.Context(), opts, createOpts, client, func(ctx context.Context, actual *core.DestroyOptions, client crclient.Client) error {
				coreErr = core.DestroyCluster(ctx, client, cluster, actual, func(ctx context.Context, platformOpts *core.DestroyOptions, client crclient.Client) error {
					platformOpts.AzurePlatform.OnRoleAssignmentCleanupError(cleanupErr)
					return test.platformErr
				})
				return coreErr
			})
			if !errors.Is(err, cleanupErr) {
				t.Fatalf("CI lost cleanup failure: %v", err)
			}
			if test.wantFinalized {
				if coreErr != nil {
					t.Fatalf("baseline core teardown should succeed, got %v", coreErr)
				}
				if getErr := client.Get(t.Context(), crclient.ObjectKeyFromObject(cluster), &v1beta1.HostedCluster{}); !apierrors.IsNotFound(getErr) {
					t.Fatalf("expected baseline finalization to delete the cluster, got %v", getErr)
				}
			} else {
				if !errors.Is(coreErr, test.platformErr) || !errors.Is(err, test.platformErr) {
					t.Fatalf("lost non-force platform failure: core=%v CI=%v", coreErr, err)
				}
				remaining := &v1beta1.HostedCluster{}
				if getErr := client.Get(t.Context(), crclient.ObjectKeyFromObject(cluster), remaining); getErr != nil || len(remaining.Finalizers) == 0 {
					t.Fatalf("expected baseline finalizer retention, got cluster=%#v err=%v", remaining, getErr)
				}
			}
		})
	}
}

func TestCreateCluster(t *testing.T) {
	opts := &PlatformAgnosticOptions{RawCreateOptions: *core.DefaultOptions()}
	err := createCluster(t.Context(), &v1beta1.HostedCluster{}, opts, t.TempDir())
	if err == nil {
		t.Fatal("expected invalid cluster options to stop creation")
	}
}

func TestRenderCreate(t *testing.T) {
	opts := core.DefaultOptions()
	if err := renderCreate(t.Context(), opts, nil, "/nonexistent/output/manifests.yaml", "/nonexistent/render.log", "/nonexistent/create.log", core.ResolveClientProvider()); err == nil {
		t.Fatal("expected an invalid log path to stop rendering")
	}
}

func TestDestroyCluster(t *testing.T) {
	if err := destroyCluster(t.Context(), t, &v1beta1.HostedCluster{}, &PlatformAgnosticOptions{}, "/nonexistent"); err == nil {
		t.Fatal("expected an invalid log path to stop destruction")
	}
}

type paginatedAWSResourceTaggingClient struct {
	outputs []*resourcegroupstaggingapi.GetResourcesOutput
	inputs  []*resourcegroupstaggingapi.GetResourcesInput
}

func (c *paginatedAWSResourceTaggingClient) GetResources(_ context.Context, input *resourcegroupstaggingapi.GetResourcesInput, _ ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	c.inputs = append(c.inputs, input)
	output := c.outputs[0]
	c.outputs = c.outputs[1:]
	return output, nil
}

func TestGetTaggedAWSResources(t *testing.T) {
	t.Parallel()
	client := &paginatedAWSResourceTaggingClient{
		outputs: []*resourcegroupstaggingapi.GetResourcesOutput{
			{
				ResourceTagMappingList: []resourcegroupstaggingapitypes.ResourceTagMapping{{ResourceARN: awssdk.String("arn:aws:s3:::first")}},
				PaginationToken:        awssdk.String("next-page"),
			},
			{
				ResourceTagMappingList: []resourcegroupstaggingapitypes.ResourceTagMapping{{ResourceARN: awssdk.String("arn:aws:s3:::second")}},
			},
		},
	}

	mappings, err := getTaggedAWSResources(context.Background(), client, "cluster-id")
	g := NewWithT(t)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(mappings).To(HaveLen(2))
	g.Expect(client.inputs).To(HaveLen(2))
	g.Expect(client.inputs[0].PaginationToken).To(BeNil())
	g.Expect(awssdk.ToString(client.inputs[1].PaginationToken)).To(Equal("next-page"))
	for _, input := range client.inputs {
		g.Expect(input.ResourceTypeFilters).To(ConsistOf(
			"elasticloadbalancing:loadbalancer",
			"elasticloadbalancing:targetgroup",
			"ec2:volume",
			"s3",
		))
		g.Expect(input.TagFilters).To(ConsistOf(resourcegroupstaggingapitypes.TagFilter{
			Key:    awssdk.String("kubernetes.io/cluster/cluster-id"),
			Values: []string{"owned"},
		}))
	}
}

func TestHasGuestResources(t *testing.T) {
	t.Parallel()
	nlbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/my-nlb/abc123"
	targetGroupARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/my-target-group/abc123"
	s3ARN := "arn:aws:s3:::my-bucket"
	volumeARN := "arn:aws:ec2:us-east-1:123456789012:volume/vol-abc123"

	tests := []struct {
		name     string
		mappings []resourcegroupstaggingapitypes.ResourceTagMapping
		want     bool
	}{
		{
			name:     "When mappings are empty it should return false",
			mappings: nil,
			want:     false,
		},
		{
			name: "When mappings contain a load balancer it should return true",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(nlbARN)},
			},
			want: true,
		},
		{
			name: "When mappings contain a target group it should return true",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(targetGroupARN)},
			},
			want: true,
		},
		{
			name: "When mappings contain an S3 bucket it should return true",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(s3ARN)},
			},
			want: true,
		},
		{
			name: "When mappings contain only a non-PV EC2 volume it should return false",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(volumeARN)},
			},
			want: false,
		},
		{
			name: "When mappings contain a PV-tagged EC2 volume it should return true",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{
					ResourceARN: awssdk.String(volumeARN),
					Tags: []resourcegroupstaggingapitypes.Tag{
						{Key: awssdk.String("kubernetes.io/created-for/pv/name"), Value: awssdk.String("pvc-1")},
					},
				},
			},
			want: true,
		},
		{
			name: "When mappings contain a malformed ARN it should skip it and continue",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String("not-an-arn")},
				{ResourceARN: awssdk.String(nlbARN)},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(hasGuestResources(t, tc.mappings)).To(Equal(tc.want))
		})
	}
}
