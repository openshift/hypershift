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

import "github.com/blang/semver"

var (
	// y-stream versions supported by e2e in main
	Version51  = semver.MustParse("5.1.0")
	Version50  = semver.MustParse("5.0.0")
	Version423 = semver.MustParse("4.23.0")
	Version422 = semver.MustParse("4.22.0")
	Version421 = semver.MustParse("4.21.0")
	Version420 = semver.MustParse("4.20.0")
	Version419 = semver.MustParse("4.19.0")
	Version418 = semver.MustParse("4.18.0")
	Version417 = semver.MustParse("4.17.0")
	Version416 = semver.MustParse("4.16.0")
)
