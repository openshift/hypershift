// Package ignitionconsumer is the NodePool controller's side of the split ignition system. For
// each NodePool it authors an IgnitionPayload CR in the hosted control plane namespace, projects
// the NodePool's config sources for the payload generator, and consumes the userData Secret the
// generator authors. The NodePool controller does not call it yet.
package ignitionconsumer
