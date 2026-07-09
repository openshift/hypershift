# Extending to Other Managed Services

## Common and Reusable Components

The following components are shared across all managed service gates and do not need to be duplicated:

| Component | Location | Notes |
|-----------|----------|-------|
| Pipeline YAML | `.tekton/pipelines/ho-release-gate.yaml` | Fully parameterized, service-agnostic |
| PipelineRun template | `.tekton/pipelines/ho-release-gate-run.yaml` | Referenced by all ITS resources via git resolver |
| Python modules | `.tekton/lib/` | `http_utils`, `prow_utils`, `slack_utils`, `kubearchive_utils`, `ho_release_gate` |
| CronJob | `nightly-promotion/cronjob.yaml` | Single CronJob triggers all service gates via `ITS_NAMES` env var; update it to add a new service |
| Gangway token | `gangway-token` Secret | Shared across all service gates |
| Slack webhook | `slack-webhook` Secret | Shared across all service gates |

The pipeline accepts all service-specific values as parameters (gate label, test job lists, release plan name), which are injected by the IntegrationTestScenario at runtime.

## Konflux Release Data

The [Konflux Release Data](https://gitlab.cee.redhat.com/releng/konflux-release-data) repository on GitLab CEE contains the tenant configuration that defines how releases are processed. For each managed service gate, the following manifests must be created:

- **IntegrationTestScenario (ITS)**: defines which pipeline to run and with which parameters
- **ReleasePlan**: created in the tenant namespace, references the application and target
- **ReleasePlanAdmission**: created in the releng namespace, authorizes the release and configures the managed pipeline

Some manifests are auto-generated from tenant config. After editing, regenerate them with `cd tenants-config && ./build-single.sh <tenant>` and include the regenerated output in the same MR.

## What Needs to Be Created for a New Service

All resources below are added to existing YAML files in the [Konflux Release Data](https://gitlab.cee.redhat.com/releng/konflux-release-data) repository, not created as new files. Follow the naming convention `hypershift-ho-release-gate-<service>` (or `hypershift-operator-ho-release-gate-<service>` for ReleasePlan) to stay consistent with existing resources.

### 1. IntegrationTestScenario (ITS)

Add a new ITS resource to the existing [`its.yaml`](https://gitlab.cee.redhat.com/releng/konflux-release-data/-/blob/main/tenants-config/cluster/stone-prd-rh01/tenants/crt-redhat-acm-tenant/hypershift-operator/nightly-promotion/its.yaml). The new resource follows the same structure, changing only the service-specific fields:

```yaml
---
apiVersion: appstudio.redhat.com/v1beta2
kind: IntegrationTestScenario
metadata:
  name: hypershift-ho-release-gate-<service>       # unique per service
spec:
  application: hypershift-operator
  contexts:
    - description: Only run via nightly CronJob trigger
      name: disabled
  params:
    - name: e2e-blocking-job-names                  # service-specific Prow jobs
      value: '["periodic-ci-openshift-hypershift-release-5.0-periodics-e2e-<service-job>"]'
    - name: e2e-informing-job-names
      value: '[]'
    - name: gate-label                              # shown in Slack notifications
      value: "<SERVICE NAME>"
    - name: release-plan-name                       # must match the ReleasePlan name
      value: "hypershift-operator-ho-release-gate-<service>"
    - name: stale-threshold-days                    # consecutive failure days before stale alert (default: 3)
      value: "3"
  resolverRef:                                      # same for all services
    params:
      - name: url
        value: https://github.com/openshift/hypershift
      - name: revision
        value: main
      - name: pathInRepo
        value: .tekton/pipelines/ho-release-gate-run.yaml
    resolver: git
    resourceKind: pipelinerun
```

Fields to customize per service: `metadata.name`, `e2e-blocking-job-names`, `e2e-informing-job-names`, `gate-label`, `release-plan-name`. The `resolverRef` block is identical for all services.

- `stale-threshold-days` (optional, default `3`): number of consecutive days of gate failures before a stale promotion alert is sent to Slack. Each managed service can set its own threshold based on how quickly a stale image becomes a concern. The stale check runs automatically using the ITS name as a label selector, so no additional configuration is needed. See [Stale Promotion Alerting](strategy.md#stale-promotion-alerting) for details.

### 2. ReleasePlan

Add a new ReleasePlan resource to the existing [`releaseplan.yaml`](https://gitlab.cee.redhat.com/releng/konflux-release-data/-/blob/main/tenants-config/cluster/stone-prd-rh01/tenants/crt-redhat-acm-tenant/hypershift-operator/nightly-promotion/releaseplan.yaml). This resource lives in the `crt-redhat-acm-tenant` namespace and links the application to the releng tenant:

```yaml
---
apiVersion: appstudio.redhat.com/v1alpha1
kind: ReleasePlan
metadata:
  labels:
    release.appstudio.openshift.io/auto-release: "false"
    release.appstudio.openshift.io/releasePlanAdmission: redhat-hypershift-operator-ho-release-gate-<service>  # <- customize
    release.appstudio.openshift.io/standing-attribution: "true"
  name: hypershift-operator-ho-release-gate-<service>  # <- customize
spec:
  application: hypershift-operator
  target: rhtap-releng-tenant
```

Fields to customize per service:

- `metadata.name`: must match the `release-plan-name` param in the ITS
- `releasePlanAdmission` label: must match the RPA name (see below)

The remaining fields are the same for all services:

- `auto-release: "false"`: releases are created explicitly by the pipeline, not automatically on every Snapshot
- `standing-attribution: "true"`: allows the Release CR to be created by an attributed SA (`nightly-promotion-sa`)

### 3. ReleasePlanAdmission (RPA)

The RPA lives in a [separate directory](https://gitlab.cee.redhat.com/releng/konflux-release-data/-/tree/main/config/stone-prd-rh01.pg1f.p1/service/ReleasePlanAdmission/crt-redhat-acm) under the releng namespace configuration. This resource is managed by the releng team but the HyperShift team provides the content. Create a new file named `redhat-hypershift-operator-ho-release-gate-<service>.yaml` following the existing [ARO HCP example](https://gitlab.cee.redhat.com/releng/konflux-release-data/-/blob/main/config/stone-prd-rh01.pg1f.p1/service/ReleasePlanAdmission/crt-redhat-acm/redhat-hypershift-operator-ho-release-gate-aro-hcp.yaml):

```yaml
---
apiVersion: appstudio.redhat.com/v1alpha1
kind: ReleasePlanAdmission
metadata:
  labels:
    release.appstudio.openshift.io/block-releases: "false"
    pp.engineering.redhat.com/business-unit: hybrid-cloud-experience
  name: redhat-hypershift-operator-ho-release-gate-<service>  # <- customize
  namespace: rhtap-releng-tenant
spec:
  applications:
    - hypershift-operator
  origin: crt-redhat-acm-tenant
  policy: app-interface-standard
  data:
    mapping:
      components:
        - name: hypershift-operator-main
          repositories:
            - url: "quay.io/redhat-services-prod/crt-redhat-acm-tenant/hypershift/hypershift-operator-verified"
      defaults:
        tags:                                       # <- customize: service-specific tag prefixes
          - "<service>-latest"
          - "<service>-latest-{{ timestamp }}"
          - "<service>-{{ git_sha }}"
          - "<service>-{{ git_short_sha }}"
        pushSourceContainer: false
    releaseNotes:
      product_name: ACM Prod Index
      product_version: "0.1"
    intention: production
  pipeline:
    pipelineRef:
      resolver: git
      params:
        - name: url
          value: "https://github.com/konflux-ci/release-service-catalog.git"
        - name: revision
          value: production
        - name: pathInRepo
          value: "pipelines/managed/rh-push-to-external-registry/rh-push-to-external-registry.yaml"
    serviceAccountName: release-app-interface-prod
    timeouts:
      pipeline: "4h0m0s"
      tasks: "4h0m0s"
```

Key fields to customize per service:

- `metadata.name`: follows the convention `redhat-hypershift-operator-ho-release-gate-<service>`
- `data.mapping.defaults.tags`: tag prefixes specific to the service (e.g. `aro-hcp-`, `rosa-`)
- `data.mapping.components[].repositories[].url`: can point to a different Quay repo if needed. If the repository does not exist, it will be auto-created on the first successful run
- The `pipeline` block is typically the same for all services (same managed pipeline)

### 4. Register in the CronJob

The CronJob is a shared component (see table above). To enable the new service gate, add the new ITS name to the `ITS_NAMES` environment variable in the existing [`cronjob.yaml`](https://gitlab.cee.redhat.com/releng/konflux-release-data/-/blob/main/tenants-config/cluster/stone-prd-rh01/tenants/crt-redhat-acm-tenant/hypershift-operator/nightly-promotion/cronjob.yaml):

```yaml
env:
  - name: ITS_NAMES
    value: "hypershift-ho-release-gate-aro-hcp,hypershift-ho-release-gate-<service>"
```

The value is a plain comma-separated string (no brackets, no quotes around individual names, no spaces). The CronJob iterates over the list and labels the Snapshot for each ITS sequentially.

## Step-by-Step: From Zero to First Gated Release

1. Identify the Prow periodic jobs for the new service
2. Create a single MR on the [Konflux Release Data](https://gitlab.cee.redhat.com/releng/konflux-release-data) repository containing:
    - The new ITS resource (added to `its.yaml`)
    - The new ReleasePlan (added to `releaseplan.yaml`)
    - The new ReleasePlanAdmission (new file under the RPA directory)
    - The CronJob update (new ITS name in `ITS_NAMES`)
    - Regenerated manifests (`cd tenants-config && ./build-single.sh <tenant>`)
3. If releng approval is required (e.g. for the RPA), the MR can be advertised in [#konflux-users](https://redhat.enterprise.slack.com/archives/C04PZ7H0VA8)
4. After merge, wait for the next nightly run or trigger manually (see [Operations and Troubleshooting](troubleshooting.md))
5. Verify the Slack notification shows the new service gate results
