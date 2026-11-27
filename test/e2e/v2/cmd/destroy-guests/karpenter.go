//go:build e2ev2

package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/pkg/manifests"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/onsi/ginkgo/v2/reporters"
)

type karpenterTeardown struct {
	mgmtClient  client.Client
	hcpKey      client.ObjectKey
	region      string
	vpcID       string
	instanceIDs []string
	claimsGone  <-chan error
}

func destroyAndVerifyKarpenter(ctx context.Context, hypershiftBin string, entry lifecycle.ClusterEntry, platform lifecycle.PlatformConfig) error {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 55*time.Minute)
	defer cancel()

	teardown, snapshotErr := startKarpenterTeardown(ctx, entry)
	// Always attempt destruction, including when provisioning or snapshot collection failed.
	destroyErr := destroyCluster(ctx, hypershiftBin, entry, platform)
	var verifyErr error
	if snapshotErr == nil && destroyErr == nil {
		verifyErr = teardown.verify(ctx, entry.InfraID)
	}

	err := errors.Join(snapshotErr, destroyErr, verifyErr)
	return errors.Join(err, writeKarpenterTeardownResult(time.Since(start), err))
}

func startKarpenterTeardown(ctx context.Context, entry lifecycle.ClusterEntry) (*karpenterTeardown, error) {
	snapshotCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	mgmtConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("load management cluster config: %w", err)
	}
	mgmtClient, err := client.New(mgmtConfig, client.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("create management cluster client: %w", err)
	}
	tc := &internal.TestContext{
		Context: snapshotCtx, MgmtClient: mgmtClient, ClusterName: entry.Name, ClusterNamespace: entry.Namespace,
	}
	hc, err := tc.GetHostedCluster()
	if err != nil {
		return nil, err
	}
	awsSpec := hc.Spec.Platform.AWS
	if hc.Spec.InfraID != entry.InfraID || awsSpec == nil || awsSpec.CloudProviderConfig == nil ||
		awsSpec.CloudProviderConfig.VPC == "" || awsSpec.SharedVPC != nil {
		return nil, fmt.Errorf("cannot verify Karpenter cleanup for cluster %s without matching dedicated AWS infrastructure", entry.Name)
	}
	hcpKey := client.ObjectKey{Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), Name: hc.Name}
	hcp := &hyperv1.HostedControlPlane{}
	if err := mgmtClient.Get(snapshotCtx, hcpKey, hcp); err != nil {
		return nil, fmt.Errorf("get HostedControlPlane before deletion: %w", err)
	}
	if !slices.Contains(hcp.Finalizers, karpenter.KarpenterFinalizer) {
		return nil, fmt.Errorf("HostedControlPlane %s lacks the Karpenter finalizer before deletion", hcpKey)
	}

	guestConfig, err := tc.GetHostedClusterRESTConfig(hc)
	if err != nil {
		return nil, err
	}
	guestClient, err := client.NewWithWatch(guestConfig, client.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("create hosted cluster client: %w", err)
	}
	claims := &karpenterv1.NodeClaimList{}
	if err := guestClient.List(snapshotCtx, claims); err != nil {
		return nil, fmt.Errorf("list NodeClaims before deletion: %w", err)
	}
	if len(claims.Items) == 0 {
		return nil, errors.New("expected active NodeClaims before deleting the hosted cluster")
	}
	claimNames := sets.New[string]()
	instanceIDs := sets.New[string]()
	for _, claim := range claims.Items {
		instanceID := path.Base(claim.Status.ProviderID)
		if !strings.HasPrefix(claim.Status.ProviderID, "aws:///") || !strings.HasPrefix(instanceID, "i-") {
			return nil, fmt.Errorf("NodeClaim %s does not have an AWS instance provider ID", claim.Name)
		}
		claimNames.Insert(claim.Name)
		instanceIDs.Insert(instanceID)
	}
	// Watch from the initial list so deletion events are recorded before the guest API goes away.
	events, err := watchtools.NewRetryWatcherWithContext(ctx, claims.ResourceVersion, &cache.ListWatch{
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return guestClient.Watch(ctx, &karpenterv1.NodeClaimList{}, &client.ListOptions{Raw: &options})
		},
	})
	if err != nil {
		return nil, fmt.Errorf("watch NodeClaims before deletion: %w", err)
	}
	claimsGone := make(chan error, 1)
	go func() {
		defer events.Stop()
		claimsGone <- waitForNodeClaimDeletion(ctx, events.ResultChan(), claimNames)
	}()

	return &karpenterTeardown{
		mgmtClient: mgmtClient, hcpKey: hcpKey, region: awsSpec.Region, vpcID: awsSpec.CloudProviderConfig.VPC,
		instanceIDs: sets.List(instanceIDs), claimsGone: claimsGone,
	}, nil
}

func waitForNodeClaimDeletion(ctx context.Context, events <-chan watch.Event, remaining sets.Set[string]) error {
	for remaining.Len() > 0 {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for NodeClaims %v to be deleted: %w", sets.List(remaining), ctx.Err())
		case event, open := <-events:
			if !open {
				return fmt.Errorf("NodeClaim watch closed before observing deletion of %v", sets.List(remaining))
			}
			switch event.Type {
			case watch.Deleted:
				claim, ok := event.Object.(*karpenterv1.NodeClaim)
				if !ok {
					return fmt.Errorf("unexpected NodeClaim deletion event object %T", event.Object)
				}
				remaining.Delete(claim.Name)
			case watch.Error:
				return fmt.Errorf("watching NodeClaims: %w", apierrors.FromObject(event.Object))
			}
		}
	}
	return nil
}

func (t *karpenterTeardown) verify(ctx context.Context, infraID string) error {
	var claimsErr error
	select {
	case claimsErr = <-t.claimsGone:
	case <-ctx.Done():
		claimsErr = fmt.Errorf("waiting for evidence of NodeClaim deletion: %w", ctx.Err())
	}

	// The preflight required the finalizer on this HCP, and destroy does not use --force.
	// Successful removal of the HCP shows that the controller released the finalizer.
	var hcpErr error
	if err := t.mgmtClient.Get(ctx, t.hcpKey, &hyperv1.HostedControlPlane{}); err == nil {
		hcpErr = fmt.Errorf("HostedControlPlane %s still exists after deletion", t.hcpKey)
	} else if !apierrors.IsNotFound(err) {
		hcpErr = fmt.Errorf("verify HostedControlPlane deletion: %w", err)
	}

	return errors.Join(claimsErr, hcpErr, t.verifyAWSResourcesDeleted(ctx, infraID))
}

func (t *karpenterTeardown) verifyAWSResourcesDeleted(ctx context.Context, infraID string) error {
	cfg := awsutil.NewSession(ctx, "karpenter-e2e-cleanup", os.Getenv("AWS_SHARED_CREDENTIALS_FILE"), "", "", t.region)
	ec2Client := ec2.NewFromConfig(*cfg)
	tagsClient := resourcegroupstaggingapi.NewFromConfig(*cfg)
	clusterTag := "kubernetes.io/cluster/" + infraID
	ownedResourceFilters := []ec2types.Filter{{
		Name:   aws.String("tag:" + clusterTag),
		Values: []string{"owned"},
	}}
	var remaining []string
	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		instances, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []ec2types.Filter{
			{Name: aws.String("instance-id"), Values: t.instanceIDs},
			{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "shutting-down", "stopping", "stopped"}},
		}})
		if err != nil {
			return false, fmt.Errorf("describe Karpenter EC2 instances: %w", err)
		}
		remaining = nil
		for _, reservation := range instances.Reservations {
			for _, instance := range reservation.Instances {
				remaining = append(remaining, aws.ToString(instance.InstanceId))
			}
		}

		vpcs, err := ec2Client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{t.vpcID}},
		}})
		if err != nil {
			return false, fmt.Errorf("describe hosted cluster VPC: %w", err)
		}
		for _, vpc := range vpcs.Vpcs {
			remaining = append(remaining, aws.ToString(vpc.VpcId))
		}

		// Query EC2 for volumes so stale tagging entries cannot keep teardown waiting.
		volumes := ec2.NewDescribeVolumesPaginator(ec2Client, &ec2.DescribeVolumesInput{Filters: ownedResourceFilters})
		for volumes.HasMorePages() {
			page, err := volumes.NextPage(ctx)
			if err != nil {
				return false, fmt.Errorf("list hosted cluster EBS volumes: %w", err)
			}
			for _, volume := range page.Volumes {
				remaining = append(remaining, aws.ToString(volume.VolumeId))
			}
		}

		addresses, err := ec2Client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{Filters: ownedResourceFilters})
		if err != nil {
			return false, fmt.Errorf("list hosted cluster Elastic IP allocations: %w", err)
		}
		for _, address := range addresses.Addresses {
			remaining = append(remaining, aws.ToString(address.AllocationId))
		}

		pages := resourcegroupstaggingapi.NewGetResourcesPaginator(tagsClient, &resourcegroupstaggingapi.GetResourcesInput{
			ResourceTypeFilters: []string{"elasticloadbalancing:loadbalancer", "s3"},
			TagFilters:          []tagtypes.TagFilter{{Key: aws.String(clusterTag), Values: []string{"owned"}}},
		})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				return false, fmt.Errorf("list tagged AWS resources: %w", err)
			}
			for _, resource := range page.ResourceTagMappingList {
				remaining = append(remaining, aws.ToString(resource.ResourceARN))
			}
		}
		return len(remaining) == 0, nil
	})
	if err != nil {
		return fmt.Errorf("verify AWS cleanup, last observed resources %v: %w", remaining, err)
	}
	return nil
}

func writeKarpenterTeardownResult(duration time.Duration, result error) error {
	artifactDir := os.Getenv("ARTIFACT_DIR")
	if artifactDir == "" {
		return errors.New("ARTIFACT_DIR is required to report Karpenter cleanup verification")
	}
	const name = "[sig-hypershift][Jira:Hypershift][Feature:AutoNode] AWS HostedCluster deletion cleans up Karpenter resources"
	spec := reporters.JUnitTestCase{Name: name, Classname: "hypershift karpenter teardown", Status: "passed", Time: duration.Seconds()}
	suite := reporters.JUnitTestSuite{Name: spec.Classname, Tests: 1, Time: spec.Time}
	if result != nil {
		spec.Status = "failed"
		spec.Failure = &reporters.JUnitFailure{Message: result.Error(), Type: "failed"}
		suite.Failures = 1
	}
	suite.TestCases = []reporters.JUnitTestCase{spec}
	report := reporters.JUnitTestSuites{Tests: 1, Failures: suite.Failures, Time: spec.Time, TestSuites: []reporters.JUnitTestSuite{suite}}
	data, err := xml.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Karpenter cleanup JUnit: %w", err)
	}
	reportPath := filepath.Join(artifactDir, "junit_karpenter_teardown.xml")
	if err := os.WriteFile(reportPath, append([]byte(xml.Header), data...), 0600); err != nil {
		return fmt.Errorf("write Karpenter cleanup JUnit: %w", err)
	}
	return nil
}
