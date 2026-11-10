//go:build e2ev2

package deterministiclb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStoredIngressAddresses(t *testing.T) {
	tests := []struct {
		name     string
		ingress  []corev1.LoadBalancerIngress
		expected []string
	}{
		{
			name:     "When ingress has an IP, it should return the IP",
			ingress:  []corev1.LoadBalancerIngress{{IP: "192.0.2.1"}},
			expected: []string{"192.0.2.1"},
		},
		{
			name:     "When ingress has a hostname, it should return the hostname without resolving it",
			ingress:  []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}},
			expected: []string{"lb.example.com"},
		},
		{
			name:     "When ingress has both fields, it should return both",
			ingress:  []corev1.LoadBalancerIngress{{IP: "192.0.2.1", Hostname: "lb.example.com"}},
			expected: []string{"192.0.2.1", "lb.example.com"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := storedIngressAddresses(tc.ingress)
			if strings.Join(got, ",") != strings.Join(tc.expected, ",") {
				t.Fatalf("storedIngressAddresses() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestVerifyServiceUnchanged(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	lb := &hyperv1.LoadBalancerPublishingStrategy{
		ServiceAnnotations: map[string]string{"service.beta.kubernetes.io/aws-load-balancer-eip-allocations": "eipalloc-test"},
		LoadBalancerClass:  "example.com/class",
	}

	tests := []struct {
		name        string
		before      *corev1.Service
		current     *corev1.Service
		expectedErr string
	}{
		{
			name:    "When ingress IP and hostname are unchanged, it should pass",
			before:  testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1", Hostname: "lb.example.com"}}, lb, "uid-1"),
			current: testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1", Hostname: "lb.example.com"}}, lb, "uid-1"),
		},
		{
			name:    "When ingress entries are reordered, it should pass",
			before:  testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1"}, {Hostname: "lb.example.com"}}, lb, "uid-1"),
			current: testService([]corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}, {IP: "192.0.2.1"}}, lb, "uid-1"),
		},
		{
			name:        "When the ingress IP changes, it should fail",
			before:      testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1"}}, lb, "uid-1"),
			current:     testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.2"}}, lb, "uid-1"),
			expectedErr: "stored ingress fields changed",
		},
		{
			name:        "When the ingress hostname changes, it should fail",
			before:      testService([]corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}}, lb, "uid-1"),
			current:     testService([]corev1.LoadBalancerIngress{{Hostname: "other.example.com"}}, lb, "uid-1"),
			expectedErr: "stored ingress fields changed",
		},
		{
			name:        "When the current ingress is empty, it should fail",
			before:      testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1"}}, lb, "uid-1"),
			current:     testService(nil, lb, "uid-1"),
			expectedErr: "lost its load balancer address",
		},
		{
			name:        "When the previous ingress is empty, it should fail",
			before:      testService(nil, lb, "uid-1"),
			current:     testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1"}}, lb, "uid-1"),
			expectedErr: "had no load balancer address",
		},
		{
			name:        "When the load balancer class changes, it should fail",
			before:      testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1"}}, lb, "uid-1"),
			current:     testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1"}}, lb, "uid-1", "other.example.com/class"),
			expectedErr: "loadBalancerClass changed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.current).Build()
			err := verifyServiceUnchanged(t.Context(), client, tc.before, tc.before, lb)
			if tc.expectedErr == "" {
				if err != nil {
					t.Fatalf("verifyServiceUnchanged() returned %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.expectedErr) {
				t.Fatalf("verifyServiceUnchanged() = %v, want error containing %q", err, tc.expectedErr)
			}
			if strings.Contains(err.Error(), "example.com") {
				t.Fatalf("verifyServiceUnchanged() leaked a hostname: %v", err)
			}
		})
	}
}

func TestResolveIngressAddresses(t *testing.T) {
	originalLookupHost := lookupHost
	t.Cleanup(func() { lookupHost = originalLookupHost })

	t.Run("When ingress has only an IP, it should not resolve DNS", func(t *testing.T) {
		lookupHost = func(context.Context, string) ([]string, error) {
			t.Fatal("unexpected DNS lookup")
			return nil, nil
		}
		addresses, err := resolveIngressAddresses(t.Context(), []corev1.LoadBalancerIngress{{IP: "192.0.2.1"}})
		if err != nil || len(addresses) != 1 || addresses[0] != "192.0.2.1" {
			t.Fatalf("resolveIngressAddresses() = %v, %v, want [192.0.2.1], nil", addresses, err)
		}
	})

	t.Run("When a hostname resolves, it should return the resolved address", func(t *testing.T) {
		lookupHost = func(context.Context, string) ([]string, error) {
			return []string{"192.0.2.10"}, nil
		}
		addresses, err := resolveIngressAddresses(t.Context(), []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}})
		if err != nil || len(addresses) != 1 || addresses[0] != "192.0.2.10" {
			t.Fatalf("resolveIngressAddresses() = %v, %v, want [192.0.2.10], nil", addresses, err)
		}
	})

	t.Run("When a hostname lookup fails, it should return the error", func(t *testing.T) {
		lookupHost = func(context.Context, string) ([]string, error) {
			return nil, errors.New("lookup failed")
		}
		_, err := resolveIngressAddresses(t.Context(), []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}})
		if err == nil || !strings.Contains(err.Error(), "failed to resolve load balancer hostname") {
			t.Fatalf("resolveIngressAddresses() error = %v, want lookup failure", err)
		}
		if strings.Contains(err.Error(), "lb.example.com") {
			t.Fatalf("resolveIngressAddresses() leaked a hostname: %v", err)
		}
	})
}

func TestVerifyExpectedIP(t *testing.T) {
	originalLookupHost := lookupHost
	t.Cleanup(func() { lookupHost = originalLookupHost })

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	lb := &hyperv1.LoadBalancerPublishingStrategy{ServiceAnnotations: map[string]string{"example.com/ip": "192.0.2.1"}}

	t.Run("When the service IP matches, it should pass", func(t *testing.T) {
		service := testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1"}}, lb, "uid-1")
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
		if err := verifyExpectedIP(t.Context(), client, service, []string{"192.0.2.1"}, 100*time.Millisecond); err != nil {
			t.Fatalf("verifyExpectedIP() = %v, want nil", err)
		}
	})

	t.Run("When the service address does not match, it should report the mismatch", func(t *testing.T) {
		service := testService([]corev1.LoadBalancerIngress{{IP: "198.51.100.7"}}, lb, "uid-1")
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
		err := verifyExpectedIP(t.Context(), client, service, []string{"203.0.113.10"}, 100*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "last resolved addresses [198.51.100.7]") {
			t.Fatalf("verifyExpectedIP() = %v, want mismatch with last addresses", err)
		}
	})

	t.Run("When DNS fails, it should report the DNS error", func(t *testing.T) {
		lookupHost = func(context.Context, string) ([]string, error) {
			return nil, errors.New("lookup failed")
		}
		service := testService([]corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}}, lb, "uid-1")
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
		err := verifyExpectedIP(t.Context(), client, service, []string{"192.0.2.1"}, 100*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "failed to resolve load balancer hostname") {
			t.Fatalf("verifyExpectedIP() = %v, want DNS diagnostic", err)
		}
	})
}

func TestWaitForPrimaryServiceAcceptsHostnameOnlyIngress(t *testing.T) {
	originalLookupHost := lookupHost
	t.Cleanup(func() { lookupHost = originalLookupHost })
	lookupHost = func(context.Context, string) ([]string, error) {
		return []string{"192.0.2.1"}, nil
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	lb := &hyperv1.LoadBalancerPublishingStrategy{ServiceAnnotations: map[string]string{"example.com/ip": "192.0.2.1"}}
	service := testService([]corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}}, lb, "uid-1")
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()

	if _, err := waitForPrimaryService(t.Context(), client, service, lb, []string{"192.0.2.1"}, time.Second); err != nil {
		t.Fatalf("waitForPrimaryService() = %v, want hostname-only ingress to pass", err)
	}
}

func TestWaitForPrimaryServiceRedactsAnnotationValues(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	lb := &hyperv1.LoadBalancerPublishingStrategy{ServiceAnnotations: map[string]string{"example.com/ip": "sensitive-value"}}
	service := testService([]corev1.LoadBalancerIngress{{IP: "192.0.2.1"}}, lb, "uid-1")
	service.Annotations["example.com/ip"] = "different-value"
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()

	_, err := waitForPrimaryService(t.Context(), client, service, lb, nil, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "does not have the requested annotation") {
		t.Fatalf("waitForPrimaryService() = %v, want annotation mismatch", err)
	}
	if strings.Contains(err.Error(), "sensitive-value") || strings.Contains(err.Error(), "different-value") {
		t.Fatalf("waitForPrimaryService() leaked an annotation value: %v", err)
	}
}

func TestWaitForPrimaryServiceIncludesLastObservation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	lb := &hyperv1.LoadBalancerPublishingStrategy{ServiceAnnotations: map[string]string{"example.com/ip": "203.0.113.10"}}
	service := testService([]corev1.LoadBalancerIngress{{IP: "198.51.100.7"}}, lb, "uid-1")
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()

	_, err := waitForPrimaryService(t.Context(), client, service, lb, []string{"203.0.113.10"}, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "last observed ingress=[198.51.100.7]") || !strings.Contains(err.Error(), "expected IPs=[203.0.113.10]") {
		t.Fatalf("waitForPrimaryService() = %v, want last observed ingress and expected IPs", err)
	}
}

func testService(ingress []corev1.LoadBalancerIngress, lb *hyperv1.LoadBalancerPublishingStrategy, uid string, classOverride ...string) *corev1.Service {
	class := lb.LoadBalancerClass
	if len(classOverride) > 0 {
		class = classOverride[0]
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "clusters-test",
			UID:       types.UID(uid),
			Annotations: map[string]string{
				"example.com/ip": "203.0.113.10",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{Ingress: ingress},
		},
	}
	if class != "" {
		service.Spec.LoadBalancerClass = ptr.To(class)
	}
	for key, value := range lb.ServiceAnnotations {
		service.Annotations[key] = value
	}
	return service
}
