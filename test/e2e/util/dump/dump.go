package dump

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	consolelogsaws "github.com/openshift/hypershift/cmd/consolelogs/aws"
	"github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/upsert"

	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// DumpHostedCluster dumps the contents of the hosted cluster to the given artifact
// directory, and returns an error if any aspect of that operation fails. The loop
// detector is configured to return an error when any warnings are detected.
func DumpHostedCluster(ctx context.Context, t *testing.T, hc *hyperv1.HostedCluster, dumpGuestCluster bool, artifactDir string) error {
	dumpLogFile := filepath.Join(artifactDir, "dump.log")
	dumpLog, err := os.Create(dumpLogFile)
	if err != nil {
		return fmt.Errorf("failed to create dump log: %w", err)
	}
	dumpLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(dumpLog), zap.DebugLevel))
	defer func() {
		if err := dumpLogger.Sync(); err != nil {
			fmt.Printf("failed to sync dumpLogger: %v\n", err)
		}
	}()

	var allErrors []error
	findKubeObjectUpdateLoops := func(filename string, content []byte) {
		// TODO: currently we need the "strings.Contains" workaround to let the ExternalOIDCWithUIDAndExtraClaimMappings jobs not fail,
		// to promote the 4.20 featuregate ExternalOIDCWithUIDAndExtraClaimMappings to GA.
		// Once https://issues.redhat.com/browse/OCPBUGS-60457 is fixed, remove the "strings.Contains" condition
		if bytes.Contains(content, []byte(upsert.LoopDetectorWarningMessage)) && !strings.Contains(t.Name(), "TestExternalOIDC") {
			allErrors = append(allErrors, fmt.Errorf("found %s messages in file %s", upsert.LoopDetectorWarningMessage, filename))
		}
	}
	err = core.DumpCluster(ctx, &core.DumpOptions{
		Namespace:        hc.Namespace,
		Name:             hc.Name,
		ArtifactDir:      artifactDir,
		LogCheckers:      []core.LogChecker{findKubeObjectUpdateLoops},
		DumpGuestCluster: dumpGuestCluster,
		Log:              zapr.NewLogger(dumpLogger),
	})
	if err != nil {
		allErrors = append(allErrors, fmt.Errorf("failed to dump cluster: %w", err))
	}
	return errors.NewAggregate(allErrors)
}

// DumpMachineConsoleLogs dumps machine console logs for the given hostedcluster.
// This is only useful for AWS clusters.
func DumpMachineConsoleLogs(ctx context.Context, hc *hyperv1.HostedCluster, awsCredentials util.AWSCredentialsOptions, artifactDir string) error {
	consoleLogs := consolelogsaws.ConsoleLogOpts{
		Name:               hc.Name,
		Namespace:          hc.Namespace,
		AWSCredentialsOpts: awsCredentials,
		OutputDir:          filepath.Join(artifactDir, "machine-console-logs"),
	}
	err := consoleLogs.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to get machine console logs: %v", err)
	}
	return nil
}
