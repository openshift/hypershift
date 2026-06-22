# AWS-Specific HyperShift Troubleshooting

This subskill provides AWS-specific debugging workflows for HyperShift hosted-cluster issues.

## When to Use This Subskill

Use this when debugging AWS-specific issues such as:
- CAPI AWS resource problems (AWSCluster, AWSMachine)
- AWS infrastructure cleanup issues
- AWS-specific machine deletion problems

## Common AWS-Specific Issues

### Issue: Machines stuck in "Deleting" phase with "WaitingForInfrastructureDeletion"

**Cause**: AWSCluster resource deleted before AWSMachine resources, blocking CAPI controller reconciliation

**Root Cause**: CAPI AWSMachine controller requires AWSCluster to be "ready" to proceed with deletion

**Symptoms**:
- Machines show `InfrastructureReady: 1 of 2 completed`
- CAPI logs show: `"AWSCluster or AWSManagedControlPlane is not ready yet"`
- AWS instances still running but controller can't terminate them

**Resolution**:

1. Gather cluster information:
   ```bash
   INFRA_ID=$(kubectl get hc -n <namespace> <cluster-name> -ojsonpath='{.spec.infraID}')
   BASE_DOMAIN=$(kubectl get hc -n <namespace> <cluster-name> -ojsonpath='{.spec.dns.baseDomain}')
   REGION=$(kubectl get hc -n <namespace> <cluster-name> -ojsonpath='{.spec.platform.aws.region}')
   ```

2. Destroy AWS infrastructure:
   ```bash
   hypershift destroy infra aws \
     --name <cluster-name> \
     --aws-creds <path-to-credentials> \
     --base-domain $BASE_DOMAIN \
     --infra-id $INFRA_ID \
     --region $REGION
   ```

3. Remove AWSMachine finalizers (infrastructure is already gone):
   ```bash
   kubectl patch awsmachine <name> -n <hcp-namespace> -p '{"metadata":{"finalizers":null}}' --type=merge
   ```

4. Verify cleanup progresses: namespace should enter Terminating state, then be deleted

## AWS-Specific HyperShift Reinstallation

When reinstalling HyperShift on AWS, you'll need these AWS-specific parameters:

### Required Parameters
- OIDC S3 bucket name
- AWS credentials path
- AWS region

### AWS-Specific Installation Example
```bash
hypershift install \
  --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
  --oidc-storage-provider-s3-credentials $AWS_CREDS \
  --oidc-storage-provider-s3-region $REGION \
  --enable-defaulting-webhook true
```

**Important**: Ensure you use the same S3 bucket, credentials, and region as the original installation to maintain continuity with existing OIDC configurations.
