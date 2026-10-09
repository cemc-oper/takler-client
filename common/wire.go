package common

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed wire_schema.json
var wireSchemaJSON []byte
var wireSchema map[string]json.RawMessage
var decimalPattern = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)
var contentTypePattern = regexp.MustCompile(`(?i)^application/json(?:[ \t]*;[ \t]*charset[ \t]*=[ \t]*(?:utf-8|"utf-8"))?$`)
var tracePattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func init() {
	if err := json.Unmarshal(wireSchemaJSON, &wireSchema); err != nil {
		panic(err)
	}
}
func readOnlyCommand(name string) bool {
	return name == "ping" || name == "coroutine" || name == "server-status" || strings.HasPrefix(name, "query ")
}
func jsonContentType(s string) bool {
	return contentTypePattern.MatchString(strings.Trim(s, " \t"))
}

// JSON's replacement of invalid UTF-8/surrogates would hide a corrupt message.
func validWireStrings(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			m, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || m < 0xdc00 || m > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inString
}
func strictWireJSON(raw []byte) (any, error) {
	if !validWireStrings(raw) {
		return nil, errors.New("invalid UTF-8 or JSON string")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var parse func() (any, error)
	parse = func() (any, error) {
		t, e := d.Token()
		if e != nil {
			return nil, e
		}
		switch t {
		case json.Delim('{'):
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := k.(string)
				if !ok {
					return nil, errors.New("invalid key")
				}
				if _, ok = m[key]; ok {
					return nil, errors.New("duplicate key")
				}
				v, e := parse()
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			_, e = d.Token()
			return m, e
		case json.Delim('['):
			a := []any{}
			for d.More() {
				v, e := parse()
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e = d.Token()
			return a, e
		default:
			return t, nil
		}
	}
	v, e := parse()
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return v, nil
}
func wireFields(value any, fields map[string]string) error {
	m, ok := value.(map[string]any)
	if !ok || len(m) != len(fields) {
		return errors.New("missing or unknown fields")
	}
	for key, kind := range fields {
		v, exists := m[key]
		if !exists {
			return errors.New("missing field")
		}
		valid := false
		switch kind {
		case "b":
			_, valid = v.(bool)
		case "ns":
			if v == nil {
				valid = true
			} else {
				_, valid = v.(string)
			}
		case "no":
			if v == nil {
				valid = true
			} else {
				_, valid = v.(map[string]any)
			}
		case "server_status":
			s, ok := v.(string)
			valid = ok && containsString([]string{"running", "halted"}, s)
		case "as":
			a, ok := v.([]any)
			valid = ok
			for _, x := range a {
				if _, ok = x.(string); !ok {
					valid = false
				}
			}
		case "flag", "uint32":
			n, ok := v.(json.Number)
			if ok {
				i, e := strconv.ParseInt(string(n), 10, 64)
				valid = e == nil
				if kind == "flag" {
					_, known := ErrorNameByCode[int32(i)]
					valid = valid && i >= 0 && i <= 2147483647 && known
				} else {
					valid = valid && i >= 0 && i < 4294967296
				}
			}
		case "items", "coroutines":
			a, ok := v.([]any)
			valid = ok
			schemaKey := "item"
			if kind == "coroutines" {
				schemaKey = "coroutine"
			}
			var schema map[string]string
			_ = json.Unmarshal(wireSchema[schemaKey], &schema)
			for _, x := range a {
				if wireFields(x, schema) != nil {
					valid = false
				}
			}
		default:
			s, ok := v.(string)
			valid = ok
			if ok {
				switch kind {
				case "decimal":
					_, e := strconv.ParseInt(s, 10, 64)
					valid = decimalPattern.MatchString(s) && e == nil
				case "base64":
					b, e := base64.StdEncoding.Strict().DecodeString(s)
					valid = e == nil && base64.StdEncoding.EncodeToString(b) == s
				case "state":
					valid = containsString([]string{"unknown", "complete", "queued", "submitted", "active", "aborted", "clear", "set"}, s)
				case "dep":
					valid = containsString([]string{"all", "trigger", "time"}, s)
				case "effect":
					valid = containsString([]string{"none", "applied", "partial", "unknown"}, s)
				}
			}
		}
		if !valid {
			return fmt.Errorf("invalid %s field", kind)
		}
	}
	return nil
}
func containsString(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}
func decodeWire(raw []byte, direction, command, trace string) (map[string]any, error) {
	value, err := strictWireJSON(raw)
	if err != nil {
		return nil, err
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("invalid envelope")
	}
	for k, v := range m {
		switch k {
		case "version", "command", "trace_id", "payload":
		case "auth", "target":
			if v != nil {
				return nil, errors.New("reserved field")
			}
		default:
			return nil, errors.New("unknown envelope field")
		}
	}
	for _, k := range []string{"version", "command", "trace_id", "payload"} {
		if _, ok = m[k]; !ok {
			return nil, errors.New("missing envelope field")
		}
	}
	c, cok := m["command"].(string)
	id, iok := m["trace_id"].(string)
	if m["version"] != "1" || !cok || !iok || !tracePattern.MatchString(id) {
		return nil, errors.New("invalid envelope metadata")
	}
	if command != "" && c != command || trace != "" && id != trace {
		return nil, errors.New("envelope mismatch")
	}
	var schemas map[string]map[string]string
	_ = json.Unmarshal(wireSchema[direction], &schemas)
	schema, ok := schemas[c]
	if !ok {
		return nil, errors.New("unknown command")
	}
	if err = wireFields(m["payload"], schema); err != nil {
		return nil, err
	}
	return m, nil
}
