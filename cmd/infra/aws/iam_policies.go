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

type EndpointsByService map[string][]string
type ServicesByDelegate map[string]EndpointsByService

// APIsByDelegatedServices uses the known policies and their bindings to cluster components
// in order to create a mapping of AWS services to delegates for each cluster component, recording the
// APIs that each component has access to with their limited credentials.
func APIsByDelegatedServices() (ServicesByDelegate, error) {
	bindings := []policyBinding{
		ingressPermPolicy("fake", "fake", false),
		imageRegistryPermPolicy,
		awsEBSCSIPermPolicy,
		cloudControllerPolicy,
		nodePoolPolicy,
		controlPlaneOperatorPolicy("fake", false),
		kmsProviderPolicy("fake"),
		cloudNetworkConfigControllerPolicy,
	}

	// delegate name -> service -> endpoints
	// e.g. control-plane-operator -> {ec2 -> [CreateVpcEndpoint, DescribeVpcEndpoints, ...], route53: [ListHostedZones]}
	delegates := ServicesByDelegate{}
	for _, binding := range bindings {
		p := policy{}
		if err := json.Unmarshal([]byte(binding.policy), &p); err != nil {
			return nil, fmt.Errorf("error unmarshalling delegate policy for %q: %v", binding.name, err)
		}
		delegate := EndpointsByService{}
		for i, statement := range p.Statement {
			for j, action := range statement.Action {
				parts := strings.Split(action, ":")
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid action in delegate policy %s.statement[%d].action[%d]: %q", binding.name, i, j, action)
				}
				service, operation := parts[0], parts[1]
				if _, set := delegate[service]; !set {
					delegate[service] = []string{}
				}
				delegate[service] = append(delegate[service], operation)
			}
		}
		delegates[binding.name] = delegate
	}
	return delegates, nil
}
