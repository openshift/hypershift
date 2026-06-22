package v1beta1

import configv1 "github.com/openshift/api/config/v1"

func (c *ClusterConfiguration) GetAPIServer() *configv1.APIServerSpec { return c.APIServer }
func (c *ClusterConfiguration) GetAuthentication() *configv1.AuthenticationSpec {
	return c.Authentication
}
func (c *ClusterConfiguration) GetFeatureGate() *configv1.FeatureGateSpec { return c.FeatureGate }
func (c *ClusterConfiguration) GetImage() *configv1.ImageSpec             { return c.Image }
func (c *ClusterConfiguration) GetIngress() *configv1.IngressSpec         { return c.Ingress }
func (c *ClusterConfiguration) GetNetwork() *configv1.NetworkSpec         { return c.Network }
func (c *ClusterConfiguration) GetOAuth() *configv1.OAuthSpec             { return c.OAuth }
func (c *ClusterConfiguration) GetScheduler() *configv1.SchedulerSpec     { return c.Scheduler }
func (c *ClusterConfiguration) GetProxy() *configv1.ProxySpec             { return c.Proxy }

func (c *ClusterConfiguration) GetTLSSecurityProfile() *configv1.TLSSecurityProfile {
	if c != nil && c.APIServer != nil {
		return c.APIServer.TLSSecurityProfile
	}
	return nil
}

func (c *ClusterConfiguration) GetAutoAssignCIDRs() []string {
	if c != nil && c.Network != nil && c.Network.ExternalIP != nil {
		return c.Network.ExternalIP.AutoAssignCIDRs
	}
	return nil
}

func (c *ClusterConfiguration) GetAuditPolicyConfig() configv1.Audit {
	if c != nil && c.APIServer != nil && c.APIServer.Audit.Profile != "" {
		return c.APIServer.Audit
	}
	return configv1.Audit{Profile: configv1.DefaultAuditProfileType}
}

func (c *ClusterConfiguration) GetFeatureGateSelection() configv1.FeatureGateSelection {
	if c != nil && c.FeatureGate != nil {
		return c.FeatureGate.FeatureGateSelection
	}
	return configv1.FeatureGateSelection{FeatureSet: configv1.Default}
}

func (c *ClusterConfiguration) GetNamedCertificates() []configv1.APIServerNamedServingCert {
	if c != nil && c.APIServer != nil {
		return c.APIServer.ServingCerts.NamedCertificates
	}
	return []configv1.APIServerNamedServingCert{}
}
