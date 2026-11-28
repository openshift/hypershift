// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package request

import (
	"context"
	"maps"
	"slices"
)

// CredentialNames lists the header names and query parameter names that a
// [runtime.ClientAuthInfoWriter] added or changed on a request.
//
// They travel in the context of the [http.Request] so that the redirect
// policy of the client can withhold them from a redirect to another host.
type CredentialNames struct {
	Headers []string
	Queries []string
}

type credentialNamesKey struct{}

// WithCredentialNames returns a child of ctx carrying names, or ctx itself
// when names is empty.
func WithCredentialNames(ctx context.Context, names CredentialNames) context.Context {
	if len(names.Headers) == 0 && len(names.Queries) == 0 {
		return ctx
	}

	return context.WithValue(ctx, credentialNamesKey{}, names)
}

// CredentialNamesFromContext returns the names stored by [WithCredentialNames].
func CredentialNamesFromContext(ctx context.Context) (CredentialNames, bool) {
	names, ok := ctx.Value(credentialNamesKey{}).(CredentialNames)

	return names, ok
}

// recordCredentials runs authenticate and records in r.credentials the headers and
// query parameters it added or changed.
func (r *Request) recordCredentials(authenticate func() error) error {
	beforeHeaders := maps.Clone(map[string][]string(r.header))
	beforeQueries := maps.Clone(map[string][]string(r.query))
	// maps.Clone is shallow: copy the value slices, since Set may reuse them.
	for k, v := range beforeHeaders {
		beforeHeaders[k] = slices.Clone(v)
	}
	for k, v := range beforeQueries {
		beforeQueries[k] = slices.Clone(v)
	}

	if err := authenticate(); err != nil {
		return err
	}

	r.credentials.Headers = appendChanged(r.credentials.Headers, beforeHeaders, r.header)
	r.credentials.Queries = appendChanged(r.credentials.Queries, beforeQueries, r.query)

	return nil
}

func appendChanged[M ~map[string][]string](names []string, before map[string][]string, after M) []string {
	for name, values := range after {
		if old, ok := before[name]; !ok || !slices.Equal(old, values) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	return slices.Compact(names)
}
