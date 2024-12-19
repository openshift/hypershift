package version

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/util"
	manifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/supportedversion"
	"github.com/openshift/hypershift/hypershift-operator/controllers/supportedversion"
	"github.com/openshift/hypershift/pkg/version"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

var (
	HypershiftImageBase = "quay.io/hypershift/hypershift-operator"
	HypershiftImageTag  = "latest"
	HyperShiftImage     = fmt.Sprintf("%s:%s", HypershiftImageBase, HypershiftImageTag)
)

// https://docs.ci.openshift.org/docs/getting-started/useful-links/#services
const (
	DefaultReleaseStream = "4-stable-multi"

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
		releaseStream = DefaultReleaseStream
	}

	var releaseURL string
	if strings.Contains(releaseStream, hyperv1.ArchitectureMulti) {
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
	var commitOnly, clientOnly bool
	namespace := "hypershift"
	cmd := &cobra.Command{
		Use:          "version",
		Short:        "Prints HyperShift CLI version",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			if commitOnly {
				fmt.Printf("%s\n", version.GetRevision())
				return
			}
			fmt.Printf("Client Version: %s\n", version.String())
			if clientOnly {
				return
			}
			client, err := util.GetClient()
			if err != nil {
				fmt.Printf("failed to connect to server: %v\n", err)
				return
			}

			supportedVersions := manifests.ConfigMap(namespace)
			if err := client.Get(cmd.Context(), crclient.ObjectKeyFromObject(supportedVersions), supportedVersions); err != nil {
				fmt.Printf("failed to find supported versions on the server: %v\n", err)
				return
			}
			if serverVersion, present := supportedVersions.Data[supportedversion.ConfigMapServerVersionKey]; present {
				fmt.Printf("Server Version: %s\n", serverVersion)
			} else {
				fmt.Println("The server did not advertise its HyperShift version.")
			}
			if supportedVersionData, present := supportedVersions.Data[supportedversion.ConfigMapVersionsKey]; present {
				var versions supportedversion.SupportedVersions
				if err := json.Unmarshal([]byte(supportedVersionData), &versions); err != nil {
					fmt.Printf("failed to parse supported versions on the server: %v\n", err)
					return
				}
				fmt.Printf("Server Supports OCP Versions: %s\n", strings.Join(versions.Versions, ", "))
			} else {
				fmt.Println("The server did not advertise supported OCP versions.")
			}
		},
	}
	cmd.Flags().BoolVar(&commitOnly, "commit-only", commitOnly, "Output only the code commit")
	cmd.Flags().BoolVar(&clientOnly, "client-only", clientOnly, "Output only the client version")
	cmd.Flags().StringVar(&namespace, "namespace", namespace, "The namespace in which HyperShift is installed")
	return cmd
}
