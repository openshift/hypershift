package aws

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/core"
	consolelogsaws "github.com/openshift/hypershift/cmd/consolelogs/aws"
	"github.com/openshift/hypershift/support/upsert"
)

type AWS struct {
	t    *testing.T
	opts core.CreateOptions
}

func (a *AWS) Describe() interface{} {
	return a.opts
}

func (a *AWS) CreateCluster(ctx context.Context, hc *hyperv1.HostedCluster) error {
	a.opts.Namespace = hc.Namespace
	a.opts.Name = hc.Name
	a.opts.InfraID = hc.Name
	return aws.CreateCluster(ctx, &a.opts)
}

func (a *AWS) DumpCluster(ctx context.Context, hc *hyperv1.HostedCluster, artifactsDir string) {
	saveMachineConsoleLogs(a.t, ctx, hc, a.opts.AWSPlatform.AWSCredentialsFile, artifactsDir)
	DumpHostedCluster(a.t, ctx, hc, artifactsDir)
	if err := dumpJournals(a.t, ctx, hc, artifactsDir, a.opts.AWSPlatform.AWSCredentialsFile); err != nil {
		a.t.Logf("Failed to dump machine journals: %v", err)
	}
}

func (a *AWS) DestroyCluster(ctx context.Context, hc *hyperv1.HostedCluster) error {
	opts := &core.DestroyOptions{
		Namespace: hc.Namespace,
		Name:      hc.Name,
		InfraID:   a.opts.InfraID,
		AWSPlatform: core.AWSPlatformDestroyOptions{
			BaseDomain:         a.opts.AWSPlatform.BaseDomain,
			AWSCredentialsFile: a.opts.AWSPlatform.AWSCredentialsFile,
			PreserveIAM:        false,
			Region:             a.opts.AWSPlatform.Region,
		},
		ClusterGracePeriod: 15 * time.Minute,
	}
	return aws.DestroyCluster(ctx, opts)
}

func New(t *testing.T, opts core.CreateOptions) *AWS {
	return &AWS{opts: opts, t: t}
}

func saveMachineConsoleLogs(t *testing.T, ctx context.Context, hc *hyperv1.HostedCluster, awsCredentialsFile string, artifactDir string) {
	consoleLogs := consolelogsaws.ConsoleLogOpts{
		Name:               hc.Name,
		Namespace:          hc.Namespace,
		AWSCredentialsFile: awsCredentialsFile,
		OutputDir:          filepath.Join(artifactDir, "machine-console-logs"),
	}
	if logsErr := consoleLogs.Run(ctx); logsErr != nil {
		t.Logf("Failed to get machine console logs: %v", logsErr)
	} else {
		t.Logf("Saved machine console logs.")
	}
}

func DumpHostedCluster(t *testing.T, ctx context.Context, hc *hyperv1.HostedCluster, artifactDir string) {
	t.Run("DumpHostedCluster", func(t *testing.T) {
		findKubeObjectUpdateLoops := func(filename string, content []byte) {
			if bytes.Contains(content, []byte(upsert.LoopDetectorWarningMessage)) {
				t.Errorf("Found %s messages in file %s", upsert.LoopDetectorWarningMessage, filename)
			}
		}
		err := aws.DumpCluster(ctx, &aws.DumpOptions{
			Namespace:   hc.Namespace,
			Name:        hc.Name,
			ArtifactDir: artifactDir,
			LogCheckers: []aws.LogChecker{findKubeObjectUpdateLoops},
		})
		if err != nil {
			t.Errorf("Failed to dump cluster: %v", err)
		}
	})
}
