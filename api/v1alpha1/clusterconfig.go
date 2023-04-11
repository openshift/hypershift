package v1alpha1

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
