FROM registry.ci.openshift.org/openshift/release:rhel-9-release-golang-1.24-openshift-4.21 AS builder
WORKDIR /workspace

ENV GOFLAGS=-mod=mod

# Copy go.mod and go.sum
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . ./

RUN make build

FROM registry.access.redhat.com/ubi9-minimal:latest
COPY --from=builder /workspace/bin/oadp-recovery /usr/bin/oadp-recovery

USER 1001

# Set the entrypoint
ENTRYPOINT ["/usr/bin/oadp-recovery", "run"]