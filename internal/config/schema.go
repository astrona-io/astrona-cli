package config

import (
	"encoding/json"
	"reflect"
	"strings"
)

// SchemaURL is where the lab config JSON Schema is published (the docs
// site serves docs/schema/). Authors reference it from config.yaml with
//
//	# yaml-language-server: $schema=https://cli.astrona.io/schema/lab-config.schema.json
//
// for completion and inline errors in VS Code (Red Hat YAML extension)
// and other yaml-language-server editors.
const SchemaURL = "https://cli.astrona.io/schema/lab-config.schema.json"

// schemaEnums are the closed value sets for string fields, keyed
// "<GoType>.<yaml key>". Kept next to the generator so the schema can't
// silently miss one.
var schemaDeprecated = map[string]bool{
	"KindConfig.labs": true, // renamed to clusters
}

var schemaEnums = map[string][]string{
	"RuntimeConfig.type":           {"kind", "qemu"},
	"ResourceItem.type":            {"file", "folder", "url"},
	"ValidationCheck.type":         CheckTypes,
	"PortForward.scheme":           {"http", "https", "tcp"},
	"KindNetworking.kubeProxyMode": {"iptables", "ipvs", "nftables", "none"},
	"KindNetworking.ipFamily":      {"ipv4", "ipv6", "dual"},
	"KindAddons.cni":               {CNICalico},
	"KindAddons.gatewayAPI":        {GatewayEnvoy},
	"QEMUImageSource.type":         {"file", "url", "oci"},
}

// Schema returns the JSON Schema (draft 2020-12) for config.yaml,
// generated from the LabConfig structs — every object rejects unknown
// keys, matching `astrona validate`.
func Schema() ([]byte, error) {
	defs := map[string]any{}
	root := schemaFor(reflect.TypeOf(LabConfig{}), defs)
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     SchemaURL,
		"title":   "Astrona lab config (config.yaml)",
		"$ref":    root["$ref"],
		"$defs":   defs,
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func schemaFor(t reflect.Type, defs map[string]any) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem(), defs)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem(), defs)}
	case reflect.Struct:
		name := t.Name()
		if _, seen := defs[name]; !seen {
			defs[name] = nil // reserve first: guards against recursive types
			props := map[string]any{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				key := strings.Split(f.Tag.Get("yaml"), ",")[0]
				if key == "" || key == "-" || !f.IsExported() {
					continue
				}
				p := schemaFor(f.Type, defs)
				if enum, ok := schemaEnums[name+"."+key]; ok {
					p["enum"] = enum
				}
				if schemaDeprecated[name+"."+key] {
					p["deprecated"] = true
				}
				props[key] = p
			}
			defs[name] = map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	default:
		return map[string]any{}
	}
}
