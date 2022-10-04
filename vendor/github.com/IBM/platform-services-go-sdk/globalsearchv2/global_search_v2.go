/**
 * (C) Copyright IBM Corp. 2021.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/*
 * IBM OpenAPI SDK Code Generator Version: 3.33.0-caf29bd0-20210603-225214
 */

// Package globalsearchv2 : Operations and models for the GlobalSearchV2 service
package globalsearchv2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
	common "github.com/IBM/platform-services-go-sdk/common"
)

// GlobalSearchV2 : Search for resources with the global and shared resource properties repository integrated in the IBM
// Cloud platform. The search repository stores and searches cloud resources attributes, which categorize or classify
// resources. A resource is a physical or logical component that can be created or reserved for an application or
// service instance and is owned by resource providers, such as Cloud Foundry, IBM Kubernetes Service, or resource
// controller in IBM Cloud. Resources are uniquely identified by a Cloud Resource Name (CRN)  or by an IMS ID. The
// properties of a resource include tags and system properties. Both properties are defined in an IBM Cloud billing
// account, and span across many regions.
//
// Version: 2.0.1
type GlobalSearchV2 struct {
	Service *core.BaseService
}

// DefaultServiceURL is the default URL to make service requests to.
const DefaultServiceURL = "https://api.global-search-tagging.cloud.ibm.com"

// DefaultServiceName is the default key used to find external configuration information.
const DefaultServiceName = "global_search"

// GlobalSearchV2Options : Service options
type GlobalSearchV2Options struct {
	ServiceName   string
	URL           string
	Authenticator core.Authenticator
}

// NewGlobalSearchV2UsingExternalConfig : constructs an instance of GlobalSearchV2 with passed in options and external configuration.
func NewGlobalSearchV2UsingExternalConfig(options *GlobalSearchV2Options) (globalSearch *GlobalSearchV2, err error) {
	if options.ServiceName == "" {
		options.ServiceName = DefaultServiceName
	}

	if options.Authenticator == nil {
		options.Authenticator, err = core.GetAuthenticatorFromEnvironment(options.ServiceName)
		if err != nil {
			return
		}
	}

	globalSearch, err = NewGlobalSearchV2(options)
	if err != nil {
		return
	}

	err = globalSearch.Service.ConfigureService(options.ServiceName)
	if err != nil {
		return
	}

	if options.URL != "" {
		err = globalSearch.Service.SetServiceURL(options.URL)
	}
	return
}

// NewGlobalSearchV2 : constructs an instance of GlobalSearchV2 with passed in options.
func NewGlobalSearchV2(options *GlobalSearchV2Options) (service *GlobalSearchV2, err error) {
	serviceOptions := &core.ServiceOptions{
		URL:           DefaultServiceURL,
		Authenticator: options.Authenticator,
	}

	baseService, err := core.NewBaseService(serviceOptions)
	if err != nil {
		return
	}

	if options.URL != "" {
		err = baseService.SetServiceURL(options.URL)
		if err != nil {
			return
		}
	}

	service = &GlobalSearchV2{
		Service: baseService,
	}

	return
}

// GetServiceURLForRegion returns the service URL to be used for the specified region
func GetServiceURLForRegion(region string) (string, error) {
	return "", fmt.Errorf("service does not support regional URLs")
}

// Clone makes a copy of "globalSearch" suitable for processing requests.
func (globalSearch *GlobalSearchV2) Clone() *GlobalSearchV2 {
	if core.IsNil(globalSearch) {
		return nil
	}
	clone := *globalSearch
	clone.Service = globalSearch.Service.Clone()
	return &clone
}

// SetServiceURL sets the service URL
func (globalSearch *GlobalSearchV2) SetServiceURL(url string) error {
	return globalSearch.Service.SetServiceURL(url)
}

// GetServiceURL returns the service URL
func (globalSearch *GlobalSearchV2) GetServiceURL() string {
	return globalSearch.Service.GetServiceURL()
}

// SetDefaultHeaders sets HTTP headers to be sent in every request
func (globalSearch *GlobalSearchV2) SetDefaultHeaders(headers http.Header) {
	globalSearch.Service.SetDefaultHeaders(headers)
}

// SetEnableGzipCompression sets the service's EnableGzipCompression field
func (globalSearch *GlobalSearchV2) SetEnableGzipCompression(enableGzip bool) {
	globalSearch.Service.SetEnableGzipCompression(enableGzip)
}

// GetEnableGzipCompression returns the service's EnableGzipCompression field
func (globalSearch *GlobalSearchV2) GetEnableGzipCompression() bool {
	return globalSearch.Service.GetEnableGzipCompression()
}

// EnableRetries enables automatic retries for requests invoked for this service instance.
// If either parameter is specified as 0, then a default value is used instead.
func (globalSearch *GlobalSearchV2) EnableRetries(maxRetries int, maxRetryInterval time.Duration) {
	globalSearch.Service.EnableRetries(maxRetries, maxRetryInterval)
}

// DisableRetries disables automatic retries for requests invoked for this service instance.
func (globalSearch *GlobalSearchV2) DisableRetries() {
	globalSearch.Service.DisableRetries()
}

// Search : Find instances of resources (v3)
// Find Cloud Foundry resources, IAM-enabled resources, or  storage and network resources running on classic
// infrastructure in a  specific account ID. You can apply query strings if necessary.
//
// To filter results, you can insert a string using the Lucene syntax and the  query string is parsed into a series of
// terms and operators. A term can be  a single word or a phrase, in which case the search is performed for all  the
// words, in the same order. To filter for a specific value regardless of  the property that contains it, type the
// search term without specifying a  field. Only resources that belong to the account ID and that are accessible  by the
// client are returned.
//
// You must use `/v3/resources/search` when you need to fetch more than `10000`  resource items. The
// `/v2/resources/search` prohibits paginating through such  a big number. On the first call, the operation returns a
// live cursor on the  data that you must use on all the subsequent calls to get the next batch of  results until you
// get the empty result set. By default, the fields returned  for every resource are "crn", "name", "family", "type",
// and "account_id". You  can specify the subset of the fields you want in your request.
func (globalSearch *GlobalSearchV2) Search(searchOptions *SearchOptions) (result *ScanResult, response *core.DetailedResponse, err error) {
	return globalSearch.SearchWithContext(context.Background(), searchOptions)
}

// SearchWithContext is an alternate form of the Search method which supports a Context parameter
func (globalSearch *GlobalSearchV2) SearchWithContext(ctx context.Context, searchOptions *SearchOptions) (result *ScanResult, response *core.DetailedResponse, err error) {
	err = core.ValidateNotNil(searchOptions, "searchOptions cannot be nil")
	if err != nil {
		return
	}
	err = core.ValidateStruct(searchOptions, "searchOptions")
	if err != nil {
		return
	}

	builder := core.NewRequestBuilder(core.POST)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = globalSearch.GetEnableGzipCompression()
	_, err = builder.ResolveRequestURL(globalSearch.Service.Options.URL, `/v3/resources/search`, nil)
	if err != nil {
		return
	}

	for headerName, headerValue := range searchOptions.Headers {
		builder.AddHeader(headerName, headerValue)
	}

	sdkHeaders := common.GetSdkHeaders("global_search", "V2", "Search")
	for headerName, headerValue := range sdkHeaders {
		builder.AddHeader(headerName, headerValue)
	}
	builder.AddHeader("Accept", "application/json")
	builder.AddHeader("Content-Type", "application/json")
	if searchOptions.TransactionID != nil {
		builder.AddHeader("transaction-id", fmt.Sprint(*searchOptions.TransactionID))
	}

	if searchOptions.AccountID != nil {
		builder.AddQuery("account_id", fmt.Sprint(*searchOptions.AccountID))
	}
	if searchOptions.Limit != nil {
		builder.AddQuery("limit", fmt.Sprint(*searchOptions.Limit))
	}
	if searchOptions.Timeout != nil {
		builder.AddQuery("timeout", fmt.Sprint(*searchOptions.Timeout))
	}
	if searchOptions.Sort != nil {
		builder.AddQuery("sort", strings.Join(searchOptions.Sort, ","))
	}

	body := make(map[string]interface{})
	if searchOptions.Query != nil {
		body["query"] = searchOptions.Query
	}
	if searchOptions.Fields != nil {
		body["fields"] = searchOptions.Fields
	}
	if searchOptions.SearchCursor != nil {
		body["search_cursor"] = searchOptions.SearchCursor
	}
	_, err = builder.SetBodyContentJSON(body)
	if err != nil {
		return
	}

	request, err := builder.Build()
	if err != nil {
		return
	}

	var rawResponse map[string]json.RawMessage
	response, err = globalSearch.Service.Request(request, &rawResponse)
	if err != nil {
		return
	}
	if rawResponse != nil {
		err = core.UnmarshalModel(rawResponse, "", &result, UnmarshalScanResult)
		if err != nil {
			return
		}
		response.Result = result
	}

	return
}

// GetSupportedTypes : DEPRECATED. Get all GhoST indices
// Retrieves a list of all GhoST indices.
func (globalSearch *GlobalSearchV2) GetSupportedTypes(getSupportedTypesOptions *GetSupportedTypesOptions) (result *SupportedTypesList, response *core.DetailedResponse, err error) {
	return globalSearch.GetSupportedTypesWithContext(context.Background(), getSupportedTypesOptions)
}

// GetSupportedTypesWithContext is an alternate form of the GetSupportedTypes method which supports a Context parameter
func (globalSearch *GlobalSearchV2) GetSupportedTypesWithContext(ctx context.Context, getSupportedTypesOptions *GetSupportedTypesOptions) (result *SupportedTypesList, response *core.DetailedResponse, err error) {
	err = core.ValidateStruct(getSupportedTypesOptions, "getSupportedTypesOptions")
	if err != nil {
		return
	}

	builder := core.NewRequestBuilder(core.GET)
	builder = builder.WithContext(ctx)
	builder.EnableGzipCompression = globalSearch.GetEnableGzipCompression()
	_, err = builder.ResolveRequestURL(globalSearch.Service.Options.URL, `/v2/resources/supported_types`, nil)
	if err != nil {
		return
	}

	for headerName, headerValue := range getSupportedTypesOptions.Headers {
		builder.AddHeader(headerName, headerValue)
	}

	sdkHeaders := common.GetSdkHeaders("global_search", "V2", "GetSupportedTypes")
	for headerName, headerValue := range sdkHeaders {
		builder.AddHeader(headerName, headerValue)
	}
	builder.AddHeader("Accept", "application/json")

	request, err := builder.Build()
	if err != nil {
		return
	}

	var rawResponse map[string]json.RawMessage
	response, err = globalSearch.Service.Request(request, &rawResponse)
	if err != nil {
		return
	}
	if rawResponse != nil {
		err = core.UnmarshalModel(rawResponse, "", &result, UnmarshalSupportedTypesList)
		if err != nil {
			return
		}
		response.Result = result
	}

	return
}

// GetSupportedTypesOptions : The GetSupportedTypes options.
type GetSupportedTypesOptions struct {

	// Allows users to set headers on API requests
	Headers map[string]string
}

// NewGetSupportedTypesOptions : Instantiate GetSupportedTypesOptions
func (*GlobalSearchV2) NewGetSupportedTypesOptions() *GetSupportedTypesOptions {
	return &GetSupportedTypesOptions{}
}

// SetHeaders : Allow user to set Headers
func (options *GetSupportedTypesOptions) SetHeaders(param map[string]string) *GetSupportedTypesOptions {
	options.Headers = param
	return options
}

// ResultItem : A resource returned in a search result.
type ResultItem struct {
	// Resource identifier in CRN format.
	CRN *string `json:"crn,omitempty"`

	// Allows users to set arbitrary properties
	additionalProperties map[string]interface{}
}

// SetProperty allows the user to set an arbitrary property on an instance of ResultItem
func (o *ResultItem) SetProperty(key string, value interface{}) {
	if o.additionalProperties == nil {
		o.additionalProperties = make(map[string]interface{})
	}
	o.additionalProperties[key] = value
}

// GetProperty allows the user to retrieve an arbitrary property from an instance of ResultItem
func (o *ResultItem) GetProperty(key string) interface{} {
	return o.additionalProperties[key]
}

// GetProperties allows the user to retrieve the map of arbitrary properties from an instance of ResultItem
func (o *ResultItem) GetProperties() map[string]interface{} {
	return o.additionalProperties
}

// MarshalJSON performs custom serialization for instances of ResultItem
func (o *ResultItem) MarshalJSON() (buffer []byte, err error) {
	m := make(map[string]interface{})
	if len(o.additionalProperties) > 0 {
		for k, v := range o.additionalProperties {
			m[k] = v
		}
	}
	if o.CRN != nil {
		m["crn"] = o.CRN
	}
	buffer, err = json.Marshal(m)
	return
}

// UnmarshalResultItem unmarshals an instance of ResultItem from the specified map of raw messages.
func UnmarshalResultItem(m map[string]json.RawMessage, result interface{}) (err error) {
	obj := new(ResultItem)
	err = core.UnmarshalPrimitive(m, "crn", &obj.CRN)
	if err != nil {
		return
	}
	delete(m, "crn")
	for k := range m {
		var v interface{}
		e := core.UnmarshalPrimitive(m, k, &v)
		if e != nil {
			err = e
			return
		}
		obj.SetProperty(k, v)
	}
	reflect.ValueOf(result).Elem().Set(reflect.ValueOf(obj))
	return
}

// ScanResult : The search scan response.
type ScanResult struct {
	// The search cursor to use on all calls after the first one.
	SearchCursor *string `json:"search_cursor" validate:"required"`

	// Value of the limit parameter specified by the user.
	Limit *int64 `json:"limit,omitempty"`

	// The array of results. Each item represents a resource. An empty array signals the end of the result set, there are
	// no more hits to fetch.
	Items []ResultItem `json:"items" validate:"required"`
}

// UnmarshalScanResult unmarshals an instance of ScanResult from the specified map of raw messages.
func UnmarshalScanResult(m map[string]json.RawMessage, result interface{}) (err error) {
	obj := new(ScanResult)
	err = core.UnmarshalPrimitive(m, "search_cursor", &obj.SearchCursor)
	if err != nil {
		return
	}
	err = core.UnmarshalPrimitive(m, "limit", &obj.Limit)
	if err != nil {
		return
	}
	err = core.UnmarshalModel(m, "items", &obj.Items, UnmarshalResultItem)
	if err != nil {
		return
	}
	reflect.ValueOf(result).Elem().Set(reflect.ValueOf(obj))
	return
}

// SearchOptions : The Search options.
type SearchOptions struct {
	// The Lucene-formatted query string. Default to '*' if not set.
	Query *string

	// The list of the fields returned by the search. Defaults to all. `crn` is always returned.
	Fields []string

	// An opaque search cursor that is returned on each operation call and that must be set on next call.
	SearchCursor *string

	// An aplhanumeric string that can be used to trace a request across services. If not specified it will be
	// automatically generated with the prefix "gst-".
	TransactionID *string

	// The account ID to filter resources.
	AccountID *string

	// The maximum number of hits to return. Defaults to 10.
	Limit *int64

	// A search timeout, bounding the search request to be executed within the specified time value and bail with the hits
	// accumulated up to that point when expired. Defaults to the system defined timeout.
	Timeout *int64

	// Comma separated properties names used for sorting.
	Sort []string

	// Allows users to set headers on API requests
	Headers map[string]string
}

// NewSearchOptions : Instantiate SearchOptions
func (*GlobalSearchV2) NewSearchOptions() *SearchOptions {
	return &SearchOptions{}
}

// SetQuery : Allow user to set Query
func (_options *SearchOptions) SetQuery(query string) *SearchOptions {
	_options.Query = core.StringPtr(query)
	return _options
}

// SetFields : Allow user to set Fields
func (_options *SearchOptions) SetFields(fields []string) *SearchOptions {
	_options.Fields = fields
	return _options
}

// SetSearchCursor : Allow user to set SearchCursor
func (_options *SearchOptions) SetSearchCursor(searchCursor string) *SearchOptions {
	_options.SearchCursor = core.StringPtr(searchCursor)
	return _options
}

// SetTransactionID : Allow user to set TransactionID
func (_options *SearchOptions) SetTransactionID(transactionID string) *SearchOptions {
	_options.TransactionID = core.StringPtr(transactionID)
	return _options
}

// SetAccountID : Allow user to set AccountID
func (_options *SearchOptions) SetAccountID(accountID string) *SearchOptions {
	_options.AccountID = core.StringPtr(accountID)
	return _options
}

// SetLimit : Allow user to set Limit
func (_options *SearchOptions) SetLimit(limit int64) *SearchOptions {
	_options.Limit = core.Int64Ptr(limit)
	return _options
}

// SetTimeout : Allow user to set Timeout
func (_options *SearchOptions) SetTimeout(timeout int64) *SearchOptions {
	_options.Timeout = core.Int64Ptr(timeout)
	return _options
}

// SetSort : Allow user to set Sort
func (_options *SearchOptions) SetSort(sort []string) *SearchOptions {
	_options.Sort = sort
	return _options
}

// SetHeaders : Allow user to set Headers
func (options *SearchOptions) SetHeaders(param map[string]string) *SearchOptions {
	options.Headers = param
	return options
}

// SupportedTypesList : A list of all GhoST indices.
type SupportedTypesList struct {
	// A list of all GhoST indices.
	SupportedTypes []string `json:"supported_types,omitempty"`
}

// UnmarshalSupportedTypesList unmarshals an instance of SupportedTypesList from the specified map of raw messages.
func UnmarshalSupportedTypesList(m map[string]json.RawMessage, result interface{}) (err error) {
	obj := new(SupportedTypesList)
	err = core.UnmarshalPrimitive(m, "supported_types", &obj.SupportedTypes)
	if err != nil {
		return
	}
	reflect.ValueOf(result).Elem().Set(reflect.ValueOf(obj))
	return
}
