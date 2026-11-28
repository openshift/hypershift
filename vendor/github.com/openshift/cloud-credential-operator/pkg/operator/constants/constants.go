package constants

import "k8s.io/apimachinery/pkg/types"

// CredentialsMode enumerates the possible modes of operation for CCO
type CredentialsMode string

const (
	// metrics vars

	// ModeMint indicates that CCO is running in a mode where it will attempt
	// to create new users/credentials when processing CredentialsRequest objects.
	ModeMint CredentialsMode = "mint"

	// ModePassthrough indicates that CCO is running in a mode where it will
	// process CredentialsRequests by passing through the shared cloud credentials.
	ModePassthrough CredentialsMode = "passthrough"

	// ModeCredsRemoved indicates that the credentials CCO uses to reconcile
	// CredentialsRequest objects has been removed, and CCO can only validate
	// that already-reconciled credentials are still in a healthy state.
	ModeCredsRemoved CredentialsMode = "credsremoved"

	// ModeManual indicates that a user has disabled CCO from reconciling
	// CredentialsRequest objects, and is processing CredentialsRequest objects
	// via some alternative means.
	ModeManual CredentialsMode = "manual"

	// ModeDegraded indicates that the cloud credentials exists, but the cannot be used
	// (usually due to insuffient permissions)
	ModeDegraded CredentialsMode = "degraded"

	// ModeUnknown is used to indicate when we are unable to determine the mode CCO is
	// running under (typically just haven't added support for the cloud/platform)
	ModeUnknown CredentialsMode = "unknown"

	// ModeManualPodIdentity is used to indicate that CCO has found at least one CredentialsRequest
	// secret with content indicating Pod-level identity/credentials in use (eg AWS STS with WebIdentity).
	ModeManualPodIdentity CredentialsMode = "manualpodidentity"

	// StatusModeMismatch is used to set a clusteroperator condition when
	// the legacy configmap setting of disabled: "true" conflicts with the
	// specified operator config mode.
	StatusModeMismatch = "ModeMismatch"

	// StatusModeInvalid is used to set a clusteroperator condition when
	// the operator config CR specifies an invalide mode
	StatusModeInvalid = "ModeInvalid"

	// MissingUpgradeableAnnotationReason is used when the cluster is not upgradeable due to
	// the CCO's config object missing the appropriate annotation.
	MissingUpgradeableAnnotationReason = "MissingUpgradeableAnnotation"

	// ErrorDeterminingUpgradeableReason is used when we encounter unexpected errors checking if a cluster can
	// be updated.
	ErrorDeterminingUpgradeableReason = "ErrorDeterminingUpgradeable"

	// MissingRootCredentialUpgradeableReason is used a cluster is in mint mode with the root credential removed.
	// In this state the root credential must be resotred before we can upgrade to the next minor release of OpenShift.
	MissingRootCredentialUpgradeableReason = "MissingRootCredential"

	// secret annoation vars

	// AnnotationKey is the annotation the cloud credentials secret will be annotated with to indicate
	// what mode the secrets can be used for.
	AnnotationKey = "cloudcredential.openshift.io/mode"

	// MintAnnotation is used whenever it is determined that the cloud creds
	// are sufficient for minting new creds to satisfy a CredentialsRequest
	MintAnnotation = "mint"

	// PassthroughAnnotation is used whenever it is determined that the cloud creds
	// are sufficient for passing through to satisfy a CredentialsRequest.
	// This would be based on having creds that can satisfy the static list of creds
	// found in this repo's manifests/ dir.
	PassthroughAnnotation = "passthrough"

	// InsufficientAnnotation is used to indicate that the creds do not have
	// sufficient permissions for cluster runtime.
	InsufficientAnnotation = "insufficient"

	// SecretAnnotatorControllerName is the name the various secret annotation accuators
	// will use for logging purposes.
	SecretAnnotatorControllerName = "secretannotator"

	// cloud credential secret info

	// AWSCloudCredSecretName is the name of the secret created by the installer containing cloud creds.
	AWSCloudCredSecretName = "aws-creds"

	// AWSSecretDataCredentialsKey is the name of the key used to store the AWS config data in the Secret
	// specified in the CredentialsRequest.Spec.SecretRef
	AWSSecretDataCredentialsKey = "credentials"

	// AzureCloudCredSecretName is the name of the secret created by the install containing cloud creds.
	AzureCloudCredSecretName = "azure-credentials"

	// CloudCredOperatorConfigMap is an optional ConfigMap that can be used to alter behavior of the operator.
	CloudCredOperatorConfigMap = "cloud-credential-operator-config"

	// CloudCredOperatorConfig is the name of the credentialsrequest.operator.openshift.io CR holding CCO's config
	CloudCredOperatorConfig = "cluster"

	// CloudCredOperatorConfigTimestampAnnotation is the annotation controllers can update to trigger a status sync.
	CloudCredOperatorConfigTimestampAnnotation = "cloudcredential.operator.openshift.io/statussync"

	// CloudCredClusterOperatorName is the name of the CCO's ClusterOperator object
	CloudCredClusterOperatorName = "cloud-credential"

	// CloudCredSecretNamespace is where the cloud credentials can be found
	CloudCredSecretNamespace = "kube-system"

	// GCPCloudCredSecretName is the name of the secret created by the installer containing cloud creds.
	GCPCloudCredSecretName = "gcp-credentials"

	// OpenStackCloudCredsSecretName is the name of the secret created by the installer containing cloud creds.
	OpenStackCloudCredsSecretName = "openstack-credentials"

	// OvirtCloudCredsSecretName is then ame of the secret created by the installer containing cloud creds.
	OvirtCloudCredsSecretName = "ovirt-credentials"

	// VSphereCloudCredSecretName is the name of the secret where credentials
	// for vSphere are stored.
	VSphereCloudCredSecretName = "vsphere-creds"

	// KubevirtCloudCredSecretName is the name of the secret where credentials
	// for Kubevirt are stored.
	KubevirtCloudCredSecretName = "kubevirt-credentials"

	// UpgradeableAnnotation is the annotation CCO will check for on the cloudcredential.operator.openshift.io
	// CR when determining upgradeability.
	UpgradeableAnnotation = "cloudcredential.openshift.io/upgradeable-to"

	// CCONameSpace Namespace defined for CCO to use
	CCONameSpace = "openshift-cloud-credential-operator"

	// DeploymentName name of the CCO deployment
	DeploymentName = "cloud-credential-operator"
)

var (
	// CredentialsModeList is a list of all the types of modes CCO can be operating under.
	CredentialsModeList = []CredentialsMode{
		ModeMint,
		ModePassthrough,
		ModeCredsRemoved,
		ModeManual,
		ModeDegraded,
		ModeUnknown,
		ModeManualPodIdentity,
	}

	// Add known stale credentials requests here

	// StaleCredentialsRequests contains the list of known stale credentials requests for the next version of OpenShift
	StaleCredentialsRequests = []types.NamespacedName{
		{
			Name:      "cloud-credential-operator-s3",
			Namespace: "openshift-cloud-credential-operator",
		},
	}
)
