# HAProxy v3 image for shared ingress in ARO/HCP

This subdirectory contains the necessary files to perform a hermetic build of an
HAProxy v3 image that we can use as a shared ingress router pod. Following best
practices, we pin both the base image and the rpm versions of the dependencies
we install.

The image uses a multi-stage build:

- a pinned Red Hat `ubi10/ubi` builder stage to install the HAProxy RPM into an
  installroot with weak dependencies and docs disabled
- a pinned Red Hat `ubi10/ubi-micro` runtime stage that copies only that
  installroot into the final image

We intentionally install only `haproxy`. The shared ingress config generator
talks to HAProxy's admin socket directly in Go, so `socat` is not required in
the production image.

If you need to change the pinned RPM inputs, do so and then regenerate the lock
file doing:

```shell
rpm-lockfile-prototype --outfile=shared-ingress/rpms.lock.yaml shared-ingress/rpms.in.yaml
```

## Local validation

Use these commands to validate the image and the shared-ingress controller
contract locally:

```shell
go test ./hypershift-operator/controllers/sharedingress
podman build -f shared-ingress/Containerfile -t localhost/hypershift-shared-ingress:test shared-ingress
podman run --rm --entrypoint /usr/sbin/haproxy localhost/hypershift-shared-ingress:test -vv
./test/integration/shared-ingress/smoke-test.sh
```

The smoke test is useful on both Linux and macOS hosts. It uses the repo's
container runtime detection to pick `podman` or `docker`, builds the Linux image
for the local container VM architecture, and then starts HAProxy with the same
command, mounted config directory, writable runtime socket directory, and
read-only root filesystem used by the shared-ingress controller.
