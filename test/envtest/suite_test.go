//go:build envtest

package envtest

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var ctx = context.Background()
var suites []SuiteSpec

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	g := NewGomegaWithT(t)

	_, thisFile, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(thisFile)
	assetsDir := filepath.Join(testDir, "..", "..", "cmd", "install", "assets", "crds", "hypershift-operator")
	karpenterAssetsDir := filepath.Join(testDir, "..", "..", "karpenter-operator", "controllers", "karpenter", "assets")

	var err error
	suites, err = LoadTestSuiteSpecs(assetsDir, karpenterAssetsDir)
	g.Expect(err).ToNot(HaveOccurred())

	RunSpecs(t, "HyperShift API Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	serverVersion, err := discoveryClient.ServerVersion()
	Expect(err).ToNot(HaveOccurred())

	Expect(serverVersion.Major).To(Equal("1"))

	minorInt, err := strconv.Atoi(strings.Split(serverVersion.Minor, "+")[0])
	Expect(err).ToNot(HaveOccurred())
	Expect(minorInt).To(BeNumerically(">=", 25), fmt.Sprintf("This test suite requires a Kube API server of at least version 1.25, current version is 1.%s", serverVersion.Minor))
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if testEnv != nil {
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
})

var _ = Describe("CRD Installation", func() {
	GenerateCRDInstallTest("Default")
	GenerateCRDInstallTest("TechPreviewNoUpgrade")
})

var _ = Describe("", func() {
	for _, suite := range suites {
		GenerateTestSuite(suite)
	}
})
