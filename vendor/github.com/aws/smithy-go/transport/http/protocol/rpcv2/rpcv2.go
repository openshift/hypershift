package rpcv2

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/aws/smithy-go"
	internalerrors "github.com/aws/smithy-go/internal/errors"
	internales "github.com/aws/smithy-go/internal/eventstream"
	internalsync "github.com/aws/smithy-go/internal/sync"
	smithyio "github.com/aws/smithy-go/io"
	"github.com/aws/smithy-go/middleware"
	"github.com/aws/smithy-go/traits"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	internalcbor "github.com/aws/smithy-go/transport/http/protocol/internal/cbor"
)

// Protocol implements an RPC v2 protocol.
//
// RPCv2 protocol family:
//   - CBOR: https://smithy.io/2.0/additional-specs/protocols/smithy-rpc-v2.html
type Protocol struct {
	queryCompatible bool
	serviceName     string

	eventstream *internales.Codec
	bufs        *internalsync.BufferPool
}

var _ smithyhttp.ClientProtocol = (*Protocol)(nil)

// ProtocolOptions configures smithy.protocols#rpcv2Cbor.
type ProtocolOptions struct{}

// NewCBOR returns an instance of the smithy.protocols#rpcv2Cbor protocol.
func NewCBOR(service *smithy.ServiceSchema, opts ...func(*ProtocolOptions)) *Protocol {
	var o ProtocolOptions
	for _, fn := range opts {
		fn(&o)
	}
	_, qc := smithy.SchemaTrait[*traits.AWSQueryCompatible](service.Schema)
	return &Protocol{
		queryCompatible: qc,
		serviceName:     service.Schema.ID().Name,
		eventstream: &internales.Codec{
			Serializer:   func() smithy.ShapeSerializer { return internalcbor.NewShapeSerializer() },
			Deserializer: func(p []byte) smithy.ShapeDeserializer { return internalcbor.NewShapeDeserializer(p) },
			ContentType:  "application/cbor",
			ErrorInfo:    internalcbor.GetProtocolErrorInfo,
		},
		bufs: internalsync.NewBufferPool(),
	}
}

// ID identifies the protocol.
func (p *Protocol) ID() smithy.ShapeID {
	return smithy.ShapeID{Namespace: "smithy.protocols", Name: "rpcv2Cbor"}
}

// SerializeRequest serializes a request for rpcv2Cbor.
func (p *Protocol) SerializeRequest(
	ctx context.Context,
	schema *smithy.OperationSchema,
	in smithy.Serializable,
	req *smithyhttp.Request,
) error {
	req.Method = http.MethodPost
	req.URL.Path = fmt.Sprintf("/service/%s/operation/%s",
		p.serviceName, middleware.GetOperationName(ctx))
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	req.Header.Set("Accept", "application/cbor")
	if p.queryCompatible {
		req.Header.Set("X-Amzn-Query-Mode", "true")
	}

	if schema.IsInputEventStream() {
		req.Header.Set("Content-Type", "application/vnd.amazon.eventstream")
		return nil
	}

	if schema.Input == nil {
		return nil
	}

	ss := internalcbor.NewShapeSerializer()
	in.Serialize(ss)

	payload := ss.Bytes()
	if len(payload) == 0 {
		return nil
	}

	if ss.IsUnitShape() {
		return nil
	}

	req.Header.Set("Content-Type", "application/cbor")

	sreq, err := req.SetStream(bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("set stream: %w", err)
	}

	*req = *sreq
	return nil
}

// DeserializeResponse deserializes a response for rpcv2Cbor.
func (p *Protocol) DeserializeResponse(
	ctx context.Context,
	schema *smithy.OperationSchema,
	types *smithy.TypeRegistry,
	resp *smithyhttp.Response,
	out smithy.Deserializable,
) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return p.deserializeError(types, resp)
	}

	if schema.IsOutputEventStream() {
		if schema.IsInputEventStream() {
			if err := p.eventstream.RequireBidiHTTP2(resp.Proto, resp.ProtoMajor); err != nil {
				return err
			}
		}
		return nil
	}

	// NOTE: payload aliases the pooled buffer, so the deserializer must not
	// retain references into it -- everything it yields to the caller is
	// copied out today.
	buf, err := p.bufs.Get(resp.Body)
	if err != nil {
		return &smithy.DeserializationError{Err: err}
	}
	defer p.bufs.Put(buf)

	payload := buf.Bytes()

	if len(payload) == 0 {
		return nil
	}

	sd := internalcbor.NewShapeDeserializer(payload)
	if err := out.Deserialize(sd); err != nil {
		return &smithy.DeserializationError{Err: err}
	}

	return nil
}

// HasInitialEventMessage is true because this is an RPC protocol.
func (*Protocol) HasInitialEventMessage() bool {
	return true
}

// SerializeEventMessage implements [smithyhttp.ClientProtocol].
func (p *Protocol) SerializeEventMessage(schema, variant *smithy.Schema, v smithy.Serializable, w io.Writer) error {
	return p.eventstream.SerializeEventMessage(schema, variant, v, w)
}

// DeserializeEventMessage implements [smithyhttp.ClientProtocol].
func (p *Protocol) DeserializeEventMessage(schema *smithy.Schema, types *smithy.TypeRegistry, r io.Reader) (smithy.Deserializable, error) {
	return p.eventstream.DeserializeEventMessage(schema, types, r)
}

// SerializeInitialRequest implements [smithyhttp.ClientProtocol].
func (p *Protocol) SerializeInitialRequest(schema *smithy.Schema, v smithy.Serializable, w io.Writer) error {
	return p.eventstream.SerializeInitialRequest(schema, v, w)
}

// DeserializeInitialResponse implements [smithyhttp.ClientProtocol].
func (p *Protocol) DeserializeInitialResponse(schema *smithy.Schema, r io.Reader, out smithy.Deserializable) error {
	return p.eventstream.DeserializeInitialResponse(schema, r, out)
}

func (p *Protocol) deserializeError(types *smithy.TypeRegistry, response *smithyhttp.Response) error {
	var errorBuffer bytes.Buffer
	if _, err := io.Copy(&errorBuffer, response.Body); err != nil {
		return &smithy.DeserializationError{Err: fmt.Errorf("failed to copy error response body, %w", err)}
	}
	errorBody := errorBuffer.Bytes()

	errorCode := "UnknownError"
	errorMessage := errorCode

	var buff [1024]byte
	ringBuffer := smithyio.NewRingBuffer(buff[:])

	body := io.TeeReader(bytes.NewReader(errorBody), ringBuffer)
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		var snapshot bytes.Buffer
		io.Copy(&snapshot, ringBuffer)
		return &smithy.DeserializationError{
			Err:      fmt.Errorf("failed to read response body, %w", err),
			Snapshot: snapshot.Bytes(),
		}
	}

	bodyType, bodyMessage, err := internalcbor.GetProtocolErrorInfo(bodyBytes)
	if err != nil {
		var snapshot bytes.Buffer
		io.Copy(&snapshot, ringBuffer)
		return &smithy.DeserializationError{
			Err:      fmt.Errorf("failed to decode response body, %w", err),
			Snapshot: snapshot.Bytes(),
		}
	}

	if bodyType != "" {
		errorCode = bodyType
	}
	if bodyMessage != "" {
		errorMessage = bodyMessage
	}

	errorCode = internalerrors.SanitizeErrorCode(errorCode)

	var queryCode string
	var queryFault smithy.ErrorFault
	if p.queryCompatible {
		queryHeader := response.Header.Get("X-Amzn-Query-Error")
		queryCode, queryFault = internalerrors.ParseQueryError(queryHeader)
	}

	perr, ok := types.DeserializableError(errorCode)
	if !ok {
		code := errorCode
		if queryCode != "" {
			code = queryCode
		}
		return &smithy.GenericAPIError{
			Code:    code,
			Message: errorMessage,
			Fault:   queryFault,
		}
	}

	if len(bodyBytes) > 0 {
		deser := internalcbor.NewShapeDeserializer(bodyBytes)
		if err := perr.Deserialize(deser); err != nil {
			return &smithy.DeserializationError{Err: err}
		}
	}

	if queryCode != "" {
		internalerrors.SetErrorCodeOverride(perr, queryCode)
	}

	return perr
}
