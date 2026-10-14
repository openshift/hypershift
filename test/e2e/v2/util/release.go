//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import "strings"

// ExtractVersionFromReleaseImage extracts the version from a release image reference.
// For example: "quay.io/openshift-release-dev/ocp-release:4.19.10-x86_64" -> "4.19.10"
func ExtractVersionFromReleaseImage(releaseImage string) string {
	parts := strings.Split(releaseImage, ":")
	if len(parts) != 2 {
		return ""
	}

	tag := parts[1]

	tagParts := strings.Split(tag, "-")
	if len(tagParts) < 2 {
		return tag // No architecture suffix, return as-is
	}

	lastPart := tagParts[len(tagParts)-1]
	if lastPart == "x86_64" || lastPart == "amd64" || lastPart == "arm64" || lastPart == "ppc64le" || lastPart == "s390x" || lastPart == "multi" {
		return strings.Join(tagParts[:len(tagParts)-1], "-")
	}

	return tag
}
