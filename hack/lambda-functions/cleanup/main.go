package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
)

const (
	OIDCProviderIDPrefix               = "e2e-oidc-provider"
	ExpiryDuration       time.Duration = time.Hour * 4
)

func cleanupOIDCProviders(ctx context.Context) ([]string, error) {
	iamClient := iam.New(session.New())

	providersListOutput, err := iamClient.ListOpenIDConnectProvidersWithContext(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return nil, err
	}

	errs := make([]error, 0)
	deletedProviders := make([]string, 0)
	for _, provider := range providersListOutput.OpenIDConnectProviderList {
		if !strings.Contains(*provider.Arn, OIDCProviderIDPrefix) {
			continue
		}

		providerOutput, err := iamClient.GetOpenIDConnectProviderWithContext(ctx, &iam.GetOpenIDConnectProviderInput{OpenIDConnectProviderArn: provider.Arn})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to retrieve OIDC provider %s: %v", *provider.Arn, err))
			continue
		}

		if time.Since(*providerOutput.CreateDate) > ExpiryDuration {
			log.Printf("deleting OIDC provider %s", *provider.Arn)
			if _, err := iamClient.DeleteOpenIDConnectProviderWithContext(ctx, &iam.DeleteOpenIDConnectProviderInput{OpenIDConnectProviderArn: provider.Arn}); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete OIDC provider %s: %v", *provider.Arn, err))
			} else {
				deletedProviders = append(deletedProviders, *provider.Arn)
			}
		}
	}

	return deletedProviders, errors.Join(errs...)
}

func lambdaHandler(ctx context.Context) (string, error) {
	deletedProviders, err := cleanupOIDCProviders(ctx)

	response := ""
	if len(deletedProviders) > 0 {
		log.Printf("Successfully deleted %d OIDC providers", len(deletedProviders))
		response = fmt.Sprintf("OIDC Providers deleted: [%s]", strings.Join(deletedProviders, ", "))
	}

	return response, err
}

func main() {
	lambda.Start(lambdaHandler)
}
