package cli

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// SchemaPath is where the generated JSON Schema is committed.
const SchemaPath = "schema/deepward.schema.json"

// ConfigSchema returns the JSON Schema (draft 2020-12) of deepward.yaml,
// generated from engine.Config. Plugin and integration sections are open
// objects validated by each plugin at runtime.
func ConfigSchema() ([]byte, error) {
	s := schemaFor(reflect.TypeOf(engine.Config{}))
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["$id"] = "https://raw.githubusercontent.com/deepward-oss/deepward/main/schema/deepward.schema.json"
	s["title"] = "deepward.yaml"
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

var (
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	nodeType            = reflect.TypeOf(yaml.Node{})
	severityType        = reflect.TypeOf(sdk.Severity(""))
	durationType        = reflect.TypeOf(sdk.Duration(0))
	dateType            = reflect.TypeOf(sdk.Date{})
)

func schemaFor(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case severityType:
		enum := make([]string, 0, len(sdk.AllSeverities))
		for _, s := range sdk.AllSeverities {
			enum = append(enum, string(s))
		}
		return map[string]any{"type": "string", "enum": enum}
	case durationType:
		return map[string]any{"type": "string", "pattern": `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`, "examples": []string{"30s", "5m", "1h"}}
	case dateType:
		return map[string]any{"type": "string", "format": "date"}
	case nodeType:
		return map[string]any{"type": "object"}
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return map[string]any{"type": "string"}
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if !f.IsExported() || name == "-" {
				continue
			}
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			if enum := f.Tag.Get("enum"); enum != "" {
				props[name] = map[string]any{"type": "string", "enum": strings.Split(enum, ",")}
				continue
			}
			props[name] = schemaFor(f.Type)
		}
		return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem())}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	}
	return map[string]any{"type": "string"}
}
