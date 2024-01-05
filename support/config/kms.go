package config

func KMSEncryptedObjects() []string {
	return []string{
		"secrets",
		"configmaps",
		"routes.route.openshift.io",
		"oauthaccesstokens.oauth.openshift.io",
		"oauthauthorizetokens.oauth.openshift.io",
	}
}
