---
name: Build HO Image
description: "Build and push hypershift-operator container image. Auto-applies when testing HO changes that require deploying to a live cluster."
---

# Build HyperShift-Operator Image

This skill enables building and pushing custom hypershift-operator (HO) images for testing changes in a live HyperShift environment.

## When to Use This Skill

This skill automatically applies when:
- You've made changes to code in `hypershift-operator/`
- You need to test HO changes against a live cluster
- The user asks to build/push a HO image
- You need to iterate on HO fixes with e2e tests

## Prerequisites

Source the environment file before using this skill:

```bash
source dev/claude-env.sh
```

## Image Registry Configuration

Environment variables from `dev/claude-env.sh`:

| Variable | Description |
|----------|-------------|
| `HO_IMAGE_REPO` | Container registry for HO images |
| `RUNTIME` | Container runtime (podman/docker) |

## Building the HO Image

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

The HO image uses the main `Dockerfile`:

```bash
$RUNTIME build -f Dockerfile --platform linux/amd64 -t $HO_IMAGE_REPO:$TAG .
```

**Note**: The build runs `make hypershift`, `make hypershift-operator`, `make karpenter-operator`, and `make product-cli` inside the container.

### Step 3: Push the Image

```bash
$RUNTIME push $HO_IMAGE_REPO:$TAG
```

## Quick One-Liner

Build and push in one command:

```bash
TAG="my-fix-1" && $RUNTIME build -f Dockerfile --platform linux/amd64 -t $HO_IMAGE_REPO:$TAG . && $RUNTIME push $HO_IMAGE_REPO:$TAG
```

## Iteration Workflow

When iterating on HO fixes:

1. Make code changes in `hypershift-operator/`
2. Build and push image with incremented tag (e.g., `fix-1`, `fix-2`, `fix-3`)
3. Reinstall HyperShift with new image
4. Run e2e test or manual validation
5. Analyze results
6. Repeat until test passes

## What Gets Built

The main `Dockerfile` builds:
- `hypershift` CLI binary
- `hypershift-no-cgo` binary
- `hypershift-operator` binary
- `karpenter-operator` binary
- `hcp` (product CLI) binary

All are included in the final image.

## Troubleshooting

### Build Fails
- Check that vendored dependencies are up to date: `go mod vendor`
- Ensure code compiles locally: `make hypershift-operator`
- Check for API generation issues: `make api`

### Push Fails
- Verify registry login: `$RUNTIME login quay.io`
- Check repository permissions

### Operator Not Running After Install
- Check operator logs: `kubectl logs -n hypershift deployment/operator`
- Verify image pull succeeded: `kubectl describe pod -n hypershift -l app=operator`
- Ensure cluster can pull from registry

### Changes Not Reflected
- Make sure you're using the correct image tag
- Check if old pods are still running: `kubectl get pods -n hypershift`
- Force rollout: `kubectl rollout restart deployment/operator -n hypershift`
