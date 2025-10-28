//go:build postconfig
// +build postconfig

package postconfig

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	homanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	mgmtClient ctrlclient.Client
)

var _ = BeforeSuite(func() {
	By("building management cluster client")
	cfg, err := loadRestConfig(flagKubeconfig)
	Expect(err).NotTo(HaveOccurred())
	mgmtClient, err = newClient(cfg)
	Expect(err).NotTo(HaveOccurred())
})

var _ = Describe("HostedCluster validations", func() {
	It("When the HostedCluster exists it should be retrievable", func() {
		Expect(flagNamespace).NotTo(BeEmpty(), "--namespace is required")
		Expect(flagName).NotTo(BeEmpty(), "--name is required")
		hc := &hyperv1.HostedCluster{}
		key := types.NamespacedName{Namespace: flagNamespace, Name: flagName}
		Expect(mgmtClient.Get(context.Background(), key, hc)).To(Succeed())
	})

	It("When the HostedCluster is healthy it should have Available=true", func() {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: flagNamespace, Name: flagName}}
		Expect(mgmtClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		cond := getCondition(hc.Status.Conditions, hyperv1.HostedClusterAvailable)
		Expect(cond).NotTo(BeNil(), "Available condition not found")
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("When created it should enforce API immutability and capability immutability", func() {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: flagNamespace, Name: flagName}}
		Expect(mgmtClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(hc), hc)).To(Succeed())

		// Attempt to change Service type for APIServer
		mutated := hc.DeepCopy()
		for i := range mutated.Spec.Services {
			if mutated.Spec.Services[i].Service == hyperv1.APIServer {
				mutated.Spec.Services[i].Type = hyperv1.NodePort
			}
		}
		err := mgmtClient.Update(context.Background(), mutated)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))

		// Attempt to change ControllerAvailabilityPolicy
		mutated = hc.DeepCopy()
		if mutated.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
			mutated.Spec.ControllerAvailabilityPolicy = hyperv1.SingleReplica
		} else {
			mutated.Spec.ControllerAvailabilityPolicy = hyperv1.HighlyAvailable
		}
		err = mgmtClient.Update(context.Background(), mutated)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	It("When created it should set custom labels and tolerations on control-plane pods", func() {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: flagNamespace, Name: flagName}}
		Expect(mgmtClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		hcpNamespace := homanifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

		podList := &corev1.PodList{}
		Expect(mgmtClient.List(context.Background(), podList, ctrlclient.InNamespace(hcpNamespace))).To(Succeed())

		var podsMissingLabel []string
		var podsMissingToleration []string
		var podsMissingAppLabel []string

		for _, pod := range podList.Items {
			// Skip KubeVirt related pods
			if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
				continue
			}

			if v, ok := pod.Labels["hypershift-e2e-test-label"]; !ok || v != "test" {
				podsMissingLabel = append(podsMissingLabel, pod.Name)
			}

			hasTol := false
			for _, tol := range pod.Spec.Tolerations {
				if tol.Key == "hypershift-e2e-test-toleration" && string(tol.Operator) == string(corev1.TolerationOpEqual) && tol.Value == "true" && string(tol.Effect) == string(corev1.TaintEffectNoSchedule) {
					hasTol = true
					break
				}
			}
			if !hasTol {
				podsMissingToleration = append(podsMissingToleration, pod.Name)
			}

			if val, ok := pod.Labels["app"]; !ok || val == "" {
				podsMissingAppLabel = append(podsMissingAppLabel, pod.Name)
			}
		}

		Expect(podsMissingLabel).To(BeEmpty(), "pods missing custom label: %s", strings.Join(podsMissingLabel, ", "))
		Expect(podsMissingToleration).To(BeEmpty(), "pods missing custom toleration: %s", strings.Join(podsMissingToleration, ", "))
		Expect(podsMissingAppLabel).To(BeEmpty(), "pods missing app label: %s", strings.Join(podsMissingAppLabel, ", "))
	})

	It("When created it should report correct FeatureGate status in guest cluster", func() {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: flagNamespace, Name: flagName}}
		Expect(mgmtClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(hc), hc)).To(Succeed())

		// Build guest client from hostedcluster status.kubeconfig
		guestCfg := waitForGuestRestConfig(hc)
		guestClient, err := ctrlclient.New(guestCfg, ctrlclient.Options{Scheme: scheme()})
		Expect(err).NotTo(HaveOccurred())

		clusterVersion := &configv1.ClusterVersion{}
		Expect(guestClient.Get(context.Background(), ctrlclient.ObjectKey{Name: "version"}, clusterVersion)).To(Succeed())

		featureGate := &configv1.FeatureGate{}
		Expect(guestClient.Get(context.Background(), ctrlclient.ObjectKey{Name: "cluster"}, featureGate)).To(Succeed())

		Expect(clusterVersion.Status.History).ToNot(BeEmpty(), "ClusterVersion history is empty")
		current := clusterVersion.Status.History[0]
		Expect(current.State).To(Equal(configv1.CompletedUpdate))

		found := false
		for _, d := range featureGate.Status.FeatureGates {
			if d.Version == current.Version {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "current version %s from ClusterVersion not found in FeatureGate status", current.Version)
	})

	It("When a custom KubeAPI DNS is configured it should expose a matching kubeconfig reference", func() {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: flagNamespace, Name: flagName}}
		Expect(mgmtClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(hc), hc)).To(Succeed())

		// If KubeAPIServerDNSName is set, HostedCluster status should expose CustomKubeconfig and referenced secret should exist
		if hc.Spec.KubeAPIServerDNSName == "" {
			Skip("no custom KubeAPIServerDNSName configured")
		}
		Expect(hc.Status.CustomKubeconfig).NotTo(BeNil())
		secret := &corev1.Secret{}
		Expect(mgmtClient.Get(context.Background(), types.NamespacedName{Namespace: hc.Namespace, Name: hc.Status.CustomKubeconfig.Name}, secret)).To(Succeed())
		Expect(secret.Data["kubeconfig"]).NotTo(BeEmpty())
	})

	It("On Azure it should honor KubeAPIServer AllowedCIDRs (smoke)", func() {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: flagNamespace, Name: flagName}}
		Expect(mgmtClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		if hc.Spec.Platform.Type != hyperv1.AzurePlatform {
			Skip("not Azure platform")
		}
		// Simply ensure we can fetch guest rest config and the field is mutable without error
		_ = waitForGuestRestConfig(hc)
		mutated := hc.DeepCopy()
		if mutated.Spec.Networking.APIServer == nil {
			mutated.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{}
		}
		mutated.Spec.Networking.APIServer.AllowedCIDRBlocks = []hyperv1.CIDRBlock{"0.0.0.0/0"}
		Expect(mgmtClient.Update(context.Background(), mutated)).To(Succeed())
	})

	It("When created it should sync the global pull secret into the guest cluster", func() {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: flagNamespace, Name: flagName}}
		Expect(mgmtClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		guestCfg := waitForGuestRestConfig(hc)
		guestClient, err := ctrlclient.New(guestCfg, ctrlclient.Options{Scheme: scheme()})
		Expect(err).NotTo(HaveOccurred())
		secret := &corev1.Secret{}
		Expect(guestClient.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "global-pull-secret"}, secret)).To(Succeed())
		Expect(secret.Data[corev1.DockerConfigJsonKey]).NotTo(BeEmpty())
	})
})

// waitForGuestRestConfig waits for status.kubeconfig, fetches the secret and returns a *rest.Config
func waitForGuestRestConfig(hc *hyperv1.HostedCluster) *rest.Config {
	var ref types.NamespacedName
	Eventually(func(g Gomega) {
		fresh := &hyperv1.HostedCluster{}
		g.Expect(mgmtClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(hc), fresh)).To(Succeed())
		if fresh.Status.KubeConfig != nil {
			ref = types.NamespacedName{Namespace: hc.Namespace, Name: fresh.Status.KubeConfig.Name}
		}
		g.Expect(fresh.Status.KubeConfig).NotTo(BeNil())
	}).Should(Succeed())

	secret := &corev1.Secret{}
	Eventually(func(g Gomega) {
		g.Expect(mgmtClient.Get(context.Background(), ref, secret)).To(Succeed())
		g.Expect(secret.Data).NotTo(BeEmpty())
		g.Expect(secret.Data["kubeconfig"]).NotTo(BeEmpty())
	}).Should(Succeed())

	cfg, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
	Expect(err).NotTo(HaveOccurred())
	cfg.QPS = -1
	cfg.Burst = -1
	return cfg
}

func getCondition(conds []metav1.Condition, t hyperv1.ConditionType) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == string(t) {
			return &conds[i]
		}
	}
	return nil
}
