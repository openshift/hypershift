package omoperator

import (
	"context"
	"fmt"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// openshift-authentication/v4-0-config-system-router-certs
//
// more details:
// https://docs.google.com/document/d/1lerQtnLFofoXaO08SX2iYOv0b7pUA0Rn3Q3htxKjnrY/edit?tab=t.0
// Position in the document: 1
func projectSecretOpenshiftAuthenticationConfigSystemRouterCerts(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string, hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "v4-0-config-system-router-certs",
			Namespace: "openshift-authentication",
		},
	}
	return runtimeObjectToInputResource(secret, coreSecretGVR)
}

// openshift-authentication/oauth-openshift
func getRouteOpenshiftAuthenticationOauthOpenshift(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	// TODO: figure out how to reconcile route on HCP
	//
	// atm reconciled in https://github.com/openshift/hypershift/blob/6b4d6324de66b9aabdbe7be434b28a17c900074b/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1305
	//
	// route.Spec.Host is used to populate osinv1.OAuthConfig.MasterPublicURL
	// xref: https://github.com/openshift/cluster-authentication-operator/blob/817783a52d042f4ac3aa8faac7421ac013b42481/pkg/controllers/payload/payload_config_controller.go#L178
	//
	// For the POC we wil keep it simple and assume
	// ony one type of route
	//
	// TODO: production code will need cover all cases
	// https://github.com/openshift/hypershift/blob/6b4d6324de66b9aabdbe7be434b28a17c900074b/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1646
	//
	// I think that on standalone route is managed by the openshift-router operator
	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hostedControlPlane, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		return nil, fmt.Errorf("OAuth strategy not specified")
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil, fmt.Errorf("unsupported (not implemented) service publishing strategy type: %v", serviceStrategy.Type)
	}
	if !util.IsPublicHCP(hostedControlPlane) {
		return nil, fmt.Errorf("unsupported (not implemented) publishing scope of cluster endpoints for: %s", hostedControlPlane.Name)
	}
	gvr := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}

	// TODO: export the route name
	// xref: https://github.com/openshift/hypershift/blob/8be1d9c6f8f79106444e48f2b7d0069b942ba0d7/control-plane-operator/controllers/hostedcontrolplane/manifests/infra.go#L104
	route, err := mgmtKubeClient.Resource(gvr).Namespace(hostedControlPlane.Namespace).Get(ctx, "oauth", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// we need to change the name and the namespace
	// so that the operator can find the resource
	//
	// TODO: should we record the orig name and namespace ?
	route.SetNamespace("openshift-authentication")
	route.SetName("oauth-openshift")
	return &libraryinputresources.Resource{
		ResourceType: gvr,
		Content:      route,
	}, nil
}

// openshift-authentication/oauth-openshift
func getServiceOpenshiftAuthenticationOauthOpenshift(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, hostedControlPlane *hypershiftv1beta1.HostedControlPlane) (*libraryinputresources.Resource, error) {
	// openshift-authentication/oauth-openshift service
	// is reconciled in https://github.com/openshift/hypershift/blob/6b4d6324de66b9aabdbe7be434b28a17c900074b/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1305
	//
	// for the POC we simply assume the reconciler runs,
	// and we can read the service manifest
	// TODO: fix me (figure out how to reconcile service on HCP)

	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(hostedControlPlane, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		return nil, fmt.Errorf("OAuth strategy not specified")
	}

	gvr := corev1.SchemeGroupVersion.WithResource("services")
	svc, err := mgmtKubeClient.Resource(gvr).Namespace(hostedControlPlane.Namespace).Get(ctx, "oauth-openshift", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// we need to change the name and the namespace
	// so that the operator can find the resource
	//
	// TODO: should we record the orig name and namespace ?
	svc.SetNamespace("openshift-authentication")
	svc.SetName("oauth-openshift")
	return &libraryinputresources.Resource{
		ResourceType: gvr,
		Content:      svc,
	}, nil
}

// openshift-authentication/v4-0-config-system-session
func getSecretOpenshiftAuthenticationConfigSystemSession(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string) (*libraryinputresources.Resource, error) {
	standaloneResourceNamespace := "openshift-authentication"
	standaloneResourceName := "v4-0-config-system-session"
	return getResourceToInputResources(ctx, coreSecretGVR, mgmtKubeClient, controlPlaneNamespace, hcpNameForNamespacedStandaloneResource(standaloneResourceNamespace, standaloneResourceName), standaloneResourceNamespace, standaloneResourceName)
}

// openshift-authentication/v4-0-config-system-cliconfig
func getConfigMapOpenshiftAuthenticationConfigSystemCliconfig(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string) (*libraryinputresources.Resource, error) {
	standaloneResourceNamespace := "openshift-authentication"
	standaloneResourceName := "v4-0-config-system-cliconfig"
	res, err := getResourceToInputResources(ctx, coreConfigMapGVR, mgmtKubeClient, controlPlaneNamespace, hcpNameForNamespacedStandaloneResource(standaloneResourceNamespace, standaloneResourceName), standaloneResourceNamespace, standaloneResourceName)
	if err != nil {
		return nil, err
	}
	if err = revertTransformationsToConfigMapOpenshiftAuthenticationConfigSystemCliconfig(res.Content); err != nil {
		return nil, err
	}
	return res, nil
}

// openshift-authentication/audit
func getConfigMapOpenshiftAuthenticationAudit(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string) (*libraryinputresources.Resource, error) {
	standaloneResourceNamespace := "openshift-authentication"
	standaloneResourceName := "audit"
	return getResourceToInputResources(ctx, coreConfigMapGVR, mgmtKubeClient, controlPlaneNamespace, hcpNameForNamespacedStandaloneResource(standaloneResourceNamespace, standaloneResourceName), standaloneResourceNamespace, standaloneResourceName)
}

// openshift-authentication/v4-0-config-system-serving-cert
//
// more details:
// https://docs.google.com/document/d/1lerQtnLFofoXaO08SX2iYOv0b7pUA0Rn3Q3htxKjnrY/edit?tab=t.0
// Position in the document: 3
func getSecretOpenshiftAuthenticationConfigSystemServingCert(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string) (*libraryinputresources.Resource, error) {
	standaloneResourceNamespace := "openshift-authentication"
	standaloneResourceName := "v4-0-config-system-serving-cert"
	return getResourceToInputResources(ctx, coreSecretGVR, mgmtKubeClient, controlPlaneNamespace, "oauth-server-crt", standaloneResourceNamespace, standaloneResourceName)
}

// openshift-authentication/v4-0-config-system-ocp-branding-template
func getSecretOpenshiftAuthenticationConfigSystemOCPBrandingTemplate(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string) (*libraryinputresources.Resource, error) {
	standaloneResourceNamespace := "openshift-authentication"
	standaloneResourceName := "v4-0-config-system-ocp-branding-template"
	return getResourceToInputResources(ctx, coreSecretGVR, mgmtKubeClient, controlPlaneNamespace, hcpNameForNamespacedStandaloneResource(standaloneResourceNamespace, standaloneResourceName), standaloneResourceNamespace, standaloneResourceName)
}

// openshift-authentication/v4-0-config-system-service-ca
//
// for more details, see:
// https://docs.google.com/document/d/1lerQtnLFofoXaO08SX2iYOv0b7pUA0Rn3Q3htxKjnrY/edit?tab=t.0
//
// TODO-long-term: figure out how to generate/use this resource on HCP.
func getConfigMapOpenshiftAuthenticationConfigSystemServiceCA(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace string) (*libraryinputresources.Resource, error) {
	standaloneResourceNamespace := "openshift-authentication"
	standaloneResourceName := "v4-0-config-system-service-ca"
	return getResourceToInputResources(ctx, coreConfigMapGVR, mgmtKubeClient, controlPlaneNamespace, "oauth-master-ca-bundle", standaloneResourceNamespace, standaloneResourceName)
}

func getResourceToInputResources(ctx context.Context, gvr schema.GroupVersionResource, mgmtKubeClient *dynamic.DynamicClient, controlPlaneNamespace, controlPlaneResourceName, standaloneResourceNamespace, standaloneResourceName string) (*libraryinputresources.Resource, error) {
	unstructuredSecret, err := mgmtKubeClient.Resource(gvr).Namespace(controlPlaneNamespace).Get(ctx, controlPlaneResourceName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	unstructuredSecret.SetNamespace(standaloneResourceNamespace)
	unstructuredSecret.SetName(standaloneResourceName)
	return &libraryinputresources.Resource{
		ResourceType: gvr,
		Content:      unstructuredSecret,
	}, nil
}

func getAndExtractSecretDataNoCopy(ctx context.Context, kubeClient *dynamic.DynamicClient, name string, namespace string) (*unstructured.Unstructured, map[string][]byte, error) {
	unstructuredSecret, err := kubeClient.Resource(coreSecretGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	secretRawData, found, err := unstructured.NestedFieldNoCopy(unstructuredSecret.Object, "data")
	if err != nil {
		return nil, nil, fmt.Errorf("failed reading .Data field from %s/%s secret, err: %w", namespace, name, err)
	}

	secretData := map[string][]byte{}
	if found && secretRawData != nil {
		var ok bool
		secretRawMap, ok := secretRawData.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("unexpected type of data in .Data field for %s/%s secret, expected map[string]interface{}, got: %T", namespace, name, secretRawData)
		}
		for k, v := range secretRawMap {
			strVal, ok := v.(string)
			if !ok {
				return nil, nil, fmt.Errorf("unexpected type stored in %s/%s secret under %s key, expected string, got: %T", namespace, name, k, v)
			}
			secretData[k] = []byte(strVal)
		}
	}

	return unstructuredSecret, secretData, nil
}
