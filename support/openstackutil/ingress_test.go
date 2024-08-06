package openstackutil

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestValidateIngressOptions(t *testing.T) {
	tests := []struct {
		name              string
		ingressProvider   string
		ingressFloatingIP string
		expectedError     string
	}{
		{
			name:              "valid options",
			ingressProvider:   "Octavia",
			ingressFloatingIP: "1.2.3.4",
			expectedError:     "",
		},
		{
			name:              "missing floating IP",
			ingressProvider:   "Octavia",
			ingressFloatingIP: "",
			expectedError:     "cannot set ingress provider without specifying floating IP",
		},
		{
			name:              "missing provider",
			ingressProvider:   "",
			ingressFloatingIP: "1.2.3.4",
			expectedError:     "cannot set floating IP without specifying ingress provider",
		},
		{
			name:              "None provider with floating IP",
			ingressProvider:   "None",
			ingressFloatingIP: "1.2.3.4",
			expectedError:     "invalid ingress provider None",
		},
		{
			name:              "invalid provider with floating IP",
			ingressProvider:   "invalid",
			ingressFloatingIP: "1.2.3.4",
			expectedError:     "invalid ingress provider invalid",
		},
		{
			name:              "empty options",
			ingressProvider:   "",
			ingressFloatingIP: "",
			expectedError:     "",
		},
	}

	for _, test := range tests {
		err := ValidateIngressOptions(hyperv1.OpenStackIngressProvider(test.ingressProvider), test.ingressFloatingIP)
		if test.expectedError != "" {
			if err == nil {
				t.Errorf("%s: expected error '%s', got no error", test.name, test.expectedError)
			} else if err.Error() != test.expectedError {
				t.Errorf("%s: expected error '%s', got '%s'", test.name, test.expectedError, err.Error())
			}
		} else {
			if err != nil {
				t.Errorf("%s: expected no error, got '%s'", test.name, err.Error())
			}
		}
	}
}
