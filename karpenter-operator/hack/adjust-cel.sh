#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
YQ="${REPO_ROOT}/hack/tools/bin/yq"
EC2_CRD="${REPO_ROOT}/karpenter-operator/controllers/karpenter/assets/karpenter.k8s.aws_ec2nodeclasses.yaml"

# OpenShift-specific relaxations on upstream EC2NodeClass CRD.
"${YQ}" eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.required = []' -i "${EC2_CRD}"

"${YQ}" eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.amiFamily.enum = ["Custom"]' -i "${EC2_CRD}"

"${YQ}" eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.amiSelectorTerms.x-kubernetes-validations = [
    {"message": "expected only \"id\" to be set", "rule": "!self.exists(x, has(x.alias) || has(x.tags) || has(x.name) || has(x.owner))"}]' -i "${EC2_CRD}"

# since amiSelectorTerms is no longer required, top level validations need to be removed accordingly.
"${YQ}" eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.x-kubernetes-validations = []' -i "${EC2_CRD}"

# additionally, role is no longer required to be set, and can be set by cluster-admin.
"${YQ}" eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.role."x-kubernetes-validations" = [{"message": "role cannot be empty", "rule": "self != '\'''\''"}]' -i "${EC2_CRD}"
