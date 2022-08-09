//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/test/e2e/util"
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
	t.Parallel()

	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	// Create a cluster
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.NodePoolReplicas = 1
	cluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)

	// Get guest client
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, cluster)

	// Wait for guest cluster nodes to become available
	numNodes := clusterOpts.NodePoolReplicas * int32(len(clusterOpts.AWSPlatform.Zones))
	util.WaitForNReadyNodes(t, ctx, guestClient, numNodes, cluster.Spec.Platform.Type)

	guestNamespace := manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name).Name
	t.Logf("Hosted control plane namespace is %s", guestNamespace)

	t.Logf("Retrieving Red Hat catalogSource deployment image")
	redhatCatalogDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redhatOperatorsDeploymentName,
			Namespace: guestNamespace,
		},
	}
	err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(redhatCatalogDeployment), redhatCatalogDeployment); err != nil {
			t.Logf("failed to get Red Hat Catalog deployment %s/%s: %s", redhatCatalogDeployment.Namespace, redhatCatalogDeployment.Name, err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())

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
	err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		if err := guestClient.Create(ctx, guestClusterCatalogSource); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Logf("failed to create guest cluster catalogSource %s/%s: %s", guestClusterCatalogSource.Namespace, guestClusterCatalogSource.Name, err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "unable to create guest cluster catalogSource")
	t.Logf("guest cluster catalogSource created")

	defer func() {
		err = guestClient.Delete(ctx, guestClusterCatalogSource)
		g.Expect(err != nil && !apierrors.IsNotFound(err)).To(BeFalse(), fmt.Sprintf("failed to delete guest cluster catalogSource %s/%s: %v", guestClusterCatalogSource.Namespace, guestClusterCatalogSource.Name, err))
	}()

	t.Run("Guest cluster catalogSources serve content", testGuestClusterCatalogReady(ctx, guestClient))
	t.Run("Install operator from Red Hat operators catalogSource", testOperatorInstallationFromSource(ctx, guestClient, redhatOperatorsCatalogSourceName))
	t.Run("Install operator from guest cluster catalogSource", testOperatorInstallationFromSource(ctx, guestClient, guestClusterCatalogSourceName))
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
		err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(guestClusterCatalogSource), guestClusterCatalogSource); err != nil {
				t.Logf("failed to get guestcluster catalogsource %s/%s: %s", guestClusterCatalogSource.Namespace, guestClusterCatalogSource.Name, err)
				return false, nil
			}
			if guestClusterCatalogSource.Status.GRPCConnectionState == nil {
				return false, nil
			}

			return guestClusterCatalogSource.Status.GRPCConnectionState.LastObservedState == "READY", nil
		}, ctx.Done())
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
		err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(subscription), subscription); err != nil {
				t.Logf("failed to get guestcluster subscription %s/%s: %s", subscription.Namespace, subscription.Name, err)
				return false, nil
			}
			return subscription.Status.InstalledCSV != "", nil
		}, ctx.Done())
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

		err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(csv), csv); err != nil {
				return false, nil
			}
			return csv.Status.Phase == operatorsv1alpha1.CSVPhaseSucceeded, nil
		}, ctx.Done())
	}
}
