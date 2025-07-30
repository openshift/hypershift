# Configuring Tag-Based Image Mirroring through ImageTagMirrorSet

## What is ImageTagMirrorSet and why do we need it?

Users can configure tag-based image mirroring for their guest clusters through the `ImageTagMirrorSet` field in a hosted cluster's specification. This feature enables workloads that reference container images by tags (rather than digests) to pull images from alternative mirror registries.

Unlike `ImageDigestMirrorSet` which works with digest-based image references, `ImageTagMirrorSet` handles tag-based references like `quay.io/openshift/my-app:v1.2.3`. This is particularly useful for operators and workloads that haven't migrated to digest-based references.

## Configuration Format

Here is an example of the expected format for the `ImageTagMirrorSet` field:

```yaml
spec:
  imageTagMirrorSet:
  - source: quay.io/openshift
    mirrors:
    - mirror.example.com/openshift
  - source: registry.redhat.io/ubi8
    mirrors:
    - mirror.example.com/ubi8
    - backup.mirror.com/ubi8
    mirrorSourcePolicy: AllowContactingSource
  - source: docker.io/library
    mirrors:
    - internal.registry.com/docker-library
    mirrorSourcePolicy: NeverContactSource
```

### Field Descriptions

- **source**: The source registry hostname or repository path to mirror
- **mirrors**: List of mirror registries/repositories to use as alternatives
- **mirrorSourcePolicy**: Optional policy controlling fallback behavior:
  - `AllowContactingSource` (default): Allow falling back to the original source if mirrors fail
  - `NeverContactSource`: Never contact the original source, only use mirrors

## Source Formats

The `source` field supports several formats:

- **Registry hostname**: `quay.io`
- **Registry with namespace**: `quay.io/openshift`
- **Registry with repository**: `quay.io/openshift/my-app`
- **Wildcard domains**: `*.redhat.io`

## Mirror Behavior

When a pod tries to pull an image like `quay.io/openshift/my-app:v1.2.3`:

1. The container runtime first attempts to pull from the first mirror: `mirror.example.com/openshift/my-app:v1.2.3`
2. If that fails, it tries subsequent mirrors in order
3. If all mirrors fail and `mirrorSourcePolicy` is `AllowContactingSource`, it falls back to the original source
4. If `mirrorSourcePolicy` is `NeverContactSource`, the pull fails if all mirrors are unavailable

## How ImageTagMirrorSet works in HyperShift

ImageTagMirrorSet configurations are processed through the following flow:

1. **HostedCluster Configuration**: Users specify `imageTagMirrorSet` in the HostedCluster spec
2. **Controller Processing**: The HyperShift operator creates an OpenShift `ImageTagMirrorSet` custom resource in the management cluster
3. **Ignition Integration**: The configuration is included in the ignition config for nodes
4. **Node Configuration**: Worker nodes receive updated `registries.conf` configuration
5. **Runtime Application**: Container runtime (CRI-O) uses the mirror configuration for tag-based image pulls

## Version Requirements

!!! important

    ImageTagMirrorSet is available starting with OpenShift 4.21+. For earlier versions, use ImageDigestMirrorSet or ImageContentSources.

## Example: Complete HostedCluster Configuration

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.21.0-x86_64
  pullSecret:
    name: my-pull-secret
  sshKey:
    name: my-ssh-key
  platform:
    type: AWS
    aws:
      region: us-east-1
      # ... other AWS configuration
  imageTagMirrorSet:
  - source: quay.io/openshift
    mirrors:
    - mirror.example.com/openshift
  - source: registry.redhat.io
    mirrors:
    - mirror.example.com/redhat
    - backup.mirror.com/redhat
    mirrorSourcePolicy: AllowContactingSource
  # ... rest of cluster configuration
```

## CLI Usage

You can also configure ImageTagMirrorSet through the HyperShift CLI by creating a YAML file with the mirror configuration:

1. Create a file `image-tag-mirrors.yaml`:
```yaml
- source: quay.io/openshift
  mirrors:
  - mirror.example.com/openshift
- source: registry.redhat.io/ubi8
  mirrors:
  - mirror.example.com/ubi8
```

2. Use the CLI flag when creating the cluster:
```bash
hypershift create cluster aws \
  --name my-cluster \
  --image-tag-mirror-set image-tag-mirrors.yaml \
  # ... other flags
```

## Validation and Error Handling

The HyperShift operator validates ImageTagMirrorSet configurations:

- **Empty source**: Sources cannot be empty strings
- **Duplicate sources**: Each source can only appear once in the configuration
- **Invalid mirror source policy**: Only `AllowContactingSource` and `NeverContactSource` are valid values
- **Mirror requirements**: At least one mirror must be specified for each source

If validation fails, the HostedCluster will not be created and an error message will describe the issue.

## Troubleshooting

### Common Issues

1. **Images still pulled from original source**: Check that your image references use tags, not digests
2. **Mirror not accessible**: Verify network connectivity and authentication to mirror registries
3. **Authentication failures**: Ensure pull secrets include credentials for mirror registries

### Debugging Steps

1. Check the ImageTagMirrorSet resource in the management cluster:
```bash
oc get imagetagmirrorset
oc describe imagetagmirrorset <cluster-name>-image-tag-mirrors
```

2. Verify node configuration:
```bash
# On a worker node
cat /etc/containers/registries.conf
```

3. Check container runtime logs:
```bash
# On a worker node
journalctl -u crio
```

## Best Practices

1. **Test mirror availability**: Ensure mirror registries are accessible and contain the required images
2. **Use authentication**: Configure proper pull secrets for private mirror registries
3. **Monitor performance**: Mirror registries should provide good performance for your geographic location
4. **Backup mirrors**: Configure multiple mirrors for high availability
5. **Network considerations**: Ensure firewall rules allow access to mirror registries

## Comparison with Other Mirroring Options

| Feature | ImageTagMirrorSet | ImageDigestMirrorSet | ImageContentSources |
|---------|-------------------|----------------------|---------------------|
| Image Reference Type | Tags | Digests | Both |
| OpenShift Version | 4.21+ | 4.13+ | All (deprecated) |
| Fallback Policy | Configurable | Configurable | Fixed |
| Recommended Use | Tag-based workloads | Digest-based workloads | Legacy support |