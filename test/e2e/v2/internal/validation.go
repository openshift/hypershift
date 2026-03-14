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

package internal

import (
	"fmt"
	"strings"

	"github.com/onsi/gomega"
	"github.com/openshift/hypershift/test/e2e/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateConditions validates that all expected conditions match the actual conditions.
// It cycles through each expected condition and verifies that a matching condition exists
// in the actual conditions list. Uses Gomega assertions to report failures.
func ValidateConditions(g gomega.Gomega, object client.Object, expectedConditions []util.Condition) {
	actualConditions, err := util.Conditions(object)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	for _, expectedCondition := range expectedConditions {
		var found bool
		var actualCondition util.Condition

		// Find the condition with matching Type
		for _, condition := range actualConditions {
			if condition.Type == expectedCondition.Type {
				found = true
				actualCondition = condition
				break
			}
		}
		printConditions := func() string {
			conditions :=
				[]string{fmt.Sprintf("%T %s/%s conditions at RV %s:", object, object.GetNamespace(), object.GetName(), object.GetResourceVersion())}
			for _, condition := range actualConditions {
				conditions = append(conditions, condition.String())
			}
			return strings.Join(conditions, "\n\t\t")
		}

		g.Expect(found).To(gomega.BeTrue(),
			fmt.Sprintf("condition %s not found\n\t%s", expectedCondition.Type, printConditions()))
		g.Expect(expectedCondition.Matches(actualCondition)).To(gomega.BeTrue(),
			fmt.Sprintf("incorrect condition: wanted %s, got %s\n\t%s",
				expectedCondition.String(), actualCondition.String(), printConditions()))
	}
}
