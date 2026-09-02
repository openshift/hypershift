package azureutil

import "time"

const (
	// PollTimeout is the timeout for Azure long-running operations (PollUntilDone)
	// to prevent reconciler goroutines from blocking indefinitely.
	PollTimeout = 5 * time.Minute

	// DriftDetectionRequeueInterval is the interval for periodic reconciliation
	// to detect out-of-band changes to Azure resources.
	DriftDetectionRequeueInterval = 5 * time.Minute

	// PLSRequeueInterval is the interval used when requeuing because a prerequisite
	// (PLS alias, KAS hostname, Private Endpoint IP, or LoadBalancerIP) is not yet available.
	PLSRequeueInterval = 30 * time.Second

	// AzureResourceNameMaxLength is the maximum length for Azure resource names.
	AzureResourceNameMaxLength = 80

	// PEConnectionStateApproved indicates a Private Endpoint connection has been approved.
	PEConnectionStateApproved = "Approved"
	// PEConnectionStatePending indicates a Private Endpoint connection is awaiting approval.
	PEConnectionStatePending = "Pending"
	// PEConnectionStateRejected indicates a Private Endpoint connection has been rejected.
	PEConnectionStateRejected = "Rejected"
)

const (
	azurePublicKeyVaultDNSSuffix       = "vault.azure.net"
	azurePublicManagedHSMDNSSuffix     = "managedhsm.azure.net"
	azureGovernmentKeyVaultDNSSuffix   = "vault.usgovcloudapi.net"
	azureGovernmentManagedHSMDNSSuffix = "managedhsm.usgovcloudapi.net"
	azureChinaKeyVaultDNSSuffix        = "vault.azure.cn"
	azureChinaManagedHSMDNSSuffix      = "managedhsm.azure.cn"
	azureGermanKeyVaultDNSSuffix       = "vault.microsoftazure.de"
	azureGermanManagedHSMDNSSuffix     = "managedhsm.microsoftazure.de"
	azureBleuKeyVaultDNSSuffix         = "vault.sovcloud-api.fr"
	azureBleuManagedHSMDNSSuffix       = "managedhsm.sovcloud-api.fr"
)
