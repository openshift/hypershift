package awsmachinetopology

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/autoscaling"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeEC2Client struct {
	describeSubnets func(context.Context, *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error)
}

type failingClient struct {
	client.Client
	getErr   error
	patchErr error
	listErr  error
}

func (c *failingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.getErr != nil {
		return c.getErr
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *failingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchErr != nil {
		return c.patchErr
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *failingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.listErr != nil {
		return c.listErr
	}
	return c.Client.List(ctx, list, opts...)
}

func (f *fakeEC2Client) DescribeSubnets(ctx context.Context, input *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return f.describeSubnets(ctx, input)
}

func TestReconcile(t *testing.T) {
	t.Run("When the worker subnet exists, it should publish its account-specific topology without changing the template spec", func(t *testing.T) {
		g := NewWithT(t)
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "zone-check", Namespace: "clusters-zone-check"},
			Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "eu-central-1"},
			}},
		}
		template := &capiaws.AWSMachineTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name: "workers-template", Namespace: hcp.Namespace,
				Annotations: map[string]string{hyperv1.NodePoolLabel: "clusters/workers", "custom.io/keep": "preserved"},
			},
			Spec: capiaws.AWSMachineTemplateSpec{Template: capiaws.AWSMachineTemplateResource{Spec: capiaws.AWSMachineSpec{
				InstanceType: "c5.24xlarge", Subnet: &capiaws.AWSResourceReference{ID: aws.String("subnet-0a1b2c3d4e5f67890")},
			}}},
		}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, template).Build()
		r := &Reconciler{
			Client: c, HostedControlPlane: client.ObjectKeyFromObject(hcp),
			EC2Client: &fakeEC2Client{describeSubnets: func(_ context.Context, input *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error) {
				g.Expect(input).To(Equal(&ec2.DescribeSubnetsInput{SubnetIds: []string{"subnet-0a1b2c3d4e5f67890"}}))
				return &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{
					SubnetId: aws.String("subnet-0a1b2c3d4e5f67890"), AvailabilityZone: aws.String("eu-central-1b"), AvailabilityZoneId: aws.String("euc1-az3"),
				}}}, nil
			}},
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(template)})
		g.Expect(err).ToNot(HaveOccurred())
		actual := &capiaws.AWSMachineTemplate{}
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(template), actual)).To(Succeed())
		g.Expect(actual.Annotations).To(HaveKeyWithValue(autoscaling.AWSSubnetTopologyAnnotation, `{"region":"eu-central-1","zone":"eu-central-1b","zoneID":"euc1-az3"}`))
		g.Expect(actual.Annotations).To(HaveKeyWithValue("custom.io/keep", "preserved"))
		g.Expect(actual.Spec).To(Equal(template.Spec))
		g.Expect(actual.Status).To(Equal(template.Status))
		g.Expect(actual.Name).To(Equal(template.Name))
		resourceVersion := actual.ResourceVersion
		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(template)})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(template), actual)).To(Succeed())
		g.Expect(actual.ResourceVersion).To(Equal(resourceVersion), "unchanged topology must not cause another write")
	})

	tests := []struct {
		name            string
		missingHCP      bool
		missingTemplate bool
		otherNamespace  bool
		unmanaged       bool
		templatePaused  bool
		hcpPaused       bool
		deleting        bool
		disabled        bool
		lookupErr       error
		getErr          error
		patchErr        error
		expectedError   string
	}{
		{name: "When the template does not exist, it should ignore the deletion", missingTemplate: true},
		{name: "When the HCP does not exist, it should leave the template untouched", missingHCP: true},
		{name: "When the request belongs to another namespace, it should not use this cluster's credentials", otherNamespace: true},
		{name: "When the template is not NodePool-managed, it should leave it untouched", unmanaged: true},
		{name: "When the template is paused, it should leave it untouched", templatePaused: true},
		{name: "When the HCP is paused, it should leave the template untouched", hcpPaused: true},
		{name: "When the HCP is deleting, it should leave the template untouched", deleting: true},
		{name: "When the HCP platform is not AWS, it should not query AWS", disabled: true},
		{name: "When discovery fails, it should remove stale topology and return a retryable error", lookupErr: fmt.Errorf("access denied"), expectedError: "access denied"},
		{name: "When fetching the template fails, it should return a retryable error", getErr: fmt.Errorf("read unavailable"), expectedError: "read unavailable"},
		{name: "When patching topology fails, it should return a retryable error", patchErr: fmt.Errorf("write conflict"), expectedError: "write conflict"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "zone-check", Namespace: "clusters-zone-check"},
				Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "eu-central-1"},
				}},
			}
			if tc.hcpPaused {
				hcp.Spec.PausedUntil = ptr.To("true")
			}
			if tc.deleting {
				hcp.DeletionTimestamp = ptr.To(metav1.NewTime(time.Now()))
				hcp.Finalizers = []string{"test.io/finalizer"}
			}
			if tc.disabled {
				hcp.Spec.Platform.Type = hyperv1.AzurePlatform
			}
			template := &capiaws.AWSMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "workers-template", Namespace: hcp.Namespace,
					Annotations: map[string]string{
						hyperv1.NodePoolLabel: "clusters/workers", "custom.io/keep": "preserved",
						autoscaling.AWSSubnetTopologyAnnotation: `{"region":"eu-central-1","zone":"eu-central-1a","zoneID":"euc1-az1"}`,
					},
				},
				Spec: capiaws.AWSMachineTemplateSpec{Template: capiaws.AWSMachineTemplateResource{Spec: capiaws.AWSMachineSpec{
					InstanceType: "c5.24xlarge", Subnet: &capiaws.AWSResourceReference{ID: aws.String("subnet-0a1b2c3d4e5f67890")},
				}}},
			}
			if tc.unmanaged {
				delete(template.Annotations, hyperv1.NodePoolLabel)
			}
			if tc.templatePaused {
				template.Annotations[capiv1.PausedAnnotation] = ""
			}
			objects := []client.Object{}
			if !tc.missingHCP {
				objects = append(objects, hcp)
			}
			if !tc.missingTemplate {
				objects = append(objects, template)
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
			r := &Reconciler{
				Client: &failingClient{Client: c, getErr: tc.getErr, patchErr: tc.patchErr}, HostedControlPlane: client.ObjectKeyFromObject(hcp),
				EC2Client: &fakeEC2Client{describeSubnets: func(_ context.Context, _ *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error) {
					g.Expect(tc.lookupErr != nil || tc.patchErr != nil).To(BeTrue(), "disabled, paused, or unrelated resources must not query AWS")
					return &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{
						SubnetId: aws.String("subnet-0a1b2c3d4e5f67890"), AvailabilityZone: aws.String("eu-central-1b"), AvailabilityZoneId: aws.String("euc1-az3"),
					}}}, tc.lookupErr
				}},
			}
			key := client.ObjectKeyFromObject(template)
			if tc.otherNamespace {
				key.Namespace = "another-control-plane"
			}
			_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			if !tc.missingTemplate {
				actual := &capiaws.AWSMachineTemplate{}
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(template), actual)).To(Succeed())
				g.Expect(actual.Spec).To(Equal(template.Spec))
				g.Expect(actual.Annotations).To(HaveKeyWithValue("custom.io/keep", "preserved"))
				if tc.lookupErr != nil {
					g.Expect(actual.Annotations).ToNot(HaveKey(autoscaling.AWSSubnetTopologyAnnotation))
				} else {
					g.Expect(actual.Annotations).To(Equal(template.Annotations))
				}
			}
		})
	}
}

func TestResolveSubnetTopology(t *testing.T) {
	t.Parallel()
	subnetID := "subnet-0a1b2c3d4e5f67890"
	subnet := ec2types.Subnet{SubnetId: aws.String(subnetID), AvailabilityZone: aws.String("eu-central-1b"), AvailabilityZoneId: aws.String("euc1-az3")}
	byID := &capiaws.AWSResourceReference{ID: aws.String(subnetID)}
	byFilters := &capiaws.AWSResourceReference{Filters: []capiaws.Filter{{Name: "tag:Name", Values: []string{"workers-eu-central-1b"}}}}
	tests := []struct {
		name          string
		reference     *capiaws.AWSResourceReference
		region        string
		disabled      bool
		outputs       []*ec2.DescribeSubnetsOutput
		describeErr   error
		expectedError string
	}{
		{name: "When a subnet ID matches, it should use the returned zone name and zone ID", reference: byID, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{Subnets: []ec2types.Subnet{subnet}}}},
		{name: "When filters match one subnet, it should discover its topology", reference: byFilters, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{Subnets: []ec2types.Subnet{subnet}}}},
		{name: "When the only match is on a later page, it should discover its topology", reference: byFilters, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{NextToken: aws.String("next-page")}, {Subnets: []ec2types.Subnet{subnet}}}},
		{name: "When matches span pages, it should reject ambiguous topology", reference: byFilters, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{Subnets: []ec2types.Subnet{subnet}, NextToken: aws.String("next-page")}, {Subnets: []ec2types.Subnet{subnet}}}, expectedError: "multiple subnets"},
		{name: "When filters match multiple subnets, it should not guess a zone", reference: byFilters, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{Subnets: []ec2types.Subnet{subnet, subnet}}}, expectedError: "multiple subnets"},
		{name: "When no subnet matches, it should return a retryable error", reference: byID, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{}}, expectedError: "did not match"},
		{name: "When DescribeSubnets fails, it should return the AWS error", reference: byID, region: "eu-central-1", describeErr: fmt.Errorf("access denied"), expectedError: "access denied"},
		{name: "When the subnet lacks a zone name, it should not publish partial topology", reference: byID, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{Subnets: []ec2types.Subnet{{SubnetId: aws.String(subnetID), AvailabilityZoneId: aws.String("euc1-az3")}}}}, expectedError: "missing its availability zone"},
		{name: "When the subnet lacks a zone ID, it should not publish partial topology", reference: byID, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{Subnets: []ec2types.Subnet{{SubnetId: aws.String(subnetID), AvailabilityZone: aws.String("eu-central-1b")}}}}, expectedError: "zone ID"},
		{name: "When AWS returns a different subnet, it should reject the topology", reference: byID, region: "eu-central-1", outputs: []*ec2.DescribeSubnetsOutput{{Subnets: []ec2types.Subnet{{SubnetId: aws.String("subnet-0b1b2c3d4e5f67890")}}}}, expectedError: "different subnet"},
		{name: "When the EC2 client is unavailable, it should return an error without querying AWS", reference: byID, region: "eu-central-1", disabled: true, expectedError: "not configured"},
		{name: "When the region is empty, it should not query AWS", reference: byID, expectedError: "region and worker subnet"},
		{name: "When the subnet reference is missing, it should not query AWS", region: "eu-central-1", expectedError: "region and worker subnet"},
		{name: "When the subnet reference is empty, it should not query AWS", reference: &capiaws.AWSResourceReference{}, region: "eu-central-1", expectedError: "ID or filters"},
		{name: "When both subnet ID and filters are set, it should not query AWS", reference: &capiaws.AWSResourceReference{ID: byID.ID, Filters: byFilters.Filters}, region: "eu-central-1", expectedError: "not both"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			calls := 0
			r := &Reconciler{EC2Client: &fakeEC2Client{describeSubnets: func(_ context.Context, input *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error) {
				if len(tc.reference.Filters) > 0 {
					g.Expect(input.SubnetIds).To(BeEmpty())
					g.Expect(input.Filters).To(Equal([]ec2types.Filter{{Name: aws.String("tag:Name"), Values: []string{"workers-eu-central-1b"}}}))
				} else {
					g.Expect(input.SubnetIds).To(Equal([]string{subnetID}))
					g.Expect(input.Filters).To(BeEmpty())
				}
				if tc.describeErr != nil {
					return nil, tc.describeErr
				}
				g.Expect(calls).To(BeNumerically("<", len(tc.outputs)), "only expected AWS pages should be requested")
				output := tc.outputs[calls]
				calls++
				return output, nil
			}}}
			if tc.disabled {
				r.EC2Client = nil
			}
			actual, err := r.resolveSubnetTopology(t.Context(), tc.region, tc.reference)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				g.Expect(actual).To(BeNil())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(actual).To(Equal(&autoscaling.AWSSubnetTopology{Region: "eu-central-1", Zone: "eu-central-1b", ZoneID: "euc1-az3"}))
			}
		})
	}
}

func TestEnqueueTemplates(t *testing.T) {
	tests := []struct {
		name      string
		otherHCP  bool
		listErr   error
		wantCount int
	}{
		{name: "When the owning HCP changes, it should enqueue only its managed templates", wantCount: 1},
		{name: "When another HCP changes, it should not enqueue templates", otherHCP: true},
		{name: "When listing templates fails, it should not enqueue unrelated resources", listErr: fmt.Errorf("list unavailable")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: "zone-check", Namespace: "clusters-zone-check"}}
			templates := []*capiaws.AWSMachineTemplate{
				{ObjectMeta: metav1.ObjectMeta{Name: "managed", Namespace: hcp.Namespace, Annotations: map[string]string{hyperv1.NodePoolLabel: "clusters/workers"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "unmanaged", Namespace: hcp.Namespace}},
				{ObjectMeta: metav1.ObjectMeta{Name: "another-cluster", Namespace: "another-control-plane", Annotations: map[string]string{hyperv1.NodePoolLabel: "clusters/other-workers"}}},
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(templates[0], templates[1], templates[2]).Build()
			r := &Reconciler{Client: &failingClient{Client: c, listErr: tc.listErr}, HostedControlPlane: client.ObjectKeyFromObject(hcp)}
			if tc.otherHCP {
				hcp.Name = "another-cluster"
			}
			requests := r.enqueueTemplates(t.Context(), hcp)
			g.Expect(requests).To(HaveLen(tc.wantCount))
			if tc.wantCount > 0 {
				g.Expect(requests[0].NamespacedName).To(Equal(client.ObjectKeyFromObject(templates[0])))
			}
		})
	}
}
