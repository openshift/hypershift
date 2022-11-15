package powervs

import (
	utilpointer "k8s.io/utils/pointer"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/IBM-Cloud/power-go-client/power/models"
)

func TestUseExistingDHCP(t *testing.T) {
	id1 := "id1"

	type expected struct {
		dhcpServerID string
	}

	type params struct {
		dhcpServers models.DHCPServers
		infraID     string
	}

	tests := map[string]struct {
		input params
		expected
	}{
		"Existing DHCP server matches infraID provided": {
			input: params{dhcpServers: models.DHCPServers{
				{
					ID: &id1,
					Network: &models.DHCPServerNetwork{
						Name: utilpointer.String("DHCPSERVERexample-4hasj_Private"),
					},
				},
			}, infraID: "example-4hasj"},
			expected: expected{dhcpServerID: id1},
		},
		"Existing DHCP server does not match the infraID provided": {
			input: params{dhcpServers: models.DHCPServers{
				{
					ID: &id1,
					Network: &models.DHCPServerNetwork{
						Name: utilpointer.String("DHCPSERVER0a4549e3cd8b463ab6a7cde2084f2dc4_Private"),
					},
				},
			}, infraID: "example-dhiha"},
			expected: expected{""},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			dhcpServerID := useExistingDHCP(test.input.dhcpServers, test.input.infraID)

			g.Expect(dhcpServerID).To(BeEquivalentTo(test.expected.dhcpServerID))
		})
	}
}
