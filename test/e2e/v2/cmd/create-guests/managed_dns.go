//go:build e2ev2

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	"github.com/blang/semver"
	"github.com/spf13/pflag"
)

func managedDNSArgs(ctx context.Context, args []string, releases releaseinfo.Provider) ([]string, error) {
	flags := pflag.NewFlagSet("managed-dns", pflag.ContinueOnError)
	flags.ParseErrorsAllowlist.UnknownFlags = true
	var enabled bool
	var releaseImage, pullSecretPath string
	flags.BoolVar(&enabled, "managed-dns", false, "")
	flags.StringVar(&releaseImage, "release-image", "", "")
	flags.StringVar(&pullSecretPath, "pull-secret", "", "")
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("parsing managed DNS fixture arguments: %w", err)
	}
	if !enabled {
		return args, nil
	}
	pullSecret, err := os.ReadFile(pullSecretPath)
	if err != nil {
		return nil, fmt.Errorf("reading managed DNS fixture pull secret: %w", err)
	}
	release, err := releases.Lookup(ctx, releaseImage, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("looking up managed DNS fixture release: %w", err)
	}
	if release == nil || release.ImageStream == nil {
		return nil, fmt.Errorf("managed DNS fixture release metadata is missing")
	}
	version, err := semver.Parse(release.Version())
	if err != nil {
		return nil, fmt.Errorf("parsing managed DNS fixture release version: %w", err)
	}
	version.Patch = 0
	version.Pre = nil
	version.Build = nil
	if version.GTE(e2eutil.Version51) {
		return args, nil
	}
	log.Print("Managed DNS requires a 5.1 or later release; creating baseline DNS fixture (managed DNS tests will skip)")
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "--managed-dns" && !strings.HasPrefix(arg, "--managed-dns=") {
			filtered = append(filtered, arg)
		}
	}
	return filtered, nil
}
