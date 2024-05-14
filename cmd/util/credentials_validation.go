package util

import (
	"fmt"
)

// IsRequiredOption returns a cobra style error message when the flag value is empty
func IsRequiredOption(flag string, value string) error {
	if len(value) == 0 {
		return fmt.Errorf("required flag(s) \"%s\" not set", flag)
	}
	return nil
}

// ValidateAwsStsCredentialInfo validates the AWS and STS credential information provided and marks the required fields
func ValidateAwsStsCredentialInfo(awsCredentialsFile, stsCredentialsFile, roleArn string) error {
	if awsCredentialsFile == "" {
		if err := IsRequiredOption("role-arn", roleArn); err != nil {
			return err
		}
		if err := IsRequiredOption("sts-creds", stsCredentialsFile); err != nil {
			return err
		}
	}
	if roleArn == "" && stsCredentialsFile == "" {
		if err := IsRequiredOption("aws-creds", awsCredentialsFile); err != nil {
			return err
		}
	}
	if stsCredentialsFile != "" && awsCredentialsFile != "" {
		return fmt.Errorf("only one of 'aws-creds' or 'role-arn' and 'sts-creds' can be provided")
	}
	return nil
}
