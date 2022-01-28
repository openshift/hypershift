FROM registry.ci.openshift.org/openshift/release:golang-1.17 as builder

WORKDIR /hypershift

COPY . .

RUN make build

FROM quay.io/openshift/origin-base:4.10
COPY --from=builder /hypershift/bin/ignition-server \
                    /hypershift/bin/hypershift \
                    /hypershift/bin/hypershift-operator \
                    /hypershift/bin/control-plane-operator \
                    /hypershift/bin/konnectivity-socks5-proxy \
                    /hypershift/bin/availability-prober \
                    /hypershift/bin/token-minter \
     /usr/bin/

ENTRYPOINT /usr/bin/hypershift
