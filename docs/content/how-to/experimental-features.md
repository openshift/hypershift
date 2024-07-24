---
title: Experimental features
---

# Experimental features

Hypershift ships with a "featuregate" package where you can specify new experimental features behind a feature gate.

The Hypershift Operator has a featuregate/ reconciler where experimental features that affect the operator can be added.

The Control Plane Operator has featuregate/ folder where experimental components reconciliation can be added.

The Hosted Cluster Config Operator has a featuregate/ reconciler where experimental features that affect the operator can be added.

Additional changes that need to interfere within the stable controllers logic can be added behind a check such as

```go
if featuregateConfig.Gates.Enabled(featuregateConfig.AutoProvision) {}
```

TODO(alberto): Elaborate experimental APIs policy.

