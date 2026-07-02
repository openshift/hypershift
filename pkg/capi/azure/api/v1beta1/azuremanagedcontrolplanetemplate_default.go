/*
Copyright 2023 The Kubernetes Authors.

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

package v1beta1

// Being able to set the vnet name in the template type is a bug, as vnet names cannot be reused across clusters.
// To avoid a breaking API change, a warning is logged.

// setDefaultSubnet sets the default Subnet for an AzureManagedControlPlaneTemplate.
func (mcp *AzureManagedControlPlaneTemplate) setDefaultSubnet() {
	if mcp.Spec.Template.Spec.VirtualNetwork.Subnet.Name == "" {
		mcp.Spec.Template.Spec.VirtualNetwork.Subnet.Name = mcp.Name
	}
	if mcp.Spec.Template.Spec.VirtualNetwork.Subnet.CIDRBlock == "" {
		mcp.Spec.Template.Spec.VirtualNetwork.Subnet.CIDRBlock = defaultAKSNodeSubnetCIDR
	}
}

// setDefault sets the default value for a pointer to a value for any comparable type.
func setDefault[T comparable](field *T, value T) {
	if field == nil {
		// shouldn't happen with proper use
		return
	}
	var zero T
	if *field == zero {
		*field = value
	}
}
