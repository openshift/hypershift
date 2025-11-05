package omoperator

import (
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const sourceContentAnnotation = "om.openshift.io/source-content"

func applyTransformationsToConfigMapOpenshiftAuthenticationConfigSystemCliconfig(unstructuredConfigSystemCliConfig *unstructured.Unstructured) error {
	unstructuredConfigSystemCliConfigData, err := extractConfigMapDataNoCopy(unstructuredConfigSystemCliConfig)
	if err != nil {
		return err
	}

	// preserve the original content in well known annotation
	originalConfigSystemCliConfigStr := unstructuredConfigSystemCliConfigData["v4-0-config-system-cliconfig"]

	configSystemCliConfigAnnotations := unstructuredConfigSystemCliConfig.GetAnnotations()
	if configSystemCliConfigAnnotations == nil {
		configSystemCliConfigAnnotations = make(map[string]string)
	}
	configSystemCliConfigAnnotations[sourceContentAnnotation] = originalConfigSystemCliConfigStr
	unstructuredConfigSystemCliConfig.SetAnnotations(configSystemCliConfigAnnotations)

	// apply known transformations for the POC
	unstructuredNestedConfigSystemCliConfig, err := decodeIndividualObj([]byte(unstructuredConfigSystemCliConfigData["v4-0-config-system-cliconfig"]))
	if err != nil {
		return err
	}
	if err = unstructured.SetNestedField(unstructuredNestedConfigSystemCliConfig.Object, "/etc/kubernetes/secrets/kubeconfig/kubeconfig", "kubeClientConfig", "kubeConfig"); err != nil {
		return err
	}
	masterPublicURL, found, err := unstructured.NestedString(unstructuredNestedConfigSystemCliConfig.Object, "oauthConfig", "masterPublicURL")
	if err != nil {
		return fmt.Errorf("reading masterPublicURL: %w", err)
	}
	if !found {
		return fmt.Errorf("field oauthConfig.masterPublicURL not found")
	}
	if err = unstructured.SetNestedField(unstructuredNestedConfigSystemCliConfig.Object, masterPublicURL, "oauthConfig", "masterURL"); err != nil {
		return err
	}
	serializedConfigSystemCliConfig, err := serializeIndividualObjToJSON(unstructuredNestedConfigSystemCliConfig)
	if err != nil {
		return err
	}

	unstructuredConfigSystemCliConfigData["v4-0-config-system-cliconfig"] = serializedConfigSystemCliConfig
	return unstructured.SetNestedStringMap(unstructuredConfigSystemCliConfig.Object, unstructuredConfigSystemCliConfigData, "data")
}

func revertTransformationsToConfigMapOpenshiftAuthenticationConfigSystemCliconfig(unstructuredConfigSystemCliConfig *unstructured.Unstructured) error {
	configSystemCliConfigAnnotations := unstructuredConfigSystemCliConfig.GetAnnotations()
	if configSystemCliConfigAnnotations == nil {
		return nil
	}
	originalConfigSystemCliConfigStr, ok := configSystemCliConfigAnnotations[sourceContentAnnotation]
	if !ok {
		return nil
	}

	unstructuredConfigSystemCliConfigData, err := extractConfigMapDataNoCopy(unstructuredConfigSystemCliConfig)
	if err != nil {
		return err
	}

	unstructuredConfigSystemCliConfigData["v4-0-config-system-cliconfig"] = originalConfigSystemCliConfigStr

	return unstructured.SetNestedStringMap(unstructuredConfigSystemCliConfig.Object, unstructuredConfigSystemCliConfigData, "data")
}
