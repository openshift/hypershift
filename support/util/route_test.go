package util

import (
	"fmt"
	"math/rand"
	"testing"

	routev1 "github.com/openshift/api/route/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
)

func TestShortenName(t *testing.T) {
	for i := 0; i < 10; i++ {
		shortName := randSeq(rand.Intn(kvalidation.DNS1123SubdomainMaxLength-10) + 1)
		longName := randSeq(kvalidation.DNS1123SubdomainMaxLength + rand.Intn(100))

		tests := []struct {
			base, suffix, expected string
		}{
			{
				base:     shortName,
				suffix:   "deploy",
				expected: shortName + "-deploy",
			},
			{
				base:     longName,
				suffix:   "deploy",
				expected: longName[:kvalidation.DNS1123SubdomainMaxLength-16] + "-" + hash(longName) + "-deploy",
			},
			{
				base:     shortName,
				suffix:   longName,
				expected: shortName + "-" + hash(shortName+"-"+longName),
			},
			{
				base:     "",
				suffix:   shortName,
				expected: "-" + shortName,
			},
			{
				base:     "",
				suffix:   longName,
				expected: "-" + hash("-"+longName),
			},
			{
				base:     shortName,
				suffix:   "",
				expected: shortName + "-",
			},
			{
				base:     longName,
				suffix:   "",
				expected: longName[:kvalidation.DNS1123SubdomainMaxLength-10] + "-" + hash(longName) + "-",
			},
		}

		for j, test := range tests {
			t.Run(fmt.Sprintf("test-%d-%d", i, j), func(t *testing.T) {
				result := ShortenName(test.base, test.suffix, kvalidation.DNS1123SubdomainMaxLength)
				if result != test.expected {
					t.Errorf("Got unexpected result. Expected: %s Got: %s", test.expected, result)
				}
			})
		}
	}
}

func TestShortenNameIsDifferent(t *testing.T) {
	shortName := randSeq(32)
	deployerName := ShortenName(shortName, "deploy", kvalidation.DNS1123SubdomainMaxLength)
	builderName := ShortenName(shortName, "build", kvalidation.DNS1123SubdomainMaxLength)
	if deployerName == builderName {
		t.Errorf("Expecting names to be different: %s\n", deployerName)
	}
	longName := randSeq(kvalidation.DNS1123SubdomainMaxLength + 10)
	deployerName = ShortenName(longName, "deploy", kvalidation.DNS1123SubdomainMaxLength)
	builderName = ShortenName(longName, "build", kvalidation.DNS1123SubdomainMaxLength)
	if deployerName == builderName {
		t.Errorf("Expecting names to be different: %s\n", deployerName)
	}
}

func TestShortenNameReturnShortNames(t *testing.T) {
	base := randSeq(32)
	for maxLength := 0; maxLength < len(base)+2; maxLength++ {
		for suffixLen := 0; suffixLen <= maxLength+1; suffixLen++ {
			suffix := randSeq(suffixLen)
			got := ShortenName(base, suffix, maxLength)
			if len(got) > maxLength {
				t.Fatalf("len(GetName(%[1]q, %[2]q, %[3]d)) = len(%[4]q) = %[5]d; want %[3]d", base, suffix, maxLength, got, len(got))
			}
		}
	}
}

// From k8s.io/kubernetes/pkg/api/generator.go
var letters = []rune("abcdefghijklmnopqrstuvwxyz0123456789-")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func TestReconcileExternalRoute(t *testing.T) {
	tests := []struct {
		name            string
		route           *routev1.Route
		hostname        string
		defaultDomain   string
		serviceName     string
		labelHCPRoutes  bool
		expectedLabels  map[string]string
		expectedHost    string
		expectedService string
	}{
		{
			name: "When labelHCPRoutes is true and route has no labels, it should add HCP label",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
				},
			},
			hostname:       "test.example.com",
			defaultDomain:  "example.com",
			serviceName:    "test-service",
			labelHCPRoutes: true,
			expectedLabels: map[string]string{
				HCPRouteLabel: "test-namespace",
			},
			expectedHost:    "test.example.com",
			expectedService: "test-service",
		},
		{
			name: "When labelHCPRoutes is true and route has existing labels, it should add HCP label",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"existing-label": "existing-value",
					},
				},
			},
			hostname:       "test.example.com",
			defaultDomain:  "example.com",
			serviceName:    "test-service",
			labelHCPRoutes: true,
			expectedLabels: map[string]string{
				"existing-label": "existing-value",
				HCPRouteLabel:    "test-namespace",
			},
			expectedHost:    "test.example.com",
			expectedService: "test-service",
		},
		{
			name: "When labelHCPRoutes is false and route has no labels, it should not add HCP label",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
				},
			},
			hostname:        "test.example.com",
			defaultDomain:   "example.com",
			serviceName:     "test-service",
			labelHCPRoutes:  false,
			expectedLabels:  nil,
			expectedHost:    "test.example.com",
			expectedService: "test-service",
		},
		{
			name: "When labelHCPRoutes is false and route has HCP label, it should remove HCP label",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
					Labels: map[string]string{
						HCPRouteLabel:    "test-namespace",
						"existing-label": "existing-value",
					},
				},
			},
			hostname:       "test.example.com",
			defaultDomain:  "example.com",
			serviceName:    "test-service",
			labelHCPRoutes: false,
			expectedLabels: map[string]string{
				"existing-label": "existing-value",
			},
			expectedHost:    "test.example.com",
			expectedService: "test-service",
		},
		{
			name: "When labelHCPRoutes is false and route has only HCP label, it should remove HCP label and leave empty map",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
					Labels: map[string]string{
						HCPRouteLabel: "test-namespace",
					},
				},
			},
			hostname:        "test.example.com",
			defaultDomain:   "example.com",
			serviceName:     "test-service",
			labelHCPRoutes:  false,
			expectedLabels:  map[string]string{},
			expectedHost:    "test.example.com",
			expectedService: "test-service",
		},
		{
			name: "When hostname is empty and route name is short, it should leave hostname empty for default generation",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
				},
			},
			hostname:       "",
			defaultDomain:  "example.com",
			serviceName:    "test-service",
			labelHCPRoutes: true,
			expectedLabels: map[string]string{
				HCPRouteLabel: "test-namespace",
			},
			expectedHost:    "", // ShortenRouteHostnameIfNeeded returns empty when name is short enough
			expectedService: "test-service",
		},
		{
			name: "When hostname is empty and route already has a hostname, it should preserve existing hostname",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
				},
				Spec: routev1.RouteSpec{
					Host: "existing.example.com",
				},
			},
			hostname:       "",
			defaultDomain:  "example.com",
			serviceName:    "test-service",
			labelHCPRoutes: true,
			expectedLabels: map[string]string{
				HCPRouteLabel: "test-namespace",
			},
			expectedHost:    "existing.example.com",
			expectedService: "test-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ReconcileExternalRoute(tt.route, tt.hostname, tt.defaultDomain, tt.serviceName, tt.labelHCPRoutes)
			if err != nil {
				t.Fatalf("ReconcileExternalRoute() error = %v, wantErr false", err)
			}

			// Check labels
			if tt.expectedLabels == nil {
				if len(tt.route.Labels) != 0 {
					t.Errorf("Expected no labels, but got %v", tt.route.Labels)
				}
			} else {
				if len(tt.route.Labels) != len(tt.expectedLabels) {
					t.Errorf("Labels length mismatch: got %d, want %d. Got: %v, Want: %v", len(tt.route.Labels), len(tt.expectedLabels), tt.route.Labels, tt.expectedLabels)
				}
				for k, v := range tt.expectedLabels {
					if got, ok := tt.route.Labels[k]; !ok || got != v {
						t.Errorf("Label %s: got %v, want %v", k, got, v)
					}
				}
				// Check that HCP label is not present when not expected
				if _, ok := tt.expectedLabels[HCPRouteLabel]; !ok {
					if _, hasLabel := tt.route.Labels[HCPRouteLabel]; hasLabel {
						t.Errorf("Expected HCP label to be removed, but it still exists")
					}
				}
			}

			// Check hostname
			if tt.route.Spec.Host != tt.expectedHost {
				t.Errorf("Host: got %v, want %v", tt.route.Spec.Host, tt.expectedHost)
			}

			// Check service name
			if tt.route.Spec.To.Name != tt.expectedService {
				t.Errorf("Service name: got %v, want %v", tt.route.Spec.To.Name, tt.expectedService)
			}

			// Check TLS config
			if tt.route.Spec.TLS == nil {
				t.Errorf("Expected TLS config to be set")
			} else {
				if tt.route.Spec.TLS.Termination != routev1.TLSTerminationPassthrough {
					t.Errorf("TLS termination: got %v, want %v", tt.route.Spec.TLS.Termination, routev1.TLSTerminationPassthrough)
				}
				if tt.route.Spec.TLS.InsecureEdgeTerminationPolicy != routev1.InsecureEdgeTerminationPolicyNone {
					t.Errorf("Insecure edge termination policy: got %v, want %v", tt.route.Spec.TLS.InsecureEdgeTerminationPolicy, routev1.InsecureEdgeTerminationPolicyNone)
				}
			}
		})
	}
}

func TestAddHCPRouteLabel(t *testing.T) {
	tests := []struct {
		name           string
		route          *routev1.Route
		expectedLabels map[string]string
	}{
		{
			name: "When route has no labels, it should add HCP label",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
				},
			},
			expectedLabels: map[string]string{
				HCPRouteLabel: "test-namespace",
			},
		},
		{
			name: "When route has existing labels, it should add HCP label without removing existing ones",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"existing-label": "existing-value",
					},
				},
			},
			expectedLabels: map[string]string{
				"existing-label": "existing-value",
				HCPRouteLabel:    "test-namespace",
			},
		},
		{
			name: "When route already has HCP label, it should update it",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
					Labels: map[string]string{
						HCPRouteLabel: "old-namespace",
					},
				},
			},
			expectedLabels: map[string]string{
				HCPRouteLabel: "test-namespace",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			AddHCPRouteLabel(tt.route)

			if len(tt.route.Labels) != len(tt.expectedLabels) {
				t.Errorf("Labels length mismatch: got %d, want %d. Got: %v, Want: %v", len(tt.route.Labels), len(tt.expectedLabels), tt.route.Labels, tt.expectedLabels)
			}
			for k, v := range tt.expectedLabels {
				if got, ok := tt.route.Labels[k]; !ok || got != v {
					t.Errorf("Label %s: got %v, want %v", k, got, v)
				}
			}
		})
	}
}

func TestRemoveHCPRouteLabel(t *testing.T) {
	tests := []struct {
		name           string
		route          *routev1.Route
		expectedLabels map[string]string
	}{
		{
			name: "When route has no labels, it should not error",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
				},
			},
			expectedLabels: nil,
		},
		{
			name: "When route has HCP label only, it should remove it and leave empty map",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
					Labels: map[string]string{
						HCPRouteLabel: "test-namespace",
					},
				},
			},
			expectedLabels: map[string]string{},
		},
		{
			name: "When route has HCP label and other labels, it should remove only HCP label",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
					Labels: map[string]string{
						HCPRouteLabel:    "test-namespace",
						"existing-label": "existing-value",
						"another-label":  "another-value",
					},
				},
			},
			expectedLabels: map[string]string{
				"existing-label": "existing-value",
				"another-label":  "another-value",
			},
		},
		{
			name: "When route does not have HCP label but has other labels, it should not modify labels",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"existing-label": "existing-value",
					},
				},
			},
			expectedLabels: map[string]string{
				"existing-label": "existing-value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RemoveHCPRouteLabel(tt.route)

			if tt.expectedLabels == nil {
				if len(tt.route.Labels) != 0 {
					t.Errorf("Expected no labels, but got %v", tt.route.Labels)
				}
			} else {
				if len(tt.route.Labels) != len(tt.expectedLabels) {
					t.Errorf("Labels length mismatch: got %d, want %d. Got: %v, Want: %v", len(tt.route.Labels), len(tt.expectedLabels), tt.route.Labels, tt.expectedLabels)
				}
				for k, v := range tt.expectedLabels {
					if got, ok := tt.route.Labels[k]; !ok || got != v {
						t.Errorf("Label %s: got %v, want %v", k, got, v)
					}
				}
				// Ensure HCP label is not present
				if _, hasLabel := tt.route.Labels[HCPRouteLabel]; hasLabel {
					t.Errorf("Expected HCP label to be removed, but it still exists")
				}
			}
		})
	}
}
