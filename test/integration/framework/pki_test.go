package framework

import (
	"testing"

	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
)

func TestRegeneratePKI(t *testing.T) {
	for _, signer := range []certificates.SignerClass{
		certificates.CustomerBreakGlassSigner,
		certificates.SREBreakGlassSigner,
	} {
		CertKeyRequest(t, signer)
	}
}
