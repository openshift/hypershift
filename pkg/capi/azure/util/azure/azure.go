package azure

const (
	// ProviderIDPrefix will be appended to the beginning of Azure resource IDs to form the Kubernetes Provider ID.
	// NOTE: this format matches the 2 slashes format used in cloud-provider and cluster-autoscaler.
	ProviderIDPrefix = "azure://"
)
