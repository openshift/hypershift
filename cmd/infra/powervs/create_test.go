package powervs

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/IBM-Cloud/power-go-client/power/models"
)

func TestUseExistingDHCP(t *testing.T) {
	id1 := "id1"
	id2 := "id2"

	type expected struct {
		dhcpServerID string
		err          error
		errExpected  bool
	}

	tests := map[string]struct {
		input    models.DHCPServers
		expected expected
	}{
		"DHCPServerDetail returned with no error": {
			input:    models.DHCPServers{{ID: &id1}},
			expected: expected{dhcpServerID: id1, err: nil, errExpected: false},
		},
		"Error expected when more than one DHCPServer exist": {
			input:    models.DHCPServers{{ID: &id1}, {ID: &id2}},
			expected: expected{"", dhcpServerLimitExceeds(2), true},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			dhcpServerID, err := useExistingDHCP(test.input)

			g.Expect(dhcpServerID).To(BeEquivalentTo(test.expected.dhcpServerID))

			if test.expected.errExpected {
				g.Expect(err).To(BeEquivalentTo(test.expected.err))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
