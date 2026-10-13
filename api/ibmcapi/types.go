// Types copied from https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/blob/ba09aa01b5f23d13917fc455b9e6aaa885809e23/api/v1beta2/ibmpowervsmachine_types.go

package ibmcapi

// PowerVSProcessorType enum attribute to identify the PowerVS instance processor type.
type PowerVSProcessorType string

const (
	// PowerVSProcessorTypeDedicated enum property to identify a Dedicated Power VS processor type.
	PowerVSProcessorTypeDedicated PowerVSProcessorType = "Dedicated"
	// PowerVSProcessorTypeShared enum property to identify a Shared Power VS processor type.
	PowerVSProcessorTypeShared PowerVSProcessorType = "Shared"
	// PowerVSProcessorTypeCapped enum property to identify a Capped Power VS processor type.
	PowerVSProcessorTypeCapped PowerVSProcessorType = "Capped"
)
