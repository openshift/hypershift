//go:build e2ev2

package tests

import (
	"bytes"
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:PullSecretRecovery] Native malformed HostedCluster pull Secret recovery",
	Label("lifecycle", "hosted-cluster-native-recovery", "malformed-pull-secret-recovery"), func() {
		RegisterNativeMalformedPullSecretRecoveryTests(internal.GetTestContext)
	})

func RegisterNativeMalformedPullSecretRecoveryTests(getTestCtx internal.TestContextGetter) {
	It("should propagate selectors to the HCP with only the dockerconfigjson key missing and restore all owned keys", func() {
		tc := getTestCtx()
		Expect(tc).NotTo(BeNil())
		cluster, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if cluster.Spec.Platform.Type != hyperv1.AWSPlatform {
			Skip("Native malformed pull Secret recovery fixture is only for AWS platform")
		}
		version, err := healthReleaseVersion(tc)
		Expect(err).NotTo(HaveOccurred(), "resolve independent originating release before mutation")
		if version.LT(semver.MustParse("4.23.0")) {
			Skip("Malformed pull Secret recovery requires OpenShift >= 4.23")
		}
		pools := &hyperv1.NodePoolList{}
		Expect(tc.MgmtClient.List(tc.Context, pools, crclient.InNamespace(cluster.Namespace))).To(Succeed())
		Expect(nativeRecoveryFixture(cluster, pools.Items, "malformed-pull-secret-recovery")).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
			g.Expect(meta.IsStatusConditionTrue(cluster.Status.Conditions, string(hyperv1.ReconciliationSucceeded))).To(BeTrue())
		}, 5*time.Minute, 5*time.Second).WithContext(tc.Context).Should(Succeed(), "HostedCluster must initially reconcile successfully")
		secret := &corev1.Secret{}
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Spec.PullSecret.Name}, secret)).To(Succeed())
		Expect(secret.Data[corev1.DockerConfigJsonKey]).NotTo(BeEmpty(), "initial referenced pull Secret must be valid")
		clusters := &hyperv1.HostedClusterList{}
		Expect(tc.MgmtClient.List(tc.Context, clusters, crclient.InNamespace(cluster.Namespace))).To(Succeed())
		snapshot, err := nativeCapturePullSecret(cluster, secret, clusters.Items)
		Expect(err).NotTo(HaveOccurred())
		Expect(cluster.Spec.NodeSelector[nativeRecoverySelector]).NotTo(Equal("true"), "dedicated fixture must require a real selector change")
		Expect(cluster.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation]).NotTo(Equal(nativeRecoveryAdditionalSelector), "dedicated fixture must require a real annotation change")
		DeferCleanup(func() {
			Expect(nativeRestorePullSecretBounded(tc.Context, tc.MgmtClient, snapshot)).To(Succeed(), "cleanup: restore pull Secret bytes/key presence and selector/annotation keys")
		})
		Expect(nativeRemovePullSecretKey(tc.Context, tc.MgmtClient, snapshot)).To(Succeed())
		Expect(nativeMutateRecoverySelectors(tc.Context, tc.MgmtClient, snapshot)).To(Succeed())
		Eventually(func() error {
			hcp := &hyperv1.HostedControlPlane{}
			if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: cluster.Name}, hcp); err != nil {
				return err
			}
			return nativeRecoverySelectorsPropagated(hcp)
		}, 2*time.Minute, 5*time.Second).WithContext(tc.Context).Should(Succeed(), "HCP must propagate both exact selectors while the HC pull Secret is malformed")
		Expect(tc.MgmtClient.Get(tc.Context, snapshot.secretKey, secret)).To(Succeed())
		_, hasDockerConfig := secret.Data[corev1.DockerConfigJsonKey]
		Expect(hasDockerConfig).To(BeFalse(), "propagation must occur before restoration")
		ctx, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 2*time.Minute)
		defer cancel()
		Expect(nativeRestorePullSecret(ctx, tc.MgmtClient, snapshot)).To(Succeed())
		Expect(tc.MgmtClient.Get(ctx, snapshot.secretKey, secret)).To(Succeed())
		restoredBytes, hasDockerConfig := secret.Data[corev1.DockerConfigJsonKey]
		Expect(hasDockerConfig).To(Equal(snapshot.hadDockerConfig))
		Expect(bytes.Equal(restoredBytes, snapshot.dockerConfig)).To(BeTrue(), "restoration must preserve original pull Secret bytes")
		Expect(tc.MgmtClient.Get(ctx, snapshot.clusterKey, cluster)).To(Succeed())
		selector, hasSelector := cluster.Spec.NodeSelector[nativeRecoverySelector]
		Expect(hasSelector).To(Equal(snapshot.hadSelector))
		Expect(selector).To(Equal(snapshot.selector))
		annotation, hasAnnotation := cluster.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation]
		Expect(hasAnnotation).To(Equal(snapshot.hadAnnotation))
		Expect(annotation).To(Equal(snapshot.annotation))
	})
}
