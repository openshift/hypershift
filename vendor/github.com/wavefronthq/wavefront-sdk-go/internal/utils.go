package internal

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

var semVerRegex = regexp.MustCompile("([0-9]\\d*)\\.(\\d+)\\.(\\d+)(?:-([a-zA-Z0-9]+))?")

func GetHostname(defaultVal string) string {
	hostname, err := os.Hostname()
	if err != nil {
		return defaultVal
	}
	return hostname
}

func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

func GetSemVer(version string) (float64, error) {
	if len(version) > 0 {
		res := semVerRegex.FindStringSubmatch(version)
		if len(res) >= 4 {
			sdkVersion := fmt.Sprintf("%s.%02s%02s", res[1], res[2], res[3])
			return strconv.ParseFloat(sdkVersion, 64)
		}
	}
	return float64(0), nil
}
