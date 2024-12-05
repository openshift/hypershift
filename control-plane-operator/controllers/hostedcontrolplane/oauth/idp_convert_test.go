package oauth

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	osinv1 "github.com/openshift/api/osin/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
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

func TestTransportForCARef(t *testing.T) {
	namespace := "test"

	testCases := []struct {
		name                    string
		hcp                     *hyperv1.HostedControlPlane
		requestToURL            string
		expectedProxyRequestURL string
	}{
		{
			name: "When no proxy configuration is provided, the transport should not be modified",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp-test",
					Namespace: namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			requestToURL:            "https://test.com",
			expectedProxyRequestURL: "",
		},
		{
			name: "When proxy configuration is provided, the transport should use proxy",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp-test",
					Namespace: namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							HTTPProxy:          "",
							HTTPSProxy:         "https://10.0.0.1",
							NoProxy:            "",
							ReadinessEndpoints: []string{},
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "proxyTrustedCA",
							},
						},
					},
				},
			},
			requestToURL:            "https://test.com",
			expectedProxyRequestURL: "https://10.0.0.1",
		},
		{
			name: "When proxy configuration is provided and request is to ignored url, the transport should not use proxy",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp-test",
					Namespace: namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							HTTPProxy:          "",
							HTTPSProxy:         "https://10.0.0.1",
							NoProxy:            "workload.svc",
							ReadinessEndpoints: []string{},
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "proxyTrustedCA",
							},
						},
					},
				},
			},
			requestToURL:            "workload.svc",
			expectedProxyRequestURL: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Generic fake base64 encoded certificate data.
			fakeCertCAData := []byte("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURQRENDQWlTZ0F3SUJBZ0lJTUdGUkF2TUlYL013RFFZSktvWklodmNOQVFFTEJRQXdKakVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1SQXdEZ1lEVlFRREV3ZHliMjkwTFdOaE1CNFhEVEkwTURReE5qRTJNemcwTmxvWApEVE0wTURReE5ERTJNemcwTmxvd0pqRVNNQkFHQTFVRUN4TUpiM0JsYm5Ob2FXWjBNUkF3RGdZRFZRUURFd2R5CmIyOTBMV05oTUlJQklqQU5CZ2txaGtpRzl3MEJBUUVGQUFPQ0FROEFNSUlCQ2dLQ0FRRUF5K01xcmxQbDZpL1kKeXdHaU1lOUZETDRsZFdDMk1TSkRPbGZaci9pbStoeVQzcTBHYnRaZmltR1dWMEtLWm1JMHpveDhodzZKZnR0dAp4bjZLY0N2aEN0ZnBQUWpZa0V2a0NjS2V6dmJYdGt3bjkrTjhlNzR6ejkzYWlWWDIvK3FYOWVBeUdvdU1OYWxFCmk2UDdieUowa3Q5M20vbEYrNWNlQ1NJTS9qTER0VTVEOHJHSUtMbmVTNFZGRHNYckgvL0VDa1R5c3NYUUF5WGcKd3ZwOVBKZlJyK2ZtZk11N2xkOE52TTBucExaQldkUjNrV2QvVzFGZVlSV3JqbmtKQ1ZUM0I4WGZzK2p6M1pCTgpnWU9pdHR3dytLZmVGNWlnRVQ1RGVrMTdncUJVcTZrY3dzQm1VeTYzS0JVa0pMSnB6SExGSVlVVjgyMk9KeFdLCkc2N0EyZUpsNHdJREFRQUJvMjR3YkRBT0JnTlZIUThCQWY4RUJBTUNBcVF3RHdZRFZSMFRBUUgvQkFVd0F3RUIKL3pCSkJnTlZIUTRFUWdSQTBCeElOMVh2MTZiNWdVdXM0anA2Y2Q2NWorcnFkNXluTHlRVEdWNlVQYUxpV1k3RQpsWHVXTTQvSUsvTnRKSzBPdmJObmJhREFyNHR1ZXFSUW1DZ2w1akFOQmdrcWhraUc5dzBCQVFzRkFBT0NBUUVBClVUemp4TldsQ21FaWZ1UmptN3F5K1oxcVRyeVU2V0lmblhlMm1xd1cvWmtva3ZsM0lmcE56czNzWUY4RGNnR3gKcnNZL3BiaFJIN2RtLzdDYkNBUFozSEZBc1dGWmswZEIwd1I2dGVhWXdtbDQvSmZSU0JzZ3JwQ2JmQUJ0MWNVcQpKR1ZhQ3AvQ2ZOcUp0SW9QNitBUldpbnRLQ2xid0JVSS9yUmhvWnVHSEVQZURlc3NTaFpwZmUxK2FDRmFYYVQrCkgzeUk1Qzl2OW5hRDhVdWkxU1J3Vm43SlQ4SVJuSHhtdHY3eUlZL05SL1NWbjBPTkxGbHN3VFREa2o2RVR6TTAKTG8zMGQ4ZmwwSjJ3YmtEekxDc3ozU0lRRjNrL2huR2tIdW5UWUhwWWF0dUZWenZyOVNlSGkrS1lkNllCb25JNApKSWFtZEZsTmZtM3dpS3FtWWZ3SEVnPT0KLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQotLS0tLUJFR0lOIENFUlRJRklDQVRFLS0tLS0KTUlJRCt6Q0NBdU9nQXdJQkFnSUlGa0hGcFZlVG1XUXdEUVlKS29aSWh2Y05BUUVMQlFBd0pqRVNNQkFHQTFVRQpDeE1KYjNCbGJuTm9hV1owTVJBd0RnWURWUVFERXdkeWIyOTBMV05oTUI0WERUSTBNRFF4TmpFMk16ZzBObG9YCkRUSTFNRFF4TmpFMk16ZzFNMW93TURFU01CQUdBMVVFQ2hNSmIzQmxibk5vYVdaME1Sb3dHQVlEVlFRREV4RnYKY0dWdWMyaHBablF0YVc1bmNtVnpjekNDQVNJd0RRWUpLb1pJaHZjTkFRRUJCUUFEZ2dFUEFEQ0NBUW9DZ2dFQgpBTFhQYktuTkhRU3pvWDJPWHVETjJWMmRBKzdabzRPTjNERFduZVNWeHZlUXZLRFNIZXVMQUdwb3dheHFPbEd0CnpxRVJWelQzaFQ2NThjd0p5d3VwczFteXdJQ290dE5mZzdadk9NQ0pmZUF2MDBuNmFXWW1JdlhCMjhEWVNRaEkKbnpqb3kyWTNwZkVha2c5VWo0VDl6SkFmaGd4RktqRzZMZ2NBSlgrTk5Zd0tScWxlN1g4SkV6WkhCVmpLOGJILwpWMEdoUDFGS3l5V1JGQ2FkWFVTTTM1NEFIUDJqME0wRENEbXR1bytHR1FGWmlDdnVnQlB6b2ZsUjF5MEpHRlk0ClFiaDBzYVZrRmFEVDd3OEd6Rzk5MHBldXhRZ2xXblF4bUw2ZUwveXlZTmk1TTdONkFZYmZaQWJxTWtmZ1NjeS8KWFpwOCtJRTVMLzVNK2g1aWxDUnBGOVVDQXdFQUFhT0NBU0V3Z2dFZE1BNEdBMVVkRHdFQi93UUVBd0lGb0RBZApCZ05WSFNVRUZqQVVCZ2dyQmdFRkJRY0RBZ1lJS3dZQkJRVUhBd0V3REFZRFZSMFRBUUgvQkFJd0FEQkpCZ05WCkhRNEVRZ1JBbUlrWTFKR2Z2c25jbkdKOVQvZkl6WmRSeXhObUNmWHJpR2wwdjVuVnlmSTkyM1hrRTNLaHd6NXYKczFrOTBYbkZDM2xmRitETUNocFIySk5Nb2R0c3F6QkxCZ05WSFNNRVJEQkNnRURRSEVnM1ZlL1hwdm1CUzZ6aQpPbnB4M3JtUDZ1cDNuS2N2SkJNWlhwUTlvdUpaanNTVmU1WXpqOGdyODIwa3JRNjlzMmR0b01DdmkyNTZwRkNZCktDWG1NRVlHQTFVZEVRUS9NRDJDT3lvdVlYQndjeTV6YTNWNmJtVjBjeTF0WjIxMExtTnBMbWg1Y0dWeWMyaHAKWm5RdVpHVjJZMngxYzNSbGNpNXZjR1Z1YzJocFpuUXVZMjl0TUEwR0NTcUdTSWIzRFFFQkN3VUFBNElCQVFCQQpnU1pMdFJZZGJoNG1JVXgxYWxyVktlR2VRN2lMeWlwL1pBd1kxc2hYTk05ZWEwZ1NLcStGQ1RHS1hmcmZlbVdrCmZRR25LNys0aTIzOUZtN0pmaE1pcU5TZ2R5bVR2djhDYlcxNjFNOVcrTkZoOEV1N2h1V2dMdzBEZHgwMys5ZTAKajFsa0dMODcvcWM2cmM0WmVYRWM5dVV3cWdrK3dSWktnbDMzblNxem42TlNuQ1BTM2hXSEFRVkRsd1NlalVoYQpJcUtxb0kzWkhsY3hybnBNWDM4Y01JYTdOL2svc1hVNVZndkxzYXN6ZjVpUWZOWlk1ZkliT0t3YjVqY1hwRWhYCldoQU84dFkyaWJBQ3BWWHlYYlI1K0VCajF4UDM3SHMvVHNaV3lsWGFJYWwyZ083QWRqVGlwenVwSTBkYmFUakMKRHJnQmFJbjZWWkExQU4zSzlVdmQKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=")
			fakeClientCertData := []byte("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUR0ekNDQXArZ0F3SUJBZ0lJZGRxdkFLYUpBNVl3RFFZSktvWklodmNOQVFFTEJRQXdOakVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1TQXdIZ1lEVlFRREV4ZGhaRzFwYmkxcmRXSmxZMjl1Wm1sbkxYTnBaMjVsY2pBZQpGdzB5TkRBME1UWXhOak00TkRsYUZ3MHlOVEEwTVRZeE5qTTROVEJhTURBeEZ6QVZCZ05WQkFvVERuTjVjM1JsCmJUcHRZWE4wWlhKek1SVXdFd1lEVlFRREV3eHplWE4wWlcwNllXUnRhVzR3Z2dFaU1BMEdDU3FHU0liM0RRRUIKQVFVQUE0SUJEd0F3Z2dFS0FvSUJBUUN4elMvMnlyNTkvQXFqalVOTlR5TW5tSGkvWkcyZW96RjE5eUdtUWtDTQpFcjFSMG9xb2V2OGtoWHNTalduK2FsUUoyaW85ekY2eGo5SjF5aDBmbVFMMzhQbm1NNStPVzYyM0FmbnNQbEI4CjlTRlhJQkdZS1JQaEVZMXYzSi91YVpsb0lDcWRnaHk0VzdFRVVVSXVNK2dLK1ZKdUV4SUhqZnJKMFdjMmRiSysKSk5OWDU3YW9wQjF2ZTFwTktIZkcrN2lCelMyejI2Y3dIUXdsQnFyMjA3MkhadUVzSG5XWXc5ekp6L3dUNm1CdgpNY3ZHeEl6aTRaSVBqVWlWUW5XanJMQ0JONGRGY2dUSUozQW5oSlJlQ1AzNiswelVjcFp3NTVMakp6bFJ5bWRRCk82TVNvVFY0ZUE1elhReTJvVS92T3IzMGthZEpHeng4YlRublFkRjBibkhEQWdNQkFBR2pnYzR3Z2Nzd0RnWUQKVlIwUEFRSC9CQVFEQWdXZ01CTUdBMVVkSlFRTU1Bb0dDQ3NHQVFVRkJ3TUNNQXdHQTFVZEV3RUIvd1FDTUFBdwpTUVlEVlIwT0JFSUVRRENSMW8rb0FrRVV4RUZUS3A0eGtvQnczM2FrOEQvNXJjYkxXR1EwTTJpTVV3Wko5eUVrCmVkMmY0cGdjSkt5ODRCeWVpc0s0UXJNcEJ5VnNZRnhtMGswd1N3WURWUjBqQkVRd1FvQkFWeUtBOTlqSTduSGgKa3ZEM3hCeVJqTWpsWG9MTjZoS0VUTnYrOXVwRTl4RjYxTlcyekNXamUyNURSZi9pd1lOUFV2QXBtTjFJRWhQawpTYTE2L1BCZGJ6QU5CZ2txaGtpRzl3MEJBUXNGQUFPQ0FRRUFBNFdWaGUyb294MG1sNmJmWlN3NmtXQXd4VDZZCmh6bG84WGdRM3g3a25hR0pQMVNsQVJYQlA3cEl0cEsvRzk3VW4xTTNRYkcwcWF6S2VZcFNtTWE4cGRPL3lDT0gKNTdkbVZqOElPNk9tamtpT014QUZaWEJkS01SRUNGMFpYKzJadUo1WW9iL0QzVmQzbGxVZ0tNR010TE9GWW1Ubgo5MGFndldXOVRkWGZmTHBER2pRTjJFUWVGVmtkQU5tNU9DRUFiOEt2bS82THc0TldNdzdHUFVwTVl0eElGeVlvCi9oSGhUWUFLRGpvckJkQkpobFBMd1VXeUN6ZFBvRmZUdUpzYzZvSFE0K3FPREY1YkI4UHNkM1pRK0hzT3VTSEcKNXNlU0F4ek5vRjNLY21iUlF2K3JRcnVxVEs3aHd5VStkdjFnMjhYUlBMQitpRm9lY1lIVUJQR1IyQT09Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K")
			fakeClientKeyData := []byte("LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlFcEFJQkFBS0NBUUVBc2MwdjlzcStmZndLbzQxRFRVOGpKNWg0djJSdG5xTXhkZmNocGtKQWpCSzlVZEtLCnFIci9KSVY3RW8xcC9tcFVDZG9xUGN4ZXNZL1NkY29kSDVrQzkvRDU1ak9mamx1dHR3SDU3RDVRZlBVaFZ5QVIKbUNrVDRSR05iOXlmN21tWmFDQXFuWUljdUZ1eEJGRkNMalBvQ3ZsU2JoTVNCNDM2eWRGbk5uV3l2aVRUVitlMgpxS1FkYjN0YVRTaDN4dnU0Z2MwdHM5dW5NQjBNSlFhcTl0TzloMmJoTEI1MW1NUGN5Yy84RStwZ2J6SEx4c1NNCjR1R1NENDFJbFVKMW82eXdnVGVIUlhJRXlDZHdKNFNVWGdqOSt2dE0xSEtXY09lUzR5YzVVY3BuVUR1akVxRTEKZUhnT2MxME10cUZQN3pxOTlKR25TUnM4ZkcwNTUwSFJkRzV4d3dJREFRQUJBb0lCQUVUQXNURmZTTFh5eGpKawpKNGczZDhLUjVPOHRhRzRWY01USzRVb25DRXFoM0c5TldLeTVrdnVPV2Y3Y2pBWURHNmdMb3BYdTl4YjJKRTNECjcrc09BZVhhV3VlM1FwV0x3ZXFvYXZuOVJxWnJLNDlES1VxTFo5SjZOUlR5WFMyVnkrcEZ0ZlRlSVRqd3k4eDkKbDNmQ1BwSXZ3cjRweGFrQ0w5M21pV0MzdG54cm9BTVluU0RlNVNnazFCZU1vd2pDZDh1T3BGODFaVjZ1ZUVlVgp1TGdNNWQ5ZCt6MWovYVc0M01PUFNKSkcralM2WVROM3lpSDVZSWkrUjdHU21tNjdQM29NZlYxUUdWbDJuMmdxCjBnMEkrZ2I5akFXY3lENHVNNlF2Sisya0o2czR3c2dsZTB5a1RpZEJoeDRHQUFBSmpFdFFNdkR0aG54NGtZM1QKTTNoVEk1RUNnWUVBMS9hWXk4dFZyWjVkdHRLS0FCS21ieFMxL2tCUmVHT1JySVBIZUdGWmdIK2s0NWhCTHlQNQpkcVNSZVd2TCs1SEFRdWt2WFYxUXpmczBwQTRSVGNyNnZ1ZXZsSFVjY2tpVWlJNkxzcjNRSm0ydVJkSVlveWZvClRHaHVFalRwZ2ErVlhlb1ozTVVFSUliWHcvNHFzTERITmYvY3JZWVA0NXh3QWxUeWxLam1ycFVDZ1lFQTBzTjQKcCtVTFRNWDJxZGJjZ2NySXpqUTdzYXBHRWhHVThMdFBQSGM4anJzbHEwWS85WnF2L091NVdOYjBqQ1RSSTR2RQpzRGlkYUlQMmg0Nit3MEpuVkdjSzVPQVVDNVZBcXN3QkZQSExPZ1pUS0xlaFdmTS9vQ2pLSDJnOHRZeWFhUnlRCnhGZzNvajdmc1FRaWc2QVBQS3NqL2U3Q25yMDFRdnVLNHJ0c0FQY0NnWUVBa1I4RlNCVG9DeFliTlVvL0w1TlkKd2RZeUFac280L1JNcEplZEI3aXJFeDB6S1RsYnZCaTVmczlSYmoxUXdra0w0Q3FnQ0dZM2NXTDMyYklXVUtjdwpYZTZFWHdkZlNUQ2FselRxalA3ZUM2U3ljZnFmVWF2MGZydkNFM3Y0Mll1cW5JUStRc3NsWGRJZTFYWkxLNVp2CkYwdEsrRlBaQTROUkJWQWQvbVdOTmcwQ2dZQVpvWS93eXhmK3RDeDFKeDRWNHJWYzdsazhGL3NCZzRYYmFNd1EKREdnZTYzOS9Qc0hVZW9WZ2VzSkZuWTZMNUlaU2psTFRJMjl4SUd0QXZRbFI4YWRqU2t5MjNORlRQMGxuKy9zOQpzdElHTW5LMmh1NW1aQUNlMTVjTkRyNGpUZ0FSUEZvV3Bxdk5YVndTeU8veGxldUVjME9qUkFBREVmdUNNOWtHCkRjanFyUUtCZ1FDeU1JbGNQL3NRODh3eWI0NkJQMkJjVFc3cHRPYW9LOXRLL0REQjBFenAwY2VydkkvdXQ5cEMKZmFISFNJVVVGQ3hHelp3YWtDL3hCYTRnajNXcEtJSTN3YkU5WG0wUWRiMDNRRmRydXBtQUVDOUFWeXpabkZhcgplMkpRUUtWUzZZVHZjbitKZzYvQ1gxeUF0NHM4OXFJU2hwWGQ0c1ZrNENleWhHTUJqNXd1WGc9PQotLS0tLUVORCBSU0EgUFJJVkFURSBLRVktLS0tLQo=")

			// CA ConfigMap used by transportForCARef to set the RootCAs.
			fakeCertCADataForConfigMap := []byte("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURQRENDQWlTZ0F3SUJBZ0lJWFN1V1lMeVlRZVF3RFFZSktvWklodmNOQVFFTEJRQXdKakVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1SQXdEZ1lEVlFRREV3ZHliMjkwTFdOaE1CNFhEVEkwTURReE1qRXdORE16T1ZvWApEVE0wTURReE1ERXdORE16T1Zvd0pqRVNNQkFHQTFVRUN4TUpiM0JsYm5Ob2FXWjBNUkF3RGdZRFZRUURFd2R5CmIyOTBMV05oTUlJQklqQU5CZ2txaGtpRzl3MEJBUUVGQUFPQ0FROEFNSUlCQ2dLQ0FRRUFyNDNTVWVKUGp4YlAKWkRUUUc1RnpDVnY3VDI0VnpWdFJpWGpoZWFWV1Byc0JTQXdVS1l3M09kSzhTd2pXdEdyS25ZVi9nOXNGNUVWWQpibzZFQ3VyaFVWSEliWjhaU1A3UDNIWnZlSHA5ak00NS9tbE5YSkttdFE4U2NMejdGNmM3YXViQUhHU210b3BOCjZndGwzMjVNV1E0TmZNUHRPSThyUlpBWEthajZGWitmZThHYVVvZGhpdTVHdzdMZGg0U1JXSkpPLzd3ZzAvUnMKTW5BYWcxc2h6UlNYdiszbXFmRXJwUzJNaVBZM0pxamdUcEkwM3VsZHpMMXdoU1ZKYjJIbDBqM0hMZzZFRDlNNwpIMzRsWENxVXl0NStWUlQ4QUYwOWp6eUlKRmZRUlBJNFVIbTBUV0dmaTRhcGNsVEtIbERFUGFibm5OS3RKWE9wCkhXYUllQitSSVFJREFRQUJvMjR3YkRBT0JnTlZIUThCQWY4RUJBTUNBcVF3RHdZRFZSMFRBUUgvQkFVd0F3RUIKL3pCSkJnTlZIUTRFUWdSQTBNdVRCazdjKzZScUZQQ1FTbWtRcm94emJlR3F5dWhqbzFOUVR2YUpXWEdOanVydgpyc0ZjY3R4TjdhTGlEYWJPODVnVmR1UnlEaGw2SVBPYXE2R1RMVEFOQmdrcWhraUc5dzBCQVFzRkFBT0NBUUVBCm5DcWcveHFSaElIQytjV2NMbENubVplbXVCZjljV2lkQWZXL3JqOUlQaCtSRUhwVC8vRUwyOHpCdEhmcmNXSHcKNzVuT3J2eklPZllBRHo3L25oZkczK1lqK1VPc2RVZFF5aTBTV3JFUEdOUjNaRXRXTUtzL3Nodm5Na3NKQzJldgpxUm9WOVBUbUtlL2RxbVVsNk1kNGVGM0xVeGFObm9aZDcyUWQ2bFdLOUl3dldTYWpTaENrNEl2aThnbWdRTGVJClRYNU9kcDBDdlQ3aENISVcwNnpPVHpib09waGQzWmVGTTVzeUVJMTlsM0dCUmdwQkR5T0NpL3FkNlJLVjhaRWUKQi9Ja2VtUmRwMGJDWS9QbGoxc2Z5L2NjTEF1WEtTM3BWK2N2SjVNS1ZHSmIrZWZtV0M1NE9LdmU1QXNXbzd6VApGRVdxSHNLTElweFZVdnZwM05VZjFBPT0KLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQotLS0tLUJFR0lOIENFUlRJRklDQVRFLS0tLS0KTUlJRCt6Q0NBdU9nQXdJQkFnSUlFMnYvcVpVaDJ1VXdEUVlKS29aSWh2Y05BUUVMQlFBd0pqRVNNQkFHQTFVRQpDeE1KYjNCbGJuTm9hV1owTVJBd0RnWURWUVFERXdkeWIyOTBMV05oTUI0WERUSTBNRFF4TWpFd05ETXpPVm9YCkRUSTFNRFF4TWpFd05ETTBOVm93TURFU01CQUdBMVVFQ2hNSmIzQmxibk5vYVdaME1Sb3dHQVlEVlFRREV4RnYKY0dWdWMyaHBablF0YVc1bmNtVnpjekNDQVNJd0RRWUpLb1pJaHZjTkFRRUJCUUFEZ2dFUEFEQ0NBUW9DZ2dFQgpBTVNwQUVIL2w1ZXNSV1Q4aDZpdXpKUmRkK3ZqYlluc1UzM29vbHlPZkxkTVBnS0U5VTJ6ai9TY3krcTNtaFg4ClhKQkJYcUFSbXlPSHd2bE5vM0pXUFNRYjdKamNtT3UreDFKWFdMTXBGa2pPcW12eTZEMWxqVEJrSXdETjVGZjkKV05xMmZLcjBVdm5yRmxVOWU1bUtETGxicmR1bVU4OUU2bjl1MzNxUExremtvcFVjQ0UxWjdNQ0I2L1hTNytTRAo4M0MwcldWMWZTK292M3ZtWk1vWVY1T2pGcUdFY05TVlFFT0pPbk5ZaHFKd0N5NDkvNEVQc0NHbko5VWh3cUNECjY2Y3lqemVEK20wMUxWRWQrOFNYcFYwZnFXNEFWVHBMMmYvVFBKc2lHVGJSb2pOSFVNWmswbUdFZ2ZhaHFMZTUKd3N4MURQMFFveWZCSHBJVUMvMFFHRDBDQXdFQUFhT0NBU0V3Z2dFZE1BNEdBMVVkRHdFQi93UUVBd0lGb0RBZApCZ05WSFNVRUZqQVVCZ2dyQmdFRkJRY0RBZ1lJS3dZQkJRVUhBd0V3REFZRFZSMFRBUUgvQkFJd0FEQkpCZ05WCkhRNEVRZ1JBbmw4WHN1bVBFbkkvcXMzWnE5K2hzUWpsemVuZjMyUjI4bHF1anhsVmp2YjZ3bTA3dmh3K0JCbngKeVpPZkNPTDN6VFVQR3lNSWo4V0pRZktHNkJJWnp6QkxCZ05WSFNNRVJEQkNnRURReTVNR1R0ejdwR29VOEpCSwphUkN1akhOdDRhcks2R09qVTFCTzlvbFpjWTJPNnUrdXdWeHkzRTN0b3VJTnBzN3ptQlYyNUhJT0dYb2c4NXFyCm9aTXRNRVlHQTFVZEVRUS9NRDJDT3lvdVlYQndjeTVoZW5WeVpTMXRaMjEwTG1oNWNHVnljMmhwWm5RdVlYcDEKY21VdVpHVjJZMngxYzNSbGNpNXZjR1Z1YzJocFpuUXVZMjl0TUEwR0NTcUdTSWIzRFFFQkN3VUFBNElCQVFDTAp3Q1locEtZcFZxMnBNQlRjak5Cc1ZRMkIxVDVOR3pyRmpDSjBDd1hTVGtRR00yQTFZZUROSWR6Y2FpK1hpSC9TCmZOMy9RdkdsMFhsZmxwbWU4NkhZbU1aVEV2eEY4YXd1Vi9pbWNWcnNMa0QzcnBFei8yTytQVHl3bGt1M1kwQWEKRFo0WDBleThiS2RtcFhyY0xUMmVJcjc0L0QrTTVCNlp4NzZVRU1pK3hiUlBlSDkyUFpwZmg0VEFYNTZ2UFBBMgpJQlFqNUg0ZmZpbmVIaE0zMzhMTmdpYzJIMTh2WmNDc3k0WnV5eEVxc1VGSGlUTmNuMGViRVdDTjNOTDh1QzI3CnRoejRhMGxaTVpjQStMRG9OdnJPRXB5QnpHbVdlejVTUHg3VTV5TVlyZllwL3FVaVY5Ky9CNThrTUovRGJoOHMKOVNDYXUzcktsaU5SWmVIZFZDUUIKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=")
			fakeCertCADataForConfigMapDecoded := make([]byte, len(fakeCertCADataForConfigMap))
			_, err := base64.StdEncoding.Decode(fakeCertCADataForConfigMapDecoded, fakeCertCADataForConfigMap)
			g.Expect(err).ToNot(HaveOccurred())
			caName := "test"
			caKey := "test"
			caConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caName,
					Namespace: namespace,
				},
				Data: map[string]string{
					caKey: string(fakeCertCADataForConfigMapDecoded),
				},
			}

			// Proxy CA ConfigMap used by transportForCARef to set the RootCAs.
			fakeProxyCertCADecoded := make([]byte, len(fakeCertCAData))
			_, err = base64.StdEncoding.Decode(fakeProxyCertCADecoded, fakeCertCAData)
			g.Expect(err).ToNot(HaveOccurred())
			proxyTrustedCA := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "proxyTrustedCA",
					Namespace: namespace,
				},
				Data: map[string]string{
					"ca-bundle.crt": string(fakeProxyCertCADecoded),
				},
			}

			// Konnectivity certs needed by the konnectivity dialer.
			konnectivityClientSecret := manifests.KonnectivityClientSecret(namespace)
			konnectivityClientSecret.Data = map[string][]byte{
				konnectivityClientDataCertKey: fakeClientCertData,
				konnectivityClientDataKey:     fakeClientKeyData,
			}
			konnectivityCAConfigMap := manifests.KonnectivityCAConfigMap(namespace)
			konnectivityCA, err := base64.StdEncoding.DecodeString(string(fakeCertCAData))
			g.Expect(err).ToNot(HaveOccurred())
			konnectivityCAConfigMap.Data = map[string]string{
				konnectivityCADataKey: string(konnectivityCA),
			}

			// Kubeconfig used by the konnectivity dialer to connect to the guest cluster and resolve SVCs DNS.
			kubeconfigSecret := manifests.KASServiceKubeconfigSecret(namespace)
			kubeconfigSecret.Data = map[string][]byte{
				kubeconfigDataKey: []byte(fmt.Sprintf(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: %s
    server: https://fake.kubernetes.server:6443
  name: fake-cluster
contexts:
- context:
    cluster: fake-cluster
    user: fake-user
  name: fake-context
current-context: fake-context
preferences: {}
users:
- name: fake-user
  user:
    client-certificate-data: %s
    client-key-data: %s
`, fakeCertCAData, fakeClientCertData, fakeClientKeyData)),
			}

			// Fake client with all the expected resources.
			scheme := scheme.Scheme
			err = hyperv1.AddToScheme(scheme)
			g.Expect(err).ToNot(HaveOccurred())
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				tc.hcp,
				caConfigMap,
				konnectivityClientSecret,
				konnectivityCAConfigMap,
				proxyTrustedCA,
				kubeconfigSecret,
			).Build()

			// Run function.
			transport, err := transportForCARef(context.Background(), client, namespace, caName, caKey, false)
			g.Expect(err).ToNot(HaveOccurred())
			tr := transport.(*http.Transport)

			// Validate proxy expectations.
			url, err := tr.Proxy(&http.Request{
				URL: &url.URL{
					Scheme: "https",
					Host:   tc.requestToURL,
				},
			})
			g.Expect(err).ToNot(HaveOccurred())
			gotURL := ""
			if url != nil {
				gotURL = url.String()
			}
			g.Expect(gotURL).To(Equal(tc.expectedProxyRequestURL))

			// Validate RootCAs expectations.
			expectedCertPool, err := x509.SystemCertPool()
			g.Expect(err).ToNot(HaveOccurred())
			if tc.hcp.Spec.Configuration != nil {
				if tc.hcp.Spec.Configuration.Proxy.TrustedCA.Name != "" {
					expectedCertPool.AppendCertsFromPEM([]byte(fakeProxyCertCADecoded))
				}
			}
			expectedCertPool.AppendCertsFromPEM([]byte(fakeCertCADataForConfigMapDecoded))
			g.Expect(tr.TLSClientConfig.RootCAs.Equal(expectedCertPool)).To(BeTrue())

			// TODO(alberto): add some validation for DialContext.
		})
	}
}
