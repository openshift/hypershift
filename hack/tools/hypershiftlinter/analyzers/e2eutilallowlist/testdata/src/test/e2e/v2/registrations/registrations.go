package registrations

import (
	g "github.com/onsi/ginkgo/v2"
	u "github.com/openshift/hypershift/test/e2e/util"
)

var _ = g.Describe("existing suite", func() {
	g.BeforeEach(func() { _ = u.Version414 })
	g.It("new spec", func() { _ = u.Version414 }) // want `reference to .*util.Version414 is not allowed`
	g.BeforeEach(func() { _ = u.Version414 })     // want `reference to .*util.Version414 is not allowed`
	g.Context("new context", func() {
		g.BeforeEach(func() { _ = u.Version414 }) // want `reference to .*util.Version414 is not allowed`
	})
})
var _ = g.Describe("duplicate", func() { _ = u.Version414 })
var _ = g.Describe("duplicate", func() { _ = u.Version414 }) // want `reference to .*util.Version414 is not allowed`
