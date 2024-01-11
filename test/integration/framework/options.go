package framework

import (
	"errors"
	"flag"
)

// Options are global test options applicable to all scenarios.
type Options struct {
	OCPath                      string
	HyperShiftCLIPath           string
	HyperShiftOperatorPath      string
	ControlPlaneOperatorPath    string
	ControlPlanePKIOperatorPath string

	Kubeconfig  string
	ArtifactDir string
	PullSecret  string
}

func DefaultOptions() *Options {
	return &Options{
		OCPath:                      "oc",
		HyperShiftCLIPath:           "hypershift",
		HyperShiftOperatorPath:      "hypershift-operator",
		ControlPlaneOperatorPath:    "control-plane-operator",
		ControlPlanePKIOperatorPath: "control-plane-pki-operator",
	}
}

func (o *Options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.OCPath, "oc", o.OCPath, "Path to the oc CLI binary. If unset, normal $PATH resolution will be used.")
	fs.StringVar(&o.HyperShiftCLIPath, "hypershift-cli", o.HyperShiftCLIPath, "Path to the HyperShift CLI binary. If unset, normal $PATH resolution will be used.")
	fs.StringVar(&o.HyperShiftOperatorPath, "hypershift-operator", o.HyperShiftOperatorPath, "Path to the HyperShift Operator binary. If unset, normal $PATH resolution will be used.")
	fs.StringVar(&o.ControlPlaneOperatorPath, "control-plane-operator", o.ControlPlaneOperatorPath, "Path to the Control Plane Operator binary. If unset, normal $PATH resolution will be used.")
	fs.StringVar(&o.ControlPlanePKIOperatorPath, "control-plane-pki-operator", o.ControlPlanePKIOperatorPath, "Path to the Control Plane PKI Operator binary. If unset, normal $PATH resolution will be used.")

	fs.StringVar(&o.ArtifactDir, "artifact-dir", o.ArtifactDir, "Path to the artifact directory.")
	fs.StringVar(&o.Kubeconfig, "kubeconfig", o.Kubeconfig, "Path to the $KUBECONFIG.")
	fs.StringVar(&o.PullSecret, "pull-secret", o.PullSecret, "Path to the pull credentials to use.")
}

func (o *Options) Validate() error {
	if o.ArtifactDir == "" {
		return errors.New("--artifact-dir is required")
	}

	if o.Kubeconfig == "" {
		return errors.New("--kubeconfig is required")
	}

	if o.PullSecret == "" {
		return errors.New("--pull-secret is required")
	}

	return nil
}
