package reqserving

import (
	"context"
	"fmt"
	"testing"
	"time"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/client-go/discovery"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

const (
	vpaNamespaceName   = "openshift-vertical-pod-autoscaler"
	vpaOperatorGroup   = "vpa-operator-group"
	vpaSubscription    = "vertical-pod-autoscaler"
	vpaCatalogSource   = "redhat-operators"
	vpaCatalogNS       = "openshift-marketplace"
	vpaChannel         = "stable"
	vpaRecommenderName = "vpa-recommender-default"
)

// EnsureVPAOperatorInstalled installs (or verifies) the VPA via OLM.
// It ensures:
// - Namespace openshift-vertical-pod-autoscaler exists
// - OperatorGroup exists
// - Subscription to redhat-operators on stable channel exists
// - CRD verticalpodautoscalers.autoscaling.k8s.io is served
// - vpa-recommender-default deployment is Available
// - VerticalPodAutoscalerController 'default' is configured with:
//   - spec.recommendationOnly=true
//   - spec.deploymentOverrides.recommender.container.args set for e2e test requirements
func EnsureVPAOperatorInstalled(ctx context.Context, t *testing.T, c crclient.Client) error {
	t.Helper()
	t.Logf("[VPA] Starting install/verification via OLM")

	// 1) Ensure namespace
	t.Logf("[VPA] Ensuring namespace %q exists", vpaNamespaceName)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: vpaNamespaceName}}
	if err := createIfNotExists(ctx, c, ns); err != nil {
		return fmt.Errorf("ensuring VPA namespace: %w", err)
	}
	t.Logf("[VPA] Namespace ensured")

	// 2) Ensure OperatorGroup
	t.Logf("[VPA] Ensuring OperatorGroup %q", vpaOperatorGroup)
	og := &operatorsv1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vpaOperatorGroup,
			Namespace: vpaNamespaceName,
		},
		Spec: operatorsv1.OperatorGroupSpec{
			// VPA operator only supports OwnNamespace install mode; target its own namespace
			TargetNamespaces: []string{vpaNamespaceName},
		},
	}
	if err := createIfNotExists(ctx, c, og); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("ensuring VPA OperatorGroup: %w", err)
	}
	// If the OperatorGroup already existed, ensure it's configured to watch only its own namespace
	existingOG := &operatorsv1.OperatorGroup{}
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(og), existingOG); err == nil {
		original := existingOG.DeepCopy()
		existingOG.Spec.TargetNamespaces = []string{vpaNamespaceName}
		if err := c.Patch(ctx, existingOG, crclient.MergeFrom(original)); err != nil {
			t.Logf("[VPA] Warning: failed to patch OperatorGroup targetNamespaces: %v", err)
		} else {
			t.Logf("[VPA] OperatorGroup configured to target only %q", vpaNamespaceName)
		}
	}
	t.Logf("[VPA] OperatorGroup ensured")

	// 3) Ensure Subscription
	t.Logf("[VPA] Ensuring Subscription %q (source=%s/%s, channel=%s)", vpaSubscription, vpaCatalogNS, vpaCatalogSource, vpaChannel)
	sub := &operatorsv1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vpaSubscription,
			Namespace: vpaNamespaceName,
		},
		Spec: &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          vpaCatalogSource,
			CatalogSourceNamespace: vpaCatalogNS,
			Channel:                vpaChannel,
			InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
			Package:                vpaSubscription,
		},
	}
	if err := createIfNotExists(ctx, c, sub); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("ensuring VPA Subscription: %w", err)
	}
	t.Logf("[VPA] Subscription ensured")

	// 4) Wait for CSV to succeed (best-effort, falls back to CRD/deploy checks)
	t.Logf("[VPA] Waiting for CSV to reach Succeeded (best-effort)")
	if err := waitForCSV(ctx, c); err != nil {
		t.Logf("warning: waiting for CSV failed: %v (continuing with CRD/deployment checks)", err)
	} else {
		t.Logf("[VPA] CSV reported Succeeded")
	}

	// 5) Wait for CRD to be served
	t.Logf("[VPA] Waiting for VPA CRD resources to be served")
	if err := waitForVPAResource(ctx); err != nil {
		return fmt.Errorf("waiting for VPA CRD served: %w", err)
	}
	t.Logf("[VPA] VPA CRD served")

	// 6) Wait for recommender Deployment Available
	t.Logf("[VPA] Waiting for %q deployment to be Available", vpaRecommenderName)
	e2eutil.WaitForDeploymentAvailable(ctx, t, c, vpaRecommenderName, vpaNamespaceName, 15*time.Minute, 10*time.Second)
	t.Logf("[VPA] %q deployment is Available", vpaRecommenderName)

	// 7) Configure VerticalPodAutoscalerController for e2e test requirements
	t.Logf("[VPA] Configuring VerticalPodAutoscalerController 'default' for e2e test")
	if err := configureVPAController(ctx, c, t); err != nil {
		return fmt.Errorf("configuring VPA Controller: %w", err)
	}
	t.Logf("[VPA] VerticalPodAutoscalerController configured")

	t.Logf("[VPA] Install/verification complete")
	return nil
}

func createIfNotExists(ctx context.Context, c crclient.Client, obj crclient.Object) error {
	err := c.Create(ctx, obj)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func waitForCSV(ctx context.Context, c crclient.Client) error {
	// Poll the Subscription for installed CSV, then the CSV phase
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		sub := &operatorsv1alpha1.Subscription{}
		if err := c.Get(ctx, crclient.ObjectKey{Namespace: vpaNamespaceName, Name: vpaSubscription}, sub); err != nil {
			return false, crclient.IgnoreNotFound(err)
		}
		if sub.Status.InstalledCSV == "" {
			return false, nil
		}
		csv := &operatorsv1alpha1.ClusterServiceVersion{}
		if err := c.Get(ctx, crclient.ObjectKey{Namespace: vpaNamespaceName, Name: sub.Status.InstalledCSV}, csv); err != nil {
			return false, crclient.IgnoreNotFound(err)
		}
		if csv.Status.Phase == operatorsv1alpha1.CSVPhaseSucceeded {
			return true, nil
		}
		return false, nil
	})
}

func waitForVPAResource(ctx context.Context) error {
	cfg, err := e2eutil.GetConfig()
	if err != nil {
		return err
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return err
	}
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		// Use targeted discovery instead of full server discovery for better performance
		_, err := disc.ServerResourcesForGroupVersion(vpaautoscalingv1.SchemeGroupVersion.String())
		if err != nil {
			// VPA API group not available yet
			return false, nil
		}
		return true, nil
	})
}

func configureVPAController(ctx context.Context, c crclient.Client, t interface{ Logf(string, ...any) }) error {
	// Wait for the VerticalPodAutoscalerController to be created by the operator
	controllerName := "default"
	controllerKey := types.NamespacedName{
		Namespace: vpaNamespaceName,
		Name:      controllerName,
	}

	// Wait for the controller to exist
	t.Logf("[VPA] Waiting for VerticalPodAutoscalerController %q to exist", controllerKey)
	var vpaController *unstructured.Unstructured
	if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "autoscaling.openshift.io",
			Version: "v1",
			Kind:    "VerticalPodAutoscalerController",
		})
		if err := c.Get(ctx, controllerKey, obj); err != nil {
			return false, crclient.IgnoreNotFound(err)
		}
		vpaController = obj
		return true, nil
	}); err != nil {
		return fmt.Errorf("waiting for VerticalPodAutoscalerController: %w", err)
	}
	t.Logf("[VPA] VerticalPodAutoscalerController %q found", controllerKey)

	// Create a patch to update the spec
	original := vpaController.DeepCopy()

	// Set recommendationOnly to true
	if err := unstructured.SetNestedField(vpaController.Object, true, "spec", "recommendationOnly"); err != nil {
		return fmt.Errorf("setting recommendationOnly: %w", err)
	}

	// Set recommender container args
	args := []interface{}{
		"--memory-aggregation-interval=1h",
		"--memory-aggregation-interval-count=12",
		"--memory-histogram-decay-half-life=1h",
	}
	if err := unstructured.SetNestedSlice(vpaController.Object, args, "spec", "deploymentOverrides", "recommender", "container", "args"); err != nil {
		return fmt.Errorf("setting recommender container args: %w", err)
	}

	// Apply the patch
	if err := c.Patch(ctx, vpaController, crclient.MergeFrom(original)); err != nil {
		return fmt.Errorf("patching VerticalPodAutoscalerController: %w", err)
	}

	t.Logf("[VPA] Successfully patched VerticalPodAutoscalerController with recommendationOnly=true and recommender args")
	return nil
}
