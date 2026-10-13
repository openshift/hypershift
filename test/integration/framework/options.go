package framework

import (
	"flag"
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Options are global test options applicable to all scenarios.
type Options struct {
	OCPath                          string
	HyperShiftCLIPath               string
	HyperShiftOperatorImage         string
	ControlPlaneOperatorImage       string
	ControlPlaneOperatorImageLabels string
	ReleaseImage                    string

	Kubeconfig  string
	ArtifactDir string
	PullSecret  string

	Mode Mode
}

// Mode describes what the test process should be doing.
type Mode string

const (
	// SetupMode sets up the HyperShift Operator, HostedClusters, and other infrastructure
	// but does not run any test code. Test processes will wait on SIGINT after setup is
	// complete, and run cleanup when interrupted.
	SetupMode Mode = "setup"
	// TestMode runs test code and expects the environment to already be set up.
	TestMode Mode = "test"
	// AllInOneMode is the default mode, where the process expects to both set up
	// the test environment and run the test code.
	AllInOneMode Mode = "all-in-one"
)

type modeValue Mode

func newModeValue(val Mode, p *Mode) *modeValue {
	*p = val
	return (*modeValue)(p)
}

func (m *modeValue) Set(val string) error {
	switch Mode(val) {
	case AllInOneMode, SetupMode, TestMode:
		break
	default:
		return fmt.Errorf("invalid mode %q", val)
	}
	*m = modeValue(val)
	return nil
}

func (m *modeValue) Get() any { return string(*m) }

func (m *modeValue) String() string { return string(*m) }

func DefaultOptions() *Options {
	return &Options{
		OCPath:            "oc",
		HyperShiftCLIPath: "hypershift",
		Mode:              AllInOneMode,
	}
}

func (o *Options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.OCPath, "oc", o.OCPath, "Path to the oc CLI binary. If unset, normal $PATH resolution will be used.")
	fs.StringVar(&o.HyperShiftCLIPath, "hypershift-cli", o.HyperShiftCLIPath, "Path to the HyperShift CLI binary. If unset, normal $PATH resolution will be used.")

	fs.StringVar(&o.HyperShiftOperatorImage, "hypershift-operator-image", o.HyperShiftOperatorImage, "Image digest for the HyperShift Operator.")
	fs.StringVar(&o.ControlPlaneOperatorImage, "control-plane-operator-image", o.ControlPlaneOperatorImage, "Image digest for the Control Plane Operator.")
	fs.StringVar(&o.ControlPlaneOperatorImageLabels, "control-plane-operator-image-labels", o.ControlPlaneOperatorImageLabels, "Image labels usually provided in LABEL blocks for the Control Plane Operator.")
	fs.StringVar(&o.ReleaseImage, "release-image", o.ReleaseImage, "Image digest for the release payload to run. Optional, will resolve latest release if unset.")

	fs.StringVar(&o.ArtifactDir, "artifact-dir", o.ArtifactDir, "Path to the artifact directory.")
	fs.StringVar(&o.Kubeconfig, "kubeconfig", o.Kubeconfig, "Path to the $KUBECONFIG.")
	fs.StringVar(&o.PullSecret, "pull-secret", o.PullSecret, "Path to the pull credentials to use.")

	fs.Var(newModeValue(AllInOneMode, &o.Mode), "mode", "The execution mode.")
}

func (o *Options) Validate() error {
	for flagName, into := range map[string]*string{
		"--hypershift-operator-image":           &o.HyperShiftOperatorImage,
		"--control-plane-operator-image":        &o.ControlPlaneOperatorImage,
		"--control-plane-operator-image-labels": &o.ControlPlaneOperatorImageLabels,
		"--artifact-dir":                        &o.ArtifactDir,
		"--kubeconfig":                          &o.Kubeconfig,
		"--pull-secret":                         &o.PullSecret,
	} {
		if into == nil || *into == "" {
			return fmt.Errorf("%s is required", flagName)
		}
	}

	return nil
}

// LoadKubeConfig loads a kubeconfig from the file and uses the default context
func LoadKubeConfig(path string) (*rest.Config, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	loader.ExplicitPath = path
	clientCmdCfg, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("could not load kubeconfig: %w", err)
	}
	cfg, err := clientcmd.NewDefaultClientConfig(*clientCmdCfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.QPS = -1
	cfg.Burst = -1
	return cfg, nil
}

// TODO: stream logs for all of the components into artifacts
// TODO: dump yamls into artifacts
