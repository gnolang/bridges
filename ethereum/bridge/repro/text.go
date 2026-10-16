package repro

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// jsonText writes a decoded ABI value — a tuple (go-ethereum's anonymous struct with the components' names in its
// json tags), a list, a number, an address, bytes — the way a holder reads it: addresses checksummed, byte arrays in
// hex, tuples as {name: value, …}, lists as [a, b, …].
func jsonText(v any) (string, error) {
	var b strings.Builder
	writeValue(&b, reflect.ValueOf(v))
	return b.String(), nil
}

var (
	addressType = reflect.TypeOf(common.Address{})
	bigPtrType  = reflect.TypeOf((*big.Int)(nil))
)

func writeValue(b *strings.Builder, v reflect.Value) {
	if !v.IsValid() {
		b.WriteString("null")
		return
	}
	switch {
	case v.Type() == addressType:
		b.WriteString(v.Interface().(common.Address).Hex())
		return
	case v.Type() == bigPtrType:
		if v.IsNil() {
			b.WriteString("0")
			return
		}
		b.WriteString(v.Interface().(*big.Int).String())
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		b.WriteString("{")
		for i := 0; i < v.NumField(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			name := t.Field(i).Tag.Get("json")
			if name == "" {
				name = t.Field(i).Name
			}
			b.WriteString(name + ": ")
			writeValue(b, v.Field(i))
		}
		b.WriteString("}")
	case reflect.Array, reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			raw := make([]byte, v.Len())
			for i := range raw {
				raw[i] = byte(v.Index(i).Uint())
			}
			b.WriteString("0x" + hex.EncodeToString(raw))
			return
		}
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			writeValue(b, v.Index(i))
		}
		b.WriteString("]")
	case reflect.String:
		fmt.Fprintf(b, "%q", v.String())
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			b.WriteString("null")
			return
		}
		writeValue(b, v.Elem())
	default:
		fmt.Fprint(b, v.Interface())
	}
}
