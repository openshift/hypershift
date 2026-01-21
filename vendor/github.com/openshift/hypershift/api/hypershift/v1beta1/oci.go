package v1beta1

// OCIPlatformSpec specifies configuration for clusters running on Oracle Cloud Infrastructure.
type OCIPlatformSpec struct {
	// identityRef is a reference to a secret holding OCI credentials
	// to be used when reconciling the hosted cluster.
	// The secret must contain two keys:
	//   - "config": OCI configuration file content (typically ~/.oci/config format)
	//   - "key": OCI API signing key (PEM-encoded private key)
	//
	// +required
	IdentityRef OCIIdentityReference `json:"identityRef"`

	// region is the OCI region in which the cluster resides.
	// A valid region must satisfy the following rules:
	//   format: Must be in the form `<countryCode>-<location>-<number>`
	//   characters: Only lowercase letters (a-z), digits (0-9), and hyphens (-) are allowed
	//   valid examples: "us-ashburn-1", "us-phoenix-1", "eu-frankfurt-1", "ap-tokyo-1"
	// For a full list of valid regions, see: https://docs.oracle.com/en-us/iaas/Content/General/Concepts/regions.htm.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^[a-z]+-[a-z]+-[0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Region is immutable"
	Region string `json:"region"`

	// compartmentID is the OCI compartment OCID where the cluster resides.
	// A valid compartment OCID must satisfy the following rules:
	//   format: Must be in the form `ocid1.compartment.oc1..<unique_ID>`
	//   characters: Only lowercase letters (a-z), digits (0-9), and periods (.) are allowed
	//   start: Must begin with `ocid1.compartment.oc1..`
	//   valid examples: "ocid1.compartment.oc1..aaaaaaaazgovbe2qxduadk3bmj5dobvoe5wnengzavax5pwsfr3bqbdrrcqa".
	// For more information about compartment OCIDs, see: https://docs.oracle.com/en-us/iaas/Content/General/Concepts/identifiers.htm.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.compartment\.oc1\.\.[a-z0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="CompartmentID is immutable"
	CompartmentID string `json:"compartmentId"`
}

// OCIIdentityReference is a reference to a secret containing OCI credentials.
type OCIIdentityReference struct {
	// name is the name of a secret in the same namespace as the HostedCluster.
	// The secret must contain the following keys:
	//   - "config": OCI configuration file content
	//   - "key": OCI API signing key (PEM format)
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}
