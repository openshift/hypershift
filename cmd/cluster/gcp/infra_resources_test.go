package gcp

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

// expectError keeps table assertions consistent and reports the calling test.
func expectError(t *testing.T, err error, expected string) {
	t.Helper()
	g := NewGomegaWithT(t)
	if expected == "" {
		g.Expect(err).ToNot(HaveOccurred())
	} else {
		g.Expect(err).To(MatchError(ContainSubstring(expected)))
	}
}

func TestInfraResourcesValidate(t *testing.T) {
	valid := infraResources{Version: 1, Router: "custom-router", NAT: "custom-nat", FirewallRule: "custom-firewall"}
	tests := []struct {
		name      string
		mutate    func(*infraResources)
		errorText string
	}{
		{name: "When exact resource names are valid, it should accept them"},
		{name: "When the version is unsupported, it should reject it", mutate: func(r *infraResources) { r.Version = 2 }, errorText: "unsupported"},
		{name: "When router is missing, it should reject it", mutate: func(r *infraResources) { r.Router = "" }, errorText: "router reference is required"},
		{name: "When NAT is missing, it should reject it", mutate: func(r *infraResources) { r.NAT = "" }, errorText: "NAT reference is required"},
		{name: "When firewall is missing, it should reject it", mutate: func(r *infraResources) { r.FirewallRule = "" }, errorText: "firewall rule reference is required"},
		{name: "When router contains a resource path, it should reject it", mutate: func(r *infraResources) { r.Router = "projects/other/routers/router" }, errorText: "invalid GCP router name"},
		{name: "When NAT contains uppercase letters, it should reject it", mutate: func(r *infraResources) { r.NAT = "Bad-NAT" }, errorText: "invalid GCP NAT name"},
		{name: "When firewall ends with a hyphen, it should reject it", mutate: func(r *infraResources) { r.FirewallRule = "firewall-" }, errorText: "invalid GCP firewall rule name"},
		{name: "When router exceeds the name limit, it should reject it", mutate: func(r *infraResources) { r.Router = strings.Repeat("a", 64) }, errorText: "invalid GCP router name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := valid
			if tt.mutate != nil {
				tt.mutate(&resources)
			}
			err := resources.validate()
			expectError(t, err, tt.errorText)
		})
	}
}

func TestParseInfraResources(t *testing.T) {
	valid := `{"version":1,"router":"custom-router","nat":"custom-nat","firewallRule":"custom-firewall"}`
	tests := []struct{ name, value, errorText string }{
		{name: "When version one metadata is complete, it should parse it", value: valid},
		{name: "When JSON is malformed, it should reject it", value: `{`, errorText: "invalid GCP infrastructure resources JSON"},
		{name: "When JSON is null, it should reject it", value: `null`, errorText: "unsupported"},
		{name: "When version is absent, it should reject it", value: `{"router":"router","nat":"nat","firewallRule":"firewall"}`, errorText: "unsupported"},
		{name: "When a name is mistyped, it should reject it", value: `{"version":1,"router":"router","nat":12,"firewallRule":"firewall"}`, errorText: "invalid GCP infrastructure resources JSON"},
		{name: "When metadata contains unknown fields, it should reject it", value: strings.TrimSuffix(valid, "}") + `,"project":"other"}`, errorText: "unknown field"},
		{name: "When metadata contains multiple objects, it should reject it", value: valid + valid, errorText: "single JSON object"},
		{name: "When metadata contains trailing garbage, it should reject it", value: valid + "garbage", errorText: "single JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			resources, err := parseInfraResources(tt.value)
			expectError(t, err, tt.errorText)
			if tt.errorText == "" {
				g.Expect(resources).To(Equal(infraResources{Version: 1, Router: "custom-router", NAT: "custom-nat", FirewallRule: "custom-firewall"}))
			}
		})
	}
}
