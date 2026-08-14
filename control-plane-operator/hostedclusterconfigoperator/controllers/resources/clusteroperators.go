package resources

import (
	"context"
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type ClusterOperatorInfo struct {
	Name           string
	VersionMapping map[string]string
	RelatedObjects []configv1.ObjectReference
}

var clusterOperators = []ClusterOperatorInfo{
	{
		Name: "openshift-apiserver",
		VersionMapping: map[string]string{
			"operator":            "release",
			"openshift-apiserver": "release",
		},
		RelatedObjects: []configv1.ObjectReference{
			{
				Group:    "operator.openshift.io",
				Resource: "openshiftapiservers",
				Name:     "cluster",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-config",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-config-managed",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-apiserver-operator",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-apiserver",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.apps.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.authorization.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.build.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.image.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.oauth.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.project.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.quota.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.route.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.security.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.template.openshift.io",
				Resource: "apiservices",
			},
			{
				Group:    "apiregistration.k8s.io",
				Name:     "v1.user.openshift.io",
				Resource: "apiservices",
			},
		},
	},
	{
		Name: "openshift-controller-manager",
		VersionMapping: map[string]string{
			"openshift-controller-manager": "release",
			"operator":                     "release",
		},
		RelatedObjects: []configv1.ObjectReference{
			{
				Group:    "operator.openshift.io",
				Resource: "openshiftcontrollermanagers",
				Name:     "cluster",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-config",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-config-managed",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-controller-manager-operator",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-controller-manager",
			},
		},
	},
	{
		Name: "kube-apiserver",
		VersionMapping: map[string]string{
			"raw-internal":   "release",
			"kube-apiserver": "kubernetes",
			"operator":       "release",
		},
		RelatedObjects: []configv1.ObjectReference{
			{
				Group:    "operator.openshift.io",
				Resource: "kubeapiservers",
				Name:     "cluster",
			},
			{
				Group:    "apiextensions.k8s.io",
				Resource: "customresourcedefinitions",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-config",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-config-managed",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-kube-apiserver-operator",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-kube-apiserver",
			},
		},
	},
	{
		Name: "kube-controller-manager",
		VersionMapping: map[string]string{
			"raw-internal":            "release",
			"kube-controller-manager": "kubernetes",
			"operator":                "release",
		},
		RelatedObjects: []configv1.ObjectReference{
			{
				Resource: "namespaces",
				Name:     "openshift-config",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-config-managed",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-kube-controller-manager",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-kube-controller-manager-operator",
			},
			{
				Group:    "operator.openshift.io",
				Resource: "kubecontrollermanagers",
				Name:     "cluster",
			},
		},
	},
	{
		Name: "kube-scheduler",
		VersionMapping: map[string]string{
			"raw-internal":   "release",
			"kube-scheduler": "kubernetes",
			"operator":       "release",
		},
		RelatedObjects: []configv1.ObjectReference{
			{
				Group:    "operator.openshift.io",
				Resource: "kubeschedulers",
				Name:     "cluster",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-config",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-kube-scheduler",
			},
			{
				Resource: "namespaces",
				Name:     "openshift-kube-scheduler-operator",
			},
		},
	},
	{
		Name: "operator-lifecycle-manager-packageserver",
		VersionMapping: map[string]string{
			"operator": "release",
		},
		RelatedObjects: []configv1.ObjectReference{
			{
				Resource: "namespaces",
				Name:     "openshift-operator-lifecycle-manager",
			},
		},
	},
}

func (r *reconciler) reconcileClusterOperators(ctx context.Context) error {
	var errs []error
	for _, info := range clusterOperators {
		clusterOperator := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: info.Name}}
		if _, err := r.CreateOrUpdate(ctx, r.client, clusterOperator, func() error {
			clusterOperator.Status = r.clusterOperatorStatus(info, clusterOperator.Status)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile %T %s: %w", clusterOperator, clusterOperator.Name, err))
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (r *reconciler) clusterOperatorStatus(coInfo ClusterOperatorInfo, currentStatus configv1.ClusterOperatorStatus) configv1.ClusterOperatorStatus {
	status := configv1.ClusterOperatorStatus{}
	versionMappingKeys := make([]string, 0, len(coInfo.VersionMapping))
	for key := range coInfo.VersionMapping {
		versionMappingKeys = append(versionMappingKeys, key)
	}
	sort.Strings(versionMappingKeys)
	for _, key := range versionMappingKeys {
		target := coInfo.VersionMapping[key]
		v, hasVersion := r.versions[target]
		if !hasVersion {
			continue
		}
		status.Versions = append(status.Versions, configv1.OperandVersion{
			Name:    key,
			Version: v,
		})
	}

	now := metav1.Now()
	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:               configv1.OperatorAvailable,
			Status:             configv1.ConditionTrue,
			LastTransitionTime: now,
			Reason:             hyperv1.AsExpectedReason,
		},
		{
			Type:               configv1.OperatorProgressing,
			Status:             configv1.ConditionFalse,
			LastTransitionTime: now,
			Reason:             hyperv1.AsExpectedReason,
		},
		{
			Type:               configv1.OperatorDegraded,
			Status:             configv1.ConditionFalse,
			LastTransitionTime: now,
			Reason:             hyperv1.AsExpectedReason,
		},
		{
			Type:               configv1.OperatorUpgradeable,
			Status:             configv1.ConditionTrue,
			LastTransitionTime: now,
			Reason:             hyperv1.AsExpectedReason,
		},
	}
	for _, condition := range conditions {
		existingCondition := findClusterOperatorStatusCondition(currentStatus.Conditions, condition.Type)
		// Use existing condition if present to keep the LastTransitionTime
		if existingCondition != nil && existingCondition.Status == condition.Status && existingCondition.Reason == condition.Reason {
			condition = *existingCondition
		}

		status.Conditions = append(status.Conditions, condition)
	}
	status.RelatedObjects = coInfo.RelatedObjects
	return status
}

// findClusterOperatorStatusCondition is identical to meta.FindStatusCondition except that it works on config1.ClusterOperatorStatusCondition instead of
// metav1.StatusCondition
func findClusterOperatorStatusCondition(conditions []configv1.ClusterOperatorStatusCondition, conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}
