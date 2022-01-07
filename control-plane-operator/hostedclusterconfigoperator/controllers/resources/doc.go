package resources

/*
Package resources contains the resources controller which is responsible for
reconciling resources on the hosted cluster. These include global configuration,
services/endpoints required by apiservers that run on the control plane,
olm service catalogs, etc.

The main reconcile function uses the pattern of collecting errors and returning
a single aggregate error at the end instead of short-circuiting when an error
occurs. This allows as much as possible to be reconciled with the guest cluster as
early as possible.

The manifests child package contains manifests for everything that is reconciled.
Other child packages contain reconciliation code for the different components that
are reconciled with the guest cluster.
*/
