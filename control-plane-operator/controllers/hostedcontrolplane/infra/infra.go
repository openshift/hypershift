package infra

type InfrastructureStatus struct {
	APIHost                 string
	APIPort                 int32
	OAuthEnabled            bool
	OAuthHost               string
	OAuthPort               int32
	KonnectivityHost        string
	KonnectivityPort        int32
	OpenShiftAPIHost        string
	OauthAPIServerHost      string
	PackageServerAPIAddress string
	Message                 string
	InternalHCPRouterHost   string
	NeedInternalRouter      bool
	ExternalHCPRouterHost   string
	NeedExternalRouter      bool
}

func (s InfrastructureStatus) IsReady() bool {
	isReady := len(s.APIHost) > 0 &&
		len(s.KonnectivityHost) > 0 &&
		s.APIPort > 0 &&
		s.KonnectivityPort > 0

	if s.OAuthEnabled {
		isReady = isReady && len(s.OAuthHost) > 0 && s.OAuthPort > 0
	}
	if s.NeedInternalRouter {
		isReady = isReady && len(s.InternalHCPRouterHost) > 0
	}
	if s.NeedExternalRouter {
		isReady = isReady && len(s.ExternalHCPRouterHost) > 0
	}
	return isReady
}
