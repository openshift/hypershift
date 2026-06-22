---
name: Build CPO Image
description: "Build and push control-plane-operator container image. Auto-applies when testing CPO changes that require deploying to a live cluster."
---

# Build Control-Plane-Operator Image

This skill enables building and pushing custom control-plane-operator (CPO) images for testing changes in a live HyperShift environment.

## When to Use This Skill

This skill automatically applies when:
- You've made changes to code in `control-plane-operator/`
- You need to test CPO changes against a live cluster
- The user asks to build/push a CPO image
- You need to iterate on CPO fixes with e2e tests

## Prerequisites

Source the environment file before using this skill:

```bash
source dev/claude-env.sh
```

## Image Registry Configuration

Environment variables from `dev/claude-env.sh`:

| Variable | Description |
|----------|-------------|
| `CPO_IMAGE_REPO` | Container registry for CPO images |
| `RUNTIME` | Container runtime (podman/docker) |

## Building the CPO Image

### Step 1: Generate a Unique Tag

Use a tag that identifies the change (branch name, feature, or timestamp):

```bash
# Option 1: Use branch name
TAG=$(git rev-parse --abbrev-ref HEAD | tr '/' '-')

# Option 2: Use short commit hash
TAG=$(git rev-parse --short HEAD)

# Option 3: Use descriptive name + number for iterations
TAG="feature-name-1"
```

### Step 2: Build the Image

The CPO image uses `Dockerfile.control-plane`:

```bash
$RUNTIME build -f Dockerfile.control-plane --platform linux/amd64 -t $CPO_IMAGE_REPO:$TAG .
```

**Note**: The build runs `make control-plane-operator` and `make control-plane-pki-operator` inside the container, so you don't need to pre-build locally.

### Step 3: Push the Image

```bash
$RUNTIME push $CPO_IMAGE_REPO:$TAG
```

## Quick One-Liner

Build and push in one command:

```bash
TAG="my-fix-1" && $RUNTIME build -f Dockerfile.control-plane --platform linux/amd64 -t $CPO_IMAGE_REPO:$TAG . && $RUNTIME push $CPO_IMAGE_REPO:$TAG
```

## Iteration Workflow

When iterating on CPO fixes:

1. Make code changes in `control-plane-operator/`
2. Build and push image with incremented tag (e.g., `fix-1`, `fix-2`, `fix-3`)
3. Run e2e test with new image
4. Analyze results
5. Repeat until test passes

## What Gets Built

The `Dockerfile.control-plane` builds:
- `control-plane-operator` binary
- `control-plane-pki-operator` binary

Both are included in the final image.

## Image Labels

The CPO image includes important capability labels that HyperShift uses:
- `io.openshift.hypershift.control-plane-operator-subcommands=true`
- `io.openshift.hypershift.control-plane-operator.v2-isdefault=true`
- Various other feature capability labels

## Troubleshooting

### Build Fails
- Check that vendored dependencies are up to date: `go mod vendor`
- Ensure code compiles locally: `make control-plane-operator`

### Push Fails
- Verify registry login: `$RUNTIME login quay.io`
- Check repository permissions

### Image Not Used by Cluster
- Verify the image tag is correct in e2e flags
- Check that the image was pushed successfully
- Ensure the cluster can pull from the registry (public or authenticated)
