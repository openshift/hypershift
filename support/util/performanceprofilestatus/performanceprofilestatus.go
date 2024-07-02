package performanceprofilestatus

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	performanceprofilev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	"gopkg.in/yaml.v2"
)

func Available() *performanceprofilev2.PerformanceProfileStatus {
	lastHeartbeatTime := "2024-04-18T06:55:45Z"
	lastTransitionTime := "2024-04-18T06:55:45Z"

	heartbeatTime, _ := time.Parse(time.RFC3339, lastHeartbeatTime)
	transitionTime, _ := time.Parse(time.RFC3339, lastTransitionTime)

	conditions := []conditionsv1.Condition{
		{
			LastHeartbeatTime:  metav1.Time{Time: heartbeatTime},
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Message:            "cgroup=v1;",
			Status:             corev1.ConditionTrue,
			Type:               conditionsv1.ConditionAvailable,
		},
		{
			LastHeartbeatTime:  metav1.Time{Time: heartbeatTime},
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionTrue,
			Type:               conditionsv1.ConditionUpgradeable,
		},
		{
			LastHeartbeatTime:  metav1.Time{Time: heartbeatTime},
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionFalse,
			Type:               conditionsv1.ConditionProgressing,
		},
		{
			LastHeartbeatTime:  metav1.Time{Time: heartbeatTime},
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionFalse,
			Type:               conditionsv1.ConditionDegraded,
		},
	}

	runtimeClass := "performance-performance"
	tuned := "openshift-cluster-node-tuning-operator/openshift-node-performance-performance"

	return &performanceprofilev2.PerformanceProfileStatus{
		Conditions:   conditions,
		Tuned:        &tuned,
		RuntimeClass: &runtimeClass,
	}
}

func Progressing() *performanceprofilev2.PerformanceProfileStatus {
	lastTransitionTime := "2024-04-18T06:55:45Z"
	transitionTime, _ := time.Parse(time.RFC3339, lastTransitionTime)

	conditions := []conditionsv1.Condition{
		{
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionFalse,
			Type:               conditionsv1.ConditionAvailable,
		},
		{
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionFalse,
			Type:               conditionsv1.ConditionUpgradeable,
		},
		{
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionTrue,
			Type:               conditionsv1.ConditionProgressing,
			Reason:             "DeploymentStarting",
			Message:            "Deployment is starting",
		},
		{
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionFalse,
			Type:               conditionsv1.ConditionDegraded,
		},
	}

	runtimeClass := "performance-performance"
	tuned := "openshift-cluster-node-tuning-operator/openshift-node-performance-performance"

	return &performanceprofilev2.PerformanceProfileStatus{
		Conditions:   conditions,
		RuntimeClass: &runtimeClass,
		Tuned:        &tuned,
	}
}

func Degraded() *performanceprofilev2.PerformanceProfileStatus {
	lastHeartbeatTime := "2024-04-18T06:55:45Z"
	lastTransitionTime := "2024-04-18T06:55:45Z"

	heartbeatTime, _ := time.Parse(time.RFC3339, lastHeartbeatTime)
	transitionTime, _ := time.Parse(time.RFC3339, lastTransitionTime)

	conditions := []conditionsv1.Condition{
		{
			LastHeartbeatTime:  metav1.Time{Time: heartbeatTime},
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionFalse,
			Type:               conditionsv1.ConditionAvailable,
		},
		{
			LastHeartbeatTime:  metav1.Time{Time: heartbeatTime},
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionFalse,
			Type:               conditionsv1.ConditionUpgradeable,
		},
		{
			LastHeartbeatTime:  metav1.Time{Time: heartbeatTime},
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionFalse,
			Type:               conditionsv1.ConditionProgressing,
		},
		{
			LastHeartbeatTime:  metav1.Time{Time: heartbeatTime},
			LastTransitionTime: metav1.Time{Time: transitionTime},
			Status:             corev1.ConditionTrue,
			Type:               conditionsv1.ConditionDegraded,
			Reason:             "GettingTunedStatusFailed",
			Message:            "Cannot list Tuned Profiles to match with profile perfprofile-hostedcluster01",
		},
	}

	runtimeClass := "performance-performance"
	tuned := "openshift-cluster-node-tuning-operator/openshift-node-performance-performance"

	return &performanceprofilev2.PerformanceProfileStatus{
		Conditions:   conditions,
		RuntimeClass: &runtimeClass,
		Tuned:        &tuned,
	}
}

// EncodeToYAML encodes the PerformanceProfileStatus to YAML format.
func EncodeToYAML(status *performanceprofilev2.PerformanceProfileStatus) ([]byte, error) {
	yamlData, err := yaml.Marshal(status)
	if err != nil {
		return nil, err
	}
	return yamlData, nil
}

// DecodeFromYAML decodes the YAML data into a PerformanceProfileStatus.
func DecodeFromYAML(yamlData []byte, status *performanceprofilev2.PerformanceProfileStatus) error {
	err := yaml.Unmarshal(yamlData, status)
	if err != nil {
		return err
	}
	return nil
}
