//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	etcdrecoverymanifests "github.com/openshift/hypershift/pkg/manifests/etcdrecovery"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EtcdResilience] Native originating HA etcd recovery",
	Label("lifecycle", "hosted-cluster-native-recovery", "native-etcd-recovery"), Ordered, func() {
		RegisterNativeEtcdRecoveryTests(internal.GetTestContext)
	})

func RegisterNativeEtcdRecoveryTests(getTestCtx internal.TestContextGetter) {
	var tc *internal.TestContext
	var pods []corev1.Pod
	var hostedClient crclient.Client
	var marker *corev1.ConfigMap

	BeforeEach(func() {
		marker = nil
		hostedClient = nil
		tc = getTestCtx()
		Expect(tc).NotTo(BeNil())
		cluster, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if cluster.Spec.Platform.Type != hyperv1.AWSPlatform {
			Skip("Native HA recovery fixture is only for AWS platform")
		}
		pools := &hyperv1.NodePoolList{}
		Expect(tc.MgmtClient.List(tc.Context, pools, crclient.InNamespace(cluster.Namespace))).To(Succeed())
		Expect(nativeRecoveryFixture(cluster, pools.Items, "native-etcd-recovery")).To(Succeed())
		sts := cpomanifests.EtcdStatefulSet(tc.ControlPlaneNamespace)
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(sts), sts)).To(Succeed())
		Expect(nativeEtcdConverged(sts)).To(Succeed())
		list := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, list, crclient.InNamespace(sts.Namespace), crclient.MatchingLabels(sts.Spec.Selector.MatchLabels))).To(Succeed())
		Expect(list.Items).To(HaveLen(3))
		pods = list.Items
		hostedClient, err = tc.GetHostedClusterClient(cluster)
		Expect(err).NotTo(HaveOccurred())
		nodes := &corev1.NodeList{}
		Expect(hostedClient.List(tc.Context, nodes)).To(Succeed())
		Expect(nodes.Items).To(BeEmpty(), "originating fixture must have zero workers")
		marker = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", GenerateName: "native-etcd-marker-"},
			Data:       map[string]string{"value": time.Now().UTC().Format(time.RFC3339Nano)},
		}
		Expect(hostedClient.Create(tc.Context, marker)).To(Succeed())
		DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 2*time.Minute)
			defer cancel()
			err := hostedClient.Delete(ctx, marker)
			if !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: remove native etcd marker")
			}
		})
	})

	AfterEach(func() {
		if tc == nil || marker == nil || hostedClient == nil || CurrentSpecReport().Failed() {
			return
		}
		Eventually(func() error {
			sts := cpomanifests.EtcdStatefulSet(tc.ControlPlaneNamespace)
			if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(sts), sts); err != nil {
				return err
			}
			return nativeEtcdConverged(sts)
		}, 30*time.Minute, 5*time.Second).WithContext(tc.Context).Should(Succeed())
		Eventually(func() error {
			actual := &corev1.ConfigMap{}
			if err := hostedClient.Get(tc.Context, crclient.ObjectKeyFromObject(marker), actual); err != nil {
				return err
			}
			return nativeMarkerSurvived(marker, actual)
		}, 30*time.Minute, 5*time.Second).WithContext(tc.Context).Should(Succeed())
	})

	It("should replace a member after simultaneous pod and data-volume loss", func() {
		pod := pods[rand.Intn(len(pods))]
		pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Namespace: pod.Namespace, Name: "data-etcd" + strings.TrimPrefix(pod.Name, "etcd"),
		}}
		var group sync.WaitGroup
		for _, object := range []crclient.Object{&pod, pvc} {
			group.Add(1)
			go func(object crclient.Object) {
				defer GinkgoRecover()
				defer group.Done()
				Expect(tc.MgmtClient.Delete(tc.Context, object)).To(Succeed())
			}(object)
		}
		group.Wait()
		Eventually(func(g Gomega) {
			replacement := &corev1.Pod{}
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(&pod), replacement)).To(Succeed())
			g.Expect(replacement.UID).NotTo(BeEmpty())
			g.Expect(replacement.UID).NotTo(Equal(pod.UID))
		}, 30*time.Minute, 5*time.Second).WithContext(tc.Context).Should(Succeed())
	})

	It("should preserve marker data when random members are killed every five seconds for thirty seconds", func() {
		for iteration := 0; iteration < 6; iteration++ {
			current := &corev1.PodList{}
			sts := cpomanifests.EtcdStatefulSet(tc.ControlPlaneNamespace)
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(sts), sts)).To(Succeed())
			Expect(tc.MgmtClient.List(tc.Context, current, crclient.InNamespace(sts.Namespace), crclient.MatchingLabels(sts.Spec.Selector.MatchLabels))).To(Succeed())
			Expect(current.Items).NotTo(BeEmpty())
			pod := current.Items[rand.Intn(len(current.Items))]
			Expect(tc.MgmtClient.Delete(tc.Context, &pod, &crclient.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)})).To(Succeed())
			select {
			case <-tc.Context.Done():
				Fail("native random-member disruption interrupted: " + tc.Context.Err().Error())
			case <-time.After(5 * time.Second):
			}
		}
	})

	It("should replace every member and preserve marker data after simultaneous loss of all pods", func() {
		var group sync.WaitGroup
		for _, pod := range pods {
			group.Add(1)
			go func(pod corev1.Pod) {
				defer GinkgoRecover()
				defer group.Done()
				ctx, cancel := context.WithTimeout(tc.Context, 5*time.Second)
				defer cancel()
				Expect(tc.MgmtClient.Delete(ctx, &pod, &crclient.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)})).To(Succeed())
			}(pod)
		}
		group.Wait()
		Eventually(func() error {
			current := &corev1.PodList{}
			sts := cpomanifests.EtcdStatefulSet(tc.ControlPlaneNamespace)
			if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(sts), sts); err != nil {
				return err
			}
			if err := tc.MgmtClient.List(tc.Context, current, crclient.InNamespace(sts.Namespace), crclient.MatchingLabels(sts.Spec.Selector.MatchLabels)); err != nil {
				return err
			}
			return nativeEtcdReplaced(pods, current.Items)
		}, 30*time.Minute, 5*time.Second).WithContext(tc.Context).Should(Succeed())
	})

	It("should recover a corrupted member through an active recovery Job without losing marker data", func() {
		pod := pods[rand.Intn(len(pods))]
		config, err := v2util.GetConfig()
		Expect(err).NotTo(HaveOccurred())
		clientset, err := kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		cluster, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		Expect(nativeCorruptEtcdMember(tc.Context, tc.MgmtClient, crclient.ObjectKeyFromObject(cluster), pod,
			func(ctx context.Context, command []string) (string, error) {
				return v2util.RunCommandInPod(ctx, clientset, config, pod.Namespace, pod.Name, "etcd", command...)
			}, 5*time.Second)).To(Succeed())
	})

	It("should recover an explicitly removed member through an active recovery Job without losing marker data", func() {
		pod := pods[rand.Intn(len(pods))]
		config, err := v2util.GetConfig()
		Expect(err).NotTo(HaveOccurred())
		clientset, err := kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(tc.Context, 5*time.Minute)
		defer cancel()
		baseCommand := []string{
			"/usr/bin/etcdctl", "--cacert=/etc/etcd/tls/etcd-ca/ca.crt", "--cert=/etc/etcd/tls/server/server.crt",
			"--key=/etc/etcd/tls/server/server.key", "--endpoints=https://etcd-client." + tc.ControlPlaneNamespace + ".svc:2379",
		}
		discovery := strings.Join(baseCommand, " ") + fmt.Sprintf(" member list | grep %s | awk '{print $1}' | tr -d ,", pod.Name)
		output, err := v2util.RunCommandInPod(ctx, clientset, config, pod.Namespace, pod.Name, "etcd", "/bin/sh", "-c", discovery)
		Expect(err).NotTo(HaveOccurred())
		memberID, err := nativeEtcdMemberID(output)
		Expect(err).NotTo(HaveOccurred())
		output, err = v2util.RunCommandInPod(ctx, clientset, config, pod.Namespace, pod.Name, "etcd", append(baseCommand, "member", "remove", memberID)...)
		Expect(nativeEtcdCommandResult(output, err, "Error:")).To(Succeed())
		Expect(tc.MgmtClient.Delete(tc.Context, &pod)).To(Succeed())
		Eventually(func(g Gomega) {
			job := etcdrecoverymanifests.EtcdRecoveryJob(tc.ControlPlaneNamespace)
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(job), job)).To(Succeed())
			g.Expect(job.Status.Active).To(Equal(int32(1)))
		}, 10*time.Minute, 5*time.Second).WithContext(tc.Context).Should(Succeed())
	})
}
