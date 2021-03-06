package ion

import (
	"bytes"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sort"
	"time"
)

// EncoderOpts holds bit-flag options for an Encoder.
type EncoderOpts uint

const (
	// EncodeSortMaps instructs the encoder to write map keys in sorted order.
	EncodeSortMaps EncoderOpts = 1
)

// Marshaler is the interface implemented by types that can marshal themselves to Ion.
type Marshaler interface {
	MarshalIon(w Writer) error
}

// MarshalText marshals values to text ion.
func MarshalText(v interface{}) ([]byte, error) {
	buf := bytes.Buffer{}
	w := NewTextWriterOpts(&buf, TextWriterQuietFinish)
	e := Encoder{
		w:    w,
		opts: EncodeSortMaps,
	}

	if err := e.Encode(v); err != nil {
		return nil, err
	}
	if err := e.Finish(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// MarshalBinary marshals values to binary ion.
func MarshalBinary(v interface{}, ssts ...SharedSymbolTable) ([]byte, error) {
	buf := bytes.Buffer{}
	w := NewBinaryWriter(&buf, ssts...)
	e := Encoder{w: w}

	if err := e.Encode(v); err != nil {
		return nil, err
	}
	if err := e.Finish(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// MarshalBinaryLST marshals values to binary ion with a fixed local symbol table.
func MarshalBinaryLST(v interface{}, lst SymbolTable) ([]byte, error) {
	buf := bytes.Buffer{}
	w := NewBinaryWriterLST(&buf, lst)
	e := Encoder{w: w}

	if err := e.Encode(v); err != nil {
		return nil, err
	}
	if err := e.Finish(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// MarshalTo marshals the given value to the given writer. It does
// not call Finish, so is suitable for encoding values inside of
// a partially-constructed Ion value.
func MarshalTo(w Writer, v interface{}) error {
	e := Encoder{
		w: w,
	}
	return e.Encode(v)
}

// An Encoder writes Ion values to an output stream.
type Encoder struct {
	w    Writer
	opts EncoderOpts
}

// NewEncoder creates a new encoder.
func NewEncoder(w Writer) *Encoder {
	return NewEncoderOpts(w, 0)
}

// NewEncoderOpts creates a new encoder with the specified options.
func NewEncoderOpts(w Writer, opts EncoderOpts) *Encoder {
	return &Encoder{
		w:    w,
		opts: opts,
	}
}

// NewTextEncoder creates a new text Encoder.
func NewTextEncoder(w io.Writer) *Encoder {
	return NewEncoder(NewTextWriter(w))
}

// NewBinaryEncoder creates a new binary Encoder.
func NewBinaryEncoder(w io.Writer, ssts ...SharedSymbolTable) *Encoder {
	return NewEncoder(NewBinaryWriter(w, ssts...))
}

// NewBinaryEncoderLST creates a new binary Encoder with a fixed local symbol table.
func NewBinaryEncoderLST(w io.Writer, lst SymbolTable) *Encoder {
	return NewEncoder(NewBinaryWriterLST(w, lst))
}

// Encode marshals the given value to Ion, writing it to the underlying writer.
func (m *Encoder) Encode(v interface{}) error {
	return m.encodeValue(reflect.ValueOf(v), NoType)
}

// EncodeAs marshals the given value to Ion with the given type hint. Use it to
// encode symbols, clobs, or sexps (which by default get encoded to strings, blobs,
// and lists respectively).
func (m *Encoder) EncodeAs(v interface{}, hint Type) error {
	return m.encodeValue(reflect.ValueOf(v), hint)
}

// Finish finishes writing the current Ion datagram.
func (m *Encoder) Finish() error {
	return m.w.Finish()
}

var marshalerType = reflect.TypeOf((*Marshaler)(nil)).Elem()

// EncodeValue recursively encodes a value.
func (m *Encoder) encodeValue(v reflect.Value, hint Type) error {
	if !v.IsValid() {
		return m.w.WriteNull()
	}

	t := v.Type()
	if t.Kind() != reflect.Ptr && v.CanAddr() && reflect.PtrTo(t).Implements(marshalerType) {
		return v.Addr().Interface().(Marshaler).MarshalIon(m.w)
	}
	if t.Implements(marshalerType) {
		return v.Interface().(Marshaler).MarshalIon(m.w)
	}

	switch t.Kind() {
	case reflect.Bool:
		return m.w.WriteBool(v.Bool())

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return m.w.WriteInt(v.Int())

	case reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return m.w.WriteInt(int64(v.Uint()))

	case reflect.Uint, reflect.Uint64, reflect.Uintptr:
		i := big.Int{}
		i.SetUint64(v.Uint())
		return m.w.WriteBigInt(&i)

	case reflect.Float32, reflect.Float64:
		return m.w.WriteFloat(v.Float())

	case reflect.String:
		if hint == SymbolType {
			return m.w.WriteSymbol(v.String())
		}
		return m.w.WriteString(v.String())

	case reflect.Interface, reflect.Ptr:
		return m.encodePtr(v, hint)

	case reflect.Struct:
		return m.encodeStruct(v)

	case reflect.Map:
		return m.encodeMap(v, hint)

	case reflect.Slice:
		return m.encodeSlice(v, hint)

	case reflect.Array:
		return m.encodeArray(v, hint)

	default:
		return fmt.Errorf("ion: unsupported type: %v", v.Type().String())
	}
}

// EncodePtr encodes an Ion null if the pointer is nil, and otherwise encodes the value that
// the pointer is pointing to.
func (m *Encoder) encodePtr(v reflect.Value, hint Type) error {
	if v.IsNil() {
		return m.w.WriteNull()
	}
	return m.encodeValue(v.Elem(), hint)
}

// EncodeMap encodes a map to the output writer as an Ion struct.
func (m *Encoder) encodeMap(v reflect.Value, hint Type) error {
	if v.IsNil() {
		return m.w.WriteNull()
	}

	m.w.BeginStruct()

	keys := keysFor(v)
	if m.opts&EncodeSortMaps != 0 {
		sort.Slice(keys, func(i, j int) bool { return keys[i].s < keys[j].s })
	}

	for _, key := range keys {
		m.w.FieldName(key.s)
		value := v.MapIndex(key.v)
		if err := m.encodeValue(value, hint); err != nil {
			return err
		}
	}

	return m.w.EndStruct()
}

// A mapkey holds the reflective map key value as well as its stringified form.
type mapkey struct {
	v reflect.Value
	s string
}

// KeysFor returns the stringified keys for the given map.
func keysFor(v reflect.Value) []mapkey {
	keys := v.MapKeys()
	res := make([]mapkey, len(keys))

	for i, key := range keys {
		// TODO: Handle other kinds of keys.
		if key.Kind() != reflect.String {
			panic("unexpected map key type")
		}
		res[i] = mapkey{
			v: key,
			s: key.String(),
		}
	}

	return res
}

// EncodeSlice encodes a slice to the output writer as an appropriate Ion type.
func (m *Encoder) encodeSlice(v reflect.Value, hint Type) error {
	elem := v.Type().Elem()
	if elem.Kind() == reflect.Uint8 && !elem.Implements(marshalerType) {
		return m.encodeBlob(v, hint)
	}

	if v.IsNil() {
		return m.w.WriteNull()
	}

	return m.encodeArray(v, hint)
}

// EncodeBlob encodes a []byte to the output writer as an Ion blob.
func (m *Encoder) encodeBlob(v reflect.Value, hint Type) error {
	if v.IsNil() {
		return m.w.WriteNull()
	}
	if hint == ClobType {
		return m.w.WriteClob(v.Bytes())
	}
	return m.w.WriteBlob(v.Bytes())
}

// EncodeArray encodes an array to the output writer as an Ion list (or sexp).
func (m *Encoder) encodeArray(v reflect.Value, hint Type) error {
	if hint == SexpType {
		m.w.BeginSexp()
	} else {
		m.w.BeginList()
	}

	for i := 0; i < v.Len(); i++ {
		if err := m.encodeValue(v.Index(i), hint); err != nil {
			return err
		}
	}

	if hint == SexpType {
		return m.w.EndSexp()
	}
	return m.w.EndList()
}

// EncodeStruct encodes a struct to the output writer as an Ion struct.
func (m *Encoder) encodeStruct(v reflect.Value) error {
	fields := fieldsFor(v.Type())
	for _, field := range fields {
		if field.annotations {
			return m.encodeWithAnnotation(v)
		}
	}

	t := v.Type()
	if t == timeType {
		return m.encodeTime(v)
	}
	if t == decimalType {
		return m.encodeDecimal(v)
	}

	if err := m.w.BeginStruct(); err != nil {
		return err
	}

FieldLoop:
	for i := range fields {
		f := &fields[i]

		fv := v
		for _, i := range f.path {
			if fv.Kind() == reflect.Ptr {
				if fv.IsNil() {
					continue FieldLoop
				}
				fv = fv.Elem()
			}
			fv = fv.Field(i)
		}

		if f.omitEmpty && emptyValue(fv) {
			continue
		}

		m.w.FieldName(f.name)
		if err := m.encodeValue(fv, f.hint); err != nil {
			return err
		}
	}

	return m.w.EndStruct()
}

// EncodeTime encodes a time.Time to the output writer as an Ion timestamp.
func (m *Encoder) encodeTime(v reflect.Value) error {
	t := v.Interface().(time.Time)
	return m.w.WriteTimestamp(t)
}

// EncodeDecimal encodes an ion.Decimal to the output writer as an Ion decimal.
func (m *Encoder) encodeDecimal(v reflect.Value) error {
	d := v.Addr().Interface().(*Decimal)
	return m.w.WriteDecimal(d)
}

func (m *Encoder) encodeWithAnnotation(v reflect.Value) error {
	original := v
	fields := fieldsFor(v.Type())
	for _, field := range fields {
		if field.annotations {
			annotations, err := findSubvalue(original, &field)
			if err != nil {
				return err
			}
			if annotations.Kind() != reflect.Slice {
				return fmt.Errorf("ion: '%v' is provided for annotations,"+
					"it must be of type []string", annotations.Kind())
			}
			listOfAnnotations := annotations.Interface().([]string)
			err = m.w.Annotations(listOfAnnotations...)
			if err != nil {
				return err
			}
		} else {
			v, _ = findSubvalue(original, &field)
		}
	}
	return m.encodeValue(v, NoType)
}

// EmptyValue returns true if the given value is the empty value for its type.
func emptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
}
