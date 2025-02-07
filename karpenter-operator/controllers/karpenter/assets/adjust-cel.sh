#!/usr/bin/env bash

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

# remove amiSelectorTerms, securityGroupSelectorTerms, subnetSelectorTerms required validation.
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.required = []' -i ${SCRIPT_DIR}/karpenter.k8s.aws_ec2nodeclasses.yaml

yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.amiFamily.enum = ["Custom"]' -i ${SCRIPT_DIR}/karpenter.k8s.aws_ec2nodeclasses.yaml

yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.amiSelectorTerms.x-kubernetes-validations = [
    {"message": "expected only \"id\" to be set", "rule": "!self.exists(x, has(x.alias) || has(x.tags) || has(x.name) || has(x.owner))"}]' -i ${SCRIPT_DIR}/karpenter.k8s.aws_ec2nodeclasses.yaml

# since amiSelectorTerms is no longer required, top level validations need to be removed accordingly.
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.x-kubernetes-validations = []' -i ${SCRIPT_DIR}/karpenter.k8s.aws_ec2nodeclasses.yaml

# additionally, role is no longer requierd to be set, and can be set by cluster-admin.
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.role."x-kubernetes-validations" = [{"message": "role cannot be empty", "rule": "self != '\'''\''"}]' -i ${SCRIPT_DIR}/karpenter.k8s.aws_ec2nodeclasses.yaml
