# cleanupOIDCProviders lambda function

This [function](https://eu-north-1.console.aws.amazon.com/lambda/home?region=eu-north-1#/functions/cleanupOIDCProviders) runs periodically to cleanup any OIDC providers not deleted after a CI run ends.
Only OIDC providers created by CI, i.e. prefixed with `e2e-oidc-provider` are considered.

Automation is setup using a schedule rule on [Amazon EventBridge](https://eu-north-1.console.aws.amazon.com/events/home?region=eu-north-1#/rules) which emits an event at a fixed rate of 1 day that triggeres the lambda function.

Logs of the function execution can be found in [CloudWatch](https://eu-north-1.console.aws.amazon.com/cloudwatch/home?region=eu-north-1#logsV2:log-groups) under the group `/aws/lambda/cleanupOIDCProviders`

# Development

## Requirements

- [Go executable](https://golang.org/dl/).
- Bash shell.
- [AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/cli-chap-install.html) v1.17 or newer.


## Deploy

To deploy a new version after code changes, run:
```bash
./publish.sh
```
The script will build the go executable, package it and upload it to aws lambda.


## Testing

To manually invoke the function on aws, run the following command:
```bash
FUNCTION_NAME="cleanupOIDCProviders"
REGION=eu-north-1

aws lambda invoke \
    --function-name $FUNCTION_NAME \
    --payload '{}' \
    --region $REGION \
    response.json

cat response.json
```
