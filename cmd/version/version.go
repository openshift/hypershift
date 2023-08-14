package version

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openshift/hypershift/pkg/version"
	"github.com/spf13/cobra"
)

var (
	HypershiftImageBase = "quay.io/hypershift/hypershift-operator"
	HypershiftImageTag  = "latest"
	HyperShiftImage     = fmt.Sprintf("%s:%s", HypershiftImageBase, HypershiftImageTag)
)

// https://docs.ci.openshift.org/docs/getting-started/useful-links/#services
const (
	defaultReleaseStream = "4-stable-multi"

	multiArchReleaseURLTemplate = "https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/latest"
	releaseURLTemplate          = "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/latest"
)

type OCPVersion struct {
	Name        string `json:"name"`
	PullSpec    string `json:"pullSpec"`
	DownloadURL string `json:"downloadURL"`
}

func LookupDefaultOCPVersion(releaseStream string) (OCPVersion, error) {
	if len(releaseStream) == 0 {
		releaseStream = defaultReleaseStream
	}

	var releaseURL string
	if strings.Contains(releaseStream, "multi") {
		releaseURL = fmt.Sprintf(multiArchReleaseURLTemplate, releaseStream)
	} else {
		releaseURL = fmt.Sprintf(releaseURLTemplate, releaseStream)
	}

	var version OCPVersion
	resp, err := http.Get(releaseURL)
	if err != nil {
		return version, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return version, err
	}
	err = json.Unmarshal(body, &version)
	if err != nil {
		return version, err
	}
	return version, nil
}

func NewVersionCommand() *cobra.Command {
	var commitOnly bool
	cmd := &cobra.Command{
		Use:          "version",
		Short:        "Prints HyperShift CLI version",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			if commitOnly {
				fmt.Printf("%s\n", version.GetRevision())
				return
			}
			fmt.Printf("%s\n", version.String())
		},
	}
	cmd.Flags().BoolVar(&commitOnly, "commit-only", commitOnly, "Output only the code commit")
	return cmd
}
