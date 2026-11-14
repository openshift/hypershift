# Agent provider upgrade fixtures

These CRDs are frozen copies from HyperShift commit
`53a841e790ec795b3393c36ae24c91708f305778`:

- `cmd/install/assets/crds/cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentclusters.yaml`
- `cmd/install/assets/crds/cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentmachines.yaml`

The tests install these schemas before applying the current generated CRDs.
Keep the fixtures unchanged when regenerating the production CRDs so that the
tests continue to exercise the previous wire contract and persisted objects.
