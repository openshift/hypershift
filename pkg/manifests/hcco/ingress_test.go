package hcco

import "testing"

func TestIngressDefaultIngressController(t *testing.T) {
	ic := IngressDefaultIngressController()
	if ic.Name != "default" {
		t.Errorf("expected name %q, got %q", "default", ic.Name)
	}
	if ic.Namespace != "openshift-ingress-operator" {
		t.Errorf("expected namespace %q, got %q", "openshift-ingress-operator", ic.Namespace)
	}
}

func TestIngressDefaultIngressPassthroughService(t *testing.T) {
	t.Run("When called, it should return service with namespace only and no name", func(t *testing.T) {
		svc := IngressDefaultIngressPassthroughService("test-ns")
		if svc.Name != "" {
			t.Errorf("expected empty name, got %q", svc.Name)
		}
		if svc.Namespace != "test-ns" {
			t.Errorf("expected namespace %q, got %q", "test-ns", svc.Namespace)
		}
	})
}

func TestIngressDefaultIngressPassthroughRoute(t *testing.T) {
	t.Run("When called, it should return route with namespace only and no name", func(t *testing.T) {
		route := IngressDefaultIngressPassthroughRoute("test-ns")
		if route.Name != "" {
			t.Errorf("expected empty name, got %q", route.Name)
		}
		if route.Namespace != "test-ns" {
			t.Errorf("expected namespace %q, got %q", "test-ns", route.Namespace)
		}
	})
}
