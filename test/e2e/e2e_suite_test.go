// +build e2e

package e2e

import (
	"flag"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "openshift.io/hypershift/api"
)

// Test suite globals
var (
	client ctrlclient.Client

	quickStartSpecOptions QuickStartSpecOptions
)

type QuickStartSpecOptions struct {
	AWSCredentialsFile string
	PullSecretFile     string
	SSHKeyFile         string
	ReleaseImage       string
}

func init() {
	flag.StringVar(&quickStartSpecOptions.AWSCredentialsFile, "e2e.quick-start.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&quickStartSpecOptions.PullSecretFile, "e2e.quick-start.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&quickStartSpecOptions.SSHKeyFile, "e2e.quick-start.ssh-key-file", "", "path to SSH public key")
	flag.StringVar(&quickStartSpecOptions.ReleaseImage, "e2e.quick-start.release-image", hyperapi.OCPReleaseImage, "OCP release image to test")
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "hypershift-e2e")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	kubeClient, err := ctrlclient.New(ctrl.GetConfigOrDie(), ctrlclient.Options{Scheme: hyperapi.Scheme})
	Expect(err).ShouldNot(HaveOccurred())
	client = kubeClient
	return nil
}, func(data []byte) {
})
