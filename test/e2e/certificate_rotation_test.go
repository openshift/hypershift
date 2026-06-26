//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

// TestCertificateRotation tests that when internal serving certificates are
// regenerated, the control plane components automatically restart to pick up
// the new certificates. Creates a cluster, waits for it to be available, then
// manually triggers certificate rotation by deleting the kas-server-crt
// secret. The CPO regenerates it, and we verify that the hypershift-operator
// detects the new certificate and triggers a control plane restart. The api
// server must be available at the end.
func TestCertificateRotation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.NodePoolReplicas = 0

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Logf("Testing certificate rotation")

		// We start by waiting for the cluster to be available. We
		// wait for the guest client to determine that.
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		t.Logf("Guest cluster is available")

		// We know that the auto generated certificates hash should now
		// be populated on the hosted control plane. Validate it. Keep
		// the value for future comparison (after rotating the cert).
		t.Logf("Waiting for initial kas-serving-cert-hash annotation to be set...")
		hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: hcpNamespace, Name: hostedCluster.Name}}
		var initialCertHashAnnotation string
		var initialRestartAnnotation string
		err := wait.PollUntilContextTimeout(
			ctx, 5*time.Second, 2*time.Minute, true,
			func(ctx context.Context) (bool, error) {
				if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hcp), hcp); err != nil {
					return false, err
				}
				initialCertHashAnnotation = hcp.Annotations["hypershift.openshift.io/kas-serving-cert-hash"]
				if initialCertHashAnnotation == "" {
					t.Log("Empty hypershift.openshift.io/kas-serving-cert-hash annotation found")
					return false, nil
				}
				initialRestartAnnotation = hcp.Annotations[hyperv1.RestartDateAnnotation]
				return true, nil
			},
		)
		g.Expect(err).NotTo(HaveOccurred(), "kas-serving-cert-hash annotation should be set after cluster is available")
		t.Logf("Initial kas-serving-cert-hash annotation: %q", initialCertHashAnnotation)
		t.Logf("Initial restart-date annotation: %q", initialRestartAnnotation)

		// Delete the secret, after doing so new certificates will need
		// to be created. The new certificate hash should end in the
		// hosted control plane and be different from the previous one.
		t.Logf("Deleting kas-server-crt secret to trigger certificate regeneration...")
		kasSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcpNamespace, Name: "kas-server-crt"}}
		err = mgtClient.Delete(ctx, kasSecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to delete kas-server-crt secret")

		// Wait for the hypershift-operator to detect the certificate
		// change by watching for both the kas-serving-cert-hash and
		// restart-date annotations to change. The restart-date change
		// proves a restart was actually triggered.
		t.Logf("Waiting for hypershift-operator to detect certificate change and trigger restart...")
		err = wait.PollUntilContextTimeout(
			ctx, 5*time.Second, 5*time.Minute, true,
			func(ctx context.Context) (bool, error) {
				if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hcp), hcp); err != nil {
					return false, err
				}

				certHashAnnotation := hcp.Annotations["hypershift.openshift.io/kas-serving-cert-hash"]
				restartAnnotation := hcp.Annotations[hyperv1.RestartDateAnnotation]

				if certHashAnnotation == "" {
					t.Log("Empty hypershift.openshift.io/kas-serving-cert-hash annotation found")
					return false, nil
				}

				if certHashAnnotation == initialCertHashAnnotation {
					t.Log("Certificate hash hasn't changed yet")
					return false, nil
				}

				if restartAnnotation == initialRestartAnnotation {
					t.Log("Restart annotation hasn't changed yet")
					return false, nil
				}

				t.Logf("New kas-serving-cert-hash: %q", certHashAnnotation)
				t.Logf("New restart-date: %q", restartAnnotation)
				return true, nil
			},
		)
		g.Expect(err).NotTo(HaveOccurred(), "expected both kas-serving-cert-hash and restart-date annotations to change after secret deletion")

		// Wait for control plane to stabilize after restart with new
		// certificates. We do that by the conditions on the hosted
		// cluster object.
		t.Logf("Waiting for control plane to become available after certificate rotation...")
		e2eutil.WaitForConditionsOnHostedControlPlane(t, ctx, mgtClient, hostedCluster, hostedCluster.Spec.Release.Image)
		t.Logf("Control plane is available after certificate rotation")
		e2eutil.EnsureNoCrashingPods(t, ctx, mgtClient, hostedCluster)

		// Verify that the API server pods have actually been restarted
		// with the new restart annotation before we validate API access.
		t.Logf("Verifying kube-apiserver pods have been restarted with new annotation...")
		expectedRestartAnnotation := hcp.Annotations[hyperv1.RestartDateAnnotation]
		err = wait.PollUntilContextTimeout(
			ctx, 5*time.Second, 5*time.Minute, true,
			func(ctx context.Context) (bool, error) {
				podList := &corev1.PodList{}
				labels := crclient.MatchingLabels{"app": "kube-apiserver"}
				if err := mgtClient.List(ctx, podList, crclient.InNamespace(hcpNamespace), labels); err != nil {
					return false, err
				}

				if len(podList.Items) == 0 {
					t.Log("No kube-apiserver pods found")
					return false, nil
				}

				for _, pod := range podList.Items {
					podRestartAnnotation := pod.Annotations[hyperv1.RestartDateAnnotation]
					if podRestartAnnotation != expectedRestartAnnotation {
						t.Logf("Pod %s has restart annotation %q, expected %q", pod.Name, podRestartAnnotation, expectedRestartAnnotation)
						return false, nil
					}
					if pod.Status.Phase != corev1.PodRunning {
						t.Logf("Pod %s is not running yet (phase: %s)", pod.Name, pod.Status.Phase)
						return false, nil
					}
				}

				t.Logf("All kube-apiserver pods have been restarted with annotation %q", expectedRestartAnnotation)
				return true, nil
			},
		)
		g.Expect(err).NotTo(HaveOccurred(), "kube-apiserver pods should be restarted with new restart-date annotation")

		// Now the new api is online, we should be able to just query
		// it.
		t.Logf("Verifying guest cluster API is still accessible...")
		err = guestClient.List(ctx, &corev1.NamespaceList{})
		g.Expect(err).NotTo(HaveOccurred(), "guest cluster API should remain accessible after certificate rotation")

		t.Logf("Certificate rotation test completed successfully!")
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "certificate-rotation", globalOpts.ServiceAccountSigningKey)
}
