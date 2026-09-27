// Package manifests provides shared manifest helpers used by e2e tests
// and operator controllers to construct Kubernetes resource scaffolds.
package manifests

import (
	"fmt"
	"strings"
)

func HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName string) string {
	return fmt.Sprintf("%s-%s", hostedClusterNamespace, strings.ReplaceAll(hostedClusterName, ".", "-"))
}
