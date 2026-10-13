package config

// KMSEncryptedObjects returns the resources declared in the KAS EncryptionConfiguration.
// TODO(https://github.com/openshift/enhancements/pull/1969#discussion_r3192690488):
// routes, oauthaccesstokens, and oauthauthorizetokens are listed here but are
// not actually encrypted today because the OpenShift API servers lack KMS
// sidecars.
func KMSEncryptedObjects() []string {
	return []string{
		"secrets",
		"configmaps",
		"routes.route.openshift.io",
		"oauthaccesstokens.oauth.openshift.io",
		"oauthauthorizetokens.oauth.openshift.io",
	}
}

// AESCBCEncryptedObjects returns the resources declared in the KAS EncryptionConfiguration
// for AESCBC encryption.
// TODO(https://github.com/openshift/enhancements/pull/1969#discussion_r3192690488):
// AESCBC currently only encrypts secrets; configmaps are not covered. This
// should be expanded to match the full set of sensitive resources once the
// encryption scope is broadened.
func AESCBCEncryptedObjects() []string {
	return []string{
		"secrets",
	}
}
