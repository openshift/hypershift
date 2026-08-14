package infraid

import (
	"fmt"
	"regexp"
	"strings"

	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

func New(clusterName string) string {
	return generateInfraID(clusterName, 27)
}

const (
	randomLen = 5
)

// generateInfraID is a straight copy of
// https://github.com/openshift/installer/blob/3d19350885d593ee2b1d9ecd7612c2d697dab2a3/pkg/asset/installconfig/clusterid.go#L60
func generateInfraID(base string, maxLen int) string {
	maxBaseLen := maxLen - (randomLen + 1)

	// replace all characters that are not `alphanum` or `-` with `-`
	re := regexp.MustCompile("[^A-Za-z0-9-]")
	base = re.ReplaceAllString(base, "-")

	// replace all multiple dashes in a sequence with single one.
	re = regexp.MustCompile(`-{2,}`)
	base = re.ReplaceAllString(base, "-")

	// truncate to maxBaseLen
	if len(base) > maxBaseLen {
		base = base[:maxBaseLen]
	}
	base = strings.TrimRight(base, "-")

	// add random chars to the end to randomize
	return fmt.Sprintf("%s-%s", base, utilrand.String(randomLen))
}
