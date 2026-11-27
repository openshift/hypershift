package cbor

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/document"
	smithycbor "github.com/aws/smithy-go/encoding/cbor"
	"github.com/aws/smithy-go/internal/serde"
)

var errUnexpectedEOF = errors.New("unexpected end of CBOR data")

// ShapeDeserializer implements unmarshaling of CBOR into Smithy shapes.
type ShapeDeserializer struct {
	p    []byte
	off  int
	head serde.Stack[deserCtx]
	opts ShapeDeserializerOptions

	arena serde.StringArena
}

type deserCtxKind byte

const (
	deserCtxList deserCtxKind = iota
	deserCtxMap
	deserCtxStruct
	deserCtxUnion
)

type deserCtx struct {
	kind      deserCtxKind
	schema    *smithy.Schema
	remaining int
}

// ShapeDeserializerOptions configures ShapeDeserializer.
type ShapeDeserializerOptions struct{}

var _ smithy.ShapeDeserializer = (*ShapeDeserializer)(nil)

// NewShapeDeserializer creates a new ShapeDeserializer.
func NewShapeDeserializer(p []byte, opts ...func(*ShapeDeserializerOptions)) *ShapeDeserializer {
	o := ShapeDeserializerOptions{}
	for _, fn := range opts {
		fn(&o)
	}

	d := &ShapeDeserializer{p: p, head: serde.NewStack[deserCtx](), opts: o}
	d.arena.Reset(len(p) / serde.ArenaPayloadFactor)
	return d
}

func (d *ShapeDeserializer) eof() bool {
	return d.off >= len(d.p)
}

func (d *ShapeDeserializer) take(n uint64) ([]byte, error) {
	remaining := len(d.p) - d.off
	if remaining < 0 || n > uint64(remaining) {
		return nil, fmt.Errorf("length %d exceeds remaining data", n)
	}

	end := d.off + int(n)
	b := d.p[d.off:end]
	d.off = end
	return b, nil
}

func (d *ShapeDeserializer) peekMajor() majorType {
	return majorType(d.p[d.off] & 0xe0 >> 5)
}

func (d *ShapeDeserializer) peekMinor() byte {
	return d.p[d.off] & 0x1f
}

func (d *ShapeDeserializer) readArg() (uint64, error) {
	if d.eof() {
		return 0, errUnexpectedEOF
	}

	minor := d.peekMinor()
	if minor < minorArg1 {
		d.off++
		return uint64(minor), nil
	}

	idx := int(minor - minorArg1)
	if idx < 0 || idx >= len(argSizes) {
		return 0, fmt.Errorf("unexpected minor value %d", minor)
	}

	n := argSizes[idx]
	if d.off+1+n > len(d.p) {
		return 0, errUnexpectedEOF
	}

	buf := d.p[d.off+1 : d.off+1+n]
	d.off += 1 + n

	switch n {
	case 1:
		return uint64(buf[0]), nil
	case 2:
		return uint64(binary.BigEndian.Uint16(buf)), nil
	case 4:
		return uint64(binary.BigEndian.Uint32(buf)), nil
	default:
		return binary.BigEndian.Uint64(buf), nil
	}
}

func (d *ShapeDeserializer) readInt64() (int64, error) {
	if d.eof() {
		return 0, errUnexpectedEOF
	}

	major := d.peekMajor()
	switch major {
	case majorTypeUint:
		v, err := d.readArg()
		if err != nil {
			return 0, err
		}
		if v > math.MaxInt64 {
			return 0, fmt.Errorf("cbor uint %d exceeds max int64", v)
		}
		return int64(v), nil
	case majorTypeNegInt:
		v, err := d.readArg()
		if err != nil {
			return 0, err
		}
		// CBOR negint: actual value is -1 - v
		if v > math.MaxInt64 {
			return 0, fmt.Errorf("cbor negint exceeds min int64")
		}
		return -1 - int64(v), nil
	default:
		return 0, fmt.Errorf("expected integer, got major type %d", major)
	}
}

func (d *ShapeDeserializer) readFloat64() (float64, error) {
	if d.eof() {
		return 0, errUnexpectedEOF
	}

	major := d.peekMajor()
	switch major {
	case majorType7:
		minor := d.peekMinor()
		switch minor {
		case major7Float16:
			if d.off+3 > len(d.p) {
				return 0, errUnexpectedEOF
			}
			bits := binary.BigEndian.Uint16(d.p[d.off+1 : d.off+3])
			d.off += 3
			return float64(math.Float32frombits(float16to32(bits))), nil
		case major7Float32:
			if d.off+5 > len(d.p) {
				return 0, errUnexpectedEOF
			}
			bits := binary.BigEndian.Uint32(d.p[d.off+1 : d.off+5])
			d.off += 5
			return float64(math.Float32frombits(bits)), nil
		case major7Float64:
			if d.off+9 > len(d.p) {
				return 0, errUnexpectedEOF
			}
			bits := binary.BigEndian.Uint64(d.p[d.off+1 : d.off+9])
			d.off += 9
			return math.Float64frombits(bits), nil
		default:
			return 0, fmt.Errorf("given majorType7, expected a minor of float type, instead got %d", minor)
		}
	case majorTypeUint:
		v, err := d.readArg()
		if err != nil {
			return 0, err
		}
		return float64(v), nil
	case majorTypeNegInt:
		v, err := d.readArg()
		if err != nil {
			return 0, err
		}
		return -1 - float64(v), nil
	default:
		return 0, fmt.Errorf("expected float, got major type %d", major)
	}
}

// ReadNil implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadNil(s *smithy.Schema) (bool, error) {
	if d.eof() {
		return false, errUnexpectedEOF
	}

	if d.peekMajor() == majorType7 {
		minor := d.peekMinor()
		if minor == major7Nil || minor == major7Undefined {
			d.off++
			return true, nil
		}
	}
	return false, nil
}

// ReadInt8 implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadInt8(s *smithy.Schema, v *int8) error {
	return readInt(d, v, math.MinInt8, math.MaxInt8)
}

// ReadInt16 implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadInt16(s *smithy.Schema, v *int16) error {
	return readInt(d, v, math.MinInt16, math.MaxInt16)
}

// ReadInt32 implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadInt32(s *smithy.Schema, v *int32) error {
	return readInt(d, v, math.MinInt32, math.MaxInt32)
}

// ReadInt64 implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadInt64(s *smithy.Schema, v *int64) error {
	return readInt(d, v, math.MinInt64, math.MaxInt64)
}

// ReadFloat32 implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadFloat32(s *smithy.Schema, v *float32) error {
	return readFloat(d, v)
}

// ReadFloat64 implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadFloat64(s *smithy.Schema, v *float64) error {
	return readFloat(d, v)
}

// ReadBool implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadBool(s *smithy.Schema, v *bool) error {
	if d.eof() {
		return errUnexpectedEOF
	}
	if d.peekMajor() != majorType7 {
		return fmt.Errorf("expected bool, got major type %d", d.peekMajor())
	}
	minor := d.peekMinor()
	switch minor {
	case major7True:
		*v = true
		d.off++
		return nil
	case major7False:
		*v = false
		d.off++
		return nil
	default:
		return fmt.Errorf("expected bool, got minor %d", minor)
	}
}

// ReadString implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadString(s *smithy.Schema, v *string) error {
	b, err := d.readStringBytes()
	if err != nil {
		return err
	}

	*v = d.arena.String(b)
	return nil
}

// readStringBytes reads a string token and returns its contents without
// materializing a string. For the definite-length form the result aliases the
// payload, so callers that retain it must copy.
func (d *ShapeDeserializer) readStringBytes() ([]byte, error) {
	if d.eof() {
		return nil, errUnexpectedEOF
	}

	if d.peekMajor() != majorTypeString {
		return nil, fmt.Errorf("expected string, got major type %d", d.peekMajor())
	}

	if d.peekMinor() == minorIndefinite {
		d.off++
		var result []byte
		for d.off < len(d.p) && d.p[d.off] != 0xff {
			if d.peekMajor() != majorTypeString {
				return nil, fmt.Errorf("expected string chunk, got major type %d", d.peekMajor())
			}
			slen, err := d.readArg()
			if err != nil {
				return nil, err
			}
			chunk, err := d.take(slen)
			if err != nil {
				return nil, err
			}
			result = append(result, chunk...)
		}
		d.off++ // skip terminator
		return result, nil
	}

	slen, err := d.readArg()
	if err != nil {
		return nil, err
	}

	return d.take(slen)
}

// ReadTime implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadTime(schema *smithy.Schema, v *time.Time) error {
	if d.eof() {
		return errUnexpectedEOF
	}

	if d.peekMajor() != majorTypeTag {
		return fmt.Errorf("expected tag for timestamp, got major type %d", d.peekMajor())
	}
	tagID, err := d.readArg()
	if err != nil {
		return err
	}
	if tagID != 1 {
		return fmt.Errorf("expected tag 1 for timestamp, got %d", tagID)
	}

	if d.eof() {
		return errUnexpectedEOF
	}

	major := d.peekMajor()
	switch major {
	case majorTypeUint, majorTypeNegInt:
		secs, err := d.readInt64()
		if err != nil {
			return err
		}
		*v = time.Unix(secs, 0)
		return nil
	case majorType7:
		f, err := d.readFloat64()
		if err != nil {
			return err
		}
		*v = time.UnixMilli(int64(f * 1e3))
		return nil
	default:
		return fmt.Errorf("unexpected major type %d in timestamp tag", major)
	}
}

// ReadBlob implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadBlob(s *smithy.Schema, v *[]byte) error {
	if isNil, err := d.ReadNil(s); isNil || err != nil {
		return err
	}

	if d.peekMajor() != majorTypeSlice {
		return fmt.Errorf("expected byte string, got major type %d", d.peekMajor())
	}
	slen, err := d.readArg()
	if err != nil {
		return err
	}
	b, err := d.take(slen)
	if err != nil {
		return err
	}
	*v = make([]byte, len(b))
	copy(*v, b)
	return nil
}

// ReadList implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadList(s *smithy.Schema) error {
	if d.eof() {
		return errUnexpectedEOF
	}

	if d.peekMajor() != majorTypeList {
		return fmt.Errorf("expected list, got major type %d", d.peekMajor())
	}
	if d.peekMinor() == minorIndefinite {
		d.off++
		d.head.Push(deserCtx{kind: deserCtxList, remaining: -1})
		return nil
	}
	count, err := d.readArg()
	if err != nil {
		return err
	}
	d.head.Push(deserCtx{kind: deserCtxList, remaining: int(count)})
	return nil
}

// ReadListItem implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadListItem(s *smithy.Schema) (bool, error) {
	lc := d.head.Top()
	if lc == nil || lc.kind != deserCtxList {
		return false, fmt.Errorf("ReadListItem called without ReadList")
	}

	if lc.remaining == -1 {
		if d.off < len(d.p) && d.p[d.off] == 0xff {
			d.off++
			d.head.Pop()
			return false, nil
		}
		return true, nil
	}
	if lc.remaining <= 0 {
		d.head.Pop()
		return false, nil
	}
	lc.remaining--
	return true, nil
}

// ReadMap implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadMap(s *smithy.Schema) error {
	if d.eof() {
		return errUnexpectedEOF
	}

	if d.peekMajor() != majorTypeMap {
		return fmt.Errorf("expected map, got major type %d", d.peekMajor())
	}
	if d.peekMinor() == minorIndefinite {
		d.off++
		d.head.Push(deserCtx{kind: deserCtxMap, remaining: -1})
		return nil
	}
	count, err := d.readArg()
	if err != nil {
		return err
	}
	d.head.Push(deserCtx{kind: deserCtxMap, remaining: int(count)})
	return nil
}

// ReadMapKey implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadMapKey(s *smithy.Schema) (string, bool, error) {
	mc := d.head.Top()
	if mc == nil || mc.kind != deserCtxMap {
		return "", false, errors.New("ReadMapKey called without ReadMap")
	}

	if mc.remaining == -1 {
		if d.off < len(d.p) && d.p[d.off] == 0xff {
			d.off++
			d.head.Pop()
			return "", false, nil
		}
	} else {
		if mc.remaining <= 0 {
			d.head.Pop()
			return "", false, nil
		}
		mc.remaining--
	}

	var key string
	if err := d.ReadString(nil, &key); err != nil {
		return "", false, err
	}
	return key, true, nil
}

// ReadStruct implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadStruct(s *smithy.Schema) error {
	if isNil, err := d.ReadNil(s); isNil || err != nil {
		return err
	}

	if d.peekMajor() != majorTypeMap {
		return fmt.Errorf("expected map for struct, got major type %d", d.peekMajor())
	}
	if d.peekMinor() == minorIndefinite {
		d.off++
		d.head.Push(deserCtx{kind: deserCtxStruct, schema: s, remaining: -1})
		return nil
	}
	count, err := d.readArg()
	if err != nil {
		return err
	}
	d.head.Push(deserCtx{kind: deserCtxStruct, schema: s, remaining: int(count)})
	return nil
}

// ReadStructMember implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadStructMember() (*smithy.Schema, error) {
	sc := d.head.Top()
	if sc == nil || sc.kind != deserCtxStruct {
		return nil, fmt.Errorf("ReadStructMember called without ReadStruct")
	}

	if sc.remaining == -1 {
		if d.off < len(d.p) && d.p[d.off] == 0xff {
			d.off++
			d.head.Pop()
			return nil, nil
		}
	} else {
		if sc.remaining <= 0 {
			d.head.Pop()
			return nil, nil
		}
		sc.remaining--
	}

	key, err := d.readStringBytes()
	if err != nil {
		return nil, err
	}

	member := sc.schema.Members()[string(key)]
	if member == nil {
		if err := d.skip(); err != nil {
			return nil, err
		}
		return d.ReadStructMember()
	}

	if isNil, err := d.ReadNil(member); err != nil {
		return nil, err
	} else if isNil {
		return d.ReadStructMember()
	}

	return member, nil
}

// ReadUnion implements [smithy.ShapeDeserializer].
func (d *ShapeDeserializer) ReadUnion(s *smithy.Schema) (*smithy.Schema, error) {
	top := d.head.Top()
	if top == nil || top.kind != deserCtxUnion || top.schema != s { // first call: open the map
		if d.eof() {
			return nil, errUnexpectedEOF
		}
		if d.peekMajor() != majorTypeMap {
			return nil, fmt.Errorf("expected map for union, got major type %d", d.peekMajor())
		}
		if d.peekMinor() == minorIndefinite {
			d.off++
			d.head.Push(deserCtx{kind: deserCtxUnion, schema: s, remaining: -1})
		} else {
			count, err := d.readArg()
			if err != nil {
				return nil, err
			}
			d.head.Push(deserCtx{kind: deserCtxUnion, schema: s, remaining: int(count)})
		}
	}

	uc := d.head.Top()
	for {
		if uc.remaining == -1 {
			if d.off < len(d.p) && d.p[d.off] == 0xff {
				d.off++
				d.head.Pop()
				return nil, nil
			}
		} else if uc.remaining <= 0 {
			d.head.Pop()
			return nil, nil
		} else {
			uc.remaining--
		}

		key, err := d.readStringBytes()
		if err != nil {
			return nil, err
		}

		if d.off < len(d.p) && d.peekMajor() == majorType7 {
			minor := d.peekMinor()
			if minor == major7Nil || minor == major7Undefined {
				d.off++
				continue
			}
		}

		member := s.Members()[string(key)]
		if member == nil {
			if err := d.skip(); err != nil {
				return nil, err
			}
			continue
		}

		return member, nil
	}
}

// ReadDocument is unimplemented and will panic.
func (d *ShapeDeserializer) ReadDocument(schema *smithy.Schema, v *document.Value) error {
	panic("unimplemented")
}

func (d *ShapeDeserializer) skip() error {
	if d.eof() {
		return errUnexpectedEOF
	}
	major := d.peekMajor()
	switch major {
	case majorTypeUint, majorTypeNegInt:
		_, err := d.readArg()
		return err
	case majorTypeSlice, majorTypeString:
		if d.peekMinor() == minorIndefinite {
			return d.skipIndefiniteBytes()
		}
		slen, err := d.readArg()
		if err != nil {
			return err
		}
		_, err = d.take(slen)
		return err
	case majorTypeList, majorTypeMap:
		itemsPerEntry := 1
		if major == majorTypeMap {
			itemsPerEntry = 2
		}
		if d.peekMinor() == minorIndefinite {
			d.off++
			for d.off < len(d.p) && d.p[d.off] != 0xff {
				for range itemsPerEntry {
					if err := d.skip(); err != nil {
						return err
					}
				}
			}
			d.off++ // skip terminator
			return nil
		}
		count, err := d.readArg()
		if err != nil {
			return err
		}
		for range int(count) * itemsPerEntry {
			if err := d.skip(); err != nil {
				return err
			}
		}
		return nil
	case majorTypeTag:
		_, err := d.readArg()
		if err != nil {
			return err
		}
		return d.skip() // skip the tagged value
	case majorType7:
		minor := d.peekMinor()
		n := 0
		if minor >= minorArg1 && minor <= minorArg8 {
			n = major7ExtraSizes[minor-minorArg1]
		}
		d.off += 1 + n
		return nil
	default:
		return fmt.Errorf("unexpected major type %d", major)
	}
}

func (d *ShapeDeserializer) skipIndefiniteBytes() error {
	d.off++ // skip the indefinite marker
	for d.off < len(d.p) && d.p[d.off] != 0xff {
		if err := d.skip(); err != nil {
			return err
		}
	}
	d.off++ // skip break
	return nil
}

type tint interface {
	int8 | int16 | int32 | int64
}

func readInt[T tint](d *ShapeDeserializer, v *T, min, max int64) error {
	n, err := d.readInt64()
	if err != nil {
		return err
	}
	if n < min || n > max {
		return fmt.Errorf("int %d exceeds %T range", n, *v)
	}
	*v = T(n)
	return nil
}

type tfloat interface {
	float32 | float64
}

func readFloat[T tfloat](d *ShapeDeserializer, v *T) error {
	n, err := d.readFloat64()
	if err != nil {
		return err
	}
	*v = T(n)
	return nil
}

// GetProtocolErrorInfo decodes error type/message from a CBOR response body.
func GetProtocolErrorInfo(p []byte) (typ, message string, err error) {
	v, decErr := smithycbor.Decode(p)
	if decErr != nil {
		return "", "", decErr
	}

	m, ok := v.(smithycbor.Map)
	if !ok {
		return "", "", nil
	}

	if t, ok := m["__type"]; ok {
		if s, ok := t.(smithycbor.String); ok {
			typ = string(s)
		}
	}
	if msg, ok := m["message"]; ok {
		if s, ok := msg.(smithycbor.String); ok {
			message = string(s)
		}
	}

	return typ, message, nil
}

// ReadBigInt is unimplemented and will return an error.
func (d *ShapeDeserializer) ReadBigInt(_ *smithy.Schema, _ *big.Int) error {
	return fmt.Errorf("unimplemented")
}

// ReadBigFloat is unimplemented and will return an error.
func (d *ShapeDeserializer) ReadBigFloat(_ *smithy.Schema, _ *big.Float) error {
	return fmt.Errorf("unimplemented")
}
