/*
Copyright 2022 The Kubernetes Authors.

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

package maps

import (
	"strings"
)

// FilterByKeyPrefix returns a sub-map of the input that only contains keys starting with 'prefix'.
func FilterByKeyPrefix(input map[string]string, prefix string) map[string]string {
	var result = map[string]string{}
	for key, value := range input {
		if strings.HasPrefix(key, prefix) {
			remainingKey := strings.TrimPrefix(key, prefix)
			if len(remainingKey) > 0 {
				result[remainingKey] = value
			}
		}
	}
	return result
}
