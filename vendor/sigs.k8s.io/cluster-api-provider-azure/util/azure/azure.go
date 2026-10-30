/*
Copyright 2022 The Kubernetes Authors.

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

package azure

import (
	"strings"
)

// AzureSystemNodeLabelPrefix is a standard node label prefix for Azure features, e.g., kubernetes.azure.com/scalesetpriority.
const AzureSystemNodeLabelPrefix = "kubernetes.azure.com"

const (
	// ProviderIDPrefix will be appended to the beginning of Azure resource IDs to form the Kubernetes Provider ID.
	// NOTE: this format matches the 2 slashes format used in cloud-provider and cluster-autoscaler.
	ProviderIDPrefix = "azure://"
)

// IsAzureSystemNodeLabelKey is a helper function that determines whether a node label key is an Azure "system" label.
func IsAzureSystemNodeLabelKey(labelKey string) bool {
	return strings.HasPrefix(labelKey, AzureSystemNodeLabelPrefix)
}
