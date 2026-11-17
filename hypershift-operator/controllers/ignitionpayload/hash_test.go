package ignitionpayload

import (
	"testing"

	. "github.com/onsi/gomega"
)

func baseHashInputs() hashInputs {
	return hashInputs{
		resolved:            ResolvedConfig{RolloutManifests: "ROLLOUT", ManagementManifests: "HAPROXY-v1"},
		releaseVersion:      "4.20.0",
		pullSecretName:      "ps",
		pullSecretContent:   []byte("psc"),
		trustBundleName:     "tb",
		trustBundleContent:  []byte("tbc"),
		rolloutGlobalConfig: []byte("gc"),
		osImageStream:       "rhel-9",
	}
}

func TestPayloadIdentityHash(t *testing.T) {
	g := NewWithT(t)
	base := baseHashInputs()
	id0 := payloadIdentityHash(base)
	g.Expect(id0).ToNot(BeEmpty())

	// Everything embedded in the payload changes the identity.
	mgmt := base
	mgmt.resolved.ManagementManifests = "HAPROXY-v2"
	g.Expect(payloadIdentityHash(mgmt)).ToNot(Equal(id0), "mgmt change must change identity")

	rollout := base
	rollout.resolved.RolloutManifests = "ROLLOUT2"
	g.Expect(payloadIdentityHash(rollout)).ToNot(Equal(id0))

	// (RF#2) in-place credential/CA content rotation changes identity.
	ps := base
	ps.pullSecretContent = []byte("psc-rotated")
	g.Expect(payloadIdentityHash(ps)).ToNot(Equal(id0))
	tb := base
	tb.trustBundleContent = []byte("tbc2")
	g.Expect(payloadIdentityHash(tb)).ToNot(Equal(id0))

	// additionalRolloutInputs is a rollout trigger, NOT part of the payload's byte identity.
	ari := base
	ari.additionalRolloutInputs = "endpoint=1.2.3.4:443;mode=Public"
	g.Expect(payloadIdentityHash(ari)).To(Equal(id0), "additionalRolloutInputs must NOT change the identity hash")

	// Full-config and cloud-config changes refresh the payload identity (they feed rendering).
	hcc := base
	hcc.hcConfigHash = "hc-v2"
	g.Expect(payloadIdentityHash(hcc)).ToNot(Equal(id0), "full-config change must change identity")
	cc := base
	cc.cloudConfigHash = "cloud-v2"
	g.Expect(payloadIdentityHash(cc)).ToNot(Equal(id0), "cloud-config change must change identity")
}

func TestRolloutHash(t *testing.T) {
	g := NewWithT(t)
	base := baseHashInputs()
	ro0 := rolloutHash(base)
	g.Expect(ro0).ToNot(BeEmpty())

	// (RF#1) a management-side (HAProxy) change must NOT advance the rollout hash.
	mgmt := base
	mgmt.resolved.ManagementManifests = "HAPROXY-v2"
	g.Expect(rolloutHash(mgmt)).To(Equal(ro0), "mgmt change must NOT change rollout hash")

	// A rollout-relevant config change does.
	rollout := base
	rollout.resolved.RolloutManifests = "ROLLOUT2"
	g.Expect(rolloutHash(rollout)).ToNot(Equal(ro0))

	// (RF#2) credential/CA CONTENT rotation uses the NAME, so it does not roll; a NAME change does.
	ps := base
	ps.pullSecretContent = []byte("psc-rotated")
	g.Expect(rolloutHash(ps)).To(Equal(ro0))
	pn := base
	pn.pullSecretName = "ps2"
	g.Expect(rolloutHash(pn)).ToNot(Equal(ro0))
	tb := base
	tb.trustBundleContent = []byte("tbc2")
	g.Expect(rolloutHash(tb)).To(Equal(ro0))

	// rolloutGlobalConfig / osImageStream / releaseVersion all feed the rollout hash.
	gc := base
	gc.rolloutGlobalConfig = []byte("gc2")
	g.Expect(rolloutHash(gc)).ToNot(Equal(ro0))
	os := base
	os.osImageStream = "rhel-10"
	g.Expect(rolloutHash(os)).ToNot(Equal(ro0))
	rv := base
	rv.releaseVersion = "4.21.0"
	g.Expect(rolloutHash(rv)).ToNot(Equal(ro0))

	// additionalRolloutInputs feeds the rollout hash only (rolls running nodes).
	ari := base
	ari.additionalRolloutInputs = "endpoint=1.2.3.4:443;mode=Public"
	g.Expect(rolloutHash(ari)).ToNot(Equal(ro0), "additionalRolloutInputs must change the rollout hash")

	// Full-config and cloud-config feed the identity hash only, never the rollout hash.
	hcc := base
	hcc.hcConfigHash = "hc-v2"
	g.Expect(rolloutHash(hcc)).To(Equal(ro0), "full-config change must NOT roll")
	cc := base
	cc.cloudConfigHash = "cloud-v2"
	g.Expect(rolloutHash(cc)).To(Equal(ro0), "cloud-config change must NOT roll")
}
