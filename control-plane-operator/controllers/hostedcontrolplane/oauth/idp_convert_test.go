package oauth

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	osinv1 "github.com/openshift/api/osin/v1"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestOpenIDProviderConversion(t *testing.T) {
	// Define common inputs
	groupsInput := []configv1.OpenIDClaim{"groups"}
	volumeMountInfo := &IDPVolumeMountInfo{
		Container: oauthContainerMain().Name,
		VolumeMounts: util.PodVolumeMounts{
			oauthContainerMain().Name: util.ContainerVolumeMounts{},
		},
	}
	const namespace = "test"
	const secretName = "secret1"
	idpSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Immutable: nil,
		Data: map[string][]byte{
			"clientSecret": []byte("secret"),
		},
	}

	testCases := []struct {
		name   string
		idp    *configv1.IdentityProviderConfig
		outErr error
		outIDP *idpData
	}{
		{
			name: "empty input",
			idp: &configv1.IdentityProviderConfig{
				Type: configv1.IdentityProviderTypeOpenID,
			},
			outErr: fmt.Errorf("type OpenID was specified, but its configuration is missing"),
			outIDP: nil,
		},
		{
			name: "empty issuer",
			idp: &configv1.IdentityProviderConfig{
				Type: configv1.IdentityProviderTypeOpenID,
				OpenID: &configv1.OpenIDIdentityProvider{
					ClientSecret: configv1.SecretNameReference{Name: secretName},
					Claims: configv1.OpenIDClaims{
						PreferredUsername: nil,
						Name:              nil,
						Email:             nil,
						Groups:            groupsInput,
					},
				},
			},
			outErr: fmt.Errorf("unsupported protocol scheme \"\""),
			outIDP: nil,
		},
		{
			name: "name and no groups in input",
			idp: &configv1.IdentityProviderConfig{
				Type: configv1.IdentityProviderTypeOpenID,
				OpenID: &configv1.OpenIDIdentityProvider{
					ClientSecret: configv1.SecretNameReference{Name: secretName},
					Issuer:       "https://accounts.google.com",
					Claims: configv1.OpenIDClaims{
						PreferredUsername: nil,
						Name:              []string{"email"},
						Email:             nil,
						Groups:            nil,
					},
				},
			},
			outErr: nil,
			outIDP: &idpData{
				provider: &osinv1.OpenIDIdentityProvider{
					TypeMeta: metav1.TypeMeta{
						Kind:       "OpenIDIdentityProvider",
						APIVersion: "osin.config.openshift.io/v1",
					},
					CA:       "",
					ClientID: "",
					ClientSecret: configv1.StringSource{
						StringSourceSpec: configv1.StringSourceSpec{
							Value:   "",
							Env:     "",
							File:    "/etc/oauth/idp/idp_secret_0_client-secret/clientSecret",
							KeyFile: "",
						},
					},
					ExtraScopes:              nil,
					ExtraAuthorizeParameters: nil,
					URLs: osinv1.OpenIDURLs{
						Authorize: "https://accounts.google.com/o/oauth2/v2/auth",
						Token:     "https://oauth2.googleapis.com/token",
						UserInfo:  "https://openidconnect.googleapis.com/v1/userinfo",
					},
					Claims: osinv1.OpenIDClaims{
						ID:                []string{"sub"},
						PreferredUsername: nil,
						Name:              []string{"email"},
						Email:             nil,
						Groups:            nil,
					},
				},
				challenge: false,
				login:     true,
			},
		},
		{
			name: "preferred username and groups in input",
			idp: &configv1.IdentityProviderConfig{
				Type: configv1.IdentityProviderTypeOpenID,
				OpenID: &configv1.OpenIDIdentityProvider{
					ClientSecret: configv1.SecretNameReference{Name: secretName},
					Issuer:       "https://accounts.google.com",
					Claims: configv1.OpenIDClaims{
						PreferredUsername: []string{"preferred_username"},
						Name:              nil,
						Email:             nil,
						Groups:            groupsInput,
					},
				},
			},
			outErr: nil,
			outIDP: &idpData{
				provider: &osinv1.OpenIDIdentityProvider{
					TypeMeta: metav1.TypeMeta{
						Kind:       "OpenIDIdentityProvider",
						APIVersion: "osin.config.openshift.io/v1",
					},
					CA:       "",
					ClientID: "",
					ClientSecret: configv1.StringSource{
						StringSourceSpec: configv1.StringSourceSpec{
							Value:   "",
							Env:     "",
							File:    "/etc/oauth/idp/idp_secret_0_client-secret/clientSecret",
							KeyFile: "",
						},
					},
					ExtraScopes:              nil,
					ExtraAuthorizeParameters: nil,
					URLs: osinv1.OpenIDURLs{
						Authorize: "https://accounts.google.com/o/oauth2/v2/auth",
						Token:     "https://oauth2.googleapis.com/token",
						UserInfo:  "https://openidconnect.googleapis.com/v1/userinfo",
					},
					Claims: osinv1.OpenIDClaims{
						ID:                []string{"sub"},
						PreferredUsername: []string{"preferred_username"},
						Name:              nil,
						Email:             nil,
						Groups:            []string{"groups"},
					},
				},
				challenge: false,
				login:     true,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithObjects(idpSecret).Build()
			outIDP, err := convertProviderConfigToIDPData(context.TODO(),
				tc.idp, nil, 0, volumeMountInfo, client, namespace, true)
			g := NewWithT(t)
			if tc.outErr != nil {
				g.Expect(err).To(Equal(tc.outErr))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(outIDP).Should(Equal(tc.outIDP))
			}
		})
	}
}
