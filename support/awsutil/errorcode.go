package awsutil

import (
	"errors"

	"github.com/aws/aws-sdk-go/aws/awserr"
)

func AWSErrorCode(err error) string {
	var awsErr awserr.Error
	if errors.As(err, &awsErr) {
		return awsErr.Code()
	}
	return "Unknown"
}
