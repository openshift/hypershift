//go:build postconfig
// +build postconfig

package postconfig

import (
	"flag"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	flagKubeconfig string
	flagNamespace  string
	flagName       string
)

func TestPostConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Post-Config Validation Suite")
}

func init() {
	flag.StringVar(&flagKubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "Path to the management cluster kubeconfig")
	flag.StringVar(&flagNamespace, "namespace", "", "Namespace of the HostedCluster")
	flag.StringVar(&flagName, "name", "", "Name of the HostedCluster")
}
