# Third Party Code from OpenShift CLI (oc)

This directory contains third party code copied from the [OpenShift CLI (oc)](https://github.com/openshift/oc) repository.

## Files and Their Sources

### manifest.go
Contains copied function calls from:
- **Source**: https://github.com/openshift/oc/blob/ea5c72052233361761ba371b7c39518d443422be/pkg/cli/image/manifest/manifest.go
- **Functions copied**: `FirstManifest`, `ManifestToImageConfig`, `ProcessManifestList`, and related types (`FilterFunc`, `ManifestLocation`)
- **Purpose**: Provides image manifest processing functionality for container image operations

### dockercredentials/credentials.go  
Contains copied credential handling code from the oc repository:
- **Purpose**: Provides Docker credential store functionality for registry authentication
- **Functions**: `NewFromFile`, `NewFromBytes`, `BasicFromKeyring`, and related types

## Rationale

At the time of copying, it was easier to copy and paste the function call contents rather than calling the exported 
functions from oc directly. Calling the functions directly would have invoked many go.mod changes including several 
conflicting dependencies between HyperShift, oc, and the library-go repositories.

## Maintenance

This code is a point-in-time copy and may become outdated as the upstream oc repository evolves. When updating or maintaining this code:

1. Consider whether direct imports from oc are feasible with current dependency management
2. If copying updates, ensure compatibility with HyperShift's usage patterns
 