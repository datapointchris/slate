package cli

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"

	"github.com/spf13/pflag"
)

func encodeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// fetchedType is the struct a fetch function's first result holds, through any slice or pointer.
// Help naming what --json prints reads it off the call that fetches the value, so the keys it
// lists follow whatever that call returns.
func fetchedType(fetch any) reflect.Type {
	return elementType(reflect.TypeOf(fetch).Out(0))
}

// jsonKeys is the keys encodeJSON writes for a struct of type t, in its order.
func jsonKeys(t reflect.Type) string {
	fields := jsonFields(t)
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		keys = append(keys, field.key)
	}
	return strings.Join(keys, ", ")
}

// jsonFieldType is the struct t holds under the JSON key key, through any slice or pointer.
func jsonFieldType(t reflect.Type, key string) reflect.Type {
	for _, field := range jsonFields(t) {
		if field.key == key {
			return elementType(field.typ)
		}
	}
	panic(t.String() + " encodes no key " + key)
}

type jsonField struct {
	key string
	typ reflect.Type
}

func jsonFields(t reflect.Type) []jsonField {
	var fields []jsonField
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch {
		case name == "" && field.Anonymous && field.Type.Kind() == reflect.Struct:
			fields = append(fields, jsonFields(field.Type)...)
		case name == "-" || !field.IsExported():
		case name == "":
			fields = append(fields, jsonField{key: field.Name, typ: field.Type})
		default:
			fields = append(fields, jsonField{key: name, typ: field.Type})
		}
	}
	return fields
}

func elementType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Slice || t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func strValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ptr returns a pointer to v, for the optional fields the wire omits when nil.
func ptr[T any](v T) *T { return &v }

// shellQuote quotes s for a hint printed to be typed back, so a value with a
// space pastes back as one argument rather than several.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// looksLikeUUID reports whether s has the 8-4-4-4-12 shape of a record id.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// rawFlagValues reads every value a flag holds, empties included.
func rawFlagValues(flag *pflag.Flag) []string {
	if slice, ok := flag.Value.(pflag.SliceValue); ok {
		return slice.GetSlice()
	}
	return []string{flag.Value.String()}
}

// flagIntent is what one field flag on this run is asking for.
type flagIntent int

const (
	flagUntouched flagIntent = iota
	flagCarriesValues
	flagClears
	flagMixesEmptyWithValues
)

// readFlagIntent classifies a field flag by the values it holds.
//
// `""` clears the field, on every field flag that can carry one. A caller
// learns what an empty value does from whichever flag they reach for first and
// uses the next the same way, so one verb cannot answer several ways without
// the disagreement being invisible.
func readFlagIntent(f *pflag.FlagSet, name string) flagIntent {
	flag := f.Lookup(name)
	if flag == nil || !flag.Changed {
		return flagUntouched
	}
	var empty, filled int
	for _, value := range rawFlagValues(flag) {
		if strings.TrimSpace(value) == "" {
			empty++
		} else {
			filled++
		}
	}
	switch {
	case empty > 0 && filled > 0:
		return flagMixesEmptyWithValues
	case empty > 0:
		return flagClears
	default:
		return flagCarriesValues
	}
}

// valuedStrFlag is strFlag for a field an empty value clears. Clearing is
// carried by the clear list, not by an empty value in the body, so a clearing
// flag reads as untouched here.
func valuedStrFlag(f *pflag.FlagSet, name string, v *string) *string {
	if readFlagIntent(f, name) != flagCarriesValues {
		return nil
	}
	return v
}
