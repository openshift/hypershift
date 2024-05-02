package aws

import (
	"encoding/json"
	"fmt"
	"strings"
)

type policy struct {
	Statement []struct {
		Action []string
	}
}

// APIsByDelegatedServices uses the known policies and their bindings to cluster components
// in order to create a mapping of AWS services to delegates for each cluster component, recording the
// APIs that each component has access to with their limited credentials.
func APIsByDelegatedServices() (map[string]map[string][]string, error) {
	bindings := []policyBinding{
		ingressPermPolicy("fake", "fake"),
		imageRegistryPermPolicy,
		awsEBSCSIPermPolicy,
		cloudControllerPolicy,
		nodePoolPolicy,
		controlPlaneOperatorPolicy("fake"),
		kmsProviderPolicy("fake"),
		cloudNetworkConfigControllerPolicy,
		kmsProviderPolicy("fake"),
	}

	// delegate name -> service -> endpoints
	// e.g. control-plane-operator -> {ec2 -> [CreateVpcEndpoint, DescribeVpcEndpoints, ...], route53: [ListHostedZones]}
	delegates := map[string]map[string][]string{}
	for _, binding := range bindings {
		p := policy{}
		if err := json.Unmarshal([]byte(binding.policy), &p); err != nil {
			return nil, fmt.Errorf("error unmarshalling delegate policy for %q: %v", binding.name, err)
		}
		delegate := map[string][]string{}
		for i, statement := range p.Statement {
			for j, action := range statement.Action {
				parts := strings.Split(action, ":")
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid action in delegate policy %s.statement[%d].action[%d]: %q", binding.name, i, j, action)
				}
				if _, set := delegate[parts[0]]; !set {
					delegate[parts[0]] = []string{}
				}
				delegate[parts[0]] = append(delegate[parts[0]], parts[1])
			}
		}
		delegates[binding.name] = delegate
	}
	return delegates, nil
}
