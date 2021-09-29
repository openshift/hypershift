# Enhancements Tracking and Backlog

Enhancement tracking repository for HyperShift.

Inspired by the [OpenShift enhancement](https://github.com/openshift/enhancements) process.

This directory provides a rally point to discuss, debate, and reach consensus
for how HyperShift [enhancements](./enhancements) are introduced.

Enhancements may take multiple releases to ultimately complete and thus provide
the basis of a community roadmap.  Enhancements may be filed from anyone in the
community, but require consensus from domain specific project maintainers in
order to implement and accept into the release.

For a quick-start, FAQ, and template references, see [the guidelines](guidelines/README.md).

## Is My Thing an Enhancement?

A rough heuristic for an enhancement is anything that:

- impacts how a cluster is operated including addition or removal of significant
  capabilities
- impacts upgrade/downgrade
- needs significant effort to complete
- requires consensus/code across multiple domains/repositories
- proposes adding a new user-facing component
- has phases of maturity (Dev Preview, Tech Preview, GA)
- demands formal documentation to utilize

It is unlikely to require an enhancement if it:

- fixes a bug
- adds more testing
- internally refactors a code or component only visible to that components
  domain
- minimal impact to distribution as a whole

If you are not sure if the proposed work requires an enhancement, file an issue
and ask!

## When to Create a New Enhancement

Create an enhancement here once you:

- have circulated your idea to see if there is interest
- (optionally) have done a prototype in your own fork
- have identified people who agree to work on and maintain the enhancement
  - many enhancements will take several releases to complete

## Why are Enhancements Tracked

As the project evolves, its important that the HyperShift community understands how we
build, test, and document our work.  Individually it is hard to understand how
all parts of the system interact, but as a community we can lean on each other
to build the right design and approach before getting too deep into an
implementation.

## Life-cycle

Pull requests to this repository should be short-lived and merged as
soon as there is consensus.

Ideally pull requests with enhancement proposals will be merged before
significant coding work begins, since this avoids having to rework the
implementation if the design changes as well as arguing in favor of
accepting a design simply because it is already implemented.

## General constraints

HyperShift has some general constraints which are outlined here in order to help
inform proposals. Every proposal should take these constraints into
consideration.

1. Webhooks on the management cluster are generally avoided for performance and
   reliability considerations.

2. There are general resource (memory/cpu) constraints on control planes. It
   should be possible to fit 4 control planes in a 64GB 16vcpu node. This
   constraint will continue to be clarified going forward.

3. There is a (poorly defined) constraint on how long it should take for a
   control plane to come up: it can't be slow (~6min). This constraint will
   continue to be clarified going forward.

4. Generally, cluster-scoped resources per control plane (CRDs, cluster roles,
   cluster role bindings, etc.) are avoided. Although ingress-operators per
   cluster may need to be an exception (for private clusters).

5. In general, do not expose any part of control plane resources (pods,
   services, etc) to a guest cluster.
