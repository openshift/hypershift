package hcco

import "testing"

func TestOAuthServerChallengingClient(t *testing.T) {
	c := OAuthServerChallengingClient()
	if c.Name != "openshift-challenging-client" {
		t.Errorf("expected name %q, got %q", "openshift-challenging-client", c.Name)
	}
}
