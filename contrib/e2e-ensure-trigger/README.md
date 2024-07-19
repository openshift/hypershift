# E2E Ensure Function trigger

This is a small script which takes care of the execution of a EnsureXXX function in the Management cluster you have up an running already. It's tedious sometimes to wait until the CI to finish the E2E test until you got feedback for just a quick execution of an function you're working on. This is why this script exists:

## Prerequisites

The function you wanna call needs to implement the `e2eutils.Ensure` interface and be based in to a `e2eutils.EnsureFunc` function (E.G `EnsureNoCrashingPods`).

## Execution

To execute this you just need follow these steps:

```
export KUBECONFIG=<ManagementCluster Kubeconfig>
go test e2e_ensure_trigger_test.go --name <EnsureFunction Name> --hc <HostedCluster Name> --hcns <HostedCluster Namespace>
```

```
export KUBECONFIG=${HOME}/MGMT.kubeconfig
go test e2e_ensure_trigger_test.go --name "EnsureNoCrashingPods" --hc jparrill-hosted --hcns clusters

ok      command-line-arguments  1.833s
```

**Additional Note**: The tests executed will not fail if there not enough arguments to be executed in order to avoid conflicts with CI unit tests execution.