//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	guestClusterCatalogSourceName    = "guestcluster-operators"
	redhatOperatorsCatalogSourceName = "redhat-operators"
	redhatOperatorsDeploymentName    = "redhat-operators-catalog"
	openshiftMarketplaceNamespace    = "openshift-marketplace"
)

// TestOLM executes a suite of olm tests which ensure that olm is
// behaving as expected on the guest cluster.
func TestOLM(t *testing.T) {
	// Skip test until https://issues.redhat.com/browse/OCPBUGS-4600 is fixed
	// Pod restarts are already ignored for *-catalog pods
	t.SkipNow()
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	// Create a cluster
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Get guest client
		t.Logf("Waiting for guest client to become available")
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		guestNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		t.Logf("Hosted control plane namespace is %s", guestNamespace)

		redhatCatalogDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      redhatOperatorsDeploymentName,
				Namespace: guestNamespace,
			},
		}
		e2eutil.EventuallyObject(t, ctx, "Red Hat catalogSource deployment image to exist",
			func(ctx context.Context) (*appsv1.Deployment, error) {
				err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(redhatCatalogDeployment), redhatCatalogDeployment)
				return redhatCatalogDeployment, err
			},
			nil,
			e2eutil.WithTimeout(15*time.Minute),
		)

		t.Logf("Creating guest cluster catalogSource using the Red Hat operators image from the hosted control plane")
		guestClusterCatalogSource := &operatorsv1alpha1.CatalogSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      guestClusterCatalogSourceName,
				Namespace: openshiftMarketplaceNamespace,
			},
			Spec: operatorsv1alpha1.CatalogSourceSpec{
				SourceType:  operatorsv1alpha1.SourceTypeGrpc,
				Image:       redhatCatalogDeployment.Spec.Template.Spec.Containers[0].Image,
				Publisher:   "OLM HyperShift Team",
				DisplayName: "OLM HyperShift Test CatalogSource",
			},
		}
		if err := guestClient.Create(ctx, guestClusterCatalogSource); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("failed to create guest cluster catalogSource %s/%s: %s", guestClusterCatalogSource.Namespace, guestClusterCatalogSource.Name, err)
		}
		t.Logf("guest cluster catalogSource created")

		defer func() {
			err := guestClient.Delete(ctx, guestClusterCatalogSource)
			g.Expect(err != nil && !apierrors.IsNotFound(err)).To(BeFalse(), fmt.Sprintf("failed to delete guest cluster catalogSource %s/%s: %v", guestClusterCatalogSource.Namespace, guestClusterCatalogSource.Name, err))
		}()

		e2eutil.EventuallyObject(t, ctx, "guest cluster catalogSources to serve content",
			func(ctx context.Context) (*operatorsv1alpha1.CatalogSource, error) {
				err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(guestClusterCatalogSource), guestClusterCatalogSource)
				return guestClusterCatalogSource, err
			},
			[]e2eutil.Predicate[*operatorsv1alpha1.CatalogSource]{
				func(catalogSource *operatorsv1alpha1.CatalogSource) (done bool, reasons string, err error) {
					want, got := "READY", ptr.Deref(catalogSource.Status.GRPCConnectionState, operatorsv1alpha1.GRPCConnectionState{}).LastObservedState
					return want == got, fmt.Sprintf("expected GRPC connection state %s, got %s", want, got), nil
				},
			},
			e2eutil.WithTimeout(15*time.Minute),
		)

		t.Run("Install operator from Red Hat operators catalogSource", testOperatorInstallationFromSource(ctx, guestClient, redhatOperatorsCatalogSourceName))
		t.Run("Install operator from guest cluster catalogSource", testOperatorInstallationFromSource(ctx, guestClient, guestClusterCatalogSourceName))
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey, globalOpts.DisableTearDown)
}

// testOperatorInstallationFromSource ensures that an operator can be installed from a catalogSource in the openshift-marketplace namespace.
func testOperatorInstallationFromSource(parentCtx context.Context, client crclient.Client, catalogSourceName string) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		testNamespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "olm-test-",
			},
		}
		err := client.Create(ctx, testNamespace)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create guest cluster namespace")
		defer func() {
			err = client.Delete(ctx, testNamespace)
			g.Expect(err != nil && !apierrors.IsNotFound(err)).To(BeFalse(), fmt.Sprintf("failed to delete test namespace %s: %v", testNamespace.Name, err))
		}()
		t.Logf("Created namespace %s for test", testNamespace.Name)

		operatorGroup := &operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace.Name,
				Name:      "olm-test",
			},
			Spec: operatorsv1.OperatorGroupSpec{
				TargetNamespaces: []string{testNamespace.Name},
			},
		}
		err = client.Create(ctx, operatorGroup)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create guest cluster operatorGroup")
		t.Logf("Created opeatorGroup %s/%s for test", operatorGroup.Namespace, operatorGroup.Name)

		subscription := &operatorsv1alpha1.Subscription{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace.Name,
				Name:      "olm-test",
			},
			Spec: &operatorsv1alpha1.SubscriptionSpec{
				InstallPlanApproval: "Automatic",
				Channel:             "stable",
				// TODO: Rely on Operator Framework managed package once released
				// instead of third party package we have no control over.
				Package:                "local-storage-operator",
				CatalogSourceNamespace: openshiftMarketplaceNamespace,
				CatalogSource:          catalogSourceName,
			},
		}
		err = client.Create(ctx, subscription)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create guest cluster subscription")
		t.Logf("Created subscription %s/%s for test", subscription.Namespace, subscription.Name)
		e2eutil.EventuallyObject(t, ctx, "CSV to be installed by Subscription",
			func(ctx context.Context) (*operatorsv1alpha1.Subscription, error) {
				err := client.Get(ctx, crclient.ObjectKeyFromObject(subscription), subscription)
				return subscription, err
			},
			[]e2eutil.Predicate[*operatorsv1alpha1.Subscription]{
				func(subscription *operatorsv1alpha1.Subscription) (done bool, reasons string, err error) {
					return subscription.Status.InstalledCSV != "", fmt.Sprintf("have installed CSV: %v", subscription.Status.InstalledCSV), nil
				},
			},
		)
		t.Logf("subscription created csv %s/%s", subscription.Namespace, subscription.Status.InstalledCSV)

		// Wait for the CSV to enter the succeeded phase
		t.Logf("Waiting for csv to enter the succeeded phase")
		csv := &operatorsv1alpha1.ClusterServiceVersion{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace.Name,
				Name:      subscription.Status.InstalledCSV,
			},
		}
		e2eutil.EventuallyObject(t, ctx, "CSV to enter the succeeded phase",
			func(ctx context.Context) (*operatorsv1alpha1.ClusterServiceVersion, error) {
				err := client.Get(ctx, crclient.ObjectKeyFromObject(csv), csv)
				return csv, err
			},
			[]e2eutil.Predicate[*operatorsv1alpha1.ClusterServiceVersion]{
				func(csv *operatorsv1alpha1.ClusterServiceVersion) (done bool, reasons string, err error) {
					want, got := operatorsv1alpha1.CSVPhaseSucceeded, csv.Status.Phase
					return want == got, fmt.Sprintf("expected CSV to be in phase %q, got %q", want, got), nil
				},
			},
		)
	}
}
