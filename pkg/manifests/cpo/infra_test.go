package cpo

import "testing"

func TestInfraManifests(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) interface {
			GetName() string
			GetNamespace() string
		}
		namespace         string
		expectedName      string
		expectedNamespace string
	}{
		{
			name: "When KubeAPIServerExternalPublicRoute is called, it should return route with correct name",
			fn: func(ns string) interface {
				GetName() string
				GetNamespace() string
			} {
				return KubeAPIServerExternalPublicRoute(ns)
			},
			namespace:         "test-ns",
			expectedName:      "kube-apiserver",
			expectedNamespace: "test-ns",
		},
		{
			name: "When KubeAPIServerExternalPrivateRoute is called, it should return route with correct name",
			fn: func(ns string) interface {
				GetName() string
				GetNamespace() string
			} {
				return KubeAPIServerExternalPrivateRoute(ns)
			},
			namespace:         "test-ns",
			expectedName:      "kube-apiserver-private",
			expectedNamespace: "test-ns",
		},
		{
			name: "When KubeAPIServerExternalPrivateService is called, it should return service with correct name",
			fn: func(ns string) interface {
				GetName() string
				GetNamespace() string
			} {
				return KubeAPIServerExternalPrivateService(ns)
			},
			namespace:         "test-ns",
			expectedName:      "kube-apiserver-private-external",
			expectedNamespace: "test-ns",
		},
		{
			name: "When OauthServerExternalPublicRoute is called, it should return route with correct name",
			fn: func(ns string) interface {
				GetName() string
				GetNamespace() string
			} {
				return OauthServerExternalPublicRoute(ns)
			},
			namespace:         "test-ns",
			expectedName:      "oauth",
			expectedNamespace: "test-ns",
		},
		{
			name: "When OauthServerExternalPrivateRoute is called, it should return route with correct name",
			fn: func(ns string) interface {
				GetName() string
				GetNamespace() string
			} {
				return OauthServerExternalPrivateRoute(ns)
			},
			namespace:         "test-ns",
			expectedName:      "oauth-private",
			expectedNamespace: "test-ns",
		},
		{
			name: "When OauthServerExternalPrivateService is called, it should return service with correct name",
			fn: func(ns string) interface {
				GetName() string
				GetNamespace() string
			} {
				return OauthServerExternalPrivateService(ns)
			},
			namespace:         "test-ns",
			expectedName:      "oauth-private-external",
			expectedNamespace: "test-ns",
		},
		{
			name: "When RouterPublicService is called, it should return service with correct name",
			fn: func(ns string) interface {
				GetName() string
				GetNamespace() string
			} {
				return RouterPublicService(ns)
			},
			namespace:         "test-ns",
			expectedName:      "router",
			expectedNamespace: "test-ns",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := tt.fn(tt.namespace)
			if obj.GetName() != tt.expectedName {
				t.Errorf("expected name %q, got %q", tt.expectedName, obj.GetName())
			}
			if obj.GetNamespace() != tt.expectedNamespace {
				t.Errorf("expected namespace %q, got %q", tt.expectedNamespace, obj.GetNamespace())
			}
		})
	}
}

func TestOauthServerService(t *testing.T) {
	svc := OauthServerService("test-ns")
	if svc.Name != "oauth-openshift" {
		t.Errorf("expected name %q, got %q", "oauth-openshift", svc.Name)
	}
	if svc.Namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", svc.Namespace)
	}
	if svc.Labels["app"] != "oauth-openshift" {
		t.Errorf("expected label app=%q, got %q", "oauth-openshift", svc.Labels["app"])
	}
}
