package omoperator

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/manifestclient"

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

func applyCreateOauthDeployment(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	// TODO: apply transformations
	// transform "openshift-authentication/v4-0-config-system-serving-cert" to "controlPlaneNamespace/oauth-server-crt"
	// transform "openshift-authentication/v4-0-config-system-service-ca" to "controlPlaneNamespace/oauth-master-ca-bundle"

	return createUnstructuredResourceForSerializedRequest(ctx, coreDeploymentGVR, mgmtKubeClient, requestToCreate, controlPlaneNamespace)
}

func applyCreateOauthConfigSystemSessionSecret(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	// on hcp the session for the oauth-server is stored in
	// controlPlaneNamespace/oauth-openshift-session secret under "v4-0-config-system-session" key
	//
	// xref: https://github.com/openshift/hypershift/blob/2f4d9100815315c8f37fdd28b538c24ccbf1eccc/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/component.go#L54
	//
	// for OM we are going to create a new secret in controlPlaneNamespace
	// under "openshift-authentication-v4-0-config-system-session" name

	return createUnstructuredResourceForSerializedRequest(ctx, coreSecretGVR, mgmtKubeClient, requestToCreate, controlPlaneNamespace)
}

func applyUpdateToOauthConfigSystemSessionSecret(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToUpdate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	return updateUnstructuredResourceForSerializedRequest(ctx, coreSecretGVR, mgmtKubeClient, requestToUpdate, controlPlaneNamespace)
}

func applyCreateOauthConfigSystemCliconfigConfigMap(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	// on hcp the oauth-server configuration is stored in
	// controlPlaneNamespace/oauth-openshift configmap under "config.yaml" key
	//
	// xref: https://github.com/openshift/hypershift/blob/675f881923cfa312115ba9bd572f39c201bbe689/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L44
	//
	// for OM we are going to create a new configmap in controlPlaneNamespace
	// under "openshift-authentication-v4-0-config-system-cliconfig" name
	//
	// TODO: figure out how to wire
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

	return createUnstructuredResourceForSerializedRequest(ctx, coreConfigMapGVR, mgmtKubeClient, requestToCreate, controlPlaneNamespace)
}

func applyUpdateToOauthConfigSystemCliconfigConfigMap(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, requestToUpdate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	return updateUnstructuredResourceForSerializedRequest(ctx, coreConfigMapGVR, mgmtKubeClient, requestToUpdate, controlPlaneNamespace)
}

func applyActionToOperatorAuthentication(ctx context.Context, actionType manifestclient.Action, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, requestToApply manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	unstructuredAuthOperator, err := decodeIndividualObj(requestToApply.GetSerializedRequest().Body)
	if err != nil {
		return err
	}

	var appliedUnstructuredAuthOperator *unstructured.Unstructured
	gvr := schema.GroupVersionResource{Group: operatorv1.SchemeGroupVersion.Group, Version: operatorv1.SchemeGroupVersion.Version, Resource: "authentications"}

	// first apply the action on the guest cluster
	switch actionType {
	case manifestclient.ActionUpdate:
		opts := &metav1.UpdateOptions{}
		err = yaml.Unmarshal(requestToApply.GetSerializedRequest().Options, opts)
		if err != nil {
			return fmt.Errorf("unable to decode options: %w", err)
		}
		appliedUnstructuredAuthOperator, err = guestClusterKubeClient.Resource(gvr).Update(ctx, unstructuredAuthOperator, *opts)
		if err != nil {
			return err
		}
	case manifestclient.ActionApplyStatus:
		opts := &metav1.ApplyOptions{}
		err = yaml.Unmarshal(requestToApply.GetSerializedRequest().Options, opts)
		if err != nil {
			return fmt.Errorf("unable to decode options: %w", err)
		}
		appliedUnstructuredAuthOperator, err = guestClusterKubeClient.Resource(gvr).ApplyStatus(ctx, "cluster", unstructuredAuthOperator, *opts)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported action type %v on %v resource", actionType, gvr)
	}

	authOperatorYaml, err := serializeUnstructuredObjToYAML(appliedUnstructuredAuthOperator)
	if err != nil {
		return err
	}

	// preserve the content of the openshift authentications in a namespaced configmap
	return getAndUpdateUnstructuredConfigMapData(ctx, mgmtKubeClient, operatorAuthenticationConfigMapName, controlPlaneNamespace, func(configMapData map[string]string) {
		configMapData["cluster.yaml"] = authOperatorYaml
	})
}

func getAndExtractConfigMapData(ctx context.Context, kubeClient *dynamic.DynamicClient, name string, namespace string) (*unstructured.Unstructured, map[string]string, error) {
	unstructuredConfigMap, err := kubeClient.Resource(coreConfigMapGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	configMapData, found, err := unstructured.NestedStringMap(unstructuredConfigMap.Object, "data")
	if err != nil {
		return nil, nil, fmt.Errorf("failed reading .Data field from %s/%s configmap, err: %w", namespace, name, err)
	}
	if !found || configMapData == nil {
		configMapData = map[string]string{}
	}

	return unstructuredConfigMap, configMapData, nil
}

func getAndUpdateUnstructuredConfigMapData(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, name string, namespace string, configMapDataUpdateFn func(map[string]string)) error {
	unstructuredConfigMap, configMapData, err := getAndExtractConfigMapData(ctx, mgmtKubeClient, name, namespace)
	if err != nil {
		return err
	}

	configMapDataUpdateFn(configMapData)

	if err = unstructured.SetNestedStringMap(unstructuredConfigMap.Object, configMapData, "data"); err != nil {
		return fmt.Errorf("failed setting .Data field for %s/%s configmap, err: %w", namespace, name, err)
	}

	_, err = mgmtKubeClient.Resource(coreConfigMapGVR).Namespace(namespace).Update(ctx, unstructuredConfigMap, metav1.UpdateOptions{})
	return err
}

func createUnstructuredResourceForSerializedRequest(ctx context.Context, gvr schema.GroupVersionResource, mgmtKubeClient *dynamic.DynamicClient, requestToCreate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	unstructuredObject, err := decodeIndividualObj(requestToCreate.GetSerializedRequest().Body)
	if err != nil {
		return err
	}
	unstructuredObject.SetName(hcpNameForNamespacedStandaloneResource(unstructuredObject.GetNamespace(), unstructuredObject.GetName()))
	unstructuredObject.SetNamespace(controlPlaneNamespace)

	opts := &metav1.CreateOptions{}
	err = yaml.Unmarshal(requestToCreate.GetSerializedRequest().Options, opts)
	if err != nil {
		return fmt.Errorf("unable to decode options: %w", err)
	}

	_, err = mgmtKubeClient.Resource(gvr).Namespace(controlPlaneNamespace).Create(ctx, unstructuredObject, *opts)
	return err
}

func updateUnstructuredResourceForSerializedRequest(ctx context.Context, gvr schema.GroupVersionResource, mgmtKubeClient *dynamic.DynamicClient, requestToUpdate manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
	unstructuredObject, err := decodeIndividualObj(requestToUpdate.GetSerializedRequest().Body)
	if err != nil {
		return err
	}
	unstructuredObject.SetName(hcpNameForNamespacedStandaloneResource(unstructuredObject.GetNamespace(), unstructuredObject.GetName()))
	unstructuredObject.SetNamespace(controlPlaneNamespace)

	opts := &metav1.UpdateOptions{}
	err = yaml.Unmarshal(requestToUpdate.GetSerializedRequest().Options, opts)
	if err != nil {
		return fmt.Errorf("unable to decode options: %w", err)
	}

	_, err = mgmtKubeClient.Resource(gvr).Namespace(controlPlaneNamespace).Update(ctx, unstructuredObject, *opts)
	return err
}

// TODO:figure out the best naming scheme
// for namespaced resources
func hcpNameForNamespacedStandaloneResource(standaloneNamespace, standaloneName string) string {
	return standaloneNamespace + "-" + standaloneName
}
