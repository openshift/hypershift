package main

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func TestInferTestType(t *testing.T) {
	tests := []struct {
		name       string
		infraID    string
		wantType   string
		wantSuffix string
	}{
		{
			name:       "When infraID is a create-cluster job, it should return create-cluster",
			infraID:    "create-cluster-hg78d",
			wantType:   "create-cluster",
			wantSuffix: "hg78d",
		},
		{
			name:       "When infraID is a node-pool job, it should return node-pool",
			infraID:    "node-pool-78vcg",
			wantType:   "node-pool",
			wantSuffix: "78vcg",
		},
		{
			name:       "When infraID is a control-plane-upgrade job, it should return control-plane-upgrade",
			infraID:    "control-plane-upgrade-6tnx2",
			wantType:   "control-plane-upgrade",
			wantSuffix: "6tnx2",
		},
		{
			name:       "When infraID is a karpenter-upgrade-control-plane job, it should return karpenter-upgrade-control-plane",
			infraID:    "karpenter-upgrade-control-plane-s6tnb",
			wantType:   "karpenter-upgrade-control-plane",
			wantSuffix: "s6tnb",
		},
		{
			name:       "When infraID is a hex string, it should return e2e-generic",
			infraID:    "00ab3695c5f73d4354b9",
			wantType:   "e2e-generic",
			wantSuffix: "",
		},
		{
			name:       "When infraID is a scale-from-zero job, it should return scale-from-zero",
			infraID:    "scale-from-zero-tw2b6",
			wantType:   "scale-from-zero",
			wantSuffix: "tw2b6",
		},
		{
			name:       "When infraID is a developer cluster name, it should return empty",
			infraID:    "brcox-mgmt-4tw4x",
			wantType:   "",
			wantSuffix: "",
		},
		{
			name:       "When infraID is a proxy test, it should return proxy",
			infraID:    "proxy-dp6cv",
			wantType:   "proxy",
			wantSuffix: "dp6cv",
		},
		{
			name:       "When infraID is an autoscaling test, it should return autoscaling",
			infraID:    "autoscaling-p9dsn",
			wantType:   "autoscaling",
			wantSuffix: "p9dsn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotSuffix := InferTestType(tt.infraID)
			if gotType != tt.wantType {
				t.Errorf("InferTestType(%q) type = %q, want %q", tt.infraID, gotType, tt.wantType)
			}
			if gotSuffix != tt.wantSuffix {
				t.Errorf("InferTestType(%q) suffix = %q, want %q", tt.infraID, gotSuffix, tt.wantSuffix)
			}
		})
	}
}

func TestExtractNamespace(t *testing.T) {
	tests := []struct {
		name     string
		txtValue string
		want     string
	}{
		{
			name:     "When TXT has resource=route with namespace, it should extract the namespace",
			txtValue: `"heritage=external-dns,external-dns/owner=4f2bbcda-debd-4d01-84f2-d68db8b6ea97,external-dns/resource=route/e2e-clusters-7c84g-node-pool-78vcg/kube-apiserver"`,
			want:     "e2e-clusters-7c84g-node-pool-78vcg",
		},
		{
			name:     "When TXT has oauth resource, it should extract the namespace",
			txtValue: `"heritage=external-dns,external-dns/owner=xxx,external-dns/resource=route/e2e-clusters-27ddr-node-pool-58vw7/oauth"`,
			want:     "e2e-clusters-27ddr-node-pool-58vw7",
		},
		{
			name:     "When TXT has no resource field, it should return empty",
			txtValue: `"heritage=external-dns,external-dns/owner=xxx"`,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractNamespace(tt.txtValue)
			if got != tt.want {
				t.Errorf("extractNamespace() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProwJobURL(t *testing.T) {
	tests := []struct {
		name       string
		testType   string
		prowJobID  string
		wantDirect bool
		wantSearch bool
		wantEmpty  bool
	}{
		{
			name:       "When prow-job-id tag is present, it should return a direct job URL",
			testType:   "node-pool",
			prowJobID:  "2074787416719233024",
			wantDirect: true,
		},
		{
			name:       "When no prow-job-id but test type is known, it should return a search URL",
			testType:   "node-pool",
			prowJobID:  "",
			wantSearch: true,
		},
		{
			name:      "When test type is e2e-generic and no prow-job-id, it should return empty",
			testType:  "e2e-generic",
			prowJobID: "",
			wantEmpty: true,
		},
		{
			name:      "When test type is empty and no prow-job-id, it should return empty",
			testType:  "",
			prowJobID: "",
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := ProwJobURL(tt.testType, tt.prowJobID)
			if tt.wantDirect {
				if !strings.Contains(url, "/view/gs/") {
					t.Errorf("expected direct job URL, got %q", url)
				}
				if !strings.Contains(url, tt.prowJobID) {
					t.Errorf("expected URL to contain job ID %q, got %q", tt.prowJobID, url)
				}
			}
			if tt.wantSearch {
				if !strings.Contains(url, "query=") {
					t.Errorf("expected search URL, got %q", url)
				}
			}
			if tt.wantEmpty && url != "" {
				t.Errorf("expected empty, got %q", url)
			}
		})
	}
}

// paginatingRoute53 is a mock that splits records across two pages to test pagination.
type paginatingRoute53 struct {
	zones     []route53types.HostedZone
	page1     []route53types.ResourceRecordSet
	page2     []route53types.ResourceRecordSet
	nextName  *string
	nextType  route53types.RRType
	callCount int
}

func (m *paginatingRoute53) ListHostedZones(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	return &route53.ListHostedZonesOutput{HostedZones: m.zones}, nil
}
func (m *paginatingRoute53) GetHostedZone(_ context.Context, _ *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	return &route53.GetHostedZoneOutput{}, nil
}
func (m *paginatingRoute53) ListResourceRecordSets(_ context.Context, input *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	m.callCount++
	if input.StartRecordName == nil {
		return &route53.ListResourceRecordSetsOutput{
			ResourceRecordSets: m.page1,
			IsTruncated:        true,
			NextRecordName:     m.nextName,
			NextRecordType:     m.nextType,
		}, nil
	}
	return &route53.ListResourceRecordSetsOutput{
		ResourceRecordSets: m.page2,
		IsTruncated:        false,
	}, nil
}
func (m *paginatingRoute53) ChangeResourceRecordSets(_ context.Context, _ *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	return &route53.ChangeResourceRecordSetsOutput{}, nil
}
func (m *paginatingRoute53) DeleteHostedZone(_ context.Context, _ *route53.DeleteHostedZoneInput, _ ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error) {
	return &route53.DeleteHostedZoneOutput{}, nil
}

func TestLookupNamespaceFromServiceZone(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		page1       []route53types.ResourceRecordSet
		page2       []route53types.ResourceRecordSet
		wantNS      string
		wantPages   int
	}{
		{
			name:        "When matching TXT is on page 1, it should return the namespace without fetching page 2",
			clusterName: "node-pool-78vcg",
			page1: []route53types.ResourceRecordSet{
				{Type: "A", Name: aws.String("api.node-pool-78vcg.ci.hypershift.devcluster.openshift.com.")},
				{Type: "TXT", ResourceRecords: []route53types.ResourceRecord{
					{Value: aws.String(`"heritage=external-dns,external-dns/owner=xxx,external-dns/resource=route/e2e-clusters-7c84g-node-pool-78vcg/kube-apiserver"`)},
				}},
			},
			wantNS:    "e2e-clusters-7c84g-node-pool-78vcg",
			wantPages: 1,
		},
		{
			name:        "When matching TXT is on page 2, it should paginate and find it",
			clusterName: "node-pool-99abc",
			page1: []route53types.ResourceRecordSet{
				{Type: "A", Name: aws.String("unrelated.ci.hypershift.devcluster.openshift.com.")},
				{Type: "TXT", ResourceRecords: []route53types.ResourceRecord{
					{Value: aws.String(`"heritage=external-dns,external-dns/owner=xxx,external-dns/resource=route/e2e-clusters-other/kube-apiserver"`)},
				}},
			},
			page2: []route53types.ResourceRecordSet{
				{Type: "TXT", ResourceRecords: []route53types.ResourceRecord{
					{Value: aws.String(`"heritage=external-dns,external-dns/owner=yyy,external-dns/resource=route/e2e-clusters-abc12-node-pool-99abc/kube-apiserver"`)},
				}},
			},
			wantNS:    "e2e-clusters-abc12-node-pool-99abc",
			wantPages: 2,
		},
		{
			name:        "When no matching TXT exists on any page, it should return empty",
			clusterName: "nonexistent-cluster",
			page1: []route53types.ResourceRecordSet{
				{Type: "TXT", ResourceRecords: []route53types.ResourceRecord{
					{Value: aws.String(`"heritage=external-dns,external-dns/owner=xxx,external-dns/resource=route/e2e-clusters-other/kube-apiserver"`)},
				}},
			},
			page2:     []route53types.ResourceRecordSet{},
			wantNS:    "",
			wantPages: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &paginatingRoute53{
				page1:    tt.page1,
				page2:    tt.page2,
				nextName: aws.String("next.record.com."),
				nextType: "TXT",
			}

			ns := LookupNamespaceFromServiceZone(context.Background(), mock, "Z12345", tt.clusterName)
			if ns != tt.wantNS {
				t.Errorf("LookupNamespaceFromServiceZone() = %q, want %q", ns, tt.wantNS)
			}
			if mock.callCount != tt.wantPages {
				t.Errorf("expected %d API calls, got %d", tt.wantPages, mock.callCount)
			}
		})
	}
}
