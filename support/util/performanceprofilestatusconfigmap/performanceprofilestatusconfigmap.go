package performanceprofilestatusconfigmap

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	performanceprofilev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	"github.com/openshift/hypershift/support/util/performanceprofilestatus"
)

func New(controlPlaneNamespace, userClustersNamespace, nodePoolName string, opts ...func(cm *corev1.ConfigMap) error) (*corev1.ConfigMap, error) {
	cm := getTestPerformanceProfileStatusConfigMap(controlPlaneNamespace, nodePoolName)
	for _, opt := range opts {
		if err := opt(cm); err != nil {
			return nil, err
		}
	}
	return cm, nil
}

func getTestPerformanceProfileStatusConfigMap(controlPlaneNamespace, nodePoolName string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "perfprofile-" + nodePoolName + "-status",
			Namespace: controlPlaneNamespace,
			Labels: map[string]string{
				"hypershift.openshift.io/nto-generated-performance-profile-status": "true",
				"hypershift.openshift.io/nodePool":                                 nodePoolName,
				"hypershift.openshift.io/performanceProfileName":                   nodePoolName,
			},
			Annotations: map[string]string{
				"hypershift.openshift.io/nodePool": nodePoolName,
			},
		},
	}
}

func ExtractConditions(performanceProfileStatusConfigMap *corev1.ConfigMap) ([]conditionsv1.Condition, error) {
	statusRaw, ok := performanceProfileStatusConfigMap.Data["status"]
	if !ok {
		return nil, fmt.Errorf("status not found in performance profile status configmap")
	}
	status := &performanceprofilev2.PerformanceProfileStatus{}

	performanceprofilestatus.DecodeFromYAML([]byte(statusRaw), status)

	return status.Conditions, nil
}

func WithStatus(status *performanceprofilev2.PerformanceProfileStatus) func(*corev1.ConfigMap) error {
	return func(cm *corev1.ConfigMap) error {
		if err := UpdateStatus(cm, status); err != nil {
			return err
		}
		return nil
	}
}

func UpdateStatus(cm *corev1.ConfigMap, status *performanceprofilev2.PerformanceProfileStatus) error {
	encodedStatus, encodeErr := performanceprofilestatus.EncodeToYAML(status)
	if encodeErr != nil {
		return encodeErr
	}
	data := map[string]string{"status": string(encodedStatus)}
	cm.Data = data
	return nil
}
