package hcco

import (
	oauthv1 "github.com/openshift/api/oauth/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func OAuthServerChallengingClient() *oauthv1.OAuthClient {
	return &oauthv1.OAuthClient{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-challenging-client",
		},
	}
}
