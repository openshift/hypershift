# Hacking

## Development How-to Guides

### How to run the HyperShift Operator in a local process

1. Ensure the `KUBECONFIG` environment variable points to a management cluster
   with no HyperShift installed yet.

2. Build HyperShift.

        $ make build

3. Install HyperShift in development mode which causes the operator deployment
   to be deployment scaled to zero so that it doesn't conflict with your local
   operator process. 

        $ bin/hypershift install --development

4. Run the HyperShift operator locally.

        $ bin/hypershift-operator run

### How to install HyperShift with a custom image

1. Build and push a custom image build to your own repository.

        make IMG=quay.io/my/hypershift:latest docker-build docker-push

2. Install HyperShift using the custom image:

        $ bin/hypershift install --hypershift-image quay.io/my/hypershift:latest

### How to run the e2e tests

1. Install HyperShift.
2. Run the tests.

   ```shell
        $ make e2e
        $ bin/test-e2e -test.v -test.timeout 0 \
          --e2e.aws-credentials-file /my/aws-credentials \
          --e2e.pull-secret-file /my/pull-secret
   ```
