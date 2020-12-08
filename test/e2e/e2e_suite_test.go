// +build e2e

package e2e

import (
	"flag"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capiv1 "sigs.k8s.io/cluster-api/api/v1alpha4"

	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
)

// Test suite globals
var (
	scheme  = runtime.NewScheme()
	client  ctrlclient.Client
	dataDir string
)

func init() {
	// TODO: extract and share this
	clientgoscheme.AddToScheme(scheme)
	hyperv1.AddToScheme(scheme)
	capiv1.AddToScheme(scheme)
	configv1.AddToScheme(scheme)
	securityv1.AddToScheme(scheme)
	operatorv1.AddToScheme(scheme)
	routev1.AddToScheme(scheme)

	flag.StringVar(&dataDir, "e2e.data-dir", "", "path to generated e2e test data")
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "hypershift-e2e")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	kubeClient, err := ctrlclient.New(ctrl.GetConfigOrDie(), ctrlclient.Options{Scheme: scheme})
	Expect(err).ShouldNot(HaveOccurred())
	client = kubeClient
	return nil
}, func(data []byte) {
})
