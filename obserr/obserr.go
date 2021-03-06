package obserr

import (
	"errors"
	"fmt"
)

// Error should be used as a drop-in replacement for Golang's native error type
// where adding key/value data or annotated info would provide useful context making
// debugging easier.
//
// This should be used in conjunction with go/src/obs/flight_recorder.go's Vals type
// where the actual telemetry/reporting happens.
type Error struct {
	orig, err error
	vals      map[string]interface{}
}

func New(e interface{}) *Error {
	var err error

	switch o := e.(type) {
	case string:
		err = errors.New(o)
	case *Error:
		return o
	case error:
		err = o
	default:
		err = fmt.Errorf("%v", o)
	}

	return &Error{
		orig: err,
		err:  err,
		vals: make(map[string]interface{}),
	}
}

func (e *Error) Error() string {
	return e.err.Error()
}

func (e *Error) Get(k string) interface{} {
	return e.vals[k]
}

func (e *Error) Set(kvs ...interface{}) *Error {
	for i := 0; i < len(kvs); i += 2 {
		e.vals[kvs[i].(string)] = kvs[i+1]
	}
	return e
}

func (e *Error) Vals() map[string]interface{} {
	return e.vals
}

func (e *Error) Annotate(ann interface{}) *Error {
	var a string

	switch o := ann.(type) {
	case string:
		a = o
	case *Error:
		a = o.err.Error()
	case error:
		a = o.Error()
	default:
		a = fmt.Sprintf("%v", o)
	}

	e.err = fmt.Errorf("%s: %s", a, e.err)
	return e
}

func Annotate(e error, an interface{}) *Error {
	return New(e).Annotate(an)
}

func Original(e error) error {
	if oe, ok := e.(*Error); ok {
		return oe.orig
	}
	return e
}
