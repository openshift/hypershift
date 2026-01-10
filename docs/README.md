Any changes to the docs in this directory can be tested locally before pushing changes to GitHub. Just follow these
steps:

## Local Development

1. cd to this directory
2. Run `make image` to build the image
3. Run `make build-containerized` to build the containerized version of the image
4. Run `make serve-containerized` to serve up the docs website locally

Any changes you make while the docs are served locally, will be updated in the local docs website.

## Container Image Management

The documentation system supports multi-architecture container images with automatic build and registry push capabilities.

### Quick Start

```bash
# Build multi-architecture image and push to registry (uses 'latest' tag)
make push

# Build and push with specific version tag
make push-versioned
```

### Available Commands

```bash
# Development
make serve-containerized    # Serve docs locally on http://localhost:8000
make build-containerized    # Build docs without serving

# Image Management
make image                  # Build image for current platform
make image-multiarch        # Build multi-architecture image
make push                   # Build and push multi-arch image with 'latest' tag
make push-versioned        # Build and push with version tag

# Utilities
make inspect               # Show current configuration
make setup-buildx          # Setup Docker buildx (if using Docker)
make clean-builder         # Remove buildx builder
make help                  # Show all available commands
```

### Multi-Architecture Support

The system automatically builds for multiple architectures:
- `linux/amd64` - Standard x86_64 Linux
- `linux/arm64` - ARM64 Linux (Apple Silicon, ARM servers)

### Registry Configuration

Images are pushed to: `quay.io/hypershift/mkdocs-material:latest` (default) or `quay.io/hypershift/mkdocs-material:VERSION`

- `make push` - Pushes with `latest` tag
- `make push-versioned` - Pushes with version from `requirements.txt`

The version can be overridden with the `MKDOCS_TAG` environment variable:

```bash
MKDOCS_TAG=custom-version make push-versioned
```