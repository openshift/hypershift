package kas

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
)

// TODO (cewong): Add tests for other params
func TestNewAPIServerParamsAPIAdvertiseAddressAndPort(t *testing.T) {
	tests := []struct {
		name             string
		advertiseAddress *string
		port             *int32
		expectedAddress  string
		expectedPort     int32
	}{
		{
			name:            "not specified",
			expectedAddress: config.DefaultAdvertiseAddress,
			expectedPort:    config.DefaultAPIServerPort,
		},
		{
			name:             "address specified",
			advertiseAddress: pointer.StringPtr("1.2.3.4"),
			expectedAddress:  "1.2.3.4",
			expectedPort:     config.DefaultAPIServerPort,
		},
		{
			name:            "port set",
			port:            pointer.Int32Ptr(6789),
			expectedAddress: config.DefaultAdvertiseAddress,
			expectedPort:    6789,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Spec.APIAdvertiseAddress = test.advertiseAddress
			hcp.Spec.APIPort = test.port
			p := NewKubeAPIServerParams(context.Background(), hcp, globalconfig.GlobalConfig{}, map[string]string{}, "", 0, false)
			g := NewGomegaWithT(t)
			g.Expect(p.AdvertiseAddress).To(Equal(test.expectedAddress))
			g.Expect(p.APIServerPort).To(Equal(test.expectedPort))
		})
	}
}
