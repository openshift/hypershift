// Code generated by Microsoft (R) AutoRest Code Generator (autorest: 3.10.3, generator: @autorest/go@4.0.0-preview.69)
// Changes may cause incorrect behavior and will be lost if the code is regenerated.
// Code generated by @autorest/go. DO NOT EDIT.

package client

import (
	"context"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// ManagedIdentityDataPlaneAPIClient contains the methods for the ManagedIdentityDataPlaneAPI group.
// Don't use this type directly, use a constructor function instead.
type ManagedIdentityDataPlaneAPIClient struct {
	internal *azcore.Client
}

// Deleteidentity - A DELETE operation to delete system assigned identity for a given proxy resource. The x-ms-identity-url
// header from ARM contains this path by default. This must be called by RPs only. Usable from only
// system assigned clientsecreturl. User assigned clientsecreturl does not support this operation.
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-01-01
//   - hostPath - The scheme, host and path from ARM's x-ms-identity-url header.
//   - options - ManagedIdentityDataPlaneAPIClientDeleteidentityOptions contains the optional parameters for the ManagedIdentityDataPlaneAPIClient.Deleteidentity
//     method.
func (client *ManagedIdentityDataPlaneAPIClient) Deleteidentity(ctx context.Context, hostPath string, options *ManagedIdentityDataPlaneAPIClientDeleteidentityOptions) (ManagedIdentityDataPlaneAPIClientDeleteidentityResponse, error) {
	var err error
	req, err := client.deleteidentityCreateRequest(ctx, hostPath, options)
	if err != nil {
		return ManagedIdentityDataPlaneAPIClientDeleteidentityResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return ManagedIdentityDataPlaneAPIClientDeleteidentityResponse{}, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK, http.StatusNoContent) {
		err = runtime.NewResponseError(httpResp)
		return ManagedIdentityDataPlaneAPIClientDeleteidentityResponse{}, err
	}
	return ManagedIdentityDataPlaneAPIClientDeleteidentityResponse{}, nil
}

// deleteidentityCreateRequest creates the Deleteidentity request.
func (client *ManagedIdentityDataPlaneAPIClient) deleteidentityCreateRequest(ctx context.Context, hostPath string, _ *ManagedIdentityDataPlaneAPIClientDeleteidentityOptions) (*policy.Request, error) {
	host := "{hostPath}"
	host = strings.ReplaceAll(host, "{hostPath}", hostPath)
	req, err := runtime.NewRequest(ctx, http.MethodDelete, host)
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-01-01")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	return req, nil
}

// Getcred - A GET operation to retrieve system or user assigned credentials for a given resource. The x-ms-identity-url header
// from ARM contains this path by default for system assigned identities. Usable from
// both system assigned clientsecreturl or user assigned clientsecreturl.
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-01-01
//   - hostPath - The scheme, host and path from ARM's x-ms-identity-url header.
//   - options - ManagedIdentityDataPlaneAPIClientGetcredOptions contains the optional parameters for the ManagedIdentityDataPlaneAPIClient.Getcred
//     method.
func (client *ManagedIdentityDataPlaneAPIClient) Getcred(ctx context.Context, hostPath string, options *ManagedIdentityDataPlaneAPIClientGetcredOptions) (ManagedIdentityDataPlaneAPIClientGetcredResponse, error) {
	var err error
	req, err := client.getcredCreateRequest(ctx, hostPath, options)
	if err != nil {
		return ManagedIdentityDataPlaneAPIClientGetcredResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return ManagedIdentityDataPlaneAPIClientGetcredResponse{}, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK) {
		err = runtime.NewResponseError(httpResp)
		return ManagedIdentityDataPlaneAPIClientGetcredResponse{}, err
	}
	resp, err := client.getcredHandleResponse(httpResp)
	return resp, err
}

// getcredCreateRequest creates the Getcred request.
func (client *ManagedIdentityDataPlaneAPIClient) getcredCreateRequest(ctx context.Context, hostPath string, _ *ManagedIdentityDataPlaneAPIClientGetcredOptions) (*policy.Request, error) {
	host := "{hostPath}"
	host = strings.ReplaceAll(host, "{hostPath}", hostPath)
	req, err := runtime.NewRequest(ctx, http.MethodGet, host)
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-01-01")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	return req, nil
}

// getcredHandleResponse handles the Getcred response.
func (client *ManagedIdentityDataPlaneAPIClient) getcredHandleResponse(resp *http.Response) (ManagedIdentityDataPlaneAPIClientGetcredResponse, error) {
	result := ManagedIdentityDataPlaneAPIClientGetcredResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.ManagedIdentityCredentials); err != nil {
		return ManagedIdentityDataPlaneAPIClientGetcredResponse{}, err
	}
	return result, nil
}

// Getcreds - A POST operation to retrieve system assigned and user assigned identity credentials for a given resource. Usable
// from both system assigned clientsecreturl and user assigned clientsecreturl.
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-01-01
//   - hostPath - The scheme, host and path from ARM's x-ms-identity-url header.
//   - credRequest - The identities requested.
//   - options - ManagedIdentityDataPlaneAPIClientGetcredsOptions contains the optional parameters for the ManagedIdentityDataPlaneAPIClient.Getcreds
//     method.
func (client *ManagedIdentityDataPlaneAPIClient) Getcreds(ctx context.Context, hostPath string, credRequest CredRequestDefinition, options *ManagedIdentityDataPlaneAPIClientGetcredsOptions) (ManagedIdentityDataPlaneAPIClientGetcredsResponse, error) {
	var err error
	req, err := client.getcredsCreateRequest(ctx, hostPath, credRequest, options)
	if err != nil {
		return ManagedIdentityDataPlaneAPIClientGetcredsResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return ManagedIdentityDataPlaneAPIClientGetcredsResponse{}, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK) {
		err = runtime.NewResponseError(httpResp)
		return ManagedIdentityDataPlaneAPIClientGetcredsResponse{}, err
	}
	resp, err := client.getcredsHandleResponse(httpResp)
	return resp, err
}

// getcredsCreateRequest creates the Getcreds request.
func (client *ManagedIdentityDataPlaneAPIClient) getcredsCreateRequest(ctx context.Context, hostPath string, credRequest CredRequestDefinition, _ *ManagedIdentityDataPlaneAPIClientGetcredsOptions) (*policy.Request, error) {
	host := "{hostPath}"
	host = strings.ReplaceAll(host, "{hostPath}", hostPath)
	req, err := runtime.NewRequest(ctx, http.MethodPost, host)
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-01-01")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	if err := runtime.MarshalAsJSON(req, credRequest); err != nil {
		return nil, err
	}
	return req, nil
}

// getcredsHandleResponse handles the Getcreds response.
func (client *ManagedIdentityDataPlaneAPIClient) getcredsHandleResponse(resp *http.Response) (ManagedIdentityDataPlaneAPIClientGetcredsResponse, error) {
	result := ManagedIdentityDataPlaneAPIClientGetcredsResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.ManagedIdentityCredentials); err != nil {
		return ManagedIdentityDataPlaneAPIClientGetcredsResponse{}, err
	}
	return result, nil
}

// Moveidentity - A POST operation to move the proxy resource to a different resource group
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-01-01
//   - hostPath - The scheme, host and path from ARM's x-ms-identity-url header.
//   - moveRequestBody - New target resource Id
//   - options - ManagedIdentityDataPlaneAPIClientMoveidentityOptions contains the optional parameters for the ManagedIdentityDataPlaneAPIClient.Moveidentity
//     method.
func (client *ManagedIdentityDataPlaneAPIClient) Moveidentity(ctx context.Context, hostPath string, moveRequestBody MoveRequestBodyDefinition, options *ManagedIdentityDataPlaneAPIClientMoveidentityOptions) (ManagedIdentityDataPlaneAPIClientMoveidentityResponse, error) {
	var err error
	req, err := client.moveidentityCreateRequest(ctx, hostPath, moveRequestBody, options)
	if err != nil {
		return ManagedIdentityDataPlaneAPIClientMoveidentityResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return ManagedIdentityDataPlaneAPIClientMoveidentityResponse{}, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK) {
		err = runtime.NewResponseError(httpResp)
		return ManagedIdentityDataPlaneAPIClientMoveidentityResponse{}, err
	}
	resp, err := client.moveidentityHandleResponse(httpResp)
	return resp, err
}

// moveidentityCreateRequest creates the Moveidentity request.
func (client *ManagedIdentityDataPlaneAPIClient) moveidentityCreateRequest(ctx context.Context, hostPath string, moveRequestBody MoveRequestBodyDefinition, _ *ManagedIdentityDataPlaneAPIClientMoveidentityOptions) (*policy.Request, error) {
	host := "{hostPath}"
	host = strings.ReplaceAll(host, "{hostPath}", hostPath)
	urlPath := "/proxy/move"
	req, err := runtime.NewRequest(ctx, http.MethodPost, runtime.JoinPaths(host, urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-01-01")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	if err := runtime.MarshalAsJSON(req, moveRequestBody); err != nil {
		return nil, err
	}
	return req, nil
}

// moveidentityHandleResponse handles the Moveidentity response.
func (client *ManagedIdentityDataPlaneAPIClient) moveidentityHandleResponse(resp *http.Response) (ManagedIdentityDataPlaneAPIClientMoveidentityResponse, error) {
	result := ManagedIdentityDataPlaneAPIClientMoveidentityResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.MoveIdentityResponse); err != nil {
		return ManagedIdentityDataPlaneAPIClientMoveidentityResponse{}, err
	}
	return result, nil
}
