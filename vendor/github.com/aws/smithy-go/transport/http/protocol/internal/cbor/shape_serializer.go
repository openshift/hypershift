package cbor

import (
	"encoding/binary"
	"math"
	"math/big"
	"time"

	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/document"
	"github.com/aws/smithy-go/traits"
)

// ShapeSerializer implements marshaling of Smithy shapes to CBOR.
type ShapeSerializer struct {
	buf  []byte
	head []byte

	opts ShapeSerializerOptions

	// rootSchema is the schema passed to the first WriteStruct call,
	// used by the protocol to determine if the input is a Unit shape.
	rootSchema *smithy.Schema
}

// ShapeSerializerOptions configures ShapeSerializer.
type ShapeSerializerOptions struct{}

var _ smithy.ShapeSerializer = (*ShapeSerializer)(nil)

// NewShapeSerializer creates a new ShapeSerializer.
func NewShapeSerializer(opts ...func(*ShapeSerializerOptions)) *ShapeSerializer {
	o := ShapeSerializerOptions{}
	for _, fn := range opts {
		fn(&o)
	}
	return &ShapeSerializer{opts: o}
}

// Bytes returns the serialized CBOR bytes.
func (s *ShapeSerializer) Bytes() []byte {
	return s.buf
}

// IsUnitShape returns true if the serialized content represents a Unit shape
// (a struct with no defined input, marked with the UnitShape trait).
func (s *ShapeSerializer) IsUnitShape() bool {
	if s.rootSchema == nil {
		return false
	}
	_, ok := smithy.SchemaTrait[*traits.UnitShape](s.rootSchema)
	return ok
}

func (s *ShapeSerializer) top() byte {
	if len(s.head) == 0 {
		return 0xff
	}
	return s.head[len(s.head)-1]
}

func (s *ShapeSerializer) push(v byte) {
	s.head = append(s.head, v)
}

func (s *ShapeSerializer) pop() {
	s.head = s.head[:len(s.head)-1]
}

// WriteBool implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteBool(schema *smithy.Schema, v bool) {
	s.writeKey(schema)
	if v {
		s.buf = append(s.buf, compose(majorType7, major7True))
	} else {
		s.buf = append(s.buf, compose(majorType7, major7False))
	}
}

func (s *ShapeSerializer) writeInt(v int64) {
	if v >= 0 {
		s.writeUint(uint64(v))
	} else {
		s.writeArg(majorTypeNegInt, uint64(-v-1))
	}
}

func (s *ShapeSerializer) writeUint(v uint64) {
	s.writeArg(majorTypeUint, v)
}

// WriteInt8 implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteInt8(schema *smithy.Schema, v int8) {
	s.writeKey(schema)
	s.writeInt(int64(v))
}

// WriteInt16 implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteInt16(schema *smithy.Schema, v int16) {
	s.writeKey(schema)
	s.writeInt(int64(v))
}

// WriteInt32 implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteInt32(schema *smithy.Schema, v int32) {
	s.writeKey(schema)
	s.writeInt(int64(v))
}

// WriteInt64 implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteInt64(schema *smithy.Schema, v int64) {
	s.writeKey(schema)
	s.writeInt(v)
}

// WriteFloat32 implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteFloat32(schema *smithy.Schema, v float32) {
	s.writeKey(schema)
	s.buf = append(s.buf, compose(majorType7, major7Float32))
	s.buf = binary.BigEndian.AppendUint32(s.buf, math.Float32bits(v))
}

// WriteFloat64 implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteFloat64(schema *smithy.Schema, v float64) {
	s.writeKey(schema)
	s.buf = append(s.buf, compose(majorType7, major7Float64))
	s.buf = binary.BigEndian.AppendUint64(s.buf, math.Float64bits(v))
}

// WriteString implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteString(schema *smithy.Schema, v string) {
	s.writeKey(schema)
	s.writeTextString(v)
}

// WriteBlob implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteBlob(schema *smithy.Schema, v []byte) {
	if v == nil {
		return
	}
	s.writeKey(schema)
	s.writeArg(majorTypeSlice, uint64(len(v)))
	s.buf = append(s.buf, v...)
}

// WriteList implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteList(schema *smithy.Schema) {
	s.writeKey(schema)
	if s.top() == ctxMapValue {
		s.pop()
	}
	s.buf = append(s.buf, compose(majorTypeList, minorIndefinite))
	s.push(ctxList)
}

// CloseList implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) CloseList() {
	if s.top() != ctxList {
		return
	}
	s.pop()
	s.buf = append(s.buf, 0xff)
}

// WriteMap implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteMap(schema *smithy.Schema) {
	if s.rootSchema == nil && len(s.head) == 0 {
		s.rootSchema = schema
	}
	s.writeKey(schema)
	if s.top() == ctxMapValue {
		s.pop()
	}
	s.buf = append(s.buf, compose(majorTypeMap, minorIndefinite))
	s.push(ctxMap)
}

// WriteKey implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteKey(_ *smithy.Schema, key string) {
	s.writeTextString(key)
	s.push(ctxMapValue)
}

// CloseMap implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) CloseMap() {
	if s.top() != ctxMap {
		return
	}
	s.pop()
	s.buf = append(s.buf, 0xff)
}

// WriteTime implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteTime(schema *smithy.Schema, v time.Time) {
	s.writeKey(schema)
	if s.top() == ctxMapValue {
		s.pop()
	}

	// rpcv2Cbor: always epoch-seconds as float64, tag 1
	epoch := float64(v.UnixMilli()) / 1e3
	s.writeArg(majorTypeTag, 1)
	s.buf = append(s.buf, compose(majorType7, major7Float64))
	s.buf = binary.BigEndian.AppendUint64(s.buf, math.Float64bits(epoch))
}

// WriteUnion implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteUnion(schema, variant *smithy.Schema) {
	s.writeKey(schema)
	// union is a map with a single key
	s.writeArg(majorTypeMap, 1)
	s.writeTextString(variant.MemberName())
	s.push(ctxMapValue)
}

// CloseUnion implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) CloseUnion() {}

// WriteStruct implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteStruct(schema *smithy.Schema) {
	if s.rootSchema == nil && len(s.head) == 0 {
		s.rootSchema = schema
	}
	s.writeKey(schema)
	if s.top() == ctxMapValue {
		s.pop()
	}
	s.buf = append(s.buf, compose(majorTypeMap, minorIndefinite))
	s.push(ctxMap)
}

// CloseStruct implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) CloseStruct() {
	s.pop()
	s.buf = append(s.buf, 0xff)
}

// WriteNil implements [smithy.ShapeSerializer].
func (s *ShapeSerializer) WriteNil(schema *smithy.Schema) {
	s.writeKey(schema)
	if s.top() == ctxMapValue {
		s.pop()
	}
	s.buf = append(s.buf, compose(majorType7, major7Nil))
}

// WriteBigInt is unimplemented and will panic.
func (s *ShapeSerializer) WriteBigInt(schema *smithy.Schema, v *big.Int) {
	panic("unimplemented")
}

// WriteBigFloat is unimplemented and will panic.
func (s *ShapeSerializer) WriteBigFloat(schema *smithy.Schema, v *big.Float) {
	panic("unimplemented")
}

// WriteDocument is unimplemented and will panic.
func (s *ShapeSerializer) WriteDocument(schema *smithy.Schema, v document.Value) {
	panic("unimplemented")
}

// writeKey writes the member name as a CBOR text string key when inside a
// struct or map context.
func (s *ShapeSerializer) writeKey(schema *smithy.Schema) {
	// If we're in a map value context (after WriteKey), just pop it and
	// don't write a key - the key was already written by WriteKey.
	if s.top() == ctxMapValue {
		s.pop()
		return
	}
	if schema == nil {
		return
	}

	if s.top() == ctxMap {
		name := schema.MemberName()
		if name != "" {
			s.writeTextString(name)
		}
	}
}

func (s *ShapeSerializer) writeTextString(v string) {
	s.writeArg(majorTypeString, uint64(len(v)))
	s.buf = append(s.buf, v...)
}

func (s *ShapeSerializer) writeArg(major majorType, arg uint64) {
	if arg < 24 {
		s.buf = append(s.buf, byte(major)<<5|byte(arg))
	} else if arg < 0x100 {
		s.buf = append(s.buf, compose(major, minorArg1), byte(arg))
	} else if arg < 0x10000 {
		s.buf = append(s.buf, compose(major, minorArg2))
		s.buf = binary.BigEndian.AppendUint16(s.buf, uint16(arg))
	} else if arg < 0x100000000 {
		s.buf = append(s.buf, compose(major, minorArg4))
		s.buf = binary.BigEndian.AppendUint32(s.buf, uint32(arg))
	} else {
		s.buf = append(s.buf, compose(major, minorArg8))
		s.buf = binary.BigEndian.AppendUint64(s.buf, arg)
	}
}

// duplicated from the old encoding/cbor

type majorType byte

const (
	majorTypeUint   majorType = 0
	majorTypeNegInt majorType = 1
	majorTypeSlice  majorType = 2
	majorTypeString majorType = 3
	majorTypeList   majorType = 4
	majorTypeMap    majorType = 5
	majorTypeTag    majorType = 6
	majorType7      majorType = 7
)

const (
	minorArg1       = 24
	minorArg2       = 25
	minorArg4       = 26
	minorArg8       = 27
	minorIndefinite = 31
)

const (
	major7False   = 20
	major7True    = 21
	major7Nil       = 22
	major7Undefined = 23
	major7Float16   = minorArg2
	major7Float32 = minorArg4
	major7Float64 = minorArg8
)

// maps minor argument indicators (minorArg1..minorArg8) to the number of bytes
// that follow for the argument value
var argSizes = [4]int{1, 2, 4, 8}

// maps minor values (minorArg1..minorArg8) in major type 7 to the number of
// payload bytes that follow
var major7ExtraSizes = [4]int{0, 2, 4, 8}

func compose(major majorType, minor byte) byte {
	return byte(major)<<5 | minor
}

// context sentinels for the serialization state stack
const (
	ctxList     byte = iota // inside a list
	ctxMap                  // inside a map (struct or smithy map)
	ctxMapValue             // next write is a map value (after WriteKey)
)
