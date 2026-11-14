package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/go-logr/logr/funcr"
)

type natGatewayHTTPClient struct {
	responses []string
	errors    []error
	calls     int
}

func (client *natGatewayHTTPClient) Do(*http.Request) (*http.Response, error) {
	index := client.calls
	client.calls++
	if index >= len(client.responses) {
		return nil, fmt.Errorf("unexpected request %d", index)
	}
	if len(client.errors) > index && client.errors[index] != nil {
		return nil, client.errors[index]
	}
	code := client.responses[index]
	status := http.StatusBadRequest
	body := fmt.Sprintf(`<Response><Errors><Error><Code>%s</Code><Message>rejected</Message></Error></Errors><RequestID>request-test</RequestID></Response>`, code)
	switch code {
	case "success":
		status = http.StatusOK
		body = `<CreateNatGatewayResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><natGateway><natGatewayId>nat-test</natGatewayId></natGateway></CreateNatGatewayResponse>`
	case "InternalError":
		status = http.StatusInternalServerError
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/xml"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func newNATGatewaySDKClient(client *natGatewayHTTPClient) *ec2.Client {
	return ec2.New(ec2.Options{
		Region: "us-east-1", Credentials: aws.AnonymousCredentials{}, HTTPClient: client,
		Retryer: awsretry.NewStandard(func(options *awsretry.StandardOptions) {
			options.MaxAttempts = 3
			options.Backoff = awsretry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
		}),
	})
}

func TestNatGatewayAttemptObserver_ApiOption(t *testing.T) {
	tests := []struct {
		name                string
		responses           []string
		errors              []error
		rejected            bool
		missingRetry        bool
		canceled            bool
		retryRejections     bool
		installationFailure bool
	}{
		{name: "When quota rejects the only attempt, it should prove rejection", responses: []string{"NatGatewayLimitExceeded"}, rejected: true},
		{name: "When a lost response precedes quota rejection, it should preserve ambiguity", responses: []string{"", "NatGatewayLimitExceeded"}, errors: []error{errors.New("response lost")}},
		{name: "When server failure precedes quota rejection, it should preserve ambiguity", responses: []string{"InternalError", "NatGatewayLimitExceeded"}},
		{name: "When throttling precedes quota rejection, it should preserve unknown rejection history", responses: []string{"Throttling", "NatGatewayLimitExceeded"}},
		{name: "When the request succeeds, it should not prove rejection", responses: []string{"success"}},
		{name: "When attempt metadata is absent, it should not prove rejection", responses: []string{"NatGatewayLimitExceeded"}, missingRetry: true},
		{name: "When the context is canceled, it should not prove rejection", canceled: true},
		{name: "When all inner attempts reject creation, it should prove rejection", responses: []string{invalidSubnet, "NatGatewayLimitExceeded"}, retryRejections: true, rejected: true},
		{name: "When observer installation fails and creation rejects, it should send the request without proving rejection", responses: []string{"NatGatewayLimitExceeded"}, installationFailure: true},
		{name: "When observer installation fails and creation succeeds, it should send the request and succeed", responses: []string{"success"}, installationFailure: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &natGatewayHTTPClient{responses: test.responses, errors: test.errors}
			observer := &natGatewayAttemptObserver{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}
			output, err := newNATGatewaySDKClient(transport).CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{SubnetId: aws.String("subnet-test"), AllocationId: aws.String("eipalloc-test")}, observer.apiOption, func(options *ec2.Options) {
				if test.retryRejections {
					options.Retryer = awsretry.AddWithErrorCodes(options.Retryer, invalidSubnet)
				}
				if test.installationFailure {
					options.APIOptions = append([]func(*middleware.Stack) error{func(stack *middleware.Stack) error { _, removeErr := stack.Finalize.Remove("Retry"); return removeErr }}, options.APIOptions...)
				}
				if test.missingRetry {
					options.APIOptions = append(options.APIOptions, func(stack *middleware.Stack) error { _, removeErr := stack.Finalize.Remove("Retry"); return removeErr })
				}
			})
			if !observer.observed || observer.rejected != test.rejected || transport.calls != len(test.responses) {
				t.Fatalf("observed=%v rejected=%v calls=%d error=%v", observer.observed, observer.rejected, transport.calls, err)
			}
			if len(test.responses) > 0 && test.responses[len(test.responses)-1] == "success" && (err != nil || output == nil || aws.ToString(output.NatGateway.NatGatewayId) != "nat-test") {
				t.Fatalf("creation success lost: output=%v error=%v", output, err)
			}
			if !test.canceled && test.responses[len(test.responses)-1] != "success" {
				var apiErr smithy.APIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != test.responses[len(test.responses)-1] {
					t.Fatalf("terminal API error not retained: %v", err)
				}
			}
		})
	}
}

func TestIsNATGatewayRejection(t *testing.T) {
	quota := &smithy.GenericAPIError{Code: "NatGatewayLimitExceeded", Message: "quota rejection"}
	tests := []struct {
		name     string
		err      error
		rejected bool
	}{
		{name: "When quota rejects creation, it should classify rejection", err: quota, rejected: true},
		{name: "When InvalidSubnet rejects creation, it should classify rejection", err: &smithy.GenericAPIError{Code: invalidSubnet}, rejected: true},
		{name: "When the allocation is not found, it should classify rejection", err: &smithy.GenericAPIError{Code: invalidElasticIPNotFound}, rejected: true},
		{name: "When a wrapped code has different case, it should classify rejection", err: fmt.Errorf("operation: %w", &smithy.GenericAPIError{Code: "natgatewaylimitexceeded"}), rejected: true},
		{name: "When the result is nil, it should not classify rejection"},
		{name: "When an unknown code is returned, it should not classify rejection", err: &smithy.GenericAPIError{Code: "Unknown"}},
		{name: "When a transport error is returned, it should not classify rejection", err: errors.New("connection lost")},
		{name: "When a deadline is exceeded, it should not classify rejection", err: context.DeadlineExceeded},
		{name: "When cancellation is wrapped, it should not classify rejection", err: &smithy.CanceledError{Err: context.Canceled}},
		{name: "When a rejection is joined with cancellation, it should not classify rejection", err: errors.Join(quota, context.Canceled)},
		{name: "When cancellation is joined with rejection, it should not classify rejection", err: errors.Join(context.Canceled, quota)},
		{name: "When cancellation wraps a quota error, it should not classify rejection", err: &smithy.CanceledError{Err: quota}},
		{name: "When a send error wraps a quota error, it should not classify rejection", err: &smithyhttp.RequestSendError{Err: quota}},
		{name: "When a timeout wraps a quota error, it should not classify rejection", err: &natGatewayTimeoutError{error: quota}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isNATGatewayRejection(test.err); got != test.rejected {
				t.Fatalf("rejected=%v, expected %v", got, test.rejected)
			}
		})
	}
}

type natGatewayTimeoutError struct{ error }

func (*natGatewayTimeoutError) Timeout() bool     { return true }
func (err *natGatewayTimeoutError) Unwrap() error { return err.error }

func TestNatGatewayAttemptObserver_Record(t *testing.T) {
	quota := &smithy.GenericAPIError{Code: "NatGatewayLimitExceeded"}
	tests := []struct {
		name     string
		attempts []awsretry.AttemptResult
		present  bool
		reenter  bool
		rejected bool
	}{
		{name: "When metadata is absent, it should retain ambiguity", attempts: []awsretry.AttemptResult{{Err: quota}}},
		{name: "When metadata is empty, it should retain ambiguity", present: true},
		{name: "When an attempt error is nil, it should retain ambiguity", present: true, attempts: []awsretry.AttemptResult{{}}},
		{name: "When every attempt rejects creation, it should record rejection", present: true, attempts: []awsretry.AttemptResult{{Err: quota}, {Err: quota}}, rejected: true},
		{name: "When the observer reenters, it should retain ambiguity", present: true, attempts: []awsretry.AttemptResult{{Err: quota}}, reenter: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := &natGatewayAttemptObserver{}
			observer.record(awsretry.AttemptResults{Results: test.attempts}, test.present, quota)
			if test.reenter {
				observer.record(awsretry.AttemptResults{Results: test.attempts}, test.present, quota)
			}
			if !observer.observed || observer.rejected != test.rejected {
				t.Fatalf("unexpected observer state: %+v", observer)
			}
		})
	}
}

type natGatewayEC2Fake struct {
	awsapi.EC2API
	testing           *testing.T
	sdk               *ec2.Client
	existing          bool
	lookupErr         error
	allocationErr     error
	releaseErr        error
	unobservedErr     error
	cancelAfterCreate context.CancelFunc
	lastCreateErr     error
	allocations       int
	creates           int
	releases          []string
	outstanding       sets.Set[string]
	events            []string
	cleanupContext    context.Context
	release           func(context.Context) error
}

func (client *natGatewayEC2Fake) DescribeNatGateways(context.Context, *ec2.DescribeNatGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	client.events = append(client.events, "lookup")
	if client.lookupErr != nil {
		return nil, client.lookupErr
	}
	result := &ec2.DescribeNatGatewaysOutput{}
	if client.existing {
		result.NatGateways = []ec2types.NatGateway{{NatGatewayId: aws.String("nat-existing"), State: ec2types.NatGatewayStateAvailable}}
	}
	return result, nil
}

func (client *natGatewayEC2Fake) AllocateAddress(_ context.Context, input *ec2.AllocateAddressInput, _ ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error) {
	client.testing.Helper()
	client.events = append(client.events, "allocate")
	if client.allocationErr != nil {
		return nil, client.allocationErr
	}
	if input.Domain != ec2types.DomainTypeVpc || len(input.TagSpecifications) != 1 {
		client.testing.Fatalf("unexpected allocation input: %+v", input)
	}
	tags := input.TagSpecifications[0]
	if tags.ResourceType != ec2types.ResourceTypeElasticIp || !slices.ContainsFunc(tags.Tags, func(tag ec2types.Tag) bool {
		return aws.ToString(tag.Key) == "kubernetes.io/cluster/infra-test" && aws.ToString(tag.Value) == "owned"
	}) || !slices.ContainsFunc(tags.Tags, func(tag ec2types.Tag) bool {
		return aws.ToString(tag.Key) == "Name" && aws.ToString(tag.Value) == "infra-test-eip-us-east-1a"
	}) {
		client.testing.Fatalf("ownership tags changed: %+v", tags)
	}
	client.allocations++
	allocationID := fmt.Sprintf("eipalloc-test-%d", client.allocations)
	client.outstanding.Insert(allocationID)
	return &ec2.AllocateAddressOutput{AllocationId: aws.String(allocationID)}, nil
}

func (client *natGatewayEC2Fake) CreateNatGateway(ctx context.Context, input *ec2.CreateNatGatewayInput, options ...func(*ec2.Options)) (*ec2.CreateNatGatewayOutput, error) {
	client.testing.Helper()
	client.events = append(client.events, "create")
	client.creates++
	if !client.outstanding.Has(aws.ToString(input.AllocationId)) || len(client.releases) >= client.allocations || aws.ToString(input.SubnetId) != "subnet-test" {
		client.testing.Fatalf("allocation released during retries or unexpected create input: %+v", input)
	}
	var output *ec2.CreateNatGatewayOutput
	var err error
	if client.unobservedErr != nil {
		err = client.unobservedErr
		client.unobservedErr = nil
	} else {
		output, err = client.sdk.CreateNatGateway(ctx, input, options...)
	}
	client.lastCreateErr = err
	if client.cancelAfterCreate != nil {
		client.cancelAfterCreate()
	}
	return output, err
}

func (client *natGatewayEC2Fake) ReleaseAddress(ctx context.Context, input *ec2.ReleaseAddressInput, _ ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error) {
	client.testing.Helper()
	client.events = append(client.events, "release")
	allocationID := aws.ToString(input.AllocationId)
	if !client.outstanding.Has(allocationID) {
		client.testing.Fatalf("release does not own allocation %s", allocationID)
	}
	client.releases = append(client.releases, allocationID)
	client.cleanupContext = ctx
	err := client.releaseErr
	if client.release != nil {
		err = client.release(ctx)
	}
	if err != nil {
		return nil, err
	}
	client.outstanding.Delete(allocationID)
	return &ec2.ReleaseAddressOutput{}, nil
}

type natGatewayTestCase struct {
	name              string
	responses         []string
	transportErrors   []error
	existing          bool
	lookupErr         error
	allocationErr     error
	releaseErr        error
	unobservedErr     error
	cancelAfterCreate bool
	releaseTimeout    bool
	invocations       int
	creates           int
	releases          int
	gateway           string
	ambiguous         bool
	missingRetry      bool
}

func TestCreateInfraOptions_CreateNATGateway(t *testing.T) {
	quota := &smithy.GenericAPIError{Code: "NatGatewayLimitExceeded", Message: "quota rejection"}
	lookupErr := errors.New("lookup failed")
	allocationErr := errors.New("allocation failed")
	releaseErr := errors.New("release denied")
	tests := []natGatewayTestCase{
		{name: "When quota conclusively rejects creation, it should release the exact allocation", responses: []string{"NatGatewayLimitExceeded"}, creates: 1, releases: 1},
		{name: "When repeated quota failures occur, it should not accumulate allocations", responses: []string{"NatGatewayLimitExceeded", "NatGatewayLimitExceeded"}, creates: 2, releases: 2, invocations: 2},
		{name: "When InvalidSubnet retries are exhausted, it should release only after retries", responses: slices.Repeat([]string{invalidSubnet}, 5), creates: 5, releases: 1},
		{name: "When InvalidElasticIpID retries are exhausted, it should release only after retries", responses: slices.Repeat([]string{invalidElasticIPNotFound}, 5), creates: 5, releases: 1},
		{name: "When case variant subnet retries are exhausted, it should release only after retries", responses: slices.Repeat([]string{"invalidsubnet"}, 5), creates: 5, releases: 1},
		{name: "When case variant allocation retries are exhausted, it should release only after retries", responses: slices.Repeat([]string{"invalidelasticipid.notfound"}, 5), creates: 5, releases: 1},
		{name: "When subnet rejection precedes quota rejection, it should release after all calls", responses: []string{invalidSubnet, "NatGatewayLimitExceeded"}, creates: 2, releases: 1},
		{name: "When a gateway already exists, it should reuse it without allocation", existing: true, gateway: "nat-existing"},
		{name: "When gateway lookup fails, it should not allocate or release", lookupErr: lookupErr},
		{name: "When allocation fails, it should not create or release", allocationErr: allocationErr},
		{name: "When creation succeeds, it should keep the allocation", responses: []string{"success"}, creates: 1, gateway: "nat-test"},
		{name: "When observer installation fails and creation succeeds, it should keep the allocation", responses: []string{"success"}, creates: 1, gateway: "nat-test", missingRetry: true},
		{name: "When observer installation fails and quota rejects, it should retain the allocation", responses: []string{"NatGatewayLimitExceeded"}, creates: 1, ambiguous: true, missingRetry: true},
		{name: "When observer installation fails across outer retries, it should retain the allocation", responses: []string{invalidSubnet, "NatGatewayLimitExceeded"}, creates: 2, ambiguous: true, missingRetry: true},
		{name: "When subnet retry succeeds, it should keep the allocation", responses: []string{invalidSubnet, "success"}, creates: 2, gateway: "nat-test"},
		{name: "When allocation retry succeeds, it should keep the allocation", responses: []string{invalidElasticIPNotFound, "success"}, creates: 2, gateway: "nat-test"},
		{name: "When release is denied, it should retain the original creation error", responses: []string{"NatGatewayLimitExceeded"}, creates: 1, releases: 1, releaseErr: releaseErr},
		{name: "When release times out, it should retain the original creation error", responses: []string{"NatGatewayLimitExceeded"}, creates: 1, releases: 1, releaseTimeout: true, cancelAfterCreate: true},
		{name: "When the parent is canceled after rejection, it should still release", responses: []string{"NatGatewayLimitExceeded"}, creates: 1, releases: 1, cancelAfterCreate: true},
		{name: "When an unknown API error occurs, it should retain the allocation", responses: []string{"Unknown"}, creates: 1, ambiguous: true},
		{name: "When transport retries fail, it should retain the allocation", responses: []string{"", "", ""}, transportErrors: []error{errors.New("lost"), errors.New("lost"), errors.New("lost")}, creates: 1, ambiguous: true},
		{name: "When deadline errors exhaust retries, it should retain the allocation", responses: []string{"", "", ""}, transportErrors: []error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded}, creates: 1, ambiguous: true},
		{name: "When creation returns cancellation, it should retain the allocation", unobservedErr: &smithy.CanceledError{Err: context.Canceled}, creates: 1, ambiguous: true},
		{name: "When an unobserved plain error occurs, it should retain the allocation", unobservedErr: errors.New("unknown failure"), creates: 1, ambiguous: true},
		{name: "When an unobserved quota rejection occurs, it should retain the allocation", unobservedErr: fmt.Errorf("operation: %w", quota), creates: 1, ambiguous: true},
		{name: "When an unobserved subnet rejection precedes quota rejection, it should retain the allocation", unobservedErr: &smithy.GenericAPIError{Code: invalidSubnet}, responses: []string{"NatGatewayLimitExceeded"}, creates: 2, ambiguous: true},
		{name: "When a joined cancellation precedes quota rejection, it should retain the allocation", unobservedErr: errors.Join(&smithy.GenericAPIError{Code: invalidSubnet}, context.Canceled), responses: []string{"NatGatewayLimitExceeded"}, creates: 2, ambiguous: true},
		{name: "When a lost response precedes quota rejection, it should retain the allocation", responses: []string{"", "NatGatewayLimitExceeded"}, transportErrors: []error{errors.New("response lost")}, creates: 1, ambiguous: true},
		{name: "When a transport error wraps quota errors, it should retain the allocation", responses: []string{"", "", ""}, transportErrors: []error{quota, quota, quota}, creates: 1, ambiguous: true},
		{name: "When server failure precedes quota rejection, it should retain the allocation", responses: []string{"InternalError", "NatGatewayLimitExceeded"}, creates: 1, ambiguous: true},
		{name: "When an unknown SDK rejection precedes quota rejection, it should retain the allocation", responses: []string{"Throttling", "NatGatewayLimitExceeded"}, creates: 1, ambiguous: true},
		{name: "When inner ambiguity precedes a retriable outer rejection, it should retain the allocation", responses: []string{"", invalidSubnet, "NatGatewayLimitExceeded"}, transportErrors: []error{errors.New("response lost")}, creates: 2, ambiguous: true},
		{name: "When a retriable outer rejection precedes inner ambiguity, it should retain the allocation", responses: []string{invalidSubnet, "", "NatGatewayLimitExceeded"}, transportErrors: []error{nil, errors.New("response lost")}, creates: 2, ambiguous: true},
		{name: "When ambiguity is followed by success, it should keep the allocation", responses: []string{"", "success"}, transportErrors: []error{errors.New("response lost")}, creates: 1, gateway: "nat-test"},
		{name: "When inner ambiguity then outer rejection precedes success, it should keep the allocation", responses: []string{"", invalidSubnet, "success"}, transportErrors: []error{errors.New("response lost")}, creates: 2, gateway: "nat-test"},
	}
	originalBackoff := retryBackoff
	retryBackoff = wait.Backoff{Steps: 5}
	t.Cleanup(func() { retryBackoff = originalBackoff })
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type contextKey struct{}
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "value-test"))
			defer cancel()
			transport := &natGatewayHTTPClient{responses: test.responses, errors: test.transportErrors}
			client := &natGatewayEC2Fake{testing: t, sdk: newNATGatewaySDKClient(transport), existing: test.existing, lookupErr: test.lookupErr, allocationErr: test.allocationErr, releaseErr: test.releaseErr, unobservedErr: test.unobservedErr, outstanding: sets.New[string]()}
			if test.missingRetry {
				client.sdk = ec2.New(client.sdk.Options(), func(options *ec2.Options) {
					options.APIOptions = append(options.APIOptions, func(stack *middleware.Stack) error { _, err := stack.Finalize.Remove("Retry"); return err })
				})
			}
			if test.cancelAfterCreate {
				client.cancelAfterCreate = cancel
			}
			cleanupFinished := make(chan struct{})
			if test.releaseTimeout {
				client.release = func(cleanupCtx context.Context) error {
					deadline, bounded := cleanupCtx.Deadline()
					if cleanupCtx.Err() != nil || !bounded || time.Until(deadline) > 250*time.Millisecond || cleanupCtx.Value(contextKey{}) != "value-test" {
						t.Fatal("cleanup inherited parent cancellation, lost values or lacks the injected deadline")
					}
					<-cleanupCtx.Done()
					close(cleanupFinished)
					return cleanupCtx.Err()
				}
			}
			var logs []string
			logger := funcr.New(func(prefix, arguments string) { logs = append(logs, prefix+arguments) }, funcr.Options{})
			options := &CreateInfraOptions{InfraID: "infra-test"}
			invocations := max(1, test.invocations)
			for invocation := 0; invocation < invocations; invocation++ {
				start := time.Now()
				var gateway string
				var err error
				if test.releaseTimeout {
					gateway, err = options.createNATGatewayWithTimeout(ctx, logger, client, "subnet-test", "us-east-1a", 250*time.Millisecond)
					select {
					case <-cleanupFinished:
					default:
						t.Fatal("creation returned before cleanup completed")
					}
					t.Logf("injected cleanup timeout completed in %s", time.Since(start))
				} else {
					gateway, err = options.CreateNATGateway(ctx, logger, client, "subnet-test", "us-east-1a")
					if time.Since(start) > 2*time.Second {
						t.Fatal("immediate outcome returned too slowly")
					}
				}
				test.assertResult(t, client, gateway, err)
				if test.releases > 0 && test.releaseErr == nil && !test.releaseTimeout && client.outstanding.Len() != 0 {
					t.Fatalf("allocations grew: %v", client.outstanding)
				}
			}
			test.assertCalls(t, client, transport, invocations)
			test.assertCleanup(t, client, logs)
		})
	}
}

func (test natGatewayTestCase) assertResult(t *testing.T, client *natGatewayEC2Fake, gateway string, err error) {
	t.Helper()
	if gateway != test.gateway {
		t.Fatalf("gateway=%q expected=%q", gateway, test.gateway)
	}
	if test.gateway != "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	primary := client.lastCreateErr
	if test.lookupErr != nil {
		primary = test.lookupErr
	}
	if test.allocationErr != nil {
		primary = test.allocationErr
	}
	if primary == nil || !errors.Is(err, primary) {
		t.Fatalf("original error lost: got=%v original=%v", err, primary)
	}
	if client.lastCreateErr != nil {
		if !strings.HasPrefix(err.Error(), "cannot create NAT gateway: ") {
			t.Fatalf("error prefix changed: %v", err)
		}
		var originalAPI, returnedAPI smithy.APIError
		if errors.As(primary, &originalAPI) && (!errors.As(err, &returnedAPI) || originalAPI != returnedAPI) {
			t.Fatalf("API error identity lost: %v", err)
		}
		var originalOperation, returnedOperation *smithy.OperationError
		if errors.As(primary, &originalOperation) && (!errors.As(err, &returnedOperation) || originalOperation != returnedOperation) {
			t.Fatalf("operation error identity lost: %v", err)
		}
	}
	if test.releaseErr != nil && errors.Is(err, test.releaseErr) {
		t.Fatal("cleanup error masked create error")
	}
}

func (test natGatewayTestCase) assertCalls(t *testing.T, client *natGatewayEC2Fake, transport *natGatewayHTTPClient, invocations int) {
	t.Helper()
	if client.creates != test.creates || len(client.releases) != test.releases || transport.calls != len(test.responses) {
		t.Fatalf("creates=%d releases=%v requests=%d events=%v", client.creates, client.releases, transport.calls, client.events)
	}
	expectedEvents := []string{}
	for invocation := 0; invocation < invocations; invocation++ {
		expectedEvents = append(expectedEvents, "lookup")
		if test.existing || test.lookupErr != nil {
			continue
		}
		expectedEvents = append(expectedEvents, "allocate")
		if test.allocationErr != nil {
			continue
		}
		expectedEvents = append(expectedEvents, slices.Repeat([]string{"create"}, test.creates/invocations)...)
		if test.releases > 0 {
			expectedEvents = append(expectedEvents, "release")
		}
	}
	if !slices.Equal(client.events, expectedEvents) {
		t.Fatalf("call trace=%v expected=%v", client.events, expectedEvents)
	}
	if client.allocations != 0 && test.releases == 0 && client.outstanding.Len() != client.allocations {
		t.Fatal("allocation was unexpectedly removed")
	}
	if test.existing || test.lookupErr != nil || test.allocationErr != nil {
		if client.allocations != 0 {
			t.Fatal("unexpected allocation")
		}
	}
	for index, allocationID := range client.releases {
		if allocationID != fmt.Sprintf("eipalloc-test-%d", index+1) {
			t.Fatalf("released wrong allocation %s", allocationID)
		}
	}
}

func (test natGatewayTestCase) assertCleanup(t *testing.T, client *natGatewayEC2Fake, logs []string) {
	t.Helper()
	if test.releases > 0 {
		if client.events[len(client.events)-1] != "release" {
			t.Fatalf("release not terminal: %v", client.events)
		}
		deadline, ok := client.cleanupContext.Deadline()
		if test.releaseTimeout {
			if !ok || !errors.Is(client.cleanupContext.Err(), context.DeadlineExceeded) || client.outstanding.Len() != 1 {
				t.Fatal("cleanup timeout did not retain the allocation")
			}
		} else if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 10*time.Second || !errors.Is(client.cleanupContext.Err(), context.Canceled) {
			t.Fatalf("cleanup context not bounded and canceled: %v", client.cleanupContext.Err())
		}
	}
	logOutput := strings.Join(logs, "\n")
	if test.ambiguous && (!strings.Contains(logOutput, "Retaining elastic IP") || !strings.Contains(logOutput, "eipalloc-test-1")) {
		t.Fatalf("missing retention limitation: %s", logOutput)
	}
	if test.releaseErr != nil && (!strings.Contains(logOutput, test.releaseErr.Error()) || !strings.Contains(logOutput, "eipalloc-test-1")) {
		t.Fatalf("missing cleanup failure: %s", logOutput)
	}
	if test.releaseTimeout && (!strings.Contains(logOutput, context.DeadlineExceeded.Error()) || !strings.Contains(logOutput, "eipalloc-test-1")) {
		t.Fatalf("missing cleanup timeout: %s", logOutput)
	}
}

func TestReleaseNATGatewayEIP(t *testing.T) {
	tests := []struct {
		name    string
		fail    bool
		timeout bool
		expired bool
	}{
		{name: "When the parent is canceled, it should detach cleanup and cancel its timer"},
		{name: "When release fails, it should log the allocation and return promptly", fail: true},
		{name: "When release times out, it should return promptly and log the allocation", timeout: true},
		{name: "When the parent deadline expired, it should detach cleanup with its own deadline", expired: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type contextKey struct{}
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "value-test"))
			cancel()
			if test.expired {
				ctx, cancel = context.WithDeadline(context.WithValue(t.Context(), contextKey{}, "value-test"), time.Now().Add(-time.Second))
				defer cancel()
			}
			client := &natGatewayEC2Fake{testing: t, outstanding: sets.New("eipalloc-test")}
			client.release = func(cleanupCtx context.Context) error {
				if cleanupCtx.Err() != nil || cleanupCtx.Value(contextKey{}) != "value-test" {
					t.Fatal("cleanup inherited cancellation or lost context values")
				}
				if deadline, ok := cleanupCtx.Deadline(); !ok || time.Until(deadline) > 250*time.Millisecond {
					t.Fatal("cleanup deadline missing or unbounded")
				}
				if test.timeout {
					<-cleanupCtx.Done()
					return cleanupCtx.Err()
				}
				if test.fail {
					return errors.New("release denied")
				}
				return nil
			}
			var logs []string
			logger := funcr.New(func(prefix, arguments string) { logs = append(logs, prefix+arguments) }, funcr.Options{})
			start := time.Now()
			releaseNATGatewayEIP(ctx, logger, client, "eipalloc-test", 250*time.Millisecond)
			if elapsed := time.Since(start); elapsed > 2*time.Second || (test.timeout && elapsed < 250*time.Millisecond) {
				t.Fatalf("unexpected cleanup duration: %v", elapsed)
			}
			if len(client.releases) != 1 || client.cleanupContext.Err() == nil {
				t.Fatal("cleanup was not called once with a canceled timer afterward")
			}
			if (test.fail || test.timeout) && (!strings.Contains(strings.Join(logs, "\n"), "eipalloc-test") || !strings.Contains(strings.Join(logs, "\n"), "Cannot release")) {
				t.Fatalf("cleanup failure not logged: %v", logs)
			}
		})
	}
}

var _ awsapi.EC2API = (*natGatewayEC2Fake)(nil)
