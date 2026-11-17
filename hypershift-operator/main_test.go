package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/rhobsmonitoring"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/go-logr/logr"
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
			name:               "When webhooks are enabled it should wait for the webhook server",
			webhookEnabled:     true,
			expectReadyToError: true,
		},
		{
			name:           "When webhooks are disabled it should report ready",
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

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			g.Expect(mgr.healthChecks["healthz"](request)).To(Succeed())
			if tc.expectReadyToError {
				g.Expect(mgr.readyChecks["readyz"](request)).To(MatchError("webhook server has not been started yet"))
			} else {
				g.Expect(mgr.readyChecks["readyz"](request)).To(Succeed())
			}
		})
	}
}

func TestValidateStartOptions(t *testing.T) {
	testCases := []struct {
		name                string
		enableGMPMonitoring bool
		rhobsEnv            string
		expectError         bool
	}{
		{name: "When GMP monitoring is disabled, it should pass"},
		{name: "When GMP monitoring is enabled without RHOBS, it should pass", enableGMPMonitoring: true},
		{name: "When RHOBS is enabled without GMP monitoring, it should pass", rhobsEnv: "1"},
		{name: "When GMP monitoring is enabled with RHOBS, it should fail", enableGMPMonitoring: true, rhobsEnv: "1", expectError: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			t.Setenv(rhobsmonitoring.EnvironmentVariable, tc.rhobsEnv)
			opts := &StartOptions{EtcdBackupMaxCount: 1, EnableGMPMonitoring: tc.enableGMPMonitoring}
			err := validateStartOptions(opts, logr.Discard())
			if tc.expectError {
				g.Expect(err).To(MatchError(ContainSubstring("--enable-gmp-monitoring")))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
