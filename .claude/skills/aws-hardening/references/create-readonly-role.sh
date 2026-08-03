#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<HELP
Usage: $(basename "$0") [ROLE_NAME]

Creates an IAM role with AWS managed ReadOnlyAccess and a deny guardrail
that blocks destructive actions (delete, terminate, IAM mutations, etc.).
The role's trust policy allows any identity in the account to assume it.

Arguments:
  ROLE_NAME    Name for the IAM role (default: AIAgentReadOnly)

Options:
  -h, --help   Show this help message

Examples:
  $(basename "$0")                  # creates AIAgentReadOnly
  $(basename "$0") MyReadOnlyRole   # custom role name
HELP
  exit 0
}

[[ "${1:-}" == "-h" || "${1:-}" == "--help" ]] && usage

ROLE_NAME="${1:-AIAgentReadOnly}"

ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

echo "Account:  $ACCOUNT_ID"
echo "Role:     $ROLE_NAME"
echo ""

if aws iam get-role --role-name "$ROLE_NAME" &>/dev/null; then
  echo "Role $ROLE_NAME already exists."
  read -r -p "Attach ReadOnlyAccess + DenyDestructive to this existing role? [y/N] " reply
  [[ "$reply" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 1; }
else
  aws iam create-role \
    --role-name "$ROLE_NAME" \
    --assume-role-policy-document "{
      \"Version\": \"2012-10-17\",
      \"Statement\": [{
        \"Effect\": \"Allow\",
        \"Principal\": {\"AWS\": \"arn:aws:iam::${ACCOUNT_ID}:root\"},
        \"Action\": \"sts:AssumeRole\"
      }]
    }" \
    --max-session-duration 3600
  echo "Role created."
fi

aws iam attach-role-policy \
  --role-name "$ROLE_NAME" \
  --policy-arn arn:aws:iam::aws:policy/ReadOnlyAccess

echo "ReadOnlyAccess policy attached."

aws iam put-role-policy \
  --role-name "$ROLE_NAME" \
  --policy-name DenyDestructive \
  --policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Sid": "DenyDestructiveAndPrivileged",
      "Effect": "Deny",
      "Action": [
        "ec2:Delete*", "ec2:Terminate*",
        "s3:DeleteObject*", "s3:DeleteBucket*",
        "iam:CreateUser", "iam:DeleteUser",
        "iam:AttachUserPolicy", "iam:DetachUserPolicy",
        "iam:CreateAccessKey", "iam:UpdateAccessKey", "iam:DeleteAccessKey",
        "iam:CreateRole", "iam:DeleteRole",
        "iam:AttachRolePolicy", "iam:DetachRolePolicy",
        "iam:PutRolePolicy", "iam:PassRole",
        "cloudtrail:StopLogging", "cloudtrail:DeleteTrail", "cloudtrail:UpdateTrail",
        "organizations:*", "account:*"
      ],
      "Resource": "*"
    }]
  }'

echo "Deny guardrail attached."
echo ""
echo "Done. Now run /aws-hardening in Claude Code to complete setup."
