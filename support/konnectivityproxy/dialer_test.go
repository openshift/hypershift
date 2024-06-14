package konnectivityproxy

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidate(t *testing.T) {

	tests := []struct {
		name        string
		o           Options
		expectValid bool
	}{
		{
			name: "valid options",
			o: Options{
				CAFile:           "test-ca",
				ClientCertBytes:  []byte("test-cert"),
				ClientKeyFile:    "test-key-name",
				KonnectivityHost: "example.org",
				KonnectivityPort: 123,
				Client:           fake.NewFakeClient(),
			},
			expectValid: true,
		},
		{
			name: "missing CA",
			o: Options{
				ClientCertBytes:  []byte("test-cert"),
				ClientKeyFile:    "test-key-name",
				KonnectivityHost: "example.org",
				KonnectivityPort: 123,
				Client:           fake.NewFakeClient(),
			},
			expectValid: false,
		},
		{
			name: "missing KonnectivityPort",
			o: Options{
				CABytes:          []byte("test-ca"),
				ClientCertBytes:  []byte("test-cert"),
				ClientKeyFile:    "test-key-name",
				KonnectivityHost: "example.org",
				Client:           fake.NewFakeClient(),
			},
			expectValid: false,
		},
		{
			name: "client cert file and bytes",
			o: Options{
				CAFile:           "test-ca",
				ClientCertFile:   "test-cert-file",
				ClientCertBytes:  []byte("test-cert"),
				ClientKeyFile:    "test-key-name",
				KonnectivityHost: "example.org",
				KonnectivityPort: 123,
				Client:           fake.NewFakeClient(),
			},
			expectValid: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.o.Validate()
			if test.expectValid && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !test.expectValid && err == nil {
				t.Errorf("did not get expected error")
			}
		})
	}
}
