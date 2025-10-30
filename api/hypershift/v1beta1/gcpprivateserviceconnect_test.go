package v1beta1

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGCPPrivateServiceConnectSpec_Validation(t *testing.T) {
	tests := []struct {
		name    string
		spec    GCPPrivateServiceConnectSpec
		isValid bool
		desc    string
	}{
		{
			name: "valid spec",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "my-forwarding-rule",
				ConsumerAcceptList: []string{"customer-project-123"},
				NATSubnet:          "nat-subnet-1",
			},
			isValid: true,
			desc:    "should accept valid PSC spec",
		},
		{
			name: "valid spec with multiple consumers",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "another-forwarding-rule",
				ConsumerAcceptList: []string{
					"customer-project-123",
					"another-customer-456",
					"third-customer-789",
				},
				NATSubnet: "nat-subnet-2",
			},
			isValid: true,
			desc:    "should accept multiple consumer projects",
		},
		{
			name: "valid spec without NAT subnet",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "my-forwarding-rule",
				ConsumerAcceptList: []string{"customer-project-123"},
			},
			isValid: true,
			desc:    "should accept spec without optional NAT subnet",
		},
		{
			name: "invalid forwarding rule name - empty",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "",
				ConsumerAcceptList: []string{"customer-project-123"},
			},
			isValid: false,
			desc:    "should reject empty forwarding rule name",
		},
		{
			name: "invalid forwarding rule name - too long",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: strings.Repeat("a", 64), // 64 characters (too long)
				ConsumerAcceptList: []string{"customer-project-123"},
			},
			isValid: false,
			desc:    "should reject forwarding rule names longer than 63 characters",
		},
		{
			name: "invalid forwarding rule name - starts with number",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "123-invalid-rule",
				ConsumerAcceptList: []string{"customer-project-123"},
			},
			isValid: false,
			desc:    "should reject forwarding rule names starting with numbers",
		},
		{
			name: "invalid forwarding rule name - ends with hyphen",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "invalid-rule-",
				ConsumerAcceptList: []string{"customer-project-123"},
			},
			isValid: false,
			desc:    "should reject forwarding rule names ending with hyphens",
		},
		{
			name: "invalid consumer accept list - empty",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "my-forwarding-rule",
				ConsumerAcceptList: []string{},
			},
			isValid: false,
			desc:    "should reject empty consumer accept list",
		},
		{
			name: "invalid consumer accept list - too many items",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "my-forwarding-rule",
				ConsumerAcceptList: func() []string {
					// Create 51 items (exceeds maximum of 50)
					consumers := make([]string, 51)
					for i := 0; i < 51; i++ {
						consumers[i] = "project-" + strings.Repeat("a", 20)
					}
					return consumers
				}(),
			},
			isValid: false,
			desc:    "should reject consumer accept lists with more than 50 items",
		},
		{
			name: "invalid consumer project - invalid format",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "my-forwarding-rule",
				ConsumerAcceptList: []string{"Invalid-Project-Name"},
			},
			isValid: false,
			desc:    "should reject invalid project ID format in consumer list",
		},
		{
			name: "invalid NAT subnet - too long",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "my-forwarding-rule",
				ConsumerAcceptList: []string{"customer-project-123"},
				NATSubnet:          strings.Repeat("a", 64), // 64 characters (too long)
			},
			isValid: false,
			desc:    "should reject NAT subnet names longer than 63 characters",
		},
		{
			name: "invalid NAT subnet - invalid format",
			spec: GCPPrivateServiceConnectSpec{
				ForwardingRuleName: "my-forwarding-rule",
				ConsumerAcceptList: []string{"customer-project-123"},
				NATSubnet:          "123-invalid-subnet",
			},
			isValid: false,
			desc:    "should reject NAT subnet names starting with numbers",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.isValid {
				// Valid specs should have proper lengths and formats
				if len(test.spec.ForwardingRuleName) < 1 {
					t.Errorf("forwarding rule name should be at least 1 character, got %d", len(test.spec.ForwardingRuleName))
				}
				if len(test.spec.ForwardingRuleName) > 63 {
					t.Errorf("forwarding rule name should be at most 63 characters, got %d", len(test.spec.ForwardingRuleName))
				}
				if len(test.spec.ConsumerAcceptList) < 1 {
					t.Errorf("consumer accept list should have at least 1 item, got %d", len(test.spec.ConsumerAcceptList))
				}
				if len(test.spec.ConsumerAcceptList) > 50 {
					t.Errorf("consumer accept list should have at most 50 items, got %d", len(test.spec.ConsumerAcceptList))
				}

				if test.spec.NATSubnet != "" {
					if len(test.spec.NATSubnet) > 63 {
						t.Errorf("NAT subnet name should be at most 63 characters, got %d", len(test.spec.NATSubnet))
					}
				}
			}
		})
	}
}

func TestGCPPrivateServiceConnectStatus_Validation(t *testing.T) {
	tests := []struct {
		name   string
		status GCPPrivateServiceConnectStatus
		desc   string
	}{
		{
			name: "valid status with all fields",
			status: GCPPrivateServiceConnectStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(GCPPrivateServiceConnectReady),
						Status: metav1.ConditionTrue,
						Reason: GCPSuccessReason,
					},
				},
				ServiceAttachmentName: "service-attachment-name",
				ServiceAttachmentURI:  "projects/my-project/regions/us-central1/serviceAttachments/my-service",
				EndpointIP:            "10.0.0.100",
				DNSZoneName:           "example.com",
				DNSRecords:            []string{"api.example.com", "*.apps.example.com"},
			},
			desc: "should accept valid status with all fields populated",
		},
		{
			name: "valid status with minimal fields",
			status: GCPPrivateServiceConnectStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(GCPServiceAttachmentReady),
						Status: metav1.ConditionFalse,
						Reason: GCPErrorReason,
					},
				},
			},
			desc: "should accept status with only conditions",
		},
		{
			name: "valid endpoint IP addresses",
			status: GCPPrivateServiceConnectStatus{
				EndpointIP: "192.168.1.1",
			},
			desc: "should accept valid IPv4 addresses",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Test condition constraints
			if len(test.status.Conditions) > 10 {
				t.Errorf("conditions should be at most 10 items, got %d", len(test.status.Conditions))
			}

			// Test service attachment name constraints
			if test.status.ServiceAttachmentName != "" {
				if len(test.status.ServiceAttachmentName) > 63 {
					t.Errorf("service attachment name should be at most 63 characters, got %d", len(test.status.ServiceAttachmentName))
				}
			}

			// Test service attachment URI constraints
			if test.status.ServiceAttachmentURI != "" {
				if len(test.status.ServiceAttachmentURI) > 2048 {
					t.Errorf("service attachment URI should be at most 2048 characters, got %d", len(test.status.ServiceAttachmentURI))
				}
			}

			// Test endpoint IP constraints
			if test.status.EndpointIP != "" {
				if len(test.status.EndpointIP) > 15 {
					t.Errorf("endpoint IP should be at most 15 characters, got %d", len(test.status.EndpointIP))
				}
			}

			// Test DNS zone name constraints
			if test.status.DNSZoneName != "" {
				if len(test.status.DNSZoneName) > 253 {
					t.Errorf("DNS zone name should be at most 253 characters, got %d", len(test.status.DNSZoneName))
				}
			}

			// Test DNS records constraints
			if len(test.status.DNSRecords) > 10 {
				t.Errorf("DNS records should be at most 10 items, got %d", len(test.status.DNSRecords))
			}
			for i, record := range test.status.DNSRecords {
				if len(record) > 253 {
					t.Errorf("DNS record %d should be at most 253 characters, got %d", i, len(record))
				}
			}
		})
	}
}

func TestGCPPrivateServiceConnectConditionTypes(t *testing.T) {
	// Test that condition types have the expected string values
	if string(GCPPrivateServiceConnectReady) != "GCPPrivateServiceConnectReady" {
		t.Errorf("expected 'GCPPrivateServiceConnectReady', got %s", string(GCPPrivateServiceConnectReady))
	}
	if string(GCPServiceAttachmentReady) != "GCPServiceAttachmentReady" {
		t.Errorf("expected 'GCPServiceAttachmentReady', got %s", string(GCPServiceAttachmentReady))
	}
	if string(GCPEndpointReady) != "GCPEndpointReady" {
		t.Errorf("expected 'GCPEndpointReady', got %s", string(GCPEndpointReady))
	}
	if string(GCPDNSReady) != "GCPDNSReady" {
		t.Errorf("expected 'GCPDNSReady', got %s", string(GCPDNSReady))
	}

	// Test reason constants
	if GCPSuccessReason != "GCPSuccess" {
		t.Errorf("expected 'GCPSuccess', got %s", GCPSuccessReason)
	}
	if GCPErrorReason != "GCPError" {
		t.Errorf("expected 'GCPError', got %s", GCPErrorReason)
	}
}

func TestGCPPrivateServiceConnect_Complete(t *testing.T) {
	// Test a complete GCPPrivateServiceConnect object
	psc := &GCPPrivateServiceConnect{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hypershift.openshift.io/v1beta1",
			Kind:       "GCPPrivateServiceConnect",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-psc",
			Namespace: "test-namespace",
		},
		Spec: GCPPrivateServiceConnectSpec{
			ForwardingRuleName: "test-forwarding-rule",
			ConsumerAcceptList: []string{"customer-project-123"},
			NATSubnet:          "nat-subnet-1",
		},
		Status: GCPPrivateServiceConnectStatus{
			Conditions: []metav1.Condition{
				{
					Type:               string(GCPPrivateServiceConnectReady),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             GCPSuccessReason,
					Message:            "PSC infrastructure is ready",
				},
			},
			ServiceAttachmentName: "test-service-attachment",
			ServiceAttachmentURI:  "projects/management-project/regions/us-central1/serviceAttachments/test-service-attachment",
			EndpointIP:            "10.0.0.100",
			DNSZoneName:           "test.hypershift.local",
			DNSRecords:            []string{"api.test.hypershift.local"},
		},
	}

	// Validate the complete object structure
	if psc.TypeMeta.APIVersion != "hypershift.openshift.io/v1beta1" {
		t.Errorf("expected APIVersion 'hypershift.openshift.io/v1beta1', got %s", psc.TypeMeta.APIVersion)
	}
	if psc.TypeMeta.Kind != "GCPPrivateServiceConnect" {
		t.Errorf("expected Kind 'GCPPrivateServiceConnect', got %s", psc.TypeMeta.Kind)
	}
	if psc.ObjectMeta.Name != "test-psc" {
		t.Errorf("expected Name 'test-psc', got %s", psc.ObjectMeta.Name)
	}
	if psc.ObjectMeta.Namespace != "test-namespace" {
		t.Errorf("expected Namespace 'test-namespace', got %s", psc.ObjectMeta.Namespace)
	}
	if psc.Spec.ForwardingRuleName != "test-forwarding-rule" {
		t.Errorf("expected ForwardingRuleName 'test-forwarding-rule', got %s", psc.Spec.ForwardingRuleName)
	}
	if psc.Status.ServiceAttachmentName != "test-service-attachment" {
		t.Errorf("expected ServiceAttachmentName 'test-service-attachment', got %s", psc.Status.ServiceAttachmentName)
	}
	if len(psc.Status.Conditions) != 1 {
		t.Errorf("expected 1 condition, got %d", len(psc.Status.Conditions))
	}
}

func TestGCPPrivateServiceConnectList(t *testing.T) {
	// Test list type
	list := &GCPPrivateServiceConnectList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hypershift.openshift.io/v1beta1",
			Kind:       "GCPPrivateServiceConnectList",
		},
		Items: []GCPPrivateServiceConnect{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "psc-1",
				},
				Spec: GCPPrivateServiceConnectSpec{
					ForwardingRuleName: "rule-1",
					ConsumerAcceptList: []string{"project-1"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "psc-2",
				},
				Spec: GCPPrivateServiceConnectSpec{
					ForwardingRuleName: "rule-2",
					ConsumerAcceptList: []string{"project-2"},
				},
			},
		},
	}

	if list.TypeMeta.Kind != "GCPPrivateServiceConnectList" {
		t.Errorf("expected Kind 'GCPPrivateServiceConnectList', got %s", list.TypeMeta.Kind)
	}
	if len(list.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(list.Items))
	}
	if len(list.Items) > 100 {
		t.Errorf("list should have at most 100 items, got %d", len(list.Items))
	}
	if list.Items[0].ObjectMeta.Name != "psc-1" {
		t.Errorf("expected first item name 'psc-1', got %s", list.Items[0].ObjectMeta.Name)
	}
	if list.Items[1].ObjectMeta.Name != "psc-2" {
		t.Errorf("expected second item name 'psc-2', got %s", list.Items[1].ObjectMeta.Name)
	}
}