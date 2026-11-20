FROM registry.ci.openshift.org/openshift/release:rhel-9-release-golang-1.26-openshift-5.1 AS builder
ARG COMMIT_HASH

WORKDIR /hypershift

COPY . .

RUN make hypershift \
  && make hypershift-no-cgo \
  && make hypershift-operator \
  && make product-cli \
  && make karpenter-operator \
  && make control-plane-operator \
  && make control-plane-pki-operator

FROM registry.access.redhat.com/ubi9:latest
COPY --from=builder /hypershift/bin/hypershift \
                    /hypershift/bin/hypershift-no-cgo \
                    /hypershift/bin/hcp \
                    /hypershift/bin/hypershift-operator \
                    /hypershift/bin/karpenter-operator \
                    /hypershift/bin/control-plane-operator \
                    /hypershift/bin/control-plane-pki-operator \
     /usr/bin/

ENTRYPOINT ["/usr/bin/hypershift"]

# TEMPORARY: Omni HyperShift is turned on for 4.20 here so the e2e release-matrix job exercises
# the feature with a plain `hypershift install`. This is intentional for this PR: there is no CI
# yet that enables the feature. Remove these ENV lines once the per-release e2e matrix turns it on
# instead. The HO reads these at runtime (os.Getenv); OMNI_HYPERSHIFT_IMAGE is deliberately unset
# so Omni HyperShift falls back to this image, which now bundles control-plane-operator. The
# feature stays off by default in the code; only this image opts in.
ENV ENABLE_OMNI_HYPERSHIFT=1
ENV OMNI_HYPERSHIFT_VERSIONS=4.20

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
LABEL io.openshift.hypershift.control-plane-pki-operator-signs-csrs=true
LABEL io.openshift.hypershift.hosted-cluster-config-operator-reports-node-count=true
LABEL io.openshift.hypershift.control-plane-operator.v2-isdefault=true
LABEL io.openshift.hypershift.control-plane-operator-supports-deterministic-lb=true
LABEL io.openshift.hypershift.control-plane-operator-supports-ignition-payload-system=true
