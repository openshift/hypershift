package omoperator

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/manifestclient"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

var (
	coreConfigMapGVR  = corev1.SchemeGroupVersion.WithResource("configmaps")
	coreSecretGVR     = corev1.SchemeGroupVersion.WithResource("secrets")
	coreDeploymentGVR = appsv1.SchemeGroupVersion.WithResource("deployments")
)

func applyCreateDeploymentOpenshiftAuthenticationOauthOpenshift(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	// TODO: which annotation/labes to remove/add
	//
	// TODO: figure out how to support audit webhook
	// xref: https://github.com/openshift/hypershift/blob/0a313eefcef5a00565506889d14959e6bc33cc2b/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/deployment.go#L38
	//
	// TODO: apply transformations
	// transform "openshift-authentication/v4-0-config-system-serving-cert" to "controlPlaneNamespace/oauth-server-crt"
	// transform "openshift-authentication/v4-0-config-system-service-ca" to "controlPlaneNamespace/oauth-master-ca-bundle"
	// transform serviceAccountName to default (hcp used default!)
	//
	// TODO: figure out how to count the number of replicas
	// TODO: set replica to 1
	//
	// TODO: add KubeadminSecretHashAnnotation ?
	// xref: https://github.com/openshift/hypershift/blob/0a313eefcef5a00565506889d14959e6bc33cc2b/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/deployment.go#L109
	//
	// TODO: inject KonnectivityContainer
	// xref: https://github.com/openshift/hypershift/blob/2f4d9100815315c8f37fdd28b538c24ccbf1eccc/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/component.go#L70
	//
	// TODO: inject AvailabilityProberContainer
	// xref: https://github.com/openshift/hypershift/blob/2f4d9100815315c8f37fdd28b538c24ccbf1eccc/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/component.go#L81C9-L81C36
	//
	// comparison of the oauth-server deployment created by the auth-operator and hcp
	// xref: https://docs.google.com/document/d/1eAgWGbkAzKc9omVHylJiBCa3khjZcWtRsXncKOmzFjo/edit?tab=t.0

	return createUnstructuredResourceForSerializedRequest(ctx, coreDeploymentGVR, mgmtKubeClient, requestToCreate, controlPlaneNamespace)
}

func applyCreateSecretOpenshiftAuthenticationConfigSystemSession(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	// on hcp the session for the oauth-server is stored in
	// controlPlaneNamespace/oauth-openshift-session secret under "v4-0-config-system-session" key
	//
	// xref: https://github.com/openshift/hypershift/blob/2f4d9100815315c8f37fdd28b538c24ccbf1eccc/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/component.go#L54
	//
	// for OM we are going to create a new secret in controlPlaneNamespace
	// under "openshift-authentication--v4-0-config-system-session" name

	return createUnstructuredResourceForSerializedRequest(ctx, coreSecretGVR, mgmtKubeClient, requestToCreate, controlPlaneNamespace)
}

// openshift-authentication/v4-0-config-system-ocp-branding-template
func applyCreateSecretOpenshiftAuthenticationConfigSystemOcpBrandingTemplate(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	// in hcp default templates are stored in the following secrets:
	// - controlPlaneNamespace/oauth-openshift-default-error-template under "data.errors.html" key
	// - controlPlaneNamespace/oauth-openshift-default-login-template under "data.login.html" key
	// - controlPlaneNamespace/oauth-openshift-default-provider-selection-template under "data.providers.html" key
	//
	// for OM we are going to create a new secret in controlPlaneNamespace
	// under "openshift-authentication--v4-0-config-system-ocp-branding-template" name

	return createUnstructuredResourceForSerializedRequest(ctx, coreSecretGVR, mgmtKubeClient, requestToCreate, controlPlaneNamespace)
}

// openshift-authentication/v4-0-config-system-cliconfig
//
// more details:
// https://docs.google.com/document/d/1lerQtnLFofoXaO08SX2iYOv0b7pUA0Rn3Q3htxKjnrY/edit?tab=t.0
// Position in the document: 21
func applyCreateConfigMapOpenshiftAuthenticationConfigSystemCliconfig(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	// on hcp the oauth-server configuration is stored in
	// controlPlaneNamespace/oauth-openshift configmap under "config.yaml" key
	//
	// xref: https://github.com/openshift/hypershift/blob/675f881923cfa312115ba9bd572f39c201bbe689/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L44
	//
	// for OM we are going to create a new configmap in controlPlaneNamespace
	// under "openshift-authentication--v4-0-config-system-cliconfig" name
	//
	// TODO: figure out which transformations are required, for example:
	// xref: https://github.com/openshift/hypershift/blob/675f881923cfa312115ba9bd572f39c201bbe689/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L63
	//
	// - namedCerts, xref: https://github.com/openshift/hypershift/blob/675f881923cfa312115ba9bd572f39c201bbe689/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L66
	//   in standalone custom certs come from "v4-0-config-system-custom-router-certs" or "v4-0-config-system-router-certs"" secrets
	//   xref: https://github.com/openshift/cluster-authentication-operator/blob/7c29d664bd571ce5f8e99456a206584651d200a7/pkg/controllers/configobservation/routersecret/observe_router_secret.go#L17
	//
	// - login-url-overrides, xref: https://github.com/openshift/hypershift/blob/675f881923cfa312115ba9bd572f39c201bbe689/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L79
	//
	// - verify masterURL because it seems to be matching masterPublicURL
	//   xref: https://github.com/openshift/hypershift/blob/675f881923cfa312115ba9bd572f39c201bbe689/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L73
	//   on standalone masterURL points to oauth-server service
	//   xref: https://github.com/openshift/cluster-authentication-operator/blob/817783a52d042f4ac3aa8faac7421ac013b42481/pkg/controllers/payload/payload_config_controller.go#L228
	//
	// - any other transformations?
	// TODO: fix me

	unstructuredConfigSystemCliConfig, err := decodeIndividualObj(requestToCreate.GetSerializedRequest().Body)
	if err != nil {
		return err
	}

	if err = applyTransformationsToConfigMapOpenshiftAuthenticationConfigSystemCliconfig(unstructuredConfigSystemCliConfig); err != nil {
		return err
	}

	opts, err := getCreateOptionsFromSerializedRequest(requestToCreate.GetSerializedRequest())
	if err != nil {
		return err
	}

	return createUnstructuredResource(ctx, coreConfigMapGVR, mgmtKubeClient, unstructuredConfigSystemCliConfig, opts, controlPlaneNamespace)
}

func applyCreateConfigMapOpenshiftAuthenticationAudit(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	// for OM we are going to create a new secret in controlPlaneNamespace
	// under "openshift-authentication--audit" name

	return createUnstructuredResourceForSerializedRequest(ctx, coreConfigMapGVR, mgmtKubeClient, requestToCreate, controlPlaneNamespace)
}

func applyUpdateConfigMapOpenshiftAuthenticationConfigSystemCliconfig(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToUpdate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	unstructuredConfigSystemCliConfig, err := decodeIndividualObj(requestToUpdate.GetSerializedRequest().Body)
	if err != nil {
		return err
	}

	if err = applyTransformationsToConfigMapOpenshiftAuthenticationConfigSystemCliconfig(unstructuredConfigSystemCliConfig); err != nil {
		return err
	}

	opts, err := getUpdateOptionsFromSerializedRequest(requestToUpdate.GetSerializedRequest())
	if err != nil {
		return err
	}
	return updateUnstructuredResource(ctx, coreConfigMapGVR, mgmtKubeClient, unstructuredConfigSystemCliConfig, opts, controlPlaneNamespace)
}

func applyUpdateSecretOpenshiftAuthenticationConfigSystemOCPBrandingTemplate(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToUpdate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	return updateUnstructuredResourceForSerializedRequest(ctx, coreSecretGVR, mgmtKubeClient, requestToUpdate, controlPlaneNamespace)
}

func applyUpdateSecretOpenshiftAuthenticationConfigSystemSession(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToUpdate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	return updateUnstructuredResourceForSerializedRequest(ctx, coreConfigMapGVR, mgmtKubeClient, requestToUpdate, controlPlaneNamespace)
}

func createUnstructuredResourceForSerializedRequest(ctx context.Context, gvr schema.GroupVersionResource, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	unstructuredObject, err := decodeIndividualObj(requestToCreate.GetSerializedRequest().Body)
	if err != nil {
		return err
	}
	unstructuredObject.SetName(hcpNameForNamespacedStandaloneResource(unstructuredObject.GetNamespace(), unstructuredObject.GetName()))
	unstructuredObject.SetNamespace(controlPlaneNamespace)

	opts, err := getCreateOptionsFromSerializedRequest(requestToCreate.GetSerializedRequest())
	if err != nil {
		return err
	}

	_, err = mgmtKubeClient.Resource(gvr).Namespace(controlPlaneNamespace).Create(ctx, unstructuredObject, opts)
	return err
}

func createUnstructuredResource(ctx context.Context, gvr schema.GroupVersionResource, mgmtKubeClient *dynamic.DynamicClient, unstructuredObject *unstructured.Unstructured, opts metav1.CreateOptions, controlPlaneNamespace string) error {
	unstructuredObject.SetName(hcpNameForNamespacedStandaloneResource(unstructuredObject.GetNamespace(), unstructuredObject.GetName()))
	unstructuredObject.SetNamespace(controlPlaneNamespace)

	_, err := mgmtKubeClient.Resource(gvr).Namespace(controlPlaneNamespace).Create(ctx, unstructuredObject, opts)
	return err
}

func updateUnstructuredResourceForSerializedRequest(ctx context.Context, gvr schema.GroupVersionResource, mgmtKubeClient *dynamic.DynamicClient, requestToUpdate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	unstructuredObject, err := decodeIndividualObj(requestToUpdate.GetSerializedRequest().Body)
	if err != nil {
		return err
	}

	opts, err := getUpdateOptionsFromSerializedRequest(requestToUpdate.GetSerializedRequest())
	if err != nil {
		return err
	}

	return updateUnstructuredResource(ctx, gvr, mgmtKubeClient, unstructuredObject, opts, controlPlaneNamespace)
}

func updateUnstructuredResource(ctx context.Context, gvr schema.GroupVersionResource, mgmtKubeClient *dynamic.DynamicClient, unstructuredObject *unstructured.Unstructured, opts metav1.UpdateOptions, controlPlaneNamespace string) error {
	unstructuredObject.SetName(hcpNameForNamespacedStandaloneResource(unstructuredObject.GetNamespace(), unstructuredObject.GetName()))
	unstructuredObject.SetNamespace(controlPlaneNamespace)

	_, err := mgmtKubeClient.Resource(gvr).Namespace(controlPlaneNamespace).Update(ctx, unstructuredObject, opts)
	return err
}

// TODO:figure out the best naming scheme for namespaced resources
// operator-name--namespace--name
func hcpNameForNamespacedStandaloneResource(standaloneNamespace, standaloneName string) string {
	return standaloneNamespace + "--" + standaloneName
}

func getUpdateOptionsFromSerializedRequest(request manifestclient.SerializedRequestish) (metav1.UpdateOptions, error) {
	opts := &metav1.UpdateOptions{}
	err := yaml.Unmarshal(request.GetSerializedRequest().Options, opts)
	if err != nil {
		return metav1.UpdateOptions{}, fmt.Errorf("unable to decode options: %w", err)
	}
	return *opts, nil
}

func getCreateOptionsFromSerializedRequest(request manifestclient.SerializedRequestish) (metav1.CreateOptions, error) {
	opts := &metav1.CreateOptions{}
	err := yaml.Unmarshal(request.GetSerializedRequest().Options, opts)
	if err != nil {
		return metav1.CreateOptions{}, fmt.Errorf("unable to decode options: %w", err)
	}
	return *opts, nil
}
