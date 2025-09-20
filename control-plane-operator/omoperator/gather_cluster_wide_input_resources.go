package omoperator

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// operator.openshift.io/authentications/cluster
func projectOperatorAuthenticationCluster(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string) (*libraryinputresources.Resource, error) {
	gvr := corev1.SchemeGroupVersion.WithResource("configmaps")
	authOperatorConfigMap, err := mgmtKubeClient.Resource(gvr).Namespace(controlPlaneNamespace).Get(ctx, operatorAuthenticationConfigMapName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	authOperatorYaml, found, err := unstructured.NestedString(authOperatorConfigMap.Object, "data", "cluster.yaml")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("missing cluster.yaml field in %s/%s configmap", controlPlaneNamespace, operatorAuthenticationConfigMapName)
	}
	unstructuredAuthOperator, err := decodeIndividualObj([]byte(authOperatorYaml))
	if err != nil {
		return nil, err
	}

	gvr = schema.GroupVersionResource{Group: operatorv1.SchemeGroupVersion.Group, Version: operatorv1.SchemeGroupVersion.Version, Resource: "authentications"}
	ret := &libraryinputresources.Resource{
		ResourceType: gvr,
		Content:      unstructuredAuthOperator,
	}
	return ret, nil
}

// config.openshift.io/authentications/cluster resource doesn't exist in HCP we need to project it from HostedControlPlane
func projectConfigAuthenticationCluster(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	cfg := &configv1.Authentication{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "Authentication",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
	if hostedControlPlane != nil && hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.Authentication != nil {
		cfg.Spec = *hostedControlPlane.Spec.Configuration.Authentication
	}

	return runtimeObjectToInputResource(cfg, configv1.SchemeGroupVersion.WithResource("authentications"))
}

// config.openshift.io/clusterversions/cluster resource doesn't exist in HCP we need to project it from HostedControlPlane
func projectConfigClusterVersionCluster(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	clusterVersion := &configv1.ClusterVersion{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "ClusterVersion",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: configv1.ClusterID(hostedControlPlane.Spec.ClusterID),
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
				AdditionalEnabledCapabilities: capabilities.CalculateEnabledCapabilities(hostedControlPlane.Spec.Capabilities),
			},
			Upstream: hostedControlPlane.Spec.UpdateService,
			Channel:  hostedControlPlane.Spec.Channel,
		},
	}

	return runtimeObjectToInputResource(clusterVersion, configv1.SchemeGroupVersion.WithResource("clusterversions"))
}

// config.openshift.io/infrastructures/cluster
func projectConfigInfrastructureCluster(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	infra := globalconfig.InfrastructureConfig()
	infra.TypeMeta = metav1.TypeMeta{
		APIVersion: configv1.SchemeGroupVersion.String(),
		Kind:       "Infrastructure",
	}
	globalconfig.ReconcileInfrastructure(infra, hostedControlPlane)

	return runtimeObjectToInputResource(infra, configv1.SchemeGroupVersion.WithResource("infrastructures"))
}

// config.openshift.io/apiservers/cluster
func projectConfigApiserverCluster(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	cfg := &configv1.APIServer{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "APIServer",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}

	if hostedControlPlane != nil && hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.APIServer != nil {
		cfg.Spec = *hostedControlPlane.Spec.Configuration.APIServer
	}

	return runtimeObjectToInputResource(cfg, configv1.SchemeGroupVersion.WithResource("apiservers"))
}

// config.openshift.io/oauths/cluster
func projectConfigOAuthCluster(hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	cfg := &configv1.OAuth{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "OAuth",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}

	if hostedControlPlane != nil && hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.OAuth != nil {
		cfg.Spec = *hostedControlPlane.Spec.Configuration.OAuth
	}

	return runtimeObjectToInputResource(cfg, configv1.SchemeGroupVersion.WithResource("oauths"))
}
