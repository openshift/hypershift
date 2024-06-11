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
	"k8s.io/apimachinery/pkg/util/wait"
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

		waitCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()

		t.Logf("Retrieving Red Hat catalogSource deployment image")
		redhatCatalogDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      redhatOperatorsDeploymentName,
				Namespace: guestNamespace,
			},
		}
		var previousGetError string
		err := wait.PollUntilContextTimeout(waitCtx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(redhatCatalogDeployment), redhatCatalogDeployment); err != nil {
				if err.Error() != previousGetError {
					t.Logf("failed to get Red Hat Catalog deployment %s/%s: %s", redhatCatalogDeployment.Namespace, redhatCatalogDeployment.Name, err)
					previousGetError = err.Error()
				}
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			t.Fatalf("failed to get Red Hat Catalog deployment: %v", err)
		}

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
		var previousCreateError string
		err = wait.PollUntilContextTimeout(waitCtx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := guestClient.Create(ctx, guestClusterCatalogSource); err != nil && !apierrors.IsAlreadyExists(err) {
				if err.Error() != previousCreateError {
					t.Logf("failed to get guest cluster catalogSource %s/%s: %s", guestClusterCatalogSource.Namespace, guestClusterCatalogSource.Name, err)
					previousCreateError = err.Error()
				}
				return false, nil
			}
			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "unable to create guest cluster catalogSource")
		t.Logf("guest cluster catalogSource created")

		defer func() {
			err = guestClient.Delete(ctx, guestClusterCatalogSource)
			g.Expect(err != nil && !apierrors.IsNotFound(err)).To(BeFalse(), fmt.Sprintf("failed to delete guest cluster catalogSource %s/%s: %v", guestClusterCatalogSource.Namespace, guestClusterCatalogSource.Name, err))
		}()

		t.Run("Guest cluster catalogSources serve content", testGuestClusterCatalogReady(waitCtx, guestClient))
		t.Run("Install operator from Red Hat operators catalogSource", testOperatorInstallationFromSource(waitCtx, guestClient, redhatOperatorsCatalogSourceName))
		t.Run("Install operator from guest cluster catalogSource", testOperatorInstallationFromSource(waitCtx, guestClient, guestClusterCatalogSourceName))
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

// testGuestClusterCatalogReady ensures that Catalog Operator is able to connect to the CatalogSource created on the guestCluster.
func testGuestClusterCatalogReady(parentCtx context.Context, client crclient.Client) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		guestClusterCatalogSource := &operatorsv1alpha1.CatalogSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      guestClusterCatalogSourceName,
				Namespace: openshiftMarketplaceNamespace,
			},
		}

		// The guest cluster catalogSource should eventually become available
		var previousError string
		err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(guestClusterCatalogSource), guestClusterCatalogSource); err != nil {
				if err.Error() != previousError {
					t.Logf("failed to get guestcluster catalogsource %s/%s: %s", guestClusterCatalogSource.Namespace, guestClusterCatalogSource.Name, err)
					previousError = err.Error()
				}
				return false, nil
			}
			if guestClusterCatalogSource.Status.GRPCConnectionState == nil {
				return false, nil
			}

			return guestClusterCatalogSource.Status.GRPCConnectionState.LastObservedState == "READY", nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "guestCluster catalogSource never became READY")
		t.Logf("guestCluster catalogSource became available")
	}
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

		// Wait for successful csv installation
		t.Logf("Waiting for csv to be installed by subscription")
		var previousSubscriptionError string
		var previousInstalledCSV string
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(subscription), subscription); err != nil {
				if err.Error() != previousSubscriptionError {
					t.Logf("failed to get guestcluster subscription %s/%s: %s", subscription.Namespace, subscription.Name, err)
					previousSubscriptionError = err.Error()
				}
				return false, nil
			}
			if subscription.Status.InstalledCSV != previousInstalledCSV {
				t.Logf("subscription %s/%s installedCSV %s", subscription.Namespace, subscription.Name, subscription.Status.InstalledCSV)
			}
			previousInstalledCSV = subscription.Status.InstalledCSV
			return subscription.Status.InstalledCSV != "", nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "csv never installed")
		t.Logf("subscription created csv %s/%s", subscription.Namespace, subscription.Status.InstalledCSV)

		// Wait for the CSV to enter the succeeded phase
		t.Logf("Waiting for csv to enter the succeeded phase")
		csv := &operatorsv1alpha1.ClusterServiceVersion{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace.Name,
				Name:      subscription.Status.InstalledCSV,
			},
		}

		var previousCSVError string
		var previousPhase operatorsv1alpha1.ClusterServiceVersionPhase
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(csv), csv); err != nil {
				if err.Error() != previousCSVError {
					t.Logf("failed to get guestcluster CSV %s/%s: %s", csv.Namespace, csv.Name, err)
					previousCSVError = err.Error()
				}
				return false, nil
			}
			if csv.Status.Phase != previousPhase {
				t.Logf("CSV %s/%s phase is %s", csv.Namespace, csv.Name, csv.Status.Phase)
			}
			previousPhase = csv.Status.Phase
			return csv.Status.Phase == operatorsv1alpha1.CSVPhaseSucceeded, nil
		})
		if err != nil {
			t.Fatalf("failed to wait for successful CSV: %v", err)
		}
	}
}
