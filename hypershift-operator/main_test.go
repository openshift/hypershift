package main

import (
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

type fakeHealthCheckManager struct {
	webhookServer webhook.Server
	healthChecks  map[string]healthz.Checker
	readyChecks   map[string]healthz.Checker
}

func (m *fakeHealthCheckManager) AddHealthzCheck(name string, check healthz.Checker) error {
	m.healthChecks[name] = check
	return nil
}

func (m *fakeHealthCheckManager) AddReadyzCheck(name string, check healthz.Checker) error {
	m.readyChecks[name] = check
	return nil
}

func (m *fakeHealthCheckManager) GetWebhookServer() webhook.Server {
	return m.webhookServer
}

func TestSetupHealthChecks(t *testing.T) {
	for _, tc := range []struct {
		name               string
		webhookEnabled     bool
		expectReadyToError bool
	}{
		{
			name:               "webhook readiness waits for the webhook server",
			webhookEnabled:     true,
			expectReadyToError: true,
		},
		{
			name:           "readiness succeeds when webhooks are disabled",
			webhookEnabled: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			mgr := &fakeHealthCheckManager{
				webhookServer: webhook.NewServer(webhook.Options{}),
				healthChecks:  map[string]healthz.Checker{},
				readyChecks:   map[string]healthz.Checker{},
			}

			g.Expect(setupHealthChecks(mgr, tc.webhookEnabled)).To(Succeed())
			g.Expect(mgr.healthChecks).To(HaveKey("healthz"))
			g.Expect(mgr.readyChecks).To(HaveKey("readyz"))

			request := httptest.NewRequest("GET", "/", nil)
			g.Expect(mgr.healthChecks["healthz"](request)).To(Succeed())
			if tc.expectReadyToError {
				g.Expect(mgr.readyChecks["readyz"](request)).To(MatchError("webhook server has not been started yet"))
			} else {
				g.Expect(mgr.readyChecks["readyz"](request)).To(Succeed())
			}
		})
	}
}
