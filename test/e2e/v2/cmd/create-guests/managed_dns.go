//go:build e2ev2

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/spf13/pflag"
)

const managesAWSDNSLabel = "io.openshift.hypershift.control-plane-operator-manages-aws-dns"

func managedDNSArgs(ctx context.Context, args []string, releases releaseinfo.Provider, images imageregistry.ImageMetadataProvider) ([]string, error) {
	flags := pflag.NewFlagSet("managed-dns", pflag.ContinueOnError)
	flags.ParseErrorsAllowlist.UnknownFlags = true
	var enabled bool
	var releaseImage, cpoImage, pullSecretPath string
	var annotations []string
	flags.BoolVar(&enabled, "managed-dns", false, "")
	flags.StringVar(&releaseImage, "release-image", "", "")
	flags.StringVar(&cpoImage, "control-plane-operator-image", "", "")
	flags.StringVar(&pullSecretPath, "pull-secret", "", "")
	flags.StringArrayVar(&annotations, "annotations", nil, "")
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
	cluster := &hyperv1.HostedCluster{}
	cluster.Spec.Release.Image = releaseImage
	cluster.Spec.Platform.Type = hyperv1.AWSPlatform
	cluster.Annotations = map[string]string{}
	for _, annotation := range annotations {
		key, value, found := strings.Cut(annotation, "=")
		if !found {
			return nil, fmt.Errorf("invalid fixture annotation: expected key=value")
		}
		cluster.Annotations[key] = value
	}
	if cpoImage != "" {
		cluster.Annotations[hyperv1.ControlPlaneOperatorImageAnnotation] = cpoImage
	}
	image, err := imageregistry.GetControlPlaneOperatorImage(ctx, cluster, releases, "", pullSecret)
	if err != nil {
		return nil, fmt.Errorf("resolving managed DNS fixture CPO: %w", err)
	}
	labels, err := imageregistry.GetControlPlaneOperatorImageLabels(ctx, cluster, image, pullSecret, images)
	if err != nil {
		return nil, fmt.Errorf("resolving managed DNS fixture CPO capabilities: %w", err)
	}
	if labels[managesAWSDNSLabel] == "true" {
		return args, nil
	}
	log.Printf("Selected CPO does not advertise %s=true; creating baseline DNS fixture without managed DNS (managed DNS tests will skip)", managesAWSDNSLabel)
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "--managed-dns" && !strings.HasPrefix(arg, "--managed-dns=") {
			filtered = append(filtered, arg)
		}
	}
	return filtered, nil
}
