---
title: Run tests
---

# How to run the e2e tests

1. Install HyperShift.
2. Run the tests.

        make e2e
        bin/test-e2e -test.v -test.timeout 0 \
        --e2e.aws-credentials-file /my/aws-credentials \
        --e2e.pull-secret-file /my/pull-secret \
        --e2e.base-domain my-basedomain
