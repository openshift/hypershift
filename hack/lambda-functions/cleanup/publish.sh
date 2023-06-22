#!/bin/bash
set -eo pipefail

FUNCTION_NAME="cleanupOIDCProviders"
REGION=eu-north-1
# this name should match the 'Handler' name configured on the AWS lambda console under 'Runtime settings' 
EXEC_NAME="oidc-cleanup"

echo "Building go executable"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $EXEC_NAME .

zip myFunction.zip $EXEC_NAME

aws lambda update-function-code --function-name $FUNCTION_NAME \
    --zip-file fileb://myFunction.zip \
    --region $REGION
