package main

import (
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/amzn/ion-go/ion"
)

type eventwriter struct {
	enc *ion.Encoder

	depth       int
	fieldname   *string
	annotations []string
	inStruct    map[int]bool
}

// NewEventWriter creates an ion.Writer that writes out a sequence
// of ion-test-driver events.
func NewEventWriter(out io.Writer) ion.Writer {
	w := ion.NewTextWriter(out)
	w.WriteSymbol("$ion_event_stream")

	return &eventwriter{enc: ion.NewEncoder(w)}
}

func (e *eventwriter) FieldName(val string) error {
	e.fieldname = &val
	return nil
}

func (e *eventwriter) Annotation(val string) error {
	e.annotations = append(e.annotations, val)
	return nil
}

func (e *eventwriter) Annotations(vals ...string) error {
	e.annotations = append(e.annotations, vals...)
	return nil
}

func (e *eventwriter) WriteNull() error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.NullType),
		ValueText: "null",
	})
}

func (e *eventwriter) WriteNullType(val ion.Type) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(val),
		ValueText: "null." + val.String(),
	})
}

func (e *eventwriter) WriteBool(val bool) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.BoolType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) WriteInt(val int64) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.IntType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) WriteUint(val uint64) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.IntType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) WriteBigInt(val *big.Int) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.IntType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) WriteFloat(val float64) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.FloatType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) WriteDecimal(val *ion.Decimal) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.DecimalType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) WriteTimestamp(val time.Time) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.TimestampType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) WriteSymbol(val string) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.SymbolType),
		ValueText: symbolify(val),
	})
}

func (e *eventwriter) WriteString(val string) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.StringType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) WriteClob(val []byte) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.ClobType),
		ValueText: clobify(val),
	})
}

func (e *eventwriter) WriteBlob(val []byte) error {
	return e.write(event{
		EventType: scalar,
		IonType:   iontype(ion.BlobType),
		ValueText: stringify(val),
	})
}

func (e *eventwriter) BeginList() error {
	err := e.write(event{
		EventType: containerStart,
		IonType:   iontype(ion.ListType),
	})
	if err != nil {
		return err
	}
	e.depth++
	return nil
}

func (e *eventwriter) EndList() error {
	e.depth--
	return e.write(event{
		EventType: containerEnd,
		IonType:   iontype(ion.ListType),
	})
}

func (e *eventwriter) BeginSexp() error {
	err := e.write(event{
		EventType: containerStart,
		IonType:   iontype(ion.SexpType),
	})
	if err != nil {
		return err
	}
	e.depth++
	return nil
}

func (e *eventwriter) EndSexp() error {
	e.depth--
	return e.write(event{
		EventType: containerEnd,
		IonType:   iontype(ion.SexpType),
	})
}

func (e *eventwriter) BeginStruct() error {
	err := e.write(event{
		EventType: containerStart,
		IonType:   iontype(ion.StructType),
	})
	if err != nil {
		return err
	}
	e.depth++
	e.inStruct[e.depth] = true
	return nil
}

func (e *eventwriter) EndStruct() error {
	e.inStruct[e.depth] = false
	e.depth--
	return e.write(event{
		EventType: containerEnd,
		IonType:   iontype(ion.StructType),
	})
}

func (e *eventwriter) Finish() error {
	if err := e.write(event{EventType: streamEnd}); err != nil {
		return err
	}
	return e.enc.Finish()
}

func (e *eventwriter) IsInStruct() bool {
	return e.inStruct[e.depth] == true
}

func stringify(val interface{}) string {
	bs, err := ion.MarshalText(val)
	if err != nil {
		panic(err)
	}
	return string(bs)
}

func symbolify(val string) string {
	buf := strings.Builder{}
	w := ion.NewTextWriterOpts(&buf, ion.TextWriterQuietFinish)

	w.WriteSymbol(val)
	if err := w.Finish(); err != nil {
		panic(err)
	}

	return buf.String()
}

func clobify(val []byte) string {
	buf := strings.Builder{}
	w := ion.NewTextWriterOpts(&buf, ion.TextWriterQuietFinish)

	w.WriteClob(val)
	if err := w.Finish(); err != nil {
		panic(err)
	}

	return buf.String()
}

func (e *eventwriter) write(ev event) error {
	name := e.fieldname
	e.fieldname = nil
	annos := e.annotations
	e.annotations = nil

	if name != nil {
		ev.FieldName = &token{Text: *name}
	}

	if len(annos) > 0 {
		asyms := make([]token, len(annos))
		for i, a := range annos {
			asyms[i] = token{Text: a}
		}
		ev.Annotations = asyms
	}

	ev.Depth = e.depth

	return e.enc.Encode(ev)
}
