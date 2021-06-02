FROM registry.ci.openshift.org/openshift/release:golang-1.16 as builder

WORKDIR /hypershift

COPY . .

RUN make

FROM quay.io/openshift/origin-base:4.7
COPY --from=builder /hypershift/bin/ignition-server /usr/bin/ignition-server
COPY --from=builder /hypershift/bin/hypershift /usr/bin/hypershift
COPY --from=builder /hypershift/bin/hypershift-operator /usr/bin/hypershift-operator
COPY --from=builder /hypershift/bin/control-plane-operator /usr/bin/control-plane-operator
COPY --from=builder /hypershift/bin/hosted-cluster-config-operator /usr/bin/hosted-cluster-config-operator

ENTRYPOINT /usr/bin/hypershift
