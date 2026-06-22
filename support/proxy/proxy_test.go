package proxy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestSetEnvVars(t *testing.T) {
	testCases := []struct {
		name                           string
		httpProxy, httpsProxy, noProxy string
		additionalNoProxy              []string
		currentEnvVars                 []corev1.EnvVar

		expected []corev1.EnvVar
	}{
		{
			name: "No proxy configured and no proxy in env vars, no change",
		},
		{
			name: "No proxy configured, proxy gets removed from env vars",
			currentEnvVars: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://foo"},
				{Name: "HTTPS_PROXY", Value: "http://foo"},
				{Name: "NO_PROXY", Value: "kube-apiserver"},
			},
		},
		{
			name:      "Proxy configured and gets added to env vars",
			httpProxy: "http://foo", httpsProxy: "http://foo", noProxy: "kube-apiserver",
			expected: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://foo"},
				{Name: "HTTPS_PROXY", Value: "http://foo"},
				{Name: "NO_PROXY", Value: "kube-apiserver"},
			},
		},
		{
			name:      "Proxy configured, env vars get changed",
			httpProxy: "http://foo", httpsProxy: "http://foo", noProxy: "kube-apiserver",
			currentEnvVars: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "nope"},
				{Name: "HTTPS_PROXY", Value: "neither"},
				{Name: "NO_PROXY", Value: "kas"},
			},
			expected: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://foo"},
				{Name: "HTTPS_PROXY", Value: "http://foo"},
				{Name: "NO_PROXY", Value: "kube-apiserver"},
			},
		},
		{
			name:      "kube-apiserver always gets included into NO_PROXY",
			httpProxy: "http://foo", httpsProxy: "http://foo",
			expected: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://foo"},
				{Name: "HTTPS_PROXY", Value: "http://foo"},
				{Name: "NO_PROXY", Value: "kube-apiserver"},
			},
		},
		{
			name:      "Additional no proxy is respected",
			httpProxy: "http://foo", httpsProxy: "http://foo",
			additionalNoProxy: []string{"dont-proxy-me"},
			expected: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://foo"},
				{Name: "HTTPS_PROXY", Value: "http://foo"},
				{Name: "NO_PROXY", Value: "dont-proxy-me,kube-apiserver"},
			},
		},
		{
			name:              "Additional no proxy does nothing if no proxy is configured",
			additionalNoProxy: []string{"dont-proxy-me"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.currentEnvVars == nil {
				tc.currentEnvVars = []corev1.EnvVar{}
			}

			envGetter := func(k string) string {
				if k == "HTTP_PROXY" {
					return tc.httpProxy
				}
				if k == "HTTPS_PROXY" {
					return tc.httpsProxy
				}
				if k == "NO_PROXY" {
					return tc.noProxy
				}
				t.Errorf("invalid env var %q was requested", k)
				return ""
			}
			setEnvVars(&tc.currentEnvVars, envGetter, tc.additionalNoProxy...)
			if diff := cmp.Diff(tc.currentEnvVars, tc.expected, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("actual env vars differ from expected: %s", diff)
			}
		})
	}
}
