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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	installassets "github.com/openshift/hypershift/cmd/install/assets"
)

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var ctx = context.Background()
var suites []SuiteSpec

// allCRDs returns all HyperShift CRDs for the given feature set,
// including hypershift-operator, cluster-api, and all provider CRDs.
func allCRDs(featureSet string) []*apiextensionsv1.CustomResourceDefinition {
	crdObjects := installassets.CustomResourceDefinitions(
		func(path string, crd *apiextensionsv1.CustomResourceDefinition) bool {
			// For feature-gated CRDs in zz_generated.crd-manifests, filter by feature set.
			if strings.Contains(path, "zz_generated.crd-manifests") {
				if annotationFS, ok := crd.Annotations["release.openshift.io/feature-set"]; ok {
					return annotationFS == featureSet
				}
			}
			return true
		},
		nil,
	)

	crds := make([]*apiextensionsv1.CustomResourceDefinition, len(crdObjects))
	for i, obj := range crdObjects {
		crds[i] = obj.(*apiextensionsv1.CustomResourceDefinition)
	}
	return crds
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	g := NewGomegaWithT(t)

	_, thisFile, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(thisFile)

	var err error
	suites, err = LoadTestSuiteSpecs(testDir)
	g.Expect(err).ToNot(HaveOccurred())

	RunSpecs(t, "HyperShift API Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")

	// Install all CRDs (hypershift-operator, cluster-api, providers) for the
	// TechPreviewNoUpgrade feature set, which is the superset containing all fields.
	crds := allCRDs("TechPreviewNoUpgrade")

	testEnv = &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			CRDs: crds,
		},
	}

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

var _ = Describe("", func() {
	for _, suite := range suites {
		GenerateTestSuite(suite)
	}
})
