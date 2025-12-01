package omoperator

import (
	"context"
	"fmt"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/manifestclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// operator.openshift.io/authentications/cluster
func applyActionOperatorAuthenticationCluster(ctx context.Context, actionType manifestclient.Action, mgmtKubeClient, guestClusterKubeClient *dynamic.DynamicClient, requestToApply manifestclient.SerializedRequestish, controlPlaneNamespace string) error {
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

func getAndExtractConfigMapDataNoCopy(ctx context.Context, kubeClient *dynamic.DynamicClient, name string, namespace string) (*unstructured.Unstructured, map[string]string, error) {
	unstructuredConfigMap, err := kubeClient.Resource(coreConfigMapGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	configMapData, err := extractConfigMapDataNoCopy(unstructuredConfigMap)
	return unstructuredConfigMap, configMapData, err
}

func extractConfigMapDataNoCopy(unstructuredConfigMap *unstructured.Unstructured) (map[string]string, error) {
	configMapRawData, found, err := unstructured.NestedFieldNoCopy(unstructuredConfigMap.Object, "data")
	if err != nil {
		return nil, fmt.Errorf("failed reading .Data field from %s/%s configmap, err: %w", unstructuredConfigMap.GetNamespace(), unstructuredConfigMap.GetName(), err)
	}

	configMapData := map[string]string{}
	if found && configMapRawData != nil {
		var ok bool
		configMapRawMap, ok := configMapRawData.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("unexpected type of data in .Data field for %s/%s configmap, expected map[string]interface{}, got: %T", unstructuredConfigMap.GetNamespace(), unstructuredConfigMap.GetName(), configMapRawData)
		}
		for k, v := range configMapRawMap {
			strVal, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("unexpected type stored in %s/%s configmap under %s key, expected string, got: %T", unstructuredConfigMap.GetNamespace(), unstructuredConfigMap.GetName(), k, v)
			}
			configMapData[k] = strVal
		}
	}

	return configMapData, nil
}

func getAndUpdateUnstructuredConfigMapData(ctx context.Context, mgmtKubeClient *dynamic.DynamicClient, name string, namespace string, configMapDataUpdateFn func(map[string]string)) error {
	unstructuredConfigMap, configMapData, err := getAndExtractConfigMapDataNoCopy(ctx, mgmtKubeClient, name, namespace)
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
