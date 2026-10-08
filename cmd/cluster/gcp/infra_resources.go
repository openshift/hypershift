package gcp

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Infrastructure references are CLI metadata, not configuration consumed by the operator.
// ObjectMeta annotations retain these references even in clients using older API types.
const infraResourcesAnnotation = "hypershift.openshift.io/gcp-infra-resources"

// infraResources records the additional exact names needed for infrastructure
// cleanup. Project, region, network, subnet, and IAM references remain in the spec.
type infraResources struct {
	Version      int    `json:"version"`
	Router       string `json:"router"`
	NAT          string `json:"nat"`
	FirewallRule string `json:"firewallRule"`
}

var infraResourceNamePattern = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

func (r infraResources) validate() error {
	if r.Version != 1 {
		return fmt.Errorf("unsupported GCP infrastructure resources version %d (expected 1)", r.Version)
	}
	for _, resource := range []struct{ name, value string }{
		{"router", r.Router}, {"NAT", r.NAT}, {"firewall rule", r.FirewallRule},
	} {
		if resource.value == "" {
			return fmt.Errorf("GCP %s reference is required in infrastructure resources", resource.name)
		}
		if len(resource.value) > 63 || !infraResourceNamePattern.MatchString(resource.value) {
			return fmt.Errorf("invalid GCP %s name %q: must be 1-63 lowercase letters, digits, or hyphens, start with a letter, and end with a letter or digit", resource.name, resource.value)
		}
	}
	return nil
}

func parseInfraResources(value string) (infraResources, error) {
	var resources infraResources
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resources); err != nil {
		return resources, fmt.Errorf("invalid GCP infrastructure resources JSON: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return resources, fmt.Errorf("GCP infrastructure resources must contain a single JSON object")
	}
	if err := resources.validate(); err != nil {
		return resources, err
	}
	return resources, nil
}
