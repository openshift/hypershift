/*
Copyright 2021 The Kubernetes Authors.

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

// NetworkInterface holds the network interface information like subnet id.
type NetworkInterface struct {
	// Subnet ID of the network interface
	Subnet string `json:"subnet,omitempty"`
}

// Subnet describes a subnet
type Subnet struct {
	Ipv4CidrBlock *string `json:"cidr"`
	Name          *string `json:"name"`
	ID            *string `json:"id"`
	Zone          *string `json:"zone"`
}

// VPCEndpoint describes a VPCEndpoint
type VPCEndpoint struct {
	Address *string `json:"address"`
	FIPID   *string `json:"floatingIPID"`
}
