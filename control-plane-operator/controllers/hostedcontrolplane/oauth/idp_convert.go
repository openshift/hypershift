package oauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/cache"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/net"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	osinv1 "github.com/openshift/api/osin/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	IDPVolumePathPrefix = "/etc/oauth/idp"
)

var (
	externalHTTPRequestTimeout = 30 * time.Second

	oidcPasswordCheckCache = cache.NewExpiring()
	oidcPasswordTTL        = 7 * 24 * time.Hour

	openIDURLsCache = cache.NewExpiring()
	openIDURLsTTL   = 10 * time.Minute
)

type idpData struct {
	provider  runtime.Object
	challenge bool
	login     bool
}

type IDPVolumeMountInfo struct {
	Container    string
	VolumeMounts util.PodVolumeMounts
	Volumes      []corev1.Volume
}

func (i *IDPVolumeMountInfo) ConfigMapPath(index int, configMapName, field, key string) string {
	v := corev1.Volume{
		Name: fmt.Sprintf("idp-cm-%d-%s", index, field),
	}
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = configMapName
	i.Volumes = append(i.Volumes, v)
	i.VolumeMounts[i.Container][v.Name] = fmt.Sprintf("%s/idp_cm_%d_%s", IDPVolumePathPrefix, index, field)
	return path.Join(i.VolumeMounts[i.Container][v.Name], key)
}

func (i *IDPVolumeMountInfo) SecretPath(index int, secretName, field, key string) string {
	v := corev1.Volume{
		Name: fmt.Sprintf("idp-secret-%d-%s", index, field),
	}
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = secretName
	i.Volumes = append(i.Volumes, v)
	i.VolumeMounts[i.Container][v.Name] = fmt.Sprintf("%s/idp_secret_%d_%s", IDPVolumePathPrefix, index, field)
	return path.Join(i.VolumeMounts[i.Container][v.Name], key)
}

func convertIdentityProviders(ctx context.Context, identityProviders []configv1.IdentityProvider, providerOverrides map[string]*ConfigOverride, kclient crclient.Client, namespace string) ([]osinv1.IdentityProvider, *IDPVolumeMountInfo, error) {
	converted := make([]osinv1.IdentityProvider, 0, len(identityProviders))
	errs := []error{}
	volumeMountInfo := &IDPVolumeMountInfo{
		Container: oauthContainerMain().Name,
		VolumeMounts: util.PodVolumeMounts{
			oauthContainerMain().Name: util.ContainerVolumeMounts{},
		},
	}

	for i, idp := range defaultIDPMappingMethods(identityProviders) {
		var providerConfigOverride *ConfigOverride = nil
		if _, ok := providerOverrides[idp.Name]; ok {
			providerConfigOverride = providerOverrides[idp.Name]
		}
		data, err := convertProviderConfigToIDPData(ctx, &idp.IdentityProviderConfig, providerConfigOverride, i, volumeMountInfo, kclient, namespace)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to apply IDP %s config: %v", idp.Name, err))
			continue
		}
		converted = append(converted,
			osinv1.IdentityProvider{
				Name:            idp.Name,
				UseAsChallenger: data.challenge,
				UseAsLogin:      data.login,
				MappingMethod:   string(idp.MappingMethod),
				Provider: runtime.RawExtension{
					Object: data.provider,
				},
			},
		)
	}

	return converted, volumeMountInfo, utilerrors.NewAggregate(errs)
}

func defaultIDPMappingMethods(identityProviders []configv1.IdentityProvider) []configv1.IdentityProvider {
	out := make([]configv1.IdentityProvider, len(identityProviders)) // do not mutate informer cache

	for i, idp := range identityProviders {
		idp.DeepCopyInto(&out[i])
		if out[i].MappingMethod == "" {
			out[i].MappingMethod = configv1.MappingMethodClaim
		}

	}

	return out
}

func convertProviderConfigToIDPData(
	ctx context.Context,
	providerConfig *configv1.IdentityProviderConfig,
	configOverride *ConfigOverride,
	i int,
	idpVolumeMounts *IDPVolumeMountInfo,
	kclient crclient.Client,
	namespace string,
) (*idpData, error) {
	const missingProviderFmt string = "type %s was specified, but its configuration is missing"

	data := &idpData{login: true}

	switch providerConfig.Type {
	case configv1.IdentityProviderTypeBasicAuth:
		basicAuthConfig := providerConfig.BasicAuth
		if basicAuthConfig == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}

		data.provider = &osinv1.BasicAuthPasswordIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "BasicAuthPasswordIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			RemoteConnectionInfo: configv1.RemoteConnectionInfo{
				URL: basicAuthConfig.URL,
				CA:  idpVolumeMounts.ConfigMapPath(i, basicAuthConfig.CA.Name, "ca", corev1.ServiceAccountRootCAKey),
				CertInfo: configv1.CertInfo{
					CertFile: idpVolumeMounts.SecretPath(i, basicAuthConfig.TLSClientCert.Name, "tls-client-key", corev1.TLSCertKey),
					KeyFile:  idpVolumeMounts.SecretPath(i, basicAuthConfig.TLSClientKey.Name, "tls-client-key", corev1.TLSPrivateKeyKey),
				},
			},
		}
		data.challenge = true

	case configv1.IdentityProviderTypeGitHub:
		githubConfig := providerConfig.GitHub
		if githubConfig == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}
		provider := &osinv1.GitHubIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "GitHubIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			ClientID: githubConfig.ClientID,
			ClientSecret: configv1.StringSource{
				StringSourceSpec: configv1.StringSourceSpec{
					File: idpVolumeMounts.SecretPath(i, githubConfig.ClientSecret.Name, "client-secret", configv1.ClientSecretKey),
				},
			},
			Organizations: githubConfig.Organizations,
			Teams:         githubConfig.Teams,
			Hostname:      githubConfig.Hostname,
		}
		if len(githubConfig.CA.Name) > 0 {
			provider.CA = idpVolumeMounts.ConfigMapPath(i, githubConfig.CA.Name, "ca", corev1.ServiceAccountRootCAKey)
		}
		data.provider = provider
		data.challenge = false

	case configv1.IdentityProviderTypeGitLab:
		gitlabConfig := providerConfig.GitLab
		if gitlabConfig == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}

		data.provider = &osinv1.GitLabIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "GitLabIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			CA:       idpVolumeMounts.ConfigMapPath(i, gitlabConfig.CA.Name, "ca", corev1.ServiceAccountRootCAKey),
			URL:      gitlabConfig.URL,
			ClientID: gitlabConfig.ClientID,
			ClientSecret: configv1.StringSource{
				StringSourceSpec: configv1.StringSourceSpec{
					File: idpVolumeMounts.SecretPath(i, gitlabConfig.ClientSecret.Name, "client-secret", configv1.ClientSecretKey),
				},
			},
			Legacy: new(bool), // we require OIDC for GitLab now
		}
		data.challenge = true

	case configv1.IdentityProviderTypeGoogle:
		googleConfig := providerConfig.Google
		if googleConfig == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}

		data.provider = &osinv1.GoogleIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "GoogleIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			ClientID: googleConfig.ClientID,
			ClientSecret: configv1.StringSource{
				StringSourceSpec: configv1.StringSourceSpec{
					File: idpVolumeMounts.SecretPath(i, googleConfig.ClientSecret.Name, "client-secret", configv1.ClientSecretKey),
				},
			},
			HostedDomain: googleConfig.HostedDomain,
		}
		data.challenge = false

	case configv1.IdentityProviderTypeHTPasswd:
		if providerConfig.HTPasswd == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}

		data.provider = &osinv1.HTPasswdPasswordIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "HTPasswdPasswordIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			File: idpVolumeMounts.SecretPath(i, providerConfig.HTPasswd.FileData.Name, "file-data", configv1.HTPasswdDataKey),
		}
		data.challenge = true

	case configv1.IdentityProviderTypeKeystone:
		keystoneConfig := providerConfig.Keystone
		if keystoneConfig == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}

		data.provider = &osinv1.KeystonePasswordIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "KeystonePasswordIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			RemoteConnectionInfo: configv1.RemoteConnectionInfo{
				URL: keystoneConfig.URL,
				CA:  idpVolumeMounts.ConfigMapPath(i, keystoneConfig.CA.Name, "ca", corev1.ServiceAccountRootCAKey),
				CertInfo: configv1.CertInfo{
					CertFile: idpVolumeMounts.SecretPath(i, keystoneConfig.TLSClientCert.Name, "tls-client-cert", corev1.TLSCertKey),
					KeyFile:  idpVolumeMounts.SecretPath(i, keystoneConfig.TLSClientKey.Name, "tls-client-key", corev1.TLSPrivateKeyKey),
				},
			},
			DomainName:          keystoneConfig.DomainName,
			UseKeystoneIdentity: true, // force use of keystone ID
		}
		data.challenge = true

	case configv1.IdentityProviderTypeLDAP:
		ldapConfig := providerConfig.LDAP
		if ldapConfig == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}

		data.provider = &osinv1.LDAPPasswordIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "LDAPPasswordIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			URL:    ldapConfig.URL,
			BindDN: ldapConfig.BindDN,
			BindPassword: configv1.StringSource{
				StringSourceSpec: configv1.StringSourceSpec{
					File: idpVolumeMounts.SecretPath(i, ldapConfig.BindPassword.Name, "bind-password", configv1.BindPasswordKey),
				},
			},
			Insecure: ldapConfig.Insecure,
			CA:       idpVolumeMounts.ConfigMapPath(i, ldapConfig.CA.Name, "ca", corev1.ServiceAccountRootCAKey),
			Attributes: osinv1.LDAPAttributeMapping{
				ID:                ldapConfig.Attributes.ID,
				PreferredUsername: ldapConfig.Attributes.PreferredUsername,
				Name:              ldapConfig.Attributes.Name,
				Email:             ldapConfig.Attributes.Email,
			},
		}
		data.challenge = true

	case configv1.IdentityProviderTypeOpenID:
		openIDConfig := providerConfig.OpenID
		if openIDConfig == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}

		openIDProvider := &osinv1.OpenIDIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "OpenIDIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			ClientID: openIDConfig.ClientID,
			ClientSecret: configv1.StringSource{
				StringSourceSpec: configv1.StringSourceSpec{
					File: idpVolumeMounts.SecretPath(i, openIDConfig.ClientSecret.Name, "client-secret", configv1.ClientSecretKey),
				},
			},
			ExtraScopes:              openIDConfig.ExtraScopes,
			ExtraAuthorizeParameters: openIDConfig.ExtraAuthorizeParameters,
		}
		//Handle special case for IBM Cloud's OIDC provider (need to override some fields not available in public api)
		if configOverride != nil {
			openIDProvider.URLs = configOverride.URLs
			openIDProvider.Claims = configOverride.Claims
		} else {
			urls, err := discoverOpenIDURLs(ctx, kclient, openIDConfig.Issuer, corev1.ServiceAccountRootCAKey, namespace, openIDConfig.CA)
			if err != nil {
				return nil, err
			}
			openIDProvider.URLs = *urls
			openIDProvider.Claims = osinv1.OpenIDClaims{
				// There is no longer a user-facing setting for ID as it is considered unsafe
				ID:                []string{configv1.UserIDClaim},
				PreferredUsername: openIDConfig.Claims.PreferredUsername,
				Name:              openIDConfig.Claims.Name,
				Email:             openIDConfig.Claims.Email,
			}
		}
		if len(openIDConfig.CA.Name) > 0 {
			openIDProvider.CA = idpVolumeMounts.ConfigMapPath(i, openIDConfig.CA.Name, "ca", corev1.ServiceAccountRootCAKey)
		}
		data.provider = openIDProvider

		// openshift CR validating in kube-apiserver does not allow
		// challenge-redirecting IdPs to be configured with OIDC so it is safe
		// to allow challenge-issuing flow if it's available on the OIDC side
		challengeFlowsAllowed, err := checkOIDCPasswordGrantFlow(
			ctx,
			kclient,
			openIDProvider.URLs.Token,
			openIDConfig.ClientID,
			namespace,
			openIDConfig.CA,
			openIDConfig.ClientSecret,
		)
		if err != nil {
			return nil, fmt.Errorf("error attempting password grant flow: %v", err)
		}
		data.challenge = challengeFlowsAllowed
		data.provider = openIDProvider
	case configv1.IdentityProviderTypeRequestHeader:
		requestHeaderConfig := providerConfig.RequestHeader
		if requestHeaderConfig == nil {
			return nil, fmt.Errorf(missingProviderFmt, providerConfig.Type)
		}
		data.provider = &osinv1.RequestHeaderIdentityProvider{
			TypeMeta: metav1.TypeMeta{
				Kind:       "RequestHeaderIdentityProvider",
				APIVersion: osinv1.GroupVersion.String(),
			},
			LoginURL:                 requestHeaderConfig.LoginURL,
			ChallengeURL:             requestHeaderConfig.ChallengeURL,
			ClientCA:                 idpVolumeMounts.ConfigMapPath(i, requestHeaderConfig.ClientCA.Name, "ca", corev1.ServiceAccountRootCAKey),
			ClientCommonNames:        requestHeaderConfig.ClientCommonNames,
			Headers:                  requestHeaderConfig.Headers,
			PreferredUsernameHeaders: requestHeaderConfig.PreferredUsernameHeaders,
			NameHeaders:              requestHeaderConfig.NameHeaders,
			EmailHeaders:             requestHeaderConfig.EmailHeaders,
		}
		data.challenge = len(requestHeaderConfig.ChallengeURL) > 0
		data.login = len(requestHeaderConfig.LoginURL) > 0

	default:
		return nil, fmt.Errorf("the identity provider type '%s' is not supported", providerConfig.Type)
	} // switch

	return data, nil
}

// discoverOpenIDURLs retrieves basic information about an OIDC server with hostname
// given by the `issuer` argument
func discoverOpenIDURLs(ctx context.Context, kclient crclient.Client, issuer, key, namespace string, ca configv1.ConfigMapNameReference) (*osinv1.OpenIDURLs, error) {
	issuer = strings.TrimRight(issuer, "/") // TODO make impossible via validation and remove
	wellKnown := issuer + "/.well-known/openid-configuration"

	cacheValue, inCache := openIDURLsCache.Get(wellKnown)
	if inCache {
		return cacheValue.(*osinv1.OpenIDURLs), nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, externalHTTPRequestTimeout)
	defer cancel()

	req, err := http.NewRequest(http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(reqCtx)

	rt, err := transportForCARef(ctx, kclient, namespace, ca.Name, key)
	if err != nil {
		return nil, err
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("couldn't get %v: unexpected response status %v", wellKnown, resp.StatusCode)
	}

	metadata := &openIDProviderJSON{}
	if err := json.NewDecoder(resp.Body).Decode(metadata); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %v", err)
	}

	for _, arg := range []struct {
		rawurl   string
		optional bool
	}{
		{
			rawurl:   metadata.AuthURL,
			optional: false,
		},
		{
			rawurl:   metadata.TokenURL,
			optional: false,
		},
		{
			rawurl:   metadata.UserInfoURL,
			optional: true,
		},
	} {
		if !isValidURL(arg.rawurl, arg.optional) {
			return nil, fmt.Errorf("invalid metadata from %s: url=%s optional=%v", wellKnown, arg.rawurl, arg.optional)
		}
	}

	result := &osinv1.OpenIDURLs{
		Authorize: metadata.AuthURL,
		Token:     metadata.TokenURL,
		UserInfo:  metadata.UserInfoURL,
	}
	openIDURLsCache.Set(wellKnown, result, openIDURLsTTL)
	return result, nil
}

func checkOIDCPasswordGrantFlow(ctx context.Context,
	kclient crclient.Client,
	tokenURL, clientID,
	namespace string,
	caRererence configv1.ConfigMapNameReference,
	clientSecretReference configv1.SecretNameReference,
) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clientSecretReference.Name,
			Namespace: namespace,
		},
	}
	err := kclient.Get(ctx, crclient.ObjectKeyFromObject(secret), secret)
	if err != nil {
		return false, fmt.Errorf("couldn't get the referenced secret: %v", err)
	}

	// check whether we already attempted this not to send unneccessary login
	// requests against the provider
	if cachedResult, ok := oidcPasswordCheckCache.Get(secret.ResourceVersion); ok {
		log.Info("using cached result for OIDC password grant check")
		return cachedResult.(bool), nil
	}

	clientSecret, ok := secret.Data["clientSecret"]
	if !ok || len(clientSecret) == 0 {
		return false, fmt.Errorf("the referenced secret does not contain a value for the 'clientSecret' key")
	}

	transport, err := transportForCARef(ctx, kclient, namespace, caRererence.Name, corev1.ServiceAccountRootCAKey)
	if err != nil {
		return false, fmt.Errorf("couldn't get a transport for the referenced CA: %v", err)
	}

	// prepare the grant-checking query
	query := url.Values{}
	query.Add("client_id", clientID)
	query.Add("client_secret", string(clientSecret))
	query.Add("grant_type", "password")
	query.Add("scope", "openid") // "openid" is the minimal scope, it MUST be present in an OIDC authn request
	query.Add("username", "test")
	query.Add("password", "test")
	body := strings.NewReader(query.Encode())

	reqCtx, cancel := context.WithTimeout(ctx, externalHTTPRequestTimeout)
	defer cancel()

	req, err := http.NewRequest("POST", tokenURL, body)
	if err != nil {
		return false, err
	}
	req = req.WithContext(reqCtx)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	// explicitly set Accept to 'application/json' as that's the expected deserializable output
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	respJSON := json.NewDecoder(resp.Body)
	respMap := map[string]interface{}{}
	if err = respJSON.Decode(&respMap); err != nil {
		// only log the error, some OIDCs ignore/don't implement the Accept header
		// and respond with HTML in case they don't support password credential grants at all
		log.Error(err, "failed to JSON-decode the response from the OIDC server's token endpoint", "tokenURL", tokenURL)
		oidcPasswordCheckCache.Set(secret.ResourceVersion, false, oidcPasswordTTL)
		return false, nil
	}

	if errVal, ok := respMap["error"]; ok {
		oidcPasswordCheckCache.Set(secret.ResourceVersion, errVal == "invalid_grant", oidcPasswordTTL) // wrong password, but password grants allowed
	} else {
		_, ok = respMap["access_token"] // in case we managed to hit the correct user
		oidcPasswordCheckCache.Set(secret.ResourceVersion, ok, oidcPasswordTTL)
	}

	result, _ := oidcPasswordCheckCache.Get(secret.ResourceVersion)
	return result.(bool), nil
}

type openIDProviderJSON struct {
	AuthURL     string `json:"authorization_endpoint"`
	TokenURL    string `json:"token_endpoint"`
	UserInfoURL string `json:"userinfo_endpoint"`
}

func isValidURL(rawurl string, optional bool) bool {
	if len(rawurl) == 0 {
		return optional
	}

	u, err := url.Parse(rawurl)
	if err != nil {
		return false
	}

	return u.Scheme == "https" && len(u.Host) > 0 && len(u.Fragment) == 0
}

func createFileStringSource(filepath string) configv1.StringSource {
	return configv1.StringSource{
		StringSourceSpec: configv1.StringSourceSpec{
			File: filepath,
		},
	}
}

func transportForCARef(ctx context.Context, kclient crclient.Client, namespace, name, key string) (http.RoundTripper, error) {
	// copy default transport
	transport := net.SetTransportDefaults(&http.Transport{
		TLSClientConfig: &tls.Config{},
	})

	if len(name) == 0 {
		return transport, nil
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := kclient.Get(ctx, crclient.ObjectKeyFromObject(cm), cm); err != nil {
		return nil, err
	}
	caData := []byte(cm.Data[key])
	if len(caData) == 0 {
		caData = cm.BinaryData[key]
	}
	if len(caData) == 0 {
		return nil, fmt.Errorf("config map %s/%s has no ca data at key %s", namespace, name, key)
	}

	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM(caData); !ok {
		// avoid logging data that could contain keys
		return nil, errors.New("error loading cert pool from ca data")
	}
	transport.TLSClientConfig.RootCAs = roots
	return transport, nil
}
