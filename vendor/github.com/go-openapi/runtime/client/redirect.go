// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/runtime/client/internal/request"
)

// maxRedirects matches the limit of the default policy of [http.Client].
const maxRedirects = 10

var errTooManyRedirects = errors.New("stopped after 10 redirects")

// redirectPolicy is the CheckRedirect of the [http.Client] built by
// [Runtime.ensureClient], and of a client supplied with [NewWithClient] or
// [runtime.ClientOperation.Client] when its CheckRedirect is nil (see [Runtime.withRedirectPolicy]).
// A supplied client with a CheckRedirect of its own keeps its policy.
//
// It follows up to 10 redirects, like the default policy of [http.Client].
//
// net/http removes Authorization and Cookie when a redirect leaves the host of
// the original request and its subdomains. It leaves every other header in
// place, and sets Referer to the previous URL, query string included.
// An API key set by [APIKeyAuth] in a header or in the query would
// follow the redirect to any host.
//
// redirectPolicy applies the net/http rule to every header and query parameter
// set by the auth writer of the operation, and removes Referer with them.
// It also removes the credentials on a redirect from https to http,
// which net/http allows when the host stays the same.
//
// A redirect to one of [Runtime.TrustedRedirectHosts] keeps all the credentials
// set by the auth writer, Authorization included.
//
// Once a hop has removed the credentials, they stay off for the rest of the
// chain, even when it comes back to the original host (see CVE-2024-45336).
func (r *Runtime) redirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errTooManyRedirects
	}

	initial := via[0].URL
	for _, hop := range via[1:] {
		if !r.credentialsMayFollow(initial, hop.URL) {
			stripCredentials(req)

			return nil
		}
	}

	if !r.credentialsMayFollow(initial, req.URL) {
		stripCredentials(req)

		return nil
	}

	restoreCredentials(req, via[0])

	return nil
}

// restoreCredentials copies back from the original request the headers set by the auth writer
// that net/http removed from req.
//
// net/http removes Authorization on a redirect to a host in [Runtime.TrustedRedirectHosts],
// before it calls CheckRedirect.
func restoreCredentials(req, original *http.Request) {
	names, ok := request.CredentialNamesFromContext(req.Context())
	if !ok {
		return
	}

	for _, name := range names.Headers {
		if _, present := req.Header[name]; present {
			continue
		}

		if values, set := original.Header[name]; set {
			req.Header[name] = slices.Clone(values)
		}
	}
}

// withRedirectPolicy returns client, or a shallow copy of it with [Runtime.redirectPolicy]
// as CheckRedirect when client has none. The client of the caller is not modified.
//
// The copy is made for each request, so it sees a Transport replaced
// later, for example by [Runtime.EnableConnectionReuse].
func (r *Runtime) withRedirectPolicy(client *http.Client) *http.Client {
	if client.CheckRedirect != nil {
		return client
	}

	c := *client
	c.CheckRedirect = r.redirectPolicy

	return &c
}

// credentialsMayFollow reports whether credentials sent to initial may be sent to dest.
//
// It returns false on a redirect from https to http. Otherwise, it returns true if the host name of dest
// is the host name of initial, a subdomain of it, or one of [Runtime.TrustedRedirectHosts] or a subdomain of it.
//
// Ports are ignored, like net/http does.
func (r *Runtime) credentialsMayFollow(initial, dest *url.URL) bool {
	if strings.EqualFold(initial.Scheme, schemeHTTPS) && !strings.EqualFold(dest.Scheme, schemeHTTPS) {
		return false
	}

	host := strings.ToLower(dest.Hostname())
	if isDomainOrSubdomain(host, strings.ToLower(initial.Hostname())) {
		return true
	}

	for _, trusted := range r.TrustedRedirectHosts {
		if trusted != "" && isDomainOrSubdomain(host, strings.ToLower(trusted)) {
			return true
		}
	}

	return false
}

// stripCredentials removes from req the credentials set by the auth writer,
// as well as Authorization, Cookie and Referer.
func stripCredentials(req *http.Request) {
	req.Header.Del(runtime.HeaderAuthorization)
	req.Header.Del("Cookie")
	// net/http sets Referer to the previous URL, query string included,
	// before it calls CheckRedirect.
	req.Header.Del("Referer")

	names, ok := request.CredentialNamesFromContext(req.Context())
	if !ok {
		return
	}

	for _, name := range names.Headers {
		req.Header.Del(name)
	}

	deleteQueryParams(req.URL, names.Queries)
}

// deleteQueryParams removes the named parameters from the raw query of u.
//
// Other parameters are kept byte for byte and in order: a round trip
// through [url.Values] would sort and re-escape them, and drop those that
// [url.ParseQuery] rejects, which breaks a signed URL.
func deleteQueryParams(u *url.URL, names []string) {
	if len(names) == 0 || u.RawQuery == "" {
		return
	}

	parts := strings.Split(u.RawQuery, "&")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		if decoded, err := url.QueryUnescape(key); err == nil {
			key = decoded
		}

		if slices.Contains(names, key) {
			continue
		}

		kept = append(kept, part)
	}

	if len(kept) != len(parts) {
		u.RawQuery = strings.Join(kept, "&")
	}
}

// isDomainOrSubdomain reports whether sub is parent or a subdomain of parent.
// Both must be lower case.
//
// This is a copy of the unexported function in net/http.
func isDomainOrSubdomain(sub, parent string) bool {
	if sub == parent {
		return true
	}

	// An IPv6 address or zone is never a subdomain:
	// "::1%.www.example.com" is not a subdomain of "www.example.com".
	if strings.ContainsAny(sub, ":%") {
		return false
	}

	if !strings.HasSuffix(sub, parent) {
		return false
	}

	return sub[len(sub)-len(parent)-1] == '.'
}
