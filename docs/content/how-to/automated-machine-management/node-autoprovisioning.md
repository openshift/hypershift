---
title: Node Autoprovisioning
---

# Node Autoprovisoning

## Context and Considerations

Node Autoprovisoning is an experimental feature in Hypershift and it is enabled behind a feature gate.
To use it the Hypershift Operator needs to run with --feature-gates=Autoprovision=true.

We implement autoprovision via Karpenter. When the feature is enabled we handle everything for the cluster consumer so a cluster admin can just create regular karpenter resources against their guest cluster at any time.

```yaml
apiVersion: karpenter.sh/v1beta1
kind: NodePool
metadata:
  name: spot-example
  annotations:
    kubernetes.io/description: "NodePool to run spot instances"
spec:
  template:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot"]
        - key: karpenter.k8s.aws/instance-category
          operator: In
          values: ["c", "m", "r"]
        - key: karpenter.k8s.aws/instance-generation
          operator: Gt
          values: ["4"]
        - key: karpenter.k8s.aws/instance-size
          operator: In
          values: ["xlarge",]
      nodeClassRef:
        apiVersion: karpenter.k8s.aws/v1beta1
        kind: EC2NodeClass
        name: default
---
apiVersion: karpenter.k8s.aws/v1beta1
kind: EC2NodeClass
metadata:
  name: default
  annotations:
    kubernetes.io/description: "General purpose EC2NodeClass for running Amazon Linux 2 nodes"
spec:
  amiFamily: Custom
  amiSelectorTerms:
  - id: ami-00722494a4a3fa2af
  subnetSelectorTerms:
    - tags:
        karpenter.sh/discovery: "your-infra-tag" # replace with your known tag
  securityGroupSelectorTerms:
    - tags:
        karpenter.sh/discovery: "your-infra-tag" # replace with your known tag
  blockDeviceMappings:
    - deviceName: /dev/xvda
      rootVolume: true
      ebs:
        volumeType: gp3
        volumeSize: 120Gi
        deleteOnTermination: true 
```
Make sure to tag your securityGroups and subnets.


### Limitations and Caveats
- We automatically reconcile the userData against the EC2NodeClass resources.
- We automatically reconcile the amiID against the EC2NodeClass resources.
- We automatically approve CSRs.
!!! warning
- The underlying implementation and APIs exposure to consumer for Autoprovisiong might change in the future.