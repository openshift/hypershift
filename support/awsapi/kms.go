package awsapi

//go:generate ../../hack/tools/bin/mockgen -source=kms.go -package=awsapi -destination=kms_mock.go

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// KMSAPI defines the KMS operations used by HyperShift.
type KMSAPI interface {
	Encrypt(ctx context.Context, params *kms.EncryptInput, optFns ...func(*kms.Options)) (*kms.EncryptOutput, error)
}

// Ensure *kms.Client implements KMSAPI
var _ KMSAPI = (*kms.Client)(nil)
