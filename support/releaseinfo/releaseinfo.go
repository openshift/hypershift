package releaseinfo

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	imageapi "github.com/openshift/api/image/v1"

	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/blang/semver"
)

// Provider knows how to find the release image metadata for an image referred
// to by its pullspec.
type Provider interface {
	Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error)
}

//go:generate ../../hack/tools/bin/mockgen -source=releaseinfo.go -package=releaseinfo -destination=providerwithregistryoverrides_mock.go
type ProviderWithRegistryOverrides interface {
	Provider
	GetRegistryOverrides() map[string]string
}

type ProviderWithOpenShiftImageRegistryOverrides interface {
	ProviderWithRegistryOverrides
	GetOpenShiftImageRegistryOverrides() map[string][]string
	GetMirroredReleaseImage() string
}

// ReleaseImage wraps an ImageStream with some utilities that help the user
// discover constituent component image information.
type ReleaseImage struct {
	*imageapi.ImageStream `json:",inline"`
	StreamMetadata        *CoreOSStreamMetadata `json:"streamMetadata"`
}

type CoreOSStreamMetadata struct {
	Stream        string                        `json:"stream"`
	Architectures map[string]CoreOSArchitecture `json:"architectures"`
}

type CoreOSArchitecture struct {
	// Artifacts is a map of platform name to Artifacts
	Artifacts map[string]CoreOSArtifact `json:"artifacts"`
	Images    CoreOSImages              `json:"images"`
	RHCOS     CoreRHCOSImage            `json:"rhel-coreos-extensions"`
}

type CoreOSArtifact struct {
	Release string                             `json:"release"`
	Formats map[string]map[string]CoreOSFormat `json:"formats"`
}

type CoreOSFormat struct {
	Location           string `json:"location"`
	Signature          string `json:"signature"`
	SHA256             string `json:"sha256"`
	UncompressedSHA256 string `json:"uncompressed-sha256"`
}

type CoreOSImages struct {
	AWS      CoreOSAWSImages      `json:"aws"`
	PowerVS  CoreOSPowerVSImages  `json:"powervs"`
	Kubevirt CoreOSKubevirtImages `json:"kubevirt"`
}

type CoreRHCOSImage struct {
	AzureDisk CoreAzureDisk `json:"azure-disk"`
}

type CoreAzureDisk struct {
	Release string `json:"release"`
	URL     string `json:"url"`
}

type CoreOSAWSImages struct {
	Regions map[string]CoreOSAWSImage `json:"regions"`
}

type CoreOSAWSImage struct {
	Release string `json:"release"`
	Image   string `json:"image"`
}

type CoreOSKubevirtImages struct {
	Release   string `json:"release"`
	Image     string `json:"image"`
	DigestRef string `json:"digest-ref"`
}

type CoreOSPowerVSImages struct {
	Regions map[string]CoreOSPowerVSImage `json:"regions"`
}

type CoreOSPowerVSImage struct {
	Release string `json:"release"`
	Object  string `json:"object"`
	Bucket  string `json:"bucket"`
	URL     string `json:"url"`
}

func (i *ReleaseImage) Version() string {
	return i.ImageStream.Name
}

func (i *ReleaseImage) ComponentImages() map[string]string {
	images := make(map[string]string)
	for _, tag := range i.ImageStream.Spec.Tags {
		images[tag.Name] = tag.From.Name
	}
	return images
}

func (i *ReleaseImage) ComponentVersions() (map[string]string, error) {
	componentVersions, err := readComponentVersions(i.ImageStream)
	if err := errors.NewAggregate(err); err != nil {
		return nil, err
	}

	versions := make(map[string]string)
	if len(i.ImageStream.Name) > 0 {
		versions["release"] = i.ImageStream.Name
	}
	for component, version := range componentVersions {
		versions[component] = version.String()
	}
	return versions, nil
}

const (
	// This LABEL is a comma-delimited list of key=version pairs that can be consumed
	// by other manifests within the payload to hardcode version strings. Version must
	// be a semantic version with no build label (+ is not allowed) and key must be
	// alphanumeric characters and dashes only. The value `0.0.1-snapshot-key` in a
	// manifest will be substituted with the version value for key.
	annotationBuildVersions = "io.openshift.build.versions"
	// This LABEL is a comma-delimited list of key=displayName pairs that can be consumed
	// by other manifests within the payload to hardcode component display names.
	// Display name may contain spaces, dashes, colons, and alphanumeric characters.
	annotationBuildVersionsDisplayNames = "io.openshift.build.version-display-names"
)

func readComponentVersions(is *imageapi.ImageStream) (ComponentVersions, []error) {
	var errs []error
	combined := make(map[string]sets.Set[string])
	combinedDisplayNames := make(map[string]sets.Set[string])
	for _, tag := range is.Spec.Tags {
		versions, ok := tag.Annotations[annotationBuildVersions]
		if !ok {
			continue
		}
		all, err := parseComponentVersionsLabel(versions, tag.Annotations[annotationBuildVersionsDisplayNames])
		if err != nil {
			errs = append(errs, fmt.Errorf("the referenced image %s had an invalid version annotation: %v", tag.Name, err))
		}
		for k, v := range all {
			if k == "kubectl" {
				if tag.Name != "cli" && tag.Name != "cli-artifacts" {
					continue
				}
			}
			existing, ok := combined[k]
			if !ok {
				existing = sets.New[string]()
				combined[k] = existing
			}
			existing.Insert(v.Version)

			existingDisplayName, ok := combinedDisplayNames[k]
			if !ok {
				existingDisplayName = sets.New[string]()
				combinedDisplayNames[k] = existingDisplayName
			}
			existingDisplayName.Insert(v.DisplayName)
		}
	}

	multiples := sets.NewString()
	var out ComponentVersions
	var keys []string
	for k := range combined {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := combined[k]
		if v.Len() > 1 {
			multiples = multiples.Insert(k)
		}
		if _, ok := out[k]; ok {
			continue
		}
		sortedList := v.UnsortedList()
		sort.Strings(sortedList)
		version := sortedList[0]
		if out == nil {
			out = make(ComponentVersions)
		}
		out[k] = ComponentVersion{Version: version}
	}
	for _, k := range keys {
		v, ok := combinedDisplayNames[k]
		if !ok {
			continue
		}
		if v.Len() > 1 {
			multiples = multiples.Insert(k)
		}
		version, ok := out[k]
		if !ok {
			continue
		}
		if len(version.DisplayName) == 0 {
			sortedList := v.UnsortedList()
			sort.Strings(sortedList)
			version.DisplayName = sortedList[0]
		}
		out[k] = version
	}

	if len(multiples) > 0 {
		errs = append(errs, fmt.Errorf("multiple versions or display names reported for the following component(s): %v", strings.Join(multiples.List(), ",  ")))
	}
	return out, errs
}

func parseComponentVersionsLabel(label, displayNames string) (ComponentVersions, error) {
	label = strings.TrimSpace(label)
	if len(label) == 0 {
		return nil, nil
	}
	var names map[string]string
	if len(displayNames) > 0 {
		names = make(map[string]string)
		for _, pair := range strings.Split(displayNames, ",") {
			pair = strings.TrimSpace(pair)
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 1 {
				return nil, fmt.Errorf("the display name pair %q must be NAME=DISPLAYNAME", pair)
			}
			if len(parts[0]) < 2 {
				return nil, fmt.Errorf("the version name %q must be at least 2 characters", parts[0])
			}
			if !reAllowedVersionKey.MatchString(parts[0]) {
				return nil, fmt.Errorf("the version name %q must only be ASCII alphanumerics and internal hyphens", parts[0])
			}
			if !reAllowedDisplayNameKey.MatchString(parts[1]) {
				return nil, fmt.Errorf("the display name %q must only be alphanumerics, spaces, and symbols in [():-]", parts[1])
			}
			names[parts[0]] = parts[1]
		}
	}

	labels := make(ComponentVersions)
	if len(label) == 0 {
		return nil, fmt.Errorf("the version pair must be NAME=VERSION")
	}
	for _, pair := range strings.Split(label, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 1 {
			return nil, fmt.Errorf("the version pair %q must be NAME=VERSION", pair)
		}
		if len(parts[0]) < 2 {
			return nil, fmt.Errorf("the version name %q must be at least 2 characters", parts[0])
		}
		if !reAllowedVersionKey.MatchString(parts[0]) {
			return nil, fmt.Errorf("the version name %q must only be ASCII alphanumerics and internal hyphens", parts[0])
		}
		v, err := semver.Parse(parts[1])
		if err != nil {
			return nil, fmt.Errorf("the version pair %q must have a valid semantic version: %v", pair, err)
		}
		v.Build = nil
		labels[parts[0]] = ComponentVersion{
			Version:     v.String(),
			DisplayName: names[parts[0]],
		}
	}
	return labels, nil
}

var (
	// reAllowedVersionKey limits the allowed component name to a strict subset
	reAllowedVersionKey = regexp.MustCompile(`^[a-z0-9]+[\-a-z0-9]*[a-z0-9]+$`)
	// reAllowedDisplayNameKey limits the allowed component name to a strict subset
	reAllowedDisplayNameKey = regexp.MustCompile(`^[a-zA-Z0-9\-\:\s\(\)]+$`)
)

// ComponentVersion includes the version and optional display name.
type ComponentVersion struct {
	Version     string
	DisplayName string
}

// String returns the version of this component.
func (v ComponentVersion) String() string {
	return v.Version
}

// ComponentVersions is a map of component names to semantic versions. Names are
// lowercase alphanumeric and dashes. Semantic versions will have all build
// labels removed, but prerelease segments are preserved.
type ComponentVersions map[string]ComponentVersion

// OrderedKeys returns the keys in this map in lexicographic order.
func (v ComponentVersions) OrderedKeys() []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (v ComponentVersions) String() string {
	return v.VersionLabel()
}

// VersionLabel formats the ComponentVersions into a valid
// versions label.
func (v ComponentVersions) VersionLabel() string {
	var keys []string
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := &bytes.Buffer{}
	for i, k := range keys {
		if i != 0 {
			buf.WriteRune(',')
		}
		fmt.Fprintf(buf, "%s=%s", k, v[k].Version)
	}
	return buf.String()
}

// DisplayNameLabel formats the ComponentVersions into a valid display
// name label.
func (v ComponentVersions) DisplayNameLabel() string {
	var keys []string
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := &bytes.Buffer{}
	for i, k := range keys {
		if i != 0 {
			buf.WriteRune(',')
		}
		if len(v[k].DisplayName) == 0 {
			continue
		}
		fmt.Fprintf(buf, "%s=%s", k, v[k].DisplayName)
	}
	return buf.String()
}
