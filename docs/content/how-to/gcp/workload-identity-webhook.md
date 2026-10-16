# GCP Workload Identity Webhook

The GCP workload identity webhook mutates hosted cluster pods at admission time so containers can authenticate to Google Cloud through Workload Identity Federation (WIF). It uses annotations on a pod's Kubernetes `ServiceAccount` to decide whether and how to inject GCP credential configuration.

In HyperShift, the webhook runs as a sidecar in the hosted cluster's `kube-apiserver` pod. The hosted cluster `MutatingWebhookConfiguration` points to `https://127.0.0.1:9443/mutate-v1-pod`, so admission calls are handled locally by the webhook sidecar in the same pod as the API server.

## Webhook Configuration

For GCP hosted clusters, HyperShift starts the webhook with these key arguments:

```text
--annotation-prefix=cloud.google.com
--gcp-default-region=<hosted-control-plane GCP region>
--kubeconfig=/var/run/app/kubeconfig/kubeconfig
--token-audience=openshift
```

This means:

- WIF annotations use the `cloud.google.com` prefix.
- The default projected token audience is `openshift`.
- The default Cloud SDK region is the hosted control plane's GCP region.
- The webhook talks to the hosted cluster API through a local kubeconfig.

HCCO creates the hosted cluster `MutatingWebhookConfiguration` with one webhook:

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: gcp-workload-identity-federation-webhook
webhooks:
- name: pod-identity-webhook.gcp.mutate.io
  admissionReviewVersions:
  - v1
  clientConfig:
    url: https://127.0.0.1:9443/mutate-v1-pod
    caBundle: <root CA bundle>
  failurePolicy: Ignore
  rules:
  - operations:
    - CREATE
    apiGroups:
    - ""
    apiVersions:
    - v1
    resources:
    - pods
  sideEffects: None
```

The webhook applies to pod `CREATE` admission requests. If the webhook is unavailable, `failurePolicy: Ignore` lets pod creation continue without mutation.

## Admission Flow

For each pod `CREATE` request, the webhook does the following:

1. Decodes the pod admission request.
2. Allows the pod without mutation if `spec.serviceAccountName` is empty.
3. Fetches the referenced `ServiceAccount` from the pod namespace.
4. Allows the pod without mutation if the `ServiceAccount` does not exist.
5. Reads GCP WIF annotations from the `ServiceAccount`.
6. Allows the pod without mutation if neither required WIF annotation is present.
7. Rejects the admission request if only one required annotation is present or if an annotation is invalid.
8. Mutates the pod and returns a JSON patch when the WIF configuration is valid.

Because the webhook configuration uses `failurePolicy: Ignore`, webhook transport failures are ignored by kube-apiserver. Validation errors returned by the webhook are still webhook responses, so callers should treat invalid annotations as pod admission failures.

## Required ServiceAccount Annotations

To enable mutation, annotate the pod's Kubernetes `ServiceAccount` with both required annotations:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: app
  namespace: example
  annotations:
    cloud.google.com/workload-identity-provider: projects/<project-number>/locations/<location>/workloadIdentityPools/<pool-id>/providers/<provider-id>
    cloud.google.com/service-account-email: <gcp-service-account>@<project>.iam.gserviceaccount.com
```

The `cloud.google.com/workload-identity-provider` value must use this form:

```text
projects/{ProjectNumber}/locations/{Location}/workloadIdentityPools/{PoolId}/providers/{ProviderId}
```

If both required annotations are absent, the webhook does nothing. If only one is present, the webhook returns an error.

## Optional ServiceAccount Annotations

The webhook also supports these optional `ServiceAccount` annotations:

```yaml
metadata:
  annotations:
    cloud.google.com/audience: <audience>
    cloud.google.com/token-expiration: "<seconds>"
    cloud.google.com/gcloud-run-as-user: "<uid>"
    cloud.google.com/injection-mode: direct|gcloud
```

Defaults in HyperShift:

- `cloud.google.com/audience` defaults to `openshift`.
- `cloud.google.com/token-expiration` defaults to 24 hours.
- Token expiration has a minimum of 1 hour.
- `cloud.google.com/injection-mode` defaults to `gcloud`.
- `cloud.google.com/gcloud-run-as-user` is unset by default.

## Optional Pod Annotations

Pods can override or refine mutation with these annotations:

```yaml
metadata:
  annotations:
    cloud.google.com/token-expiration: "<seconds>"
    cloud.google.com/skip-containers: "container-a,init-container-b"
```

The pod-level `cloud.google.com/token-expiration` annotation overrides the `ServiceAccount` token expiration. The `cloud.google.com/skip-containers` annotation prevents the webhook from adding environment variables and volume mounts to the named init containers or containers.

## Common Mutations

For valid WIF configuration, the webhook adds or replaces a projected Kubernetes service account token volume:

```yaml
volumes:
- name: gcp-iam-token
  projected:
    defaultMode: 0440
    sources:
    - serviceAccountToken:
        audience: openshift
        expirationSeconds: <resolved-expiration>
        path: token
```

The token is mounted at:

```text
/var/run/secrets/sts.googleapis.com/serviceaccount/token
```

For every non-skipped init container and container, the webhook adds the token volume mount and injects these environment variables if they are not already present:

```yaml
env:
- name: CLOUDSDK_COMPUTE_REGION
  value: <hosted-control-plane GCP region>
- name: CLOUDSDK_CORE_PROJECT
  value: <project parsed from service-account-email>
```

The `CLOUDSDK_CORE_PROJECT` value is parsed from the GCP service account email. For example, `app@my-project.iam.gserviceaccount.com` yields `my-project`.

## GCloud Injection Mode

`gcloud` mode is the default when `cloud.google.com/injection-mode` is absent or set to `gcloud`.

In this mode, the webhook adds an emptyDir volume for Cloud SDK configuration:

```yaml
volumes:
- name: gcloud-config
  emptyDir: {}
```

It prepends or replaces an init container named `gcloud-setup`:

```yaml
initContainers:
- name: gcloud-setup
  image: gcr.io/google.com/cloudsdktool/google-cloud-cli:stable
  command:
  - sh
  - -c
  - |
    gcloud iam workload-identity-pools create-cred-config \
      $(GCP_WORKLOAD_IDENTITY_PROVIDER) \
      --service-account=$(GCP_SERVICE_ACCOUNT) \
      --output-file=$(CLOUDSDK_CONFIG)/federation.json \
      --credential-source-file=/var/run/secrets/sts.googleapis.com/serviceaccount/token
    gcloud auth login --cred-file=$(CLOUDSDK_CONFIG)/federation.json
```

It injects these fields into workload containers:

```yaml
env:
- name: GOOGLE_APPLICATION_CREDENTIALS
  value: /var/run/secrets/gcloud/config/federation.json
- name: CLOUDSDK_CONFIG
  value: /var/run/secrets/gcloud/config
volumeMounts:
- name: gcp-iam-token
  mountPath: /var/run/secrets/sts.googleapis.com/serviceaccount
  readOnly: true
- name: gcloud-config
  mountPath: /var/run/secrets/gcloud/config
```

Use this mode when the pod should rely on the Cloud SDK init container to generate the external account credentials file before workload containers start.

## Direct Injection Mode

Direct mode is enabled with this `ServiceAccount` annotation:

```yaml
metadata:
  annotations:
    cloud.google.com/injection-mode: direct
```

In direct mode, the webhook does not inject a `gcloud-setup` init container. Instead, it builds the external account credentials JSON itself and stores it in a pod annotation:

```yaml
metadata:
  annotations:
    cloud.google.com/external-credentials-json: |-
      {
        "type": "external_account",
        "audience": "//iam.googleapis.com/projects/<project-number>/locations/<location>/workloadIdentityPools/<pool-id>/providers/<provider-id>",
        "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
        "token_url": "https://sts.googleapis.com/v1/token",
        "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/<gcp-service-account>@<project>.iam.gserviceaccount.com:generateAccessToken",
        "credential_source": {
          "file": "/var/run/secrets/sts.googleapis.com/serviceaccount/token",
          "format": {
            "type": "text"
          }
        }
      }
```

It mounts that annotation through a DownwardAPI volume:

```yaml
volumes:
- name: external-credential-config
  downwardAPI:
    defaultMode: 0440
    items:
    - path: federation.json
      fieldRef:
        apiVersion: v1
        fieldPath: metadata.annotations['cloud.google.com/external-credentials-json']
```

It injects these fields into workload containers:

```yaml
env:
- name: GOOGLE_APPLICATION_CREDENTIALS
  value: /var/run/secrets/workload-identity/federation.json
volumeMounts:
- name: gcp-iam-token
  mountPath: /var/run/secrets/sts.googleapis.com/serviceaccount
  readOnly: true
- name: external-credential-config
  mountPath: /var/run/secrets/workload-identity
  readOnly: true
```

Direct mode avoids the Cloud SDK setup init container and is the mode used by HyperShift's GCP WIF webhook e2e test.

## Result

After mutation, an annotated workload gets:

- A projected Kubernetes service account token with the configured audience.
- A Google external account credentials file.
- `GOOGLE_APPLICATION_CREDENTIALS` pointing to that credentials file.
- Optional Cloud SDK region and project environment variables.
- In `gcloud` mode, an init container that creates and logs in with the credentials file.
- In `direct` mode, no init container; the credentials JSON is generated by the webhook and mounted through DownwardAPI.

The application can then use standard Google authentication libraries that read `GOOGLE_APPLICATION_CREDENTIALS` to exchange the Kubernetes token through GCP Security Token Service and impersonate the configured Google service account.
