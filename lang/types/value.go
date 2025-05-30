// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// Additional permission under GNU GPL version 3 section 7
//
// If you modify this program, or any covered work, by linking or combining it
// with embedded mcl code and modules (and that the embedded mcl code and
// modules which link with this program, contain a copy of their source code in
// the authoritative form) containing parts covered by the terms of any other
// license, the licensors of this program grant you additional permission to
// convey the resulting work. Furthermore, the licensors of this program grant
// the original author, James Shubin, additional permission to update this
// additional permission if he deems it necessary to achieve the goals of this
// additional permission.

package types

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/purpleidea/mgmt/util/errwrap"
)

var (
	// ErrNilValue is returned when ValueOf() attempts to represent a nil
	// pointer as an mcl value. This is not supported in mcl.
	ErrNilValue = errors.New("cannot represent a nil golang value in mcl")

	// ErrInvalidValue is returned when ValueOf() is called on an invalid or
	// zero reflect.Value.
	ErrInvalidValue = errors.New("cannot represent invalid reflect.Value")

	// ValueFalse is a false value in our system. Can be used where needed.
	ValueFalse, _ = ValueOfGolang(false)

	// ValueTrue is a true value in our system. Can be used where needed.
	ValueTrue, _ = ValueOfGolang(true)
)

// Value represents an interface to get values out of each type. It is similar
// to the reflection interfaces used in the golang standard library.
type Value interface {
	fmt.Stringer // String() string (for display purposes)
	Type() *Type
	Less(Value) bool // to find the smaller of the two values (for sort)
	Cmp(Value) error // error if the two values aren't the same
	Copy() Value     // returns a copy of this value
	Value() interface{}
	Bool() bool
	Str() string
	Int() int64
	Float() float64
	List() []Value
	Map() map[Value]Value // keys must all have same type, same for values
	Struct() map[string]Value
	Func() interface{} // func(interfaces.Txn, []interfaces.Func) (interfaces.Func, error)
}

// ValueOfGolang is a helper that takes a golang value, and produces the mcl
// equivalent internal representation. This is very useful for writing tests. A
// reminder that if you pass in a nil value, or something containing a nil
// value, then you won't get what you want. See our documentation for ValueOf.
func ValueOfGolang(i interface{}) (Value, error) {
	return ValueOf(reflect.ValueOf(i))
}

// ValueOf takes a reflect.Value and returns an equivalent Value. Remember that
// the mcl type system currently can't represent certain values that *are*
// possible in golang. This is intentional. For example, mcl can't represent a
// *string (pointer to a string) where as this is quite common in golang. This
// is because mcl has no `nil/null` values. It is designed this way to avoid the
// well-known expensive "null-pointer-exception" style bugs. A version two of
// the language might consider an "Optional" type. In the meantime, you can
// still represent an "undefined" value, but only so far as when it's passed to
// a resource field. This is done with our "elvis" operator. When using this
// function, if you pass in something with a nil value, then expect a panic or
// an error if you're lucky.
func ValueOf(v reflect.Value) (Value, error) {
	// Gracefully handle invalid values instead of panic(). Invalid
	// reflect.Value values can come from nil values, or the zero value.
	if !v.IsValid() {
		return nil, ErrInvalidValue
	}

	value := v
	typ := value.Type()
	kind := typ.Kind()
	for kind == reflect.Ptr {
		// Prevent panic() if value is a nil pointer and return an error.
		if value.IsNil() {
			return nil, ErrNilValue
		}

		typ = typ.Elem() // un-nest one pointer
		kind = typ.Kind()

		// un-nest value from pointer
		value = value.Elem() // XXX: is this correct?
	}

	// Special cases:
	if value.CanInterface() {
		if v, ok := (value.Interface()).(net.HardwareAddr); ok {
			return &StrValue{V: v.String()}, nil
		}
	}
	// TODO: net/url.URL, time.Duration, etc. Note: avoid net/mail.Address

	switch kind { // match on destination field kind
	case reflect.Bool:
		return &BoolValue{V: value.Bool()}, nil

	case reflect.String:
		return &StrValue{V: value.String()}, nil

	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Int16, reflect.Int8:
		return &IntValue{V: value.Int()}, nil

	case reflect.Uint, reflect.Uint64, reflect.Uint32, reflect.Uint16, reflect.Uint8:
		return &IntValue{V: int64(value.Uint())}, nil

	case reflect.Float64, reflect.Float32:
		return &FloatValue{V: value.Float()}, nil

	case reflect.Array, reflect.Slice:
		values := []Value{}
		for i := 0; i < value.Len(); i++ {
			x := value.Index(i)
			v, err := ValueOf(x) // recurse
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}

		t, err := TypeOf(value.Type().Elem()) // type of contents
		if err != nil {
			return nil, errwrap.Wrapf(err, "can't determine type of %+v", value)
		}

		return &ListValue{
			T: NewType(fmt.Sprintf("[]%s", t.String())),
			V: values,
		}, nil

	case reflect.Map:
		m := make(map[Value]Value)

		// loop through the list of map keys in undefined order
		for _, mk := range value.MapKeys() {
			mv := value.MapIndex(mk)

			k, err := ValueOf(mk) // recurse
			if err != nil {
				return nil, err
			}
			v, err := ValueOf(mv) // recurse
			if err != nil {
				return nil, err
			}

			m[k] = v
		}

		kt, err := TypeOf(value.Type().Key()) // type of key
		if err != nil {
			return nil, errwrap.Wrapf(err, "can't determine key type of %+v", value)
		}
		vt, err := TypeOf(value.Type().Elem()) // type of value
		if err != nil {
			return nil, errwrap.Wrapf(err, "can't determine value type of %+v", value)
		}

		return &MapValue{
			T: NewType(fmt.Sprintf("map{%s: %s}", kt.String(), vt.String())),
			V: m,
		}, nil

	case reflect.Struct:
		// TODO: we could take this simpler "get the full type" approach
		// for all the values, but I think that building them up when
		// possible for the other cases is a more robust approach!
		t, err := TypeOf(value.Type())
		if err != nil {
			return nil, errwrap.Wrapf(err, "can't determine type of %+v", value)
		}
		l := value.NumField() // number of struct fields according to value

		if l != len(t.Ord) {
			// programming error?
			return nil, fmt.Errorf("incompatible number of fields")
		}

		values := make(map[string]Value)
		for i := 0; i < l; i++ {
			x := value.Field(i)
			v, err := ValueOf(x) // recurse
			if err != nil {
				return nil, err
			}
			name := t.Ord[i] // how else can we get the field name?
			values[name] = v
		}

		return &StructValue{
			T: t,
			V: values,
		}, nil

	case reflect.Func:
		t, err := TypeOf(value.Type())
		if err != nil {
			return nil, errwrap.Wrapf(err, "can't determine type of %+v", value)
		}
		if t.Out == nil {
			return nil, fmt.Errorf("cannot only represent functions with one output value")
		}

		f := func(ctx context.Context, args []Value) (Value, error) {
			in := []reflect.Value{}
			for _, x := range args {
				// TODO: should we build this method instead?
				//v := x.Reflect() // types.Value -> reflect.Value
				v := reflect.ValueOf(x.Value())
				in = append(in, v)
			}

			// FIXME: can we pass in ctx ?
			// FIXME: can we trap panic's ?
			out := value.Call(in) // []reflect.Value
			if len(out) != 1 {    // TODO: panic, b/c already checked in TypeOf?
				return nil, fmt.Errorf("cannot only represent functions with one output value")
			}

			return ValueOf(out[0]) // recurse
		}

		return &FuncValue{
			T: t,
			V: f,
		}, nil

	// TODO: should this return a variant value?
	// TODO: add this into ConfigurableValueOf like ConfigurableTypeOf ?
	case reflect.Interface:
		opts := []TypeOfOption{
			//StructTagOpt(StructTag),
			//StrictStructTagOpt(false),
			//SkipBadStructFieldsOpt(false),
			AllowInterfaceTypeOpt(true),
		}
		t, err := ConfigurableTypeOf(value.Type(), opts...)
		//t, err := TypeOf(value.Type())
		if err != nil {
			return nil, errwrap.Wrapf(err, "can't determine type of %+v", value)
		}

		v, err := ValueOf(value.Elem()) // recurse
		if err != nil {
			return nil, errwrap.Wrapf(err, "can't determine value of %+v", value)
		}

		return &VariantValue{
			T: t,
			V: v,
		}, nil

	default:
		return nil, fmt.Errorf("unable to represent value of %+v which has kind: %v", v, kind)
	}
}

// Into mutates the given reflect.Value with the data represented by the Value.
//
// Container types like map/list (and to a certain extent structs) will be
// cleared before adding the contained data such that the existing data doesn't
// affect the outcome, and the output reflect.Value directly maps to the input
// Value.
//
// In almost every case, it is likely that the reflect.Value will be modified,
// instantiating nil pointers and even potentially partially filling data before
// returning an error. It should be assumed that if this returns an error, the
// reflect.Value passed in has been trashed and should be discarded before
// reuse.
func Into(v Value, rv reflect.Value) error {
	typ := rv.Type()
	kind := typ.Kind()
	for kind == reflect.Ptr {
		typ = typ.Elem() // un-nest one pointer
		kind = typ.Kind()

		// if pointer was nil, instantiate the destination type and point
		// at it to prevent nil pointer dereference when setting values
		if rv.IsNil() {
			rv.Set(reflect.New(typ))
		}
		rv = rv.Elem() // un-nest rv from pointer
	}
	if !rv.CanSet() {
		return fmt.Errorf("can't set value, is it unexported?")
	}

	// capture rv and v in a closure that is static for the scope of this Into() call
	// mustInto ensures rv is in a list of compatible types before attempting to reflect it
	mustInto := func(kinds ...reflect.Kind) error {
		// sigh. Go can be so elegant, and then it makes you do this
		for _, n := range kinds {
			if kind == n {
				return nil
			}
		}
		// No matching kind found, must be an incompatible conversion
		return fmt.Errorf("cannot Into() %+v of type %s into %s", v, v.Type(), typ)
	}

	if typ == nil {
		return fmt.Errorf("cannot Into() %+v of type %s into a nil type", v, v.Type())
	}
	// This is used when we are setting a resource field which has type of
	// interface{} instead of a string, bool, list, etc...
	if isInterface := typ.Kind() == reflect.Interface; isInterface {
		//x := reflect.ValueOf(v) // no!
		// use the value with type interface{}, not types.Value
		x := reflect.ValueOf(v.Value())
		rv.Set(x)
		return nil
	}

	switch v := v.(type) {
	case *BoolValue:
		if err := mustInto(reflect.Bool); err != nil {
			return err
		}

		rv.SetBool(v.V)
		return nil

	case *StrValue:
		if err := mustInto(reflect.String); err != nil {
			return err
		}

		rv.SetString(v.V)
		return nil

	case *IntValue:
		// overflow check
		switch kind { // match on destination field kind
		case reflect.Int, reflect.Int64, reflect.Int32, reflect.Int16, reflect.Int8:
			ff := reflect.Zero(typ)  // test on a non-ptr equivalent
			if ff.OverflowInt(v.V) { // this is valid!
				return fmt.Errorf("%+v is an `%s`, and rv `%d` will overflow it", rv.Interface(), rv.Kind(), v.V)
			}
			rv.SetInt(v.V)
			return nil

		case reflect.Uint, reflect.Uint64, reflect.Uint32, reflect.Uint16, reflect.Uint8:
			ff := reflect.Zero(typ)
			if ff.OverflowUint(uint64(v.V)) { // TODO: is this correct?
				return fmt.Errorf("%+v is an `%s`, and rv `%d` will overflow it", rv.Interface(), rv.Kind(), v.V)
			}
			rv.SetUint(uint64(v.V))
			return nil
		default:
			return fmt.Errorf("cannot Into() %+v of type %s into %s", v, v.Type(), typ)
		}

	case *FloatValue:
		if err := mustInto(reflect.Float32, reflect.Float64); err != nil {
			return err
		}

		ff := reflect.Zero(typ)
		if ff.OverflowFloat(v.V) {
			return fmt.Errorf("%+v is an `%s`, and value `%f` will overflow it", rv.Interface(), rv.Kind(), v.V)
		}
		rv.SetFloat(v.V)
		return nil

	case *ListValue:
		count := len(v.V)

		switch kind {
		case reflect.Slice:
			pow := nextPowerOfTwo(uint(count))
			nval := reflect.MakeSlice(rv.Type(), count, int(pow))
			rv.Set(nval)

		case reflect.Array:
			if count > rv.Len() {
				return fmt.Errorf("%+v is too small for %+v", typ, v)
			}
			rv.Set(reflect.New(typ).Elem())

		default:
			return mustInto() // nothing, always returns err
		}

		for i, x := range v.V {
			f := rv.Index(i)
			el := reflect.New(f.Type()).Elem()
			if err := Into(x, el); err != nil { // recurse
				return err
			}
			f.Set(el)
		}
		return nil

	case *MapValue:
		if err := mustInto(reflect.Map); err != nil {
			return err
		}

		rv.Set(reflect.MakeMapWithSize(typ, len(v.V)))

		// convert both key and value, then set them in the map
		for mk, mv := range v.V {
			key := reflect.New(typ.Key()).Elem()
			if err := Into(mk, key); err != nil { // recurse
				return err
			}
			val := reflect.New(typ.Elem()).Elem()
			if err := Into(mv, val); err != nil { // recurse
				return err
			}
			rv.SetMapIndex(key, val)
		}
		return nil

	case *StructValue:
		if err := mustInto(reflect.Struct); err != nil {
			return err
		}

		// Into sets the value of the given reflect.Value to the value of this obj
		mapping, err := TypeStructTagToFieldName(typ)
		if err != nil {
			return err
		}

		keys := []string{}
		for k := range v.T.Map {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys { // loop in deterministic order
			mk := k
			// map mcl field name -> go field name based on `lang:""` tag
			if key, exists := mapping[k]; exists {
				mk = key
			}
			field := rv.FieldByName(mk)
			if err := Into(v.V[k], field); err != nil { // recurse
				return err
			}
		}
		return nil

	//case *FuncValue:
	//	if err := mustInto(reflect.Func); err != nil {
	//		return err
	//	}
	//
	//	// wrap our function with the translation that is necessary
	//	fn := func(args []reflect.Value) (results []reflect.Value) { // build
	//		innerArgs := []Value{}
	//		for _, x := range args {
	//			v, err := ValueOf(x) // reflect.Value -> Value
	//			if err != nil {
	//				panic(fmt.Errorf("can't determine value of %+v", x))
	//			}
	//			innerArgs = append(innerArgs, v)
	//		}
	//		result, err := v.V(innerArgs) // call it
	//		if err != nil {
	//			// when calling our function with the Call method, then
	//			// we get the error output and have a chance to decide
	//			// what to do with it, but when calling it from within
	//			// a normal golang function call, the error represents
	//			// that something went horribly wrong, aka a panic...
	//			panic(fmt.Errorf("function panic: %+v", err))
	//		}
	//		out := reflect.New(rv.Type().Out(0))
	//		// convert the lang result back to a Go value
	//		if err := Into(result, out); err != nil {
	//			panic(fmt.Errorf("function return conversion panic: %+v", err))
	//		}
	//		return []reflect.Value{out} // only one result
	//	}
	//	rv.Set(reflect.MakeFunc(rv.Type(), fn))
	//	return nil

	case *VariantValue:
		return Into(v.V, rv)

	default:
		return fmt.Errorf("cannot Into() %+v of type (%T) %s into %s", v, v, v.Type(), typ)
	}
}

// ValueSlice is a linear list of values. It is used for sorting purposes.
type ValueSlice []Value

func (vs ValueSlice) Len() int           { return len(vs) }
func (vs ValueSlice) Swap(i, j int)      { vs[i], vs[j] = vs[j], vs[i] }
func (vs ValueSlice) Less(i, j int) bool { return vs[i].Less(vs[j]) }

// Base implements the missing methods that all types need.
type Base struct{}

// Bool represents the value of this type as a bool if it is one. If this is not
// a bool, then this panics.
func (obj *Base) Bool() bool {
	panic("not a bool")
}

// Str represents the value of this type as a string if it is one. If this is
// not a string, then this panics.
func (obj *Base) Str() string {
	panic("not an str") // yes, i think this is the correct grammar
}

// Int represents the value of this type as an integer if it is one. If this is
// not an integer, then this panics.
func (obj *Base) Int() int64 {
	panic("not an int")
}

// Float represents the value of this type as a float if it is one. If this is
// not a float, then this panics.
func (obj *Base) Float() float64 {
	panic("not a float")
}

// List represents the value of this type as a list if it is one. If this is not
// a list, then this panics.
func (obj *Base) List() []Value {
	panic("not a list")
}

// Map represents the value of this type as a dictionary if it is one. If this
// is not a map, then this panics.
func (obj *Base) Map() map[Value]Value {
	panic("not a list")
}

// Struct represents the value of this type as a struct if it is one. If this is
// not a struct, then this panics.
func (obj *Base) Struct() map[string]Value {
	panic("not a struct")
}

// Func represents the value of this type as a function if it is one. If this is
// not a function, then this panics.
func (obj *Base) Func() interface{} {
	panic("not a func")
}

// Less compares to value and returns true if we're smaller. It is recommended
// that this base implementation of the method be replaced in the specific type.
// This *may* panic if the two types aren't the same.
// NOTE: this can be used as an example template to write your own function.
//func (obj *Base) Less(v Value) bool {
//	// TODO: cheap less, be smarter in each type eg: int's should cmp as int
//	return obj.String() < v.String()
//}

// Cmp returns an error if this value isn't the same as the arg passed in. This
// implementation uses the base Less implementation and should be replaced. It
// is always nice to implement this properly so that we get better error output.
// NOTE: this can be used as an example template to write your own function.
//func (obj *Base) Cmp(v Value) error {
//	// if they're both true or both false, then they must be the same,
//	// because we expect that if x < & && y < x then x == y
//	if obj.Less(v) != v.Less(obj) {
//		return fmt.Errorf("values differ according to less")
//	}
//	return nil
//}

// BoolValue represents a boolean value.
type BoolValue struct {
	Base
	V bool
}

// NewBool creates a new boolean value.
func NewBool() *BoolValue { return &BoolValue{} }

// String returns a visual representation of this value.
func (obj *BoolValue) String() string {
	return strconv.FormatBool(obj.V) // true or false
	//if obj.V {
	//	return "true"
	//}
	//return "false"
}

// Type returns the type data structure that represents this type.
func (obj *BoolValue) Type() *Type { return NewType("bool") }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same.
func (obj *BoolValue) Less(v Value) bool {
	//return obj.String() < v.(*BoolValue).String()
	if obj.V != v.(*BoolValue).V { // there must be one false
		// f, t -> t ; t, f -> f
		return !obj.V // TODO: should `false` sort less?
	}
	return false // they're the same
}

// Cmp returns an error if this value isn't the same as the arg passed in.
func (obj *BoolValue) Cmp(val Value) error {
	if obj == nil || val == nil {
		return fmt.Errorf("cannot cmp to nil")
	}
	if err := obj.Type().Cmp(val.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp types")
	}

	if obj.V != val.(*BoolValue).V {
		return fmt.Errorf("values are different")
	}
	return nil
}

// Copy returns a copy of this value.
func (obj *BoolValue) Copy() Value {
	return &BoolValue{V: obj.V}
}

// Value returns the raw value of this type.
func (obj *BoolValue) Value() interface{} {
	return obj.V
}

// Bool represents the value of this type as a bool if it is one. If this is not
// a bool, then this panics.
func (obj *BoolValue) Bool() bool {
	return obj.V
}

// StrValue represents a string value.
type StrValue struct {
	Base
	V string
}

// NewStr creates a new string value.
func NewStr() *StrValue { return &StrValue{} }

// String returns a visual representation of this value.
func (obj *StrValue) String() string {
	return strconv.Quote(obj.V) // wraps in quotes, turns tabs into \t etc...
	//return fmt.Sprintf(`"%s"`, obj.V)
}

// Type returns the type data structure that represents this type.
func (obj *StrValue) Type() *Type { return NewType("str") }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same.
func (obj *StrValue) Less(v Value) bool {
	return obj.V < v.(*StrValue).V
}

// Cmp returns an error if this value isn't the same as the arg passed in.
func (obj *StrValue) Cmp(val Value) error {
	if obj == nil || val == nil {
		return fmt.Errorf("cannot cmp to nil")
	}
	if err := obj.Type().Cmp(val.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp types")
	}

	if obj.V != val.(*StrValue).V {
		return fmt.Errorf("values are different")
	}
	return nil
}

// Copy returns a copy of this value.
func (obj *StrValue) Copy() Value {
	return &StrValue{V: obj.V}
}

// Value returns the raw value of this type.
func (obj *StrValue) Value() interface{} {
	return obj.V
}

// Str represents the value of this type as a string if it is one. If this is
// not a string, then this panics.
func (obj *StrValue) Str() string {
	return obj.V
}

// IntValue represents an integer value.
type IntValue struct {
	Base
	V int64
}

// NewInt creates a new int value.
func NewInt() *IntValue { return &IntValue{} }

// String returns a visual representation of this value.
func (obj *IntValue) String() string {
	return strconv.FormatInt(obj.V, 10)
}

// Type returns the type data structure that represents this type.
func (obj *IntValue) Type() *Type { return NewType("int") }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same.
func (obj *IntValue) Less(v Value) bool {
	return obj.V < v.(*IntValue).V
}

// Cmp returns an error if this value isn't the same as the arg passed in.
func (obj *IntValue) Cmp(val Value) error {
	if obj == nil || val == nil {
		return fmt.Errorf("cannot cmp to nil")
	}
	if err := obj.Type().Cmp(val.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp types")
	}

	if obj.V != val.(*IntValue).V {
		return fmt.Errorf("values are different")
	}
	return nil
}

// Copy returns a copy of this value.
func (obj *IntValue) Copy() Value {
	return &IntValue{V: obj.V}
}

// Value returns the raw value of this type.
func (obj *IntValue) Value() interface{} {
	return obj.V
}

// Int represents the value of this type as an integer if it is one. If this is
// not an integer, then this panics.
func (obj *IntValue) Int() int64 {
	return obj.V
}

// FloatValue represents an integer value.
type FloatValue struct {
	Base
	V float64
}

// NewFloat creates a new float value.
func NewFloat() *FloatValue { return &FloatValue{} }

// String returns a visual representation of this value.
func (obj *FloatValue) String() string {
	// TODO: is this the right display mode?
	// FIXME: floats don't print nicely: https://github.com/golang/go/issues/46118
	return strconv.FormatFloat(obj.V, 'f', -1, 64) // -1 for exact precision
}

// Type returns the type data structure that represents this type.
func (obj *FloatValue) Type() *Type { return NewType("float") }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same.
func (obj *FloatValue) Less(v Value) bool {
	return obj.V < v.(*FloatValue).V
}

// Cmp returns an error if this value isn't the same as the arg passed in.
func (obj *FloatValue) Cmp(val Value) error {
	if obj == nil || val == nil {
		return fmt.Errorf("cannot cmp to nil")
	}
	if err := obj.Type().Cmp(val.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp types")
	}

	// FIXME: should we compare with an epsilon?
	if obj.V != val.(*FloatValue).V {
		return fmt.Errorf("values are different")
	}
	return nil
}

// Copy returns a copy of this value.
func (obj *FloatValue) Copy() Value {
	return &FloatValue{V: obj.V}
}

// Value returns the raw value of this type.
func (obj *FloatValue) Value() interface{} {
	return obj.V
}

// Float represents the value of this type as a float if it is one. If this is
// not a float, then this panics.
func (obj *FloatValue) Float() float64 {
	return obj.V
}

// ListValue represents a list value.
type ListValue struct {
	Base
	V []Value // all elements must have type T.Val
	T *Type
}

// NewList creates a new list with the specified list type.
func NewList(t *Type) *ListValue {
	if t.Kind != KindList {
		return nil // sanity check
	}
	return &ListValue{
		V: []Value{},
		T: t,
	}
}

// String returns a visual representation of this value.
func (obj *ListValue) String() string {
	var s []string
	for _, x := range obj.V {
		s = append(s, x.String())
	}
	return fmt.Sprintf("[%s]", strings.Join(s, ", "))
}

// Type returns the type data structure that represents this type.
func (obj *ListValue) Type() *Type { return obj.T }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same.
func (obj *ListValue) Less(v Value) bool {
	V := v.(*ListValue).V
	i, j := len(obj.V), len(V)

	for x := 0; x < i && x < j; x++ { // keep to min count of both lists
		if obj.V[x].Less(V[x]) {
			return true
		}
	}

	return i < j // TODO: i think this is correct :)
}

// Cmp returns an error if this value isn't the same as the arg passed in.
func (obj *ListValue) Cmp(val Value) error {
	if obj == nil || val == nil {
		return fmt.Errorf("cannot cmp to nil")
	}
	if err := obj.Type().Cmp(val.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp types")
	}

	cmp := val.(*ListValue)

	if len(obj.V) != len(cmp.V) {
		return fmt.Errorf("lists have different lengths")
	}

	for i := range obj.V {
		if err := obj.V[i].Cmp(cmp.V[i]); err != nil {
			return errwrap.Wrapf(err, "index %d did not cmp", i)
		}
	}

	return nil
}

// Copy returns a copy of this value.
func (obj *ListValue) Copy() Value {
	v := []Value{}
	for _, x := range obj.V {
		v = append(v, x.Copy())
	}
	return &ListValue{
		V: v,
		T: obj.T.Copy(),
	}
}

// Value returns the raw value of this type.
func (obj *ListValue) Value() interface{} {
	typ := obj.T.Reflect()
	// create an empty slice (of len=0) with room for cap=len(obj.V) elements
	val := reflect.MakeSlice(typ, 0, len(obj.V))

	for _, x := range obj.V {
		val = reflect.Append(val, reflect.ValueOf(x.Value())) // recurse
	}
	return val.Interface()
}

// List represents the value of this type as a list if it is one. If this is not
// a list, then this panics.
func (obj *ListValue) List() []Value {
	return obj.V
}

// Len returns the number of elements in this list.
func (obj *ListValue) Len() int {
	return len(obj.V)
}

// Add adds an element to this list. It errors if the type does not match.
func (obj *ListValue) Add(v Value) error {
	if obj.T.Val.Kind != KindVariant { // skip cmp if dest is a variant
		if err := obj.T.Val.Cmp(v.Type()); err != nil {
			return errwrap.Wrapf(err, "value does not match list element type")
		}
	}

	obj.V = append(obj.V, v)
	return nil
}

// Lookup looks up a value by index. On success it also returns the Value.
func (obj *ListValue) Lookup(index int) (value Value, exists bool) {
	if index >= 0 && index < len(obj.V) {
		return obj.V[index], true // found
	}
	return nil, false
}

// Contains searches for a value in the list. On success it returns the index.
func (obj *ListValue) Contains(v Value) (index int, exists bool) {
	for i, x := range obj.V {
		if v.Cmp(x) == nil {
			return i, true
		}
	}
	return -1, false
}

// MapValue represents a dictionary value.
type MapValue struct {
	Base
	// the types of all keys and values are represented inside of T
	V map[Value]Value
	T *Type
}

// NewMap creates a new map with the specified map type.
func NewMap(t *Type) *MapValue {
	if t.Kind != KindMap {
		return nil // sanity check
	}
	return &MapValue{
		V: make(map[Value]Value),
		T: t,
	}
}

// String returns a visual representation of this value.
func (obj *MapValue) String() string {
	keys := []Value{}
	for k := range obj.V {
		keys = append(keys, k)
	}
	sort.Sort(ValueSlice(keys)) // deterministic print order

	var s []string
	for _, k := range keys {
		s = append(s, fmt.Sprintf("%s: %s", k.String(), obj.V[k].String()))
	}
	return fmt.Sprintf("{%s}", strings.Join(s, ", "))
}

// Type returns the type data structure that represents this type.
func (obj *MapValue) Type() *Type { return obj.T }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same.
func (obj *MapValue) Less(v Value) bool {
	V := v.(*MapValue)
	return obj.String() < V.String() // FIXME: implement a proper less func
}

// Cmp returns an error if this value isn't the same as the arg passed in.
func (obj *MapValue) Cmp(val Value) error {
	if obj == nil || val == nil {
		return fmt.Errorf("cannot cmp to nil")
	}
	if err := obj.Type().Cmp(val.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp types")
	}

	cmp := val.(*MapValue)

	if len(obj.V) != len(cmp.V) {
		return fmt.Errorf("maps have different lengths")
	}

	for k := range obj.V {
		v, exists := cmp.V[k]
		if !exists {
			return fmt.Errorf("key %s does not exist", k)
		}

		if err := obj.V[k].Cmp(v); err != nil {
			return errwrap.Wrapf(err, "index %s did not cmp", k)
		}
	}

	return nil
}

// Copy returns a copy of this value.
func (obj *MapValue) Copy() Value {
	m := map[Value]Value{}
	for k, v := range obj.V {
		m[k.Copy()] = v.Copy()
	}
	return &MapValue{
		V: m,
		T: obj.T.Copy(),
	}
}

// Value returns the raw value of this type.
func (obj *MapValue) Value() interface{} {
	typ := obj.T.Reflect()
	val := reflect.MakeMap(typ)

	for k, v := range obj.V {
		val.SetMapIndex(reflect.ValueOf(k.Value()), reflect.ValueOf(v.Value())) // dual recurse
	}
	return val.Interface()
}

// Map represents the value of this type as a dictionary if it is one. If this
// is not a map, then this panics.
func (obj *MapValue) Map() map[Value]Value {
	return obj.V
}

// Len returns the number of elements in this map.
func (obj *MapValue) Len() int {
	return len(obj.V)
}

// Add adds an element to this map. It errors if the types do not match.
func (obj *MapValue) Add(k, v Value) error { // TODO: change method name?

	//if obj.T.Key.Kind != KindVariant {
	if err := obj.T.Key.Cmp(k.Type()); err != nil {
		return errwrap.Wrapf(err, "key does not match map key type")
	}
	//}

	if obj.T.Val.Kind != KindVariant { // skip cmp if dest is a variant
		if err := obj.T.Val.Cmp(v.Type()); err != nil {
			return errwrap.Wrapf(err, "val does not match map val type")
		}
	}

	obj.V[k] = v
	return nil
}

// Lookup searches the map for a key. On success it also returns the Value.
func (obj *MapValue) Lookup(key Value) (value Value, exists bool) {
	//v, exists := obj.V[key] // not what we want!
	for k, v := range obj.V {
		if k.Cmp(key) == nil {
			return v, true // found
		}
	}
	return nil, false
}

// StructValue represents a struct value. The keys are ordered.
// TODO: if all functions require arg names to call, we don't need to order!
type StructValue struct {
	Base
	V map[string]Value // each field can have a different type
	T *Type            // contains ordered field types
}

// NewStruct creates a new struct with the specified field types.
func NewStruct(t *Type) *StructValue {
	if t.Kind != KindStruct {
		return nil // sanity check
	}
	v := make(map[string]Value)
	for _, k := range t.Ord {
		v[k] = t.Map[k].New() // don't leave struct fields uninitialized
	}
	return &StructValue{
		V: v,
		T: t, // TODO: should we allow changes to this after create?
	}
}

// String returns a visual representation of this value.
func (obj *StructValue) String() string {
	var s []string
	for _, k := range obj.T.Ord {
		s = append(s, fmt.Sprintf("%s: %s", k, obj.V[k].String()))
	}
	return fmt.Sprintf("struct{%s}", strings.Join(s, "; "))
}

// Type returns the type data structure that represents this type.
func (obj *StructValue) Type() *Type { return obj.T }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same.
func (obj *StructValue) Less(v Value) bool {
	V := v.(*StructValue)
	return obj.String() < V.String() // FIXME: implement a proper less func
}

// Cmp returns an error if this value isn't the same as the arg passed in.
func (obj *StructValue) Cmp(val Value) error {
	if obj == nil || val == nil {
		return fmt.Errorf("cannot cmp to nil")
	}
	if err := obj.Type().Cmp(val.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp types")
	}

	cmp := val.(*StructValue)

	// compare values
	for k := range obj.V {
		if err := obj.V[k].Cmp(cmp.V[k]); err != nil {
			return errwrap.Wrapf(err, "field %s did not cmp", k)
		}
	}

	return nil
}

// Copy returns a copy of this value.
func (obj *StructValue) Copy() Value {
	m := map[string]Value{}
	for k, v := range obj.V {
		m[k] = v.Copy()
	}
	return &StructValue{
		V: m,
		T: obj.T.Copy(),
	}
}

// Value returns the raw value of this type.
func (obj *StructValue) Value() interface{} {
	typ := obj.T.Reflect()
	val := reflect.New(typ).Elem() // New returns a PtrTo(typ)

	for _, k := range obj.T.Ord {
		val.FieldByName(k).Set(reflect.ValueOf(obj.V[k].Value())) // recurse
	}
	return val.Interface()
}

// Struct represents the value of this type as a struct if it is one. If this is
// not a struct, then this panics.
func (obj *StructValue) Struct() map[string]Value {
	return obj.V
}

// Set sets a field to this value. It errors if the types do not match.
func (obj *StructValue) Set(k string, v Value) error { // TODO: change method name?
	typ, exists := obj.T.Map[k]
	if !exists {
		return fmt.Errorf("field %s does not exist", k)
	}

	if typ.Kind != KindVariant { // skip cmp if dest is a variant
		if err := typ.Cmp(v.Type()); err != nil {
			return errwrap.Wrapf(err, "value of type does not match field type")
		}
	}

	obj.V[k] = v // set
	return nil
}

// Lookup searches the struct for a key. On success it also returns the Value.
func (obj *StructValue) Lookup(k string) (value Value, exists bool) {
	v, exists := obj.V[k] // FIXME: should we return zero values if missing?
	return v, exists
}

// FuncValue represents a function which takes a list of Value arguments and
// returns a Value. It can also return an error which could represent that
// something went horribly wrong. (Think, an internal panic.)
//
// This is not general enough to represent all functions in the language (see
// the full.FuncValue), but it is a useful common case.
//
// FuncValue is not a Value, but it is a useful building block for implementing
// Func nodes.
type FuncValue struct {
	Base
	V func(context.Context, []Value) (Value, error)
	T *Type // contains ordered field types, arg names are a bonus part
}

// NewFunc creates a useless function which will get overwritten by something
// more useful later.
func NewFunc(t *Type) *FuncValue {
	if t.Kind != KindFunc {
		return nil // sanity check
	}
	v := func(context.Context, []Value) (Value, error) {
		// You were not supposed to call the temporary function, you
		// were supposed to replace it with a real implementation!
		return nil, fmt.Errorf("nil function")
	}
	// return an empty interface{}
	return &FuncValue{
		V: v,
		T: t,
	}
}

// String returns a visual representation of this value.
func (obj *FuncValue) String() string {
	return fmt.Sprintf("func(%+v)", obj.T) // TODO: can't print obj.V w/o vet warning
}

// Type returns the type data structure that represents this type.
func (obj *FuncValue) Type() *Type { return obj.T }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same. In this situation, they can't be compared so we
// panic.
func (obj *FuncValue) Less(v Value) bool {
	//V := v.(*FuncValue)
	//return obj.String() < V.String() // FIXME: implement a proper less func
	panic("you cannot compare functions")
}

// Cmp returns an error if this value isn't the same as the arg passed in. In
// this situation, they can't be compared so we panic.
func (obj *FuncValue) Cmp(val Value) error {
	//if obj == nil || val == nil {
	//	return fmt.Errorf("cannot cmp to nil")
	//}
	//if err := obj.Type().Cmp(val.Type()); err != nil {
	//	return errwrap.Wrapf(err, "cannot cmp types")
	//}
	//
	//return fmt.Errorf("cannot cmp funcs") // TODO: can we ?
	panic("you cannot compare functions")
}

// Copy returns a copy of this value.
func (obj *FuncValue) Copy() Value {
	return &FuncValue{
		V: obj.V,
		T: obj.T.Copy(),
	}
}

// Value returns the raw value of this type.
func (obj *FuncValue) Value() interface{} {
	//typ := obj.T.Reflect()
	//
	//// wrap our function with the translation that is necessary
	//fn := func(args []reflect.Value) (results []reflect.Value) { // build
	//	innerArgs := []Value{}
	//	for _, x := range args {
	//		v, err := ValueOf(x) // reflect.Value -> Value
	//		if err != nil {
	//			panic(fmt.Sprintf("can't determine value of %+v", x))
	//		}
	//		innerArgs = append(innerArgs, v)
	//	}
	//	result, err := obj.V(innerArgs) // call it
	//	if err != nil {
	//		// when calling our function with the Call method, then
	//		// we get the error output and have a chance to decide
	//		// what to do with it, but when calling it from within
	//		// a normal golang function call, the error represents
	//		// that something went horribly wrong, aka a panic...
	//		panic(fmt.Sprintf("function panic: %+v", err))
	//	}
	//	return []reflect.Value{reflect.ValueOf(result.Value())} // only one result
	//}
	//val := reflect.MakeFunc(typ, fn)
	//return val.Interface()
	return obj.V
}

// Call runs the function value and returns its result. It returns an error if
// something goes wrong during execution, and panic's if you call this with
// inappropriate input types, or if it returns an inappropriate output type.
func (obj *FuncValue) Call(ctx context.Context, args []Value) (Value, error) {
	// cmp input args type to obj.T
	if obj.T == nil {
		return nil, fmt.Errorf("the type is nil")
	}
	length := len(obj.T.Ord)
	if length != len(args) {
		return nil, fmt.Errorf("arg length of %d does not match expected of %d", len(args), length)
	}
	for i := 0; i < length; i++ {
		if err := args[i].Type().Cmp(obj.T.Map[obj.T.Ord[i]]); err != nil {
			return nil, errwrap.Wrapf(err, "cannot cmp input types")
		}
	}

	result, err := obj.V(ctx, args) // call it
	if result == nil {
		if err == nil {
			return nil, fmt.Errorf("function returned nil result")
		}
		return nil, err
	}
	if err := result.Type().Cmp(obj.T.Out); err != nil {
		return nil, errwrap.Wrapf(err, "cannot cmp return types")
	}

	return result, err
}

// VariantValue represents a variant value.
type VariantValue struct {
	Base
	V Value // formerly I experimented with using interface{} instead
	T *Type
}

// NewVariant creates a new variant value.
// TODO: I haven't thought about this thoroughly yet.
func NewVariant(t *Type) *VariantValue {
	if t.Kind != KindVariant {
		return nil // sanity check
	}
	return &VariantValue{
		T: t,
	}
}

// String returns a visual representation of this value.
func (obj *VariantValue) String() string {
	//return fmt.Sprintf("%v", obj.V)
	return obj.V.String()
}

// Type returns the type data structure that represents this type.
func (obj *VariantValue) Type() *Type { return obj.T }

// Less compares to value and returns true if we're smaller. This panics if the
// two types aren't the same. For variants, the two sub types must be the same.
func (obj *VariantValue) Less(v Value) bool {
	//return obj.String() < v.String() // FIXME: implement a proper less func
	V := v.(*VariantValue).V
	return obj.V.Less(V)
}

// Cmp returns an error if this value isn't the same as the arg passed in.
func (obj *VariantValue) Cmp(val Value) error {
	if obj == nil || val == nil {
		return fmt.Errorf("cannot cmp to nil")
	}
	if err := obj.Type().Cmp(val.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp types")
	}

	V := val.(*VariantValue).V

	//if !reflect.DeepEqual(obj.V, V) {
	//	return fmt.Errorf("values are different")
	//}

	if err := obj.V.Type().Cmp(V.Type()); err != nil {
		return errwrap.Wrapf(err, "cannot cmp sub types")
	}

	if err := obj.V.Cmp(V); err != nil {
		return errwrap.Wrapf(err, "values are different")
	}
	return nil
}

// Copy returns a copy of this value.
func (obj *VariantValue) Copy() Value {
	return &VariantValue{
		V: obj.V.Copy(),
		T: obj.T.Copy(),
	}
}

// Value returns the raw value of this type.
func (obj *VariantValue) Value() interface{} {
	return obj.V.Value()
}

// Bool represents the value of this type as a bool if it is one. If this is not
// a bool, then this panics.
func (obj *VariantValue) Bool() bool {
	//return obj.V.(bool)
	return obj.V.Bool()
}

// Str represents the value of this type as a string if it is one. If this is
// not a string, then this panics.
func (obj *VariantValue) Str() string {
	//return obj.V.(string)
	return obj.V.Str()
}

// Int represents the value of this type as an integer if it is one. If this is
// not an integer, then this panics.
func (obj *VariantValue) Int() int64 {
	//return obj.V.(int64)
	return obj.V.Int()
}

// Float represents the value of this type as a float if it is one. If this is
// not a float, then this panics.
func (obj *VariantValue) Float() float64 {
	//return obj.V.(float64)
	return obj.V.Float()
}

// List represents the value of this type as a list if it is one. If this is not
// a list, then this panics.
func (obj *VariantValue) List() []Value {
	return obj.V.List()
}

// Map represents the value of this type as a dictionary if it is one. If this
// is not a map, then this panics.
func (obj *VariantValue) Map() map[Value]Value {
	return obj.V.Map()
}

// Struct represents the value of this type as a struct if it is one. If this is
// not a struct, then this panics.
func (obj *VariantValue) Struct() map[string]Value {
	return obj.V.Struct()
}

// Func represents the value of this type as a function if it is one. If this is
// not a function, then this panics.
func (obj *VariantValue) Func() interface{} {
	return obj.V.Func()
}
