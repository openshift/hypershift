// Package mirrorregistry provisions a disposable container image mirror
// registry on an OpenShift management cluster for image-mirroring e2e tests.
//
// The registry runs the upstream distribution "registry" 2.x server as a
// Deployment, serves TLS with a certificate signed by a test CA, and is exposed
// to hosted cluster nodes through a passthrough Route with a deterministic
// host. Images are copied in through the in-cluster Service by a Job while the
// registry is writable; the registry is then switched to read-only mode before
// the Route is created, so the public endpoint is anonymous and read-only.
//
// The package is a test fixture: it provisions infrastructure and reports
// what it mirrored. It deliberately does not import HyperShift's production
// image-resolution code and does not assert any resolution or fallback
// behavior; consuming tests own their assertions.
//
// The package has no build tag and no Ginkgo dependency so that it can be used
// from Ginkgo specs, from lifecycle hooks in create-guests, and from
// standalone tools, and so that its unit tests run with "make test".
package mirrorregistry
