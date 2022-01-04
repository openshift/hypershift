package version

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
)

var (
	// TODO: This goes away when control-plane-operator becomes another component
	// in the OCP payload.
	HyperShiftImage = "quay.io/hypershift/hypershift:latest"
)

// https://docs.ci.openshift.org/docs/getting-started/useful-links/#services
const releaseURL = "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4-stable/latest"

type OCPVersion struct {
	Name        string `json:"name"`
	PullSpec    string `json:"pullSpec"`
	DownloadURL string `json:"downloadURL"`
}

func LookupDefaultOCPVersion() (OCPVersion, error) {
	var version OCPVersion
	resp, err := http.Get(releaseURL)
	if err != nil {
		return version, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return version, err
	}
	err = json.Unmarshal(body, &version)
	if err != nil {
		return version, err
	}
	return version, nil
}
