package reconcilerpolicy

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/util"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// EnableHostedClustersAnnotationScopingEnv is the env var that enables annotation-based scoping for hosted clusters.
	EnableHostedClustersAnnotationScopingEnv = "ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING"
	// HostedClustersScopeAnnotationEnv is the env var that specifies the scope annotation value to match.
	HostedClustersScopeAnnotationEnv = "HOSTEDCLUSTERS_SCOPE_ANNOTATION"
	// HostedClustersScopeAnnotation is the annotation key used to scope hosted clusters to specific operators.
	HostedClustersScopeAnnotation = "hypershift.openshift.io/scope"
)

// PredicatesForHostedClusterAnnotationScoping returns predicate filters for all event types that will ignore incoming
// event requests for resources in which the parent hostedcluster does not
// match the "scope" annotation specified in the HOSTEDCLUSTERS_SCOPE_ANNOTATION env var. If not defined or empty, the
// default behavior is to accept all events for hostedclusters that do not have the annotation.
// The ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING env var must also be set to "true" to enable the scoping feature.
func PredicatesForHostedClusterAnnotationScoping(r client.Reader) predicate.Predicate {
	hcAnnotationScopingEnabledEnvVal := os.Getenv(EnableHostedClustersAnnotationScopingEnv)
	hcScopeAnnotationEnvVal := os.Getenv(HostedClustersScopeAnnotationEnv)
	filter := func(obj client.Object) bool {
		if hcAnnotationScopingEnabledEnvVal != "true" {
			return true // process event; the scoping feature has not been enabled via the ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING env var
		}
		hostedClusterScopeAnnotation := getHostedClusterScopeAnnotation(obj, r)
		if hostedClusterScopeAnnotation == "" && hcScopeAnnotationEnvVal == "" {
			return true // process event; both the operator's scope and hostedcluster's scope are empty
		}
		if hostedClusterScopeAnnotation != hcScopeAnnotationEnvVal {
			return false // ignore event; the associated hostedcluster's scope annotation does not match what is defined in HOSTEDCLUSTERS_SCOPE_ANNOTATION
		}
		return true
	}
	return predicate.NewPredicateFuncs(filter)
}

// getHostedClusterScopeAnnotation will extract the "scope" annotation from the hostedcluster resource that owns the specified object.
// Depending on the object type being passed in, slightly different paths will be used to ultimately retrieve the hostedcluster resource containing the annotation.
// If an annotation is not found, an empty string is returned.
func getHostedClusterScopeAnnotation(obj client.Object, r client.Reader) string {
	hostedClusterName := ""
	nodePoolName := ""
	switch obj := obj.(type) {
	case *hyperv1.HostedCluster:
		if obj.GetAnnotations() != nil {
			return obj.GetAnnotations()[HostedClustersScopeAnnotation]
		}
	case *hyperv1.NodePool:
		hostedClusterName = fmt.Sprintf("%s/%s", obj.Namespace, obj.Spec.ClusterName)
	default:
		if obj.GetAnnotations() != nil {
			nodePoolName = obj.GetAnnotations()["hypershift.openshift.io/nodePool"]
			hostedClusterName = obj.GetAnnotations()[k8sutil.HostedClusterAnnotation]
		}
		if nodePoolName != "" {
			namespacedName := util.ParseNamespacedName(nodePoolName)
			np := &hyperv1.NodePool{}
			err := r.Get(context.Background(), namespacedName, np)
			if err != nil {
				return ""
			}
			hostedClusterName = fmt.Sprintf("%s/%s", np.Namespace, np.Spec.ClusterName)
		}
	}
	if hostedClusterName == "" {
		return ""
	}
	namespacedName := util.ParseNamespacedName(hostedClusterName)
	hcluster := &hyperv1.HostedCluster{}
	err := r.Get(context.Background(), namespacedName, hcluster)
	if err != nil {
		return ""
	}
	if hcluster.GetAnnotations() != nil {
		return hcluster.GetAnnotations()[HostedClustersScopeAnnotation]
	}
	return ""
}
