package fixtures

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestParseTenantClassString(t *testing.T) {
	testsCases := []struct {
		name          string
		optionString  string
		expectedName  string
		expectedGroup string
	}{
		{
			name:          "straight class name, no options",
			optionString:  "tenant1",
			expectedName:  "tenant1",
			expectedGroup: "",
		},
		{
			name:          "class name with group option",
			optionString:  "tenant1,group=group1",
			expectedName:  "tenant1",
			expectedGroup: "group1",
		},
		{
			name:          "ignore invalid option",
			optionString:  "tenant1,invalid=invalid",
			expectedName:  "tenant1",
			expectedGroup: "",
		},
		{
			name:          "class name with group option, and ignore invalid options",
			optionString:  "tenant1, group=group1,invalid=invalid",
			expectedName:  "tenant1",
			expectedGroup: "group1",
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			res, options := parseTenantClassString(tc.optionString)
			g.Expect(res).To(Equal(tc.expectedName))
			g.Expect(options).To(Equal(tc.expectedGroup))
		})
	}
}
