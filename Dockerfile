FROM registry.ci.openshift.org/openshift/release:golang-1.20 as builder

WORKDIR /hypershift

COPY . .

RUN make build

FROM quay.io/openshift/origin-base:4.14
COPY --from=builder /hypershift/bin/hypershift \
                    /hypershift/bin/hcp \
                    /hypershift/bin/hypershift-operator \
                    /hypershift/bin/control-plane-operator \
     /usr/bin/

RUN cd /usr/bin && \
    ln -s control-plane-operator ignition-server && \
    ln -s control-plane-operator konnectivity-socks5-proxy && \
    ln -s control-plane-operator availability-prober && \
    ln -s control-plane-operator token-minter

ENTRYPOINT ["/usr/bin/hypershift"]

LABEL io.openshift.hypershift.control-plane-operator-subcommands=true
LABEL io.openshift.hypershift.control-plane-operator-skips-haproxy=true
LABEL io.openshift.hypershift.ignition-server-healthz-handler=true
LABEL io.openshift.hypershift.control-plane-operator-manages-ignition-server=true
LABEL io.openshift.hypershift.control-plane-operator-manages.cluster-machine-approver=true
LABEL io.openshift.hypershift.control-plane-operator-manages.cluster-autoscaler=true
LABEL io.openshift.hypershift.control-plane-operator-manages.decompress-decode-config=true
LABEL io.openshift.hypershift.control-plane-operator-creates-aws-sg=true
LABEL io.openshift.hypershift.control-plane-operator-applies-management-kas-network-policy-label=true
LABEL io.openshift.hypershift.restricted-psa=true
