package manifests

import (
	"fmt"
	"strings"
)

func HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName string) string {
	return fmt.Sprintf("%s-%s", hostedClusterNamespace, strings.ReplaceAll(hostedClusterName, ".", "-"))
}
