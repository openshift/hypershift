//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestValidateIngressOperatorConfiguration(t *testing.T) {
	loadBalancer := &operatorv1.EndpointPublishingStrategy{
		Type: operatorv1.LoadBalancerServiceStrategyType,
		LoadBalancer: &operatorv1.LoadBalancerStrategy{
			Scope: operatorv1.ExternalLoadBalancer,
		},
	}

	tests := []struct {
		name      string
		hosted    *hyperv1.HostedCluster
		wantError string
	}{
		{
			name:      "When operator configuration is missing, it should return an error",
			hosted:    &hyperv1.HostedCluster{},
			wantError: "OperatorConfiguration should be set",
		},
		{
			name: "When ingress operator configuration is missing, it should return an error",
			hosted: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				OperatorConfiguration: &hyperv1.OperatorConfiguration{},
			}},
			wantError: "IngressOperator configuration should be set",
		},
		{
			name: "When endpoint publishing strategy is missing, it should return an error",
			hosted: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				OperatorConfiguration: &hyperv1.OperatorConfiguration{
					IngressOperator: &hyperv1.IngressOperatorSpec{},
				},
			}},
			wantError: "EndpointPublishingStrategy should be set",
		},
		{
			name: "When endpoint publishing strategy is not LoadBalancerService, it should return an error",
			hosted: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				OperatorConfiguration: &hyperv1.OperatorConfiguration{
					IngressOperator: &hyperv1.IngressOperatorSpec{EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.HostNetworkStrategyType,
					}},
				},
			}},
			wantError: "EndpointPublishingStrategy should be LoadBalancerService",
		},
		{
			name: "When LoadBalancer configuration is missing, it should return an error",
			hosted: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				OperatorConfiguration: &hyperv1.OperatorConfiguration{
					IngressOperator: &hyperv1.IngressOperatorSpec{EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
						Type: operatorv1.LoadBalancerServiceStrategyType,
					}},
				},
			}},
			wantError: "LoadBalancer configuration should be set",
		},
		{
			name: "When LoadBalancer scope is external, it should return an error",
			hosted: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				OperatorConfiguration: &hyperv1.OperatorConfiguration{
					IngressOperator: &hyperv1.IngressOperatorSpec{EndpointPublishingStrategy: loadBalancer},
				},
			}},
			wantError: "LoadBalancer scope should be Internal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIngressOperatorConfiguration(t.Context(), nil, tt.hosted)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("ValidateIngressOperatorConfiguration() error = %v, want error containing %q", err, tt.wantError)
			}
		})
	}
}
