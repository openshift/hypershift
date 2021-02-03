package clusteroperator

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configlister "github.com/openshift/client-go/config/listers/config/v1"
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
}

var clusterOperatorNames sets.String

func init() {
	clusterOperatorNames = sets.NewString()
	for _, co := range clusterOperators {
		clusterOperatorNames.Insert(co.Name)
	}
}

type ControlPlaneClusterOperatorSyncer struct {
	Client   configclient.Interface
	Lister   configlister.ClusterOperatorLister
	Log      logr.Logger
	Versions map[string]string
}

func (r *ControlPlaneClusterOperatorSyncer) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	if err := r.ensureClusterOperatorsExist(); err != nil {
		return ctrl.Result{}, err
	}
	if !clusterOperatorNames.Has(req.Name) {
		return ctrl.Result{}, nil
	}

	clusterOperator, err := r.Lister.Get(req.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cannot fetch cluster operator %s: %v", req.Name, err)
	}
	err = r.ensureClusterOperatorIsUpToDate(clusterOperator)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cannot update cluster operator %s: %v", req.Name, err)
	}
	return ctrl.Result{}, nil
}

func (r *ControlPlaneClusterOperatorSyncer) ensureClusterOperatorsExist() error {
	existingOperators, err := r.Lister.List(labels.Everything())
	if err != nil {
		return err
	}
	notFound := sets.NewString(clusterOperatorNames.List()...)
	for _, co := range existingOperators {
		if notFound.Has(co.Name) {
			notFound.Delete(co.Name)
		}
	}
	if notFound.Len() == 0 {
		return nil
	}
	errs := []error{}
	for _, coInfo := range clusterOperators {
		if notFound.Has(coInfo.Name) {
			if err := r.createClusterOperator(coInfo); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.NewAggregate(errs)
}

func (r *ControlPlaneClusterOperatorSyncer) ensureClusterOperatorIsUpToDate(co *configv1.ClusterOperator) error {
	coInfo := clusterOperatorInfo(co.Name)
	needsUpdate := false
	expectedStatus := r.clusterOperatorStatus(coInfo)

	now := metav1.Now()
	// Check version info
	for _, operandVersion := range expectedStatus.Versions {
		found := false
		for i, actualOperandVersion := range co.Status.Versions {
			if actualOperandVersion.Name != operandVersion.Name {
				continue
			}
			found = true
			if actualOperandVersion.Version == operandVersion.Version {
				continue
			}
			co.Status.Versions[i].Version = operandVersion.Version
			needsUpdate = true
			break
		}
		if !found {
			co.Status.Versions = append(co.Status.Versions, operandVersion)
			needsUpdate = true
		}
	}

	// Check conditions
	for _, condition := range expectedStatus.Conditions {
		found := false
		for i, actualCondition := range co.Status.Conditions {
			if actualCondition.Type != condition.Type {
				continue
			}
			found = true
			if actualCondition.Status == condition.Status {
				continue
			}
			co.Status.Conditions[i].Status = condition.Status
			co.Status.Conditions[i].Reason = "AsExpected"
			co.Status.Conditions[i].LastTransitionTime = now
			needsUpdate = true
			break
		}
		if !found {
			co.Status.Conditions = append(co.Status.Conditions, condition)
			needsUpdate = true
		}
	}

	// Check related objects
	if !reflect.DeepEqual(expectedStatus.RelatedObjects, co.Status.RelatedObjects) {
		needsUpdate = true
		co.Status.RelatedObjects = expectedStatus.RelatedObjects
	}

	if !needsUpdate {
		return nil
	}
	_, err := r.Client.ConfigV1().ClusterOperators().UpdateStatus(context.TODO(), co, metav1.UpdateOptions{})
	return err
}

func (r *ControlPlaneClusterOperatorSyncer) createClusterOperator(coInfo ClusterOperatorInfo) error {
	co := &configv1.ClusterOperator{}
	co.Name = coInfo.Name
	co, err := r.Client.ConfigV1().ClusterOperators().Create(context.TODO(), co, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create cluster operator %s: %v", coInfo.Name, err)
	}

	co.Status = r.clusterOperatorStatus(coInfo)

	if _, err := r.Client.ConfigV1().ClusterOperators().UpdateStatus(context.TODO(), co, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update cluster operator status for %s: %v", coInfo.Name, err)
	}
	return nil
}

func (r *ControlPlaneClusterOperatorSyncer) clusterOperatorStatus(coInfo ClusterOperatorInfo) configv1.ClusterOperatorStatus {
	status := configv1.ClusterOperatorStatus{}
	for key, target := range coInfo.VersionMapping {
		v, hasVersion := r.Versions[target]
		if !hasVersion {
			continue
		}
		status.Versions = append(status.Versions, configv1.OperandVersion{
			Name:    key,
			Version: v,
		})
	}
	now := metav1.Now()
	status.Conditions = []configv1.ClusterOperatorStatusCondition{
		{
			Type:               configv1.OperatorAvailable,
			Status:             configv1.ConditionTrue,
			LastTransitionTime: now,
			Reason:             "AsExpected",
		},
		{
			Type:               configv1.OperatorProgressing,
			Status:             configv1.ConditionFalse,
			LastTransitionTime: now,
			Reason:             "AsExpected",
		},
		{
			Type:               configv1.OperatorDegraded,
			Status:             configv1.ConditionFalse,
			LastTransitionTime: now,
			Reason:             "AsExpected",
		},
		{
			Type:               configv1.OperatorUpgradeable,
			Status:             configv1.ConditionTrue,
			LastTransitionTime: now,
			Reason:             "AsExpected",
		},
	}
	status.RelatedObjects = coInfo.RelatedObjects
	return status
}

func clusterOperatorInfo(name string) ClusterOperatorInfo {
	for _, coInfo := range clusterOperators {
		if coInfo.Name == name {
			return coInfo
		}
	}
	// should not happen
	return ClusterOperatorInfo{}
}
