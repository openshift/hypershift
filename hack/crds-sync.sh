#!/bin/sh 

set -euo pipefail


cp vendor/github.com/aws/karpenter-provider-aws/pkg/apis/crds/* karpenter-operator/controllers/karpenter/assets/