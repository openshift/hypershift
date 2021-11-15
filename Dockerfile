FROM registry.ci.openshift.org/openshift/release:golang-1.16 as builder

WORKDIR /hypershift

COPY . .

RUN make build

FROM quay.io/openshift/origin-base:4.9

# This is pretty gross, we need `make` for CI because we install hypershift
# through a maketarget in this image for the 4.8 release branch.
# We can not yum install make, because this would break building it locally
# as all repo URLs in the image are kube service URLs that only work in the
# CI build clusters.
COPY --from=builder /usr/bin/make /usr/bin/make

COPY --from=builder /hypershift/bin/ignition-server /usr/bin/ignition-server
COPY --from=builder /hypershift/bin/hypershift /usr/bin/hypershift
COPY --from=builder /hypershift/bin/hypershift-operator /usr/bin/hypershift-operator
COPY --from=builder /hypershift/bin/control-plane-operator /usr/bin/control-plane-operator
COPY --from=builder /hypershift/bin/hosted-cluster-config-operator /usr/bin/hosted-cluster-config-operator
COPY --from=builder /hypershift/bin/konnectivity-socks5-proxy /usr/bin/konnectivity-socks5-proxy
COPY --from=builder /hypershift/bin/availability-prober /usr/bin/availability-prober
COPY --from=builder /hypershift/bin/token-minter /usr/bin/token-minter

ENTRYPOINT /usr/bin/hypershift
