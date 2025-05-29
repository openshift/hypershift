package konnectivityhttpsproxy

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	. "github.com/onsi/gomega"
)

func TestShouldDialDirectFunc(t *testing.T) {
	tests := []struct {
		name                       string
		connectDirectlyToCloudAPIs bool
		isCloudAPI                 bool
		emptyProxyURL              bool
		expected                   bool
	}{
		{
			name:                       "cloud API",
			connectDirectlyToCloudAPIs: true,
			isCloudAPI:                 true,
			expected:                   true,
		},
		{
			name:                       "not cloud API",
			connectDirectlyToCloudAPIs: true,
			isCloudAPI:                 false,
			expected:                   false,
		},
		{
			name:                       "not cloud API, no proxy URL",
			connectDirectlyToCloudAPIs: true,
			isCloudAPI:                 false,
			emptyProxyURL:              true,
			expected:                   true,
		},
		{
			name:                       "not cloud API, has proxy URL",
			connectDirectlyToCloudAPIs: true,
			isCloudAPI:                 false,
			emptyProxyURL:              false,
			expected:                   false,
		},
		{
			name:                       "do not connect directly to cloud APIs",
			connectDirectlyToCloudAPIs: false,
			isCloudAPI:                 true,
			expected:                   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isCloudAPI := func(string) bool {
				return tc.isCloudAPI
			}
			userProxyFunc := func(u *url.URL) (*url.URL, error) {
				if tc.emptyProxyURL {
					return nil, nil
				}
				return u, nil
			}
			g := NewGomegaWithT(t)
			proxyURL, err := url.Parse("http://proxy.example.com:3128")
			g.Expect(err).NotTo(HaveOccurred())
			f := shouldDialDirectFunc(tc.connectDirectlyToCloudAPIs, isCloudAPI, userProxyFunc)
			result, err := f(proxyURL)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestAddBasicAuthHeader(t *testing.T) {
	userInfo := url.UserPassword("user", "password")
	tests := []struct {
		name           string
		userInfo       *url.Userinfo
		expectedHeader string
	}{
		{
			name:           "no userinfo",
			userInfo:       nil,
			expectedHeader: "",
		},
		{
			name:           "userinfo present",
			userInfo:       userInfo,
			expectedHeader: fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(userInfo.String()))),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			f := addBasicAuthHeader(tc.userInfo)
			req := &http.Request{
				Header: http.Header{},
			}
			f(req)
			value := req.Header.Get("Proxy-Authorization")
			g.Expect(value).To(Equal(tc.expectedHeader))
		})
	}
}
