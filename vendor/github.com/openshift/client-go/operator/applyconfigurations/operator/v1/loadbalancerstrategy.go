// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
)

// LoadBalancerStrategyApplyConfiguration represents a declarative configuration of the LoadBalancerStrategy type for use
// with apply.
type LoadBalancerStrategyApplyConfiguration struct {
	Scope               *operatorv1.LoadBalancerScope                     `json:"scope,omitempty"`
	AllowedSourceRanges []operatorv1.CIDR                                 `json:"allowedSourceRanges,omitempty"`
	ProviderParameters  *ProviderLoadBalancerParametersApplyConfiguration `json:"providerParameters,omitempty"`
	DNSManagementPolicy *operatorv1.LoadBalancerDNSManagementPolicy       `json:"dnsManagementPolicy,omitempty"`
}

// LoadBalancerStrategyApplyConfiguration constructs a declarative configuration of the LoadBalancerStrategy type for use with
// apply.
func LoadBalancerStrategy() *LoadBalancerStrategyApplyConfiguration {
	return &LoadBalancerStrategyApplyConfiguration{}
}

// WithScope sets the Scope field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Scope field is set to the value of the last call.
func (b *LoadBalancerStrategyApplyConfiguration) WithScope(value operatorv1.LoadBalancerScope) *LoadBalancerStrategyApplyConfiguration {
	b.Scope = &value
	return b
}

// WithAllowedSourceRanges adds the given value to the AllowedSourceRanges field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the AllowedSourceRanges field.
func (b *LoadBalancerStrategyApplyConfiguration) WithAllowedSourceRanges(values ...operatorv1.CIDR) *LoadBalancerStrategyApplyConfiguration {
	for i := range values {
		b.AllowedSourceRanges = append(b.AllowedSourceRanges, values[i])
	}
	return b
}

// WithProviderParameters sets the ProviderParameters field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ProviderParameters field is set to the value of the last call.
func (b *LoadBalancerStrategyApplyConfiguration) WithProviderParameters(value *ProviderLoadBalancerParametersApplyConfiguration) *LoadBalancerStrategyApplyConfiguration {
	b.ProviderParameters = value
	return b
}

// WithDNSManagementPolicy sets the DNSManagementPolicy field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DNSManagementPolicy field is set to the value of the last call.
func (b *LoadBalancerStrategyApplyConfiguration) WithDNSManagementPolicy(value operatorv1.LoadBalancerDNSManagementPolicy) *LoadBalancerStrategyApplyConfiguration {
	b.DNSManagementPolicy = &value
	return b
}
