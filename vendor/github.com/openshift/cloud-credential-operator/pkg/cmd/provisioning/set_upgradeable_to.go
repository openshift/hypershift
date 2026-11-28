package provisioning

import (
	"context"
	"log"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"golang.org/x/mod/semver"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cloud-credential-operator/pkg/operator/constants"
)

// clusterVersionName is the name of the singleton ClusterVersion resource.
const clusterVersionName = "version"

type setUpgradeableToOptions struct {
	KubeConfigFile string
}

var (
	// SetUpgradeableToOpts captures the options that affect setting the upgradeable-to annotation.
	SetUpgradeableToOpts = setUpgradeableToOptions{}
)

// NewSetUpgradeableToCmd provides the "set-upgradeable-to" subcommand
func NewSetUpgradeableToCmd() *cobra.Command {
	setUpgradeableToCmd := &cobra.Command{
		Use:   "set-upgradeable-to <version>",
		Short: "Signal that cloud credentials are ready for an OpenShift version",
		Long: "Set the cloudcredential.openshift.io/upgradeable-to annotation on the cluster's " +
			"CloudCredential config to the given major.minor version, telling the Cloud Credential " +
			"Operator that credentials for that version are in place.",
		Args: cobra.ExactArgs(1),
		RunE: runSetUpgradeableTo,
		// main.go already reports whatever Execute returns, so letting cobra print it too
		// would show every error twice.
		SilenceErrors: true,
	}

	setUpgradeableToCmd.PersistentFlags().StringVar(&SetUpgradeableToOpts.KubeConfigFile, "kubeconfig", "", "Path to the kubeconfig file (defaults to $KUBECONFIG, then ~/.kube/config)")

	return setUpgradeableToCmd
}

func runSetUpgradeableTo(cmd *cobra.Command, args []string) error {
	// Arguments parsed successfully, so anything that fails from here on is a runtime problem
	// rather than misuse. Printing the usage block after it would bury the message.
	cmd.SilenceUsage = true

	version, err := normalizeVersion(args[0])
	if err != nil {
		return err
	}

	if version != strings.TrimPrefix(args[0], "v") {
		log.Printf("Using %s, the major.minor version the operator compares against", version)
	}

	kubeClient, err := newClusterClient(SetUpgradeableToOpts.KubeConfigFile)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	if err := checkVersionIsUpgrade(ctx, kubeClient, version); err != nil {
		return err
	}

	return applyUpgradeableAnnotation(ctx, kubeClient, version)
}

// normalizeVersion validates a target OpenShift version and reduces it to the bare
// major.minor form the operator compares against. A patch or prerelease level is accepted
// because automation will have a full release version to hand, and the operator discards
// everything below the minor anyway: see utils.UpgradeableCheck in pkg/operator/utils.
func normalizeVersion(version string) (string, error) {
	canonical := version
	if !strings.HasPrefix(canonical, "v") {
		canonical = "v" + canonical
	}

	// semver accepts a bare major such as "v4" and MajorMinor turns it into "v4.0",
	// which is a release that does not exist. Require the minor explicitly.
	if !semver.IsValid(canonical) || !strings.Contains(version, ".") {
		return "", errors.Errorf("%q is not a valid version, expected a major.minor version such as 4.19", version)
	}

	return strings.TrimPrefix(semver.MajorMinor(canonical), "v"), nil
}

// checkVersionIsUpgrade rejects a target the operator would ignore. The annotation is only
// honoured when it is strictly greater than the cluster's completed version (see
// utils.UpgradeableCheck in pkg/operator/utils), so setting the current version or lower
// writes something that does nothing.
//
// It is best-effort in the same sense as checkNamespacesExist: a caller who can write the
// CloudCredential but not read ClusterVersion skips the check rather than being blocked by it.
// A missing ClusterVersion is still fatal, because a cluster without one is not the cluster the
// administrator meant to point at.
func checkVersionIsUpgrade(ctx context.Context, kubeClient client.Client, version string) error {
	clusterVersion := &configv1.ClusterVersion{}
	err := kubeClient.Get(ctx, types.NamespacedName{Name: clusterVersionName}, clusterVersion)
	switch {
	case apierrors.IsForbidden(err):
		log.Printf("WARNING: not allowed to read the cluster version, skipping the check that %s is an upgrade", version)
		return nil
	case err != nil:
		return errors.Wrap(err, "could not read the cluster version, check that --kubeconfig points at the intended cluster")
	}

	current := completedVersion(clusterVersion)
	if current == "" {
		return errors.New("the cluster has no completed version in ClusterVersion status history")
	}

	currentMajorMinor := semver.MajorMinor("v" + current)
	if currentMajorMinor == "" {
		return errors.Errorf("could not parse the cluster's current version %q", current)
	}

	if semver.Compare("v"+version, currentMajorMinor) != 1 {
		return errors.Errorf("%s is not an upgrade from the cluster's current version %s",
			version, strings.TrimPrefix(currentMajorMinor, "v"))
	}

	return nil
}

// completedVersion returns the most recent completed version from the update history. The CVO
// orders the history most-recent-first, so the first completed entry is what the cluster is
// actually running. This mirrors the unexported getClusterVersionCompleted in
// pkg/operator/utils; it is six lines, and duplicating it is preferable to exporting operator
// internals to ccoctl.
func completedVersion(clusterVersion *configv1.ClusterVersion) string {
	for _, update := range clusterVersion.Status.History {
		if update.State == configv1.CompletedUpdate {
			return update.Version
		}
	}
	return ""
}

// applyUpgradeableAnnotation updates only the upgradeable-to annotation on the
// singleton CloudCredential config. Like applySecrets, it first retrieves the
// existing object and uses a regular update, avoiding server-side apply field
// ownership conflicts with administrators or other controllers.
func applyUpgradeableAnnotation(ctx context.Context, kubeClient client.Client, version string) error {
	cloudCredential := &operatorv1.CloudCredential{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Name: constants.CloudCredOperatorConfig}, cloudCredential); err != nil {
		return errors.Wrapf(err, "failed to get cloudcredential/%s", constants.CloudCredOperatorConfig)
	}
	original := cloudCredential.DeepCopy()
	if cloudCredential.Annotations == nil {
		cloudCredential.Annotations = map[string]string{}
	}
	cloudCredential.Annotations[constants.UpgradeableAnnotation] = version

	// Patch only the annotation rather than sending the complete CloudCredential
	// spec back to the API. In particular, older cluster config objects can omit
	// optional OperatorSpec fields whose zero values fail validation in a full PUT.
	// This is a regular merge patch, not server-side apply, so it neither claims
	// field ownership nor forces conflicts.
	if err := kubeClient.Patch(ctx, cloudCredential, client.MergeFrom(original)); err != nil {
		return errors.Wrapf(err, "failed to set the upgradeable-to annotation on cloudcredential/%s",
			constants.CloudCredOperatorConfig)
	}

	log.Printf("Set %s=%s on cloudcredential/%s", constants.UpgradeableAnnotation, version, constants.CloudCredOperatorConfig)
	return nil
}
