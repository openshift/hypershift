package globalconfig

import (
	"bytes"
	"fmt"
	"text/template"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/util"
)

// Abbreviated version of the installer's InstallConfig type
// Bare minimum required to support MCS
type InstallConfig struct {
	MachineCIDRs []string
	Platform     string
	Region       string
}

func NewInstallConfig(hcp *hyperv1.HostedControlPlane) *InstallConfig {
	cfg := &InstallConfig{
		MachineCIDRs: util.MachineCIDRs(hcp.Spec.Networking.MachineNetwork),
		Platform:     string(hcp.Spec.Platform.Type),
	}
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		cfg.Region = hcp.Spec.Platform.AWS.Region
	}
	return cfg
}

// TODO (csrwng): replace with installconfig type if importing the type from the installer
// becomes a viable option. Currently it requires vendoring a lot of unrelated
// libraries.
const installConfigTemplateString = `apiVersion: v1
controlPlane:
  replicas: 1
networking:
  machineNetwork:
{{- range .MachineCIDRs }}
  - cidr: {{ . }}
{{- end }}
platform:
{{- if eq .Platform "AWS" }}
  aws:
    region: {{ .Region }}
{{- else }}
  none: {}
{{- end }}
`

var installConfigTemplate = template.Must(template.New("install-config").Parse(installConfigTemplateString))

func (c *InstallConfig) String() string {
	out := &bytes.Buffer{}
	if err := installConfigTemplate.Execute(out, c); err != nil {
		panic(fmt.Sprintf("unexpected error executing install-config template: %v", err))
	}
	return out.String()
}
