//go:build e2ev2

package lifecycle

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation"
)

const ExpectedNodePoolsEnv = "E2E_EXPECTED_NODE_POOLS"

func ValidateExpectedNodePools(pools map[string]int32) error {
	for name, replicas := range pools {
		if len(validation.IsDNS1123Subdomain(name)) != 0 || replicas < 0 {
			return fmt.Errorf("invalid configured NodePool %q replicas %d", name, replicas)
		}
	}
	return nil
}

func ParseExpectedNodePools(value string) (map[string]int32, error) {
	if value == "" {
		return nil, nil
	}
	var configured map[string]*int32
	if err := json.Unmarshal([]byte(value), &configured); err != nil {
		return nil, fmt.Errorf("decoding configured NodePools: %w", err)
	}
	if configured == nil {
		return nil, fmt.Errorf("configured NodePools must be a JSON object")
	}
	pools := make(map[string]int32, len(configured))
	for name, replicas := range configured {
		if replicas == nil {
			return nil, fmt.Errorf("configured NodePool %q must have numeric replicas", name)
		}
		pools[name] = *replicas
	}
	if err := ValidateExpectedNodePools(pools); err != nil {
		return nil, err
	}
	return pools, nil
}
