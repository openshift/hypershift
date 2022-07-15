package powervs

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestCreateServiceIDClient(t *testing.T) {

	type args struct {
		name               string
		apiKey             string
		account            string
		resourceGroupID    string
		crYaml             string
		secretRefName      string
		secretRefNamespace string
	}

	tests := map[string]struct {
		input       args
		errExpected bool
	}{
		"Create client ID with proper CR YAML": {
			input: args{
				name:               "name",
				apiKey:             "apiKey",
				account:            "account",
				resourceGroupID:    "id_1234",
				crYaml:             kubeCloudControllerManagerCR,
				secretRefName:      "secretName",
				secretRefNamespace: "secretNamespace",
			},
			errExpected: false,
		},
		"Create client ID with invalid CR YAML": {
			input: args{
				name:               "name",
				apiKey:             "apiKey",
				account:            "account",
				resourceGroupID:    "id_1234",
				crYaml:             "invalid yaml",
				secretRefName:      "secretName",
				secretRefNamespace: "secretNamespace",
			},
			errExpected: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			_, err := createServiceIDClient(test.input.name, test.input.apiKey, test.input.account, test.input.resourceGroupID, test.input.crYaml, test.input.secretRefName, test.input.secretRefNamespace)
			if test.errExpected {
				g.Expect(err).ToNot(BeNil())
			} else {
				g.Expect(err).To(BeNil())
			}

		})
	}
}
