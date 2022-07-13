package dump

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/go-logr/zapr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	consolelogsaws "github.com/openshift/hypershift/cmd/consolelogs/aws"
	"github.com/openshift/hypershift/support/upsert"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/util/errors"
)

// DumpHostedCluster dumps the contents of the hosted cluster to the given artifact
// directory, and returns an error if any aspect of that operation fails. The loop
// detector is configured to return an error when any warnings are detected.
func DumpHostedCluster(ctx context.Context, t *testing.T, hc *hyperv1.HostedCluster, dumpGuestCluster bool, artifactDir string) error {
	var allErrors []error
	findKubeObjectUpdateLoops := func(filename string, content []byte) {
		if bytes.Contains(content, []byte(upsert.LoopDetectorWarningMessage)) {
			allErrors = append(allErrors, fmt.Errorf("found %s messages in file %s", upsert.LoopDetectorWarningMessage, filename))
		}
	}
	err := core.DumpCluster(ctx, &core.DumpOptions{
		Namespace:        hc.Namespace,
		Name:             hc.Name,
		ArtifactDir:      artifactDir,
		LogCheckers:      []core.LogChecker{findKubeObjectUpdateLoops},
		DumpGuestCluster: dumpGuestCluster,
		Log:              zapr.NewLogger(zaptest.NewLogger(t)),
	})
	if err != nil {
		allErrors = append(allErrors, fmt.Errorf("failed to dump cluster: %w", err))
	}
	return errors.NewAggregate(allErrors)
}

// DumpMachineConsoleLogs dumps machine console logs for the given hostedcluster.
// This is only useful for AWS clusters.
func DumpMachineConsoleLogs(ctx context.Context, hc *hyperv1.HostedCluster, awsCredentialsFile string, artifactDir string) error {
	consoleLogs := consolelogsaws.ConsoleLogOpts{
		Name:               hc.Name,
		Namespace:          hc.Namespace,
		AWSCredentialsFile: awsCredentialsFile,
		OutputDir:          filepath.Join(artifactDir, "machine-console-logs"),
	}
	err := consoleLogs.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to get machine console logs: %v", err)
	}
	return nil
}
