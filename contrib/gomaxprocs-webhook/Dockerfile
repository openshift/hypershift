FROM registry.ci.openshift.org/openshift/release:rhel-9-release-golang-1.24-openshift-4.21 AS builder
WORKDIR /workspace

# Use vendored dependencies for hermetic builds
ENV GOFLAGS=-mod=vendor

# Copy only the gomaxprocs-webhook sources
COPY . ./

RUN make build

FROM registry.access.redhat.com/ubi9-minimal:latest
COPY --from=builder /workspace/bin/gomaxprocs-webhook /usr/bin/gomaxprocs-webhook
USER 1001

ENTRYPOINT ["/usr/bin/gomaxprocs-webhook", "serve"]