# GMP assets

Generated `PodMonitoring` manifests for the Google Managed Prometheus (GMP)
backend. They are staged here, outside `v2/assets`, on purpose:

- `v2/assets` is embedded with `//go:embed */*.yaml`, and every manifest in it
  is reconciled by the component framework (`support/controlplane-component`).
- This directory is **not embedded and not reconciled**. Nothing in the
  control-plane-operator reads it at runtime today.
- `TestGMPAssetsAreNotEmbedded` fails if any embedded manifest declares a GMP
  resource (a `PodMonitoring` kind or a `monitoring.googleapis.com`
  apiVersion), or if a file staged here also exists at the same relative path
  in `v2/assets`. The follow-up that activates GMP monitoring replaces it.

The CoreOS `ServiceMonitor` / `PodMonitor` assets under `v2/assets` remain the
source of truth. `hack/gmp-config-gen` derives these files from them:

```bash
make generate-gmp-config   # rewrite these files
make verify-gmp-config     # fail if they are stale
```

CI runs `verify-gmp-config` as part of `make lint`. Do not hand-edit these
files: change the CoreOS asset and regenerate.

Loading these manifests, selecting them per platform, and reconciling them with
the GMP collector is a separate, flag-gated runtime change.
