package util

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGenerateRoleAssignmentName(t *testing.T) {
	t.Parallel()
	const scope = "/subscriptions/ABCDEF/resourceGroups/Shared-DNS"
	tests := []struct {
		name      string
		infraID   string
		component string
		scope     string
	}{
		{"When create and destroy scope casing differs, it should reproduce the same GUID", "Infra-A", "Ingress", scope},
		{"When the scope is lowercase, it should preserve the lowercase naming algorithm", "Infra-A", "Ingress", strings.ToLower(scope)},
		{"When infraID casing differs, it should preserve the infraID hashing input", "infra-a", "Ingress", scope},
		{"When component casing differs, it should preserve the component hashing input", "Infra-A", "ingress", scope},
		{"When the scope is empty, it should retain deterministic naming", "Infra-A", "Ingress", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			expected := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("%s-%s-%s", test.infraID, test.component, strings.ToLower(test.scope)))).String()
			actual := GenerateRoleAssignmentName(test.infraID, test.component, test.scope)
			if actual != expected || actual != GenerateRoleAssignmentName(test.infraID, test.component, strings.ToUpper(test.scope)) {
				t.Fatalf("expected case-independent scope naming %s, got %s", expected, actual)
			}
		})
	}
}
