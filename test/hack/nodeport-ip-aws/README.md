# Configure NodePort public IP on AWS management cluster

## Overview
The scripts in this directory create a bastion machine for a management cluster on AWS
that forwards the standard service port range (32000-32767) to a master machine on the
cluster.

This makes it possible to test a NodePort publishing strategy for Hypershift clusters
on management clusters created as standalone OCP clusters.

## Prerequisites

- `hypershift` - the [hypershift CLI](https://github.com/openshift/hypershift)
- `oc` - the standard [openshift CLI](https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz)
- `aws` - the [AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/install-cliv2.html) and account credentials
- `jq` - [json query tool](https://stedolan.github.io/jq/download/)
- `ssh`

## Setup

Ensure a `KUBECONFIG` variable is configured that is pointing to your management cluster.

If necessary, add your private SSH key to your SSH agent. This is so the setup script
can SSH to the bastion machine.
```
ssh-add PRIVATEKEY_FILE
```

Invoke the `setup.sh` script in this directory, first exporting any
environment variables that you need to change from the default.

Variables used in the script:

- `AWSCREDS` - AWS credentials file (defaults to `~/.aws/credentials`)
- `SSHPUBLICKEY` - SSH public key to use for bastion (defaults to `~/.ssh/id_rsa.pub`)
- `HYPERSHIFT` - location of the hypershift binary (defaults to `./bin/hypershift`)


## Teardown

Invoke the `teardown.sh` script in this directory, changing any variables as needed.

Variables used in the script:

- `AWSCREDS` - AWS credentials file (defaults to `~/.aws/credentials`)
- `HYPERSHIFT` - location of the hypershift binary (defaults to `./bin/hypershift)
