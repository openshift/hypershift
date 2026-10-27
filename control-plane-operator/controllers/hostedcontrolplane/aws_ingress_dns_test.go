package hostedcontrolplane

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/globalconfig"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

func TestGetZoneIDFromStatus(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		zoneType hyperv1.AWSDNSZoneType
		expected string
	}{
		{
			name: "When zone exists in status, it should return the ID",
			hcp: &hyperv1.HostedControlPlane{
				Status: hyperv1.HostedControlPlaneStatus{
					Platform: &hyperv1.PlatformStatus{
						AWS: &hyperv1.AWSPlatformStatus{
							DNSZones: []hyperv1.AWSDNSZoneStatus{
								{ZoneID: "ZPUB", ZoneType: hyperv1.PublicIngressZone, Name: "in.test.example.com"},
							},
						},
					},
				},
			},
			zoneType: hyperv1.PublicIngressZone,
			expected: "ZPUB",
		},
		{
			name: "When zone type is not in status, it should return empty",
			hcp: &hyperv1.HostedControlPlane{
				Status: hyperv1.HostedControlPlaneStatus{
					Platform: &hyperv1.PlatformStatus{
						AWS: &hyperv1.AWSPlatformStatus{
							DNSZones: []hyperv1.AWSDNSZoneStatus{
								{ZoneID: "ZPUB", ZoneType: hyperv1.PublicIngressZone, Name: "in.test.example.com"},
							},
						},
					},
				},
			},
			zoneType: hyperv1.PrivateIngressZone,
			expected: "",
		},
		{
			name:     "When status platform is nil, it should return empty",
			hcp:      &hyperv1.HostedControlPlane{},
			zoneType: hyperv1.PublicIngressZone,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(getZoneIDFromStatus(tt.hcp, tt.zoneType)).To(Equal(tt.expected))
		})
	}
}

func TestSetZoneInStatus(t *testing.T) {
	g := NewGomegaWithT(t)

	hcp := &hyperv1.HostedControlPlane{}
	setZoneInStatus(hcp, hyperv1.PublicIngressZone, "ZPUB", "in.test.example.com", []string{"ns1.example.com", "ns2.example.com"})
	g.Expect(hcp.Status.Platform.AWS.DNSZones).To(HaveLen(1))
	g.Expect(hcp.Status.Platform.AWS.DNSZones[0].ZoneID).To(Equal("ZPUB"))
	g.Expect(hcp.Status.Platform.AWS.DNSZones[0].NameServers).To(Equal([]string{"ns1.example.com", "ns2.example.com"}))

	setZoneInStatus(hcp, hyperv1.PrivateIngressZone, "ZPRIV", "in.test.example.com", nil)
	g.Expect(hcp.Status.Platform.AWS.DNSZones).To(HaveLen(2))

	setZoneInStatus(hcp, hyperv1.PublicIngressZone, "ZPUB2", "in.test.example.com", []string{"ns3.example.com"})
	g.Expect(hcp.Status.Platform.AWS.DNSZones).To(HaveLen(2))
	g.Expect(getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)).To(Equal("ZPUB2"))
	g.Expect(hcp.Status.Platform.AWS.DNSZones[0].NameServers).To(Equal([]string{"ns3.example.com"}))
}

func TestVerifyOrCreateZone(t *testing.T) {
	errCreateNotExpected := func() (string, error) { return "", errors.New("createFn should not be called") }

	tests := []struct {
		name         string
		zoneID       string
		setupMock    func(*awsapi.MockROUTE53API)
		createFn     func() (string, error)
		expectedZone string
		expectError  bool
	}{
		{
			name:         "When zoneID is empty, it should create a new zone",
			zoneID:       "",
			setupMock:    func(m *awsapi.MockROUTE53API) {},
			createFn:     func() (string, error) { return "Z-NEW", nil },
			expectedZone: "Z-NEW",
		},
		{
			name:   "When zoneID exists and is valid, it should keep it without creating",
			zoneID: "Z-EXISTING",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.GetHostedZoneOutput{}, nil)
			},
			createFn:     errCreateNotExpected,
			expectedZone: "Z-EXISTING",
		},
		{
			name:   "When zoneID was deleted externally, it should clear and recreate",
			zoneID: "Z-GONE",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, &route53types.NoSuchHostedZone{})
			},
			createFn:     func() (string, error) { return "Z-RECREATED", nil },
			expectedZone: "Z-RECREATED",
		},
		{
			name:   "When GetHostedZone fails with a non-NotFound error, it should return the error and keep the zoneID",
			zoneID: "Z-ERR",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("throttled"))
			},
			createFn:     errCreateNotExpected,
			expectError:  true,
			expectedZone: "Z-ERR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			zoneID := tt.zoneID
			err := verifyOrCreateZone(context.Background(), mockR53, &zoneID, "test", tt.createFn, logr.Discard())
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			g.Expect(zoneID).To(Equal(tt.expectedZone))
		})
	}
}

// managedDNSHCP returns a minimal managed-DNS HostedControlPlane with the given
// existing status zones already recorded.
func managedDNSHCP(zones ...hyperv1.AWSDNSZoneStatus) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					Region:     "us-east-1",
					ManagedDNS: hyperv1.AWSManagedDNSSpec{IngressDomainPrefix: "in"},
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC: "vpc-12345",
					},
				},
			},
		},
	}
	if len(zones) > 0 {
		hcp.Status.Platform = &hyperv1.PlatformStatus{AWS: &hyperv1.AWSPlatformStatus{DNSZones: zones}}
	}
	return hcp
}

func TestReconcilePublicZone(t *testing.T) {
	t.Run("When the zone does not exist yet it creates one and records its ID and nameservers", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)

		// CreatePublicHostedZone: lookup finds nothing, so a new zone is created.
		mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ListHostedZonesOutput{}, nil)
		mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.CreateHostedZoneOutput{
			HostedZone:    &route53types.HostedZone{Id: aws.String("/hostedzone/Z-PUB-NEW")},
			DelegationSet: &route53types.DelegationSet{NameServers: []string{"ns1.example.com", "ns2.example.com"}},
		}, nil)

		r := &HostedControlPlaneReconciler{}
		hcp := managedDNSHCP()

		ns, err := r.reconcilePublicIngressZone(context.Background(), mockR53, hcp, "in.test.example.com")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ns).To(Equal([]string{"ns1.example.com", "ns2.example.com"}))

		g.Expect(getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)).To(Equal("Z-PUB-NEW"))
		zones := hcp.Status.Platform.AWS.DNSZones
		g.Expect(zones).To(HaveLen(1))
		g.Expect(zones[0].Name).To(Equal("in.test.example.com"))
		g.Expect(zones[0].NameServers).To(Equal([]string{"ns1.example.com", "ns2.example.com"}))
	})

	t.Run("When the zone already exists it looks up its nameservers so NS delegation still works", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)

		// One GetHostedZone verifies the existing zone; a second looks up the
		// nameservers because an existing zone yields none from creation.
		mockR53.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.GetHostedZoneOutput{
			DelegationSet: &route53types.DelegationSet{NameServers: []string{"ns1.example.com", "ns2.example.com"}},
		}, nil).Times(2)

		r := &HostedControlPlaneReconciler{}
		hcp := managedDNSHCP(hyperv1.AWSDNSZoneStatus{ZoneID: "Z-PUB", ZoneType: hyperv1.PublicIngressZone})

		ns, err := r.reconcilePublicIngressZone(context.Background(), mockR53, hcp, "in.test.example.com")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ns).To(Equal([]string{"ns1.example.com", "ns2.example.com"}))
		g.Expect(getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)).To(Equal("Z-PUB"))
		g.Expect(hcp.Status.Platform.AWS.DNSZones[0].NameServers).To(Equal([]string{"ns1.example.com", "ns2.example.com"}))
	})
}

func TestReconcilePrivateZone(t *testing.T) {
	t.Run("When the cluster uses a shared VPC it does not manage a private zone", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		// A strict mock with no expectations fails the test if any Route53 call is made.
		mockR53 := awsapi.NewMockROUTE53API(ctrl)

		r := &HostedControlPlaneReconciler{}
		hcp := managedDNSHCP()
		hcp.Spec.Platform.AWS.SharedVPC = &hyperv1.AWSSharedVPC{}

		g.Expect(r.reconcilePrivateIngressZone(context.Background(), mockR53, hcp, "in.test.example.com")).To(Succeed())
		g.Expect(getZoneIDFromStatus(hcp, hyperv1.PrivateIngressZone)).To(BeEmpty())
		g.Expect(hcp.Status.Platform).To(BeNil(), "no private zone should be recorded for a shared VPC cluster")
	})

	t.Run("When the private zone already exists it is verified and recorded", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		mockR53.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.GetHostedZoneOutput{}, nil)

		r := &HostedControlPlaneReconciler{}
		hcp := managedDNSHCP(hyperv1.AWSDNSZoneStatus{ZoneID: "Z-PRIV", ZoneType: hyperv1.PrivateIngressZone})

		g.Expect(r.reconcilePrivateIngressZone(context.Background(), mockR53, hcp, "in.test.example.com")).To(Succeed())
		g.Expect(getZoneIDFromStatus(hcp, hyperv1.PrivateIngressZone)).To(Equal("Z-PRIV"))
		g.Expect(hcp.Status.Platform.AWS.DNSZones[0].Name).To(Equal("in.test.example.com"))
	})
}

// TestReconcileIngressDNSSharedVPC locks the shared-VPC managed-DNS contract:
// only the public ingress zone is created (the private zone is pre-created and
// owned by the VPC owner), and the terminal condition still reports available.
// It composes the sub-reconcilers in the same order as
// reconcileAWSManagedIngressDNSZones, which builds its Route53 client from
// awsSession and so cannot take a mock.
func TestReconcileIngressDNSSharedVPC(t *testing.T) {
	g := NewGomegaWithT(t)
	ctrl := gomock.NewController(t)
	// A strict mock fails on any unexpected call, so the private zone never
	// touching Route53 is enforced, not just asserted from status.
	mockR53 := awsapi.NewMockROUTE53API(ctrl)

	// Only the public zone is created: lookup finds nothing, so a new zone is made.
	mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ListHostedZonesOutput{}, nil)
	mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.CreateHostedZoneOutput{
		HostedZone:    &route53types.HostedZone{Id: aws.String("/hostedzone/Z-PUB-NEW")},
		DelegationSet: &route53types.DelegationSet{NameServers: []string{"ns1.example.com", "ns2.example.com"}},
	}, nil)

	r := &HostedControlPlaneReconciler{}
	hcp := managedDNSHCP()
	hcp.Spec.Platform.AWS.SharedVPC = &hyperv1.AWSSharedVPC{}
	domain := globalconfig.ManagedDNSIngressZoneDomain(hcp)

	publicNS, err := r.reconcilePublicIngressZone(context.Background(), mockR53, hcp, domain)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(r.reconcilePrivateIngressZone(context.Background(), mockR53, hcp, domain)).To(Succeed())
	g.Expect(r.reconcileNSDelegation(context.Background(), mockR53, hcp, domain, publicNS)).To(Succeed())

	g.Expect(getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)).To(Equal("Z-PUB-NEW"))
	g.Expect(getZoneIDFromStatus(hcp, hyperv1.PrivateIngressZone)).To(BeEmpty(), "shared VPC clusters must not manage a private ingress zone")
	g.Expect(hcp.Status.Platform.AWS.DNSZones).To(HaveLen(1), "only the public ingress zone should be recorded")

	cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(cond.Reason).To(Equal(hyperv1.AWSManagedDNSSuccessReason))
}

func TestReconcileNSDelegationNoDelegation(t *testing.T) {
	g := NewGomegaWithT(t)
	ctrl := gomock.NewController(t)
	// No delegation is configured, so this path must make zero Route53 calls; a
	// strict mock with no expectations enforces that.
	mockR53 := awsapi.NewMockROUTE53API(ctrl)

	r := &HostedControlPlaneReconciler{}
	hcp := managedDNSHCP()

	g.Expect(r.reconcileNSDelegation(context.Background(), mockR53, hcp, "in.test.example.com", nil)).To(Succeed())

	cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(cond.Reason).To(Equal(hyperv1.AWSManagedDNSSuccessReason))
	g.Expect(cond.Message).To(Equal("DNS zones created"))
}

func TestVerifyNSDelegation(t *testing.T) {
	// domain uses the reserved .invalid TLD (RFC 6761), so the NS lookup never
	// resolves and the terminal condition always lands in the pending branch.
	const domain = "in.test.invalid"

	tests := []struct {
		name            string
		dnsEndpointErr  error
		existingCond    *metav1.Condition
		expectedMessage string
	}{
		{
			name:            "When NS delegation is not yet resolvable, it should report pending",
			expectedMessage: "DNS zones created; NS delegation not yet resolvable",
		},
		{
			name:            "When the DNSEndpoint has not reconciled, it should surface that in the message",
			dnsEndpointErr:  errors.New("boom"),
			expectedMessage: "DNS zones created; DNSEndpoint for NS delegation not yet reconciled: boom",
		},
		{
			name: "When delegation stays unresolvable past the max wait, it should warn to check the parent zone",
			existingCond: &metav1.Condition{
				Type:               string(hyperv1.AWSManagedDNSAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.AWSManagedDNSPendingReason,
				LastTransitionTime: metav1.NewTime(time.Now().Add(-30 * time.Minute)),
			},
			expectedMessage: "NS delegation not resolvable after 10m0s; verify NS records exist in the parent zone for " + domain,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := &HostedControlPlaneReconciler{}
			hcp := managedDNSHCP()
			if tt.existingCond != nil {
				meta.SetStatusCondition(&hcp.Status.Conditions, *tt.existingCond)
			}

			g.Expect(r.verifyNSDelegation(context.Background(), hcp, domain, tt.dnsEndpointErr)).To(Succeed())

			cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(hyperv1.AWSManagedDNSPendingReason))
			g.Expect(cond.Message).To(Equal(tt.expectedMessage))
		})
	}
}

func TestDestroyAWSManagedIngressDNSZones(t *testing.T) {
	t.Run("When managed DNS is not enabled, it should be a no-op", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := &HostedControlPlaneReconciler{}
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{}}},
		}

		// awsSession is nil; the guard must return before any AWS client is built.
		g.Expect(r.destroyAWSManagedIngressDNSZones(context.Background(), hcp)).To(Succeed())
	})
}

func TestReconcileDNSEndpoint(t *testing.T) {
	g := NewGomegaWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())
	scheme.AddKnownTypeWithName(dnsEndpointGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(dnsEndpointGVK.GroupVersion().WithKind("DNSEndpointList"), &unstructured.UnstructuredList{})

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &HostedControlPlaneReconciler{Client: fakeClient}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns", UID: "abc-123"},
	}

	// Trailing dots on the domain and nameservers must be stripped in the record.
	err := r.reconcileDNSEndpoint(context.Background(), hcp, "in.test.example.com.", []string{"ns1.example.com.", "ns2.example.com."})
	g.Expect(err).NotTo(HaveOccurred())

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(dnsEndpointGVK)
	g.Expect(fakeClient.Get(context.Background(), crclient.ObjectKey{Namespace: "test-ns", Name: "test-hcp-ingress-delegation"}, got)).To(Succeed())

	// The DNSEndpoint must be owned by the HCP so it is garbage collected with it.
	owners := got.GetOwnerReferences()
	g.Expect(owners).To(HaveLen(1))
	g.Expect(owners[0].Name).To(Equal("test-hcp"))

	endpoints, found, err := unstructured.NestedSlice(got.Object, "spec", "endpoints")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(found).To(BeTrue())
	g.Expect(endpoints).To(HaveLen(1))

	ep := endpoints[0].(map[string]interface{})
	g.Expect(ep["dnsName"]).To(Equal("in.test.example.com"))
	g.Expect(ep["recordType"]).To(Equal("NS"))
	g.Expect(ep["recordTTL"]).To(Equal(int64(300)))
	g.Expect(ep["targets"]).To(Equal([]interface{}{"ns1.example.com", "ns2.example.com"}))
}

func TestThrottleDNSReconcile(t *testing.T) {
	t.Run("When there is no prior reconcile it proceeds and records the attempt", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := &HostedControlPlaneReconciler{}
		hcp := managedDNSHCP()
		hcp.Name = "hcp-a"

		g.Expect(r.throttleDNSReconcile(hcp)).To(BeTrue())
		_, recorded := r.lastDNSReconcile.Load("hcp-a")
		g.Expect(recorded).To(BeTrue(), "the attempt should be recorded before any work")
		// An immediate second attempt is throttled by the retry interval.
		g.Expect(r.throttleDNSReconcile(hcp)).To(BeFalse())
	})

	t.Run("When the retry interval has elapsed it proceeds again", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := &HostedControlPlaneReconciler{}
		hcp := managedDNSHCP()
		hcp.Name = "hcp-b"
		r.lastDNSReconcile.Store("hcp-b", time.Now().Add(-dnsRetryInterval-time.Second))

		g.Expect(r.throttleDNSReconcile(hcp)).To(BeTrue())
	})

	t.Run("When DNS is already available it waits the longer reverify interval", func(t *testing.T) {
		g := NewGomegaWithT(t)
		r := &HostedControlPlaneReconciler{}
		hcp := managedDNSHCP()
		hcp.Name = "hcp-c"
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:   string(hyperv1.AWSManagedDNSAvailable),
			Status: metav1.ConditionTrue,
			Reason: hyperv1.AWSManagedDNSSuccessReason,
		})

		// Past the retry interval but within the reverify interval: still throttled.
		r.lastDNSReconcile.Store("hcp-c", time.Now().Add(-dnsRetryInterval-time.Second))
		g.Expect(r.throttleDNSReconcile(hcp)).To(BeFalse())

		// Past the reverify interval: proceeds.
		r.lastDNSReconcile.Store("hcp-c", time.Now().Add(-dnsReverifyInterval-time.Second))
		g.Expect(r.throttleDNSReconcile(hcp)).To(BeTrue())
	})
}
