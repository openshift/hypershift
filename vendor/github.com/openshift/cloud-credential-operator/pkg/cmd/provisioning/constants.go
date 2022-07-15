package provisioning

const (
	// PrivateKeyFile is the name of the private key file created by "ccoctl create key-pair" command
	PrivateKeyFile = "serviceaccount-signer.private"
	// PublicKeyFile is the name of the public key file created by "ccoctl create key-pair" command
	PublicKeyFile = "serviceaccount-signer.public"
	// DiscoveryDocumentURI is a URI for the OpenID configuration discovery document
	DiscoveryDocumentURI = ".well-known/openid-configuration"
	// KeysURI is a URI for public key that enables client to validate a JSON Web Token issued by the Identity Provider
	KeysURI = "keys.json"
	// ManifestsDirName is the name of the directory to save installer manifests created by ccoctl
	ManifestsDirName = "manifests"
	// TLSDirName is the name of the directory to save bound service account signing key created by ccoctl
	TLSDirName = "tls"
	// OidcTokenPath is the path where oidc token is stored in the pod
	OidcTokenPath = "/var/run/secrets/openshift/serviceaccount/token"
	// DiscoveryDocumentTemplate is a template of the discovery document that needs to be populated with appropriate values
	DiscoveryDocumentTemplate = `{
	"issuer": "%s",
	"jwks_uri": "%s/%s",
    "response_types_supported": [
        "id_token"
    ],
    "subject_types_supported": [
        "public"
    ],
    "id_token_signing_alg_values_supported": [
        "RS256"
    ],
    "claims_supported": [
        "aud",
        "exp",
        "sub",
        "iat",
        "iss",
        "sub"
    ]
}`
	// featureGateAnnotation is the annotation used to indicate that a specific manifest is hidden behind a feature gate.
	featureGateAnnotation = "release.openshift.io/feature-gate"

	// deletionAnnotation is the annotation used to tell the CVO that a resource should be deleted
	deletionAnnotation = "release.openshift.io/delete"
)
