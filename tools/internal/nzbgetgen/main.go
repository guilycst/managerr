// Command nzbgetgen generates the standalone NZBGet compatibility client DTOs
// and positional method wrappers from clients/nzbget/openrpc.json.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strings"
)

type document struct {
	OpenRPC    string   `json:"openrpc"`
	Methods    []method `json:"methods"`
	Components struct {
		Schemas map[string]schema `json:"schemas"`
	} `json:"components"`
}

type method struct {
	Name           string      `json:"name"`
	ParamStructure string      `json:"paramStructure"`
	Params         []parameter `json:"params"`
	Result         result      `json:"result"`
}

type parameter struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Schema   schema `json:"schema"`
}

type result struct {
	Name   string `json:"name"`
	Schema schema `json:"schema"`
}

type schema struct {
	Ref        string            `json:"$ref"`
	Type       schemaTypes       `json:"type"`
	Format     string            `json:"format"`
	Items      *schema           `json:"items"`
	Properties map[string]schema `json:"properties"`
	Required   []string          `json:"required"`
	GoName     string            `json:"x-go-name"`
	GoType     string            `json:"x-go-type"`
	GoOrder    []string          `json:"x-go-order"`
}

// schemaTypes accepts the Draft 7 `type` form used by OpenRPC: a single
// primitive name or a two-member union containing exactly one `null` member.
// Keeping this as a small type makes nullable wire fields explicit in the
// contract instead of relying on OpenAPI's non-Draft-7 `nullable` keyword.
type schemaTypes []string

func (types *schemaTypes) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*types = schemaTypes{single}
		return nil
	}
	var union []string
	if err := json.Unmarshal(data, &union); err != nil || len(union) == 0 {
		return errors.New("schema type must be a string or non-empty string array")
	}
	*types = schemaTypes(union)
	return nil
}

func (types schemaTypes) has(value string) bool {
	for _, current := range types {
		if current == value {
			return true
		}
	}
	return false
}

func (types schemaTypes) primary() string {
	for _, current := range types {
		if current != "null" {
			return current
		}
	}
	return ""
}

func (types schemaTypes) nullable() bool {
	return types.has("null")
}

var expectedMethods = []string{"version", "listgroups", "listfiles", "history"}

func main() {
	input := flag.String("input", "clients/nzbget/openrpc.json", "OpenRPC compatibility document")
	output := flag.String("output", "clients/nzbget/generated.go", "generated Go file")
	check := flag.Bool("check", false, "fail if output differs from generated content")
	flag.Parse()

	data, err := os.ReadFile(*input)
	if err != nil {
		fatal(err)
	}
	generated, err := Generate(data)
	if err != nil {
		fatal(err)
	}
	if *check {
		current, readErr := os.ReadFile(*output)
		if readErr != nil {
			fatal(readErr)
		}
		if !bytes.Equal(current, generated) {
			fatal(errors.New("generated NZBGet client is out of date"))
		}
		return
	}
	if err := os.WriteFile(*output, generated, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "nzbgetgen:", err)
	os.Exit(1)
}

// Generate parses and validates the Mastarr-owned OpenRPC contract and returns
// deterministic generated Go source. It intentionally accepts only the four
// initial read methods so adding an upstream mutation cannot silently expand
// this read-only client.
func Generate(data []byte) ([]byte, error) {
	if err := rejectOpenAPINullable(data); err != nil {
		return nil, err
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode OpenRPC: %w", err)
	}
	if doc.OpenRPC != "1.2.6" {
		return nil, fmt.Errorf("unsupported OpenRPC version %q", doc.OpenRPC)
	}
	if len(doc.Methods) != len(expectedMethods) {
		return nil, fmt.Errorf("expected exactly %d read methods, got %d", len(expectedMethods), len(doc.Methods))
	}
	for index, expected := range expectedMethods {
		current := doc.Methods[index]
		if current.Name != expected {
			return nil, fmt.Errorf("method %d is %q, expected %q", index, current.Name, expected)
		}
		if current.ParamStructure != "by-position" {
			return nil, fmt.Errorf("method %q must use by-position parameters", current.Name)
		}
		for _, param := range current.Params {
			if !param.Required {
				return nil, fmt.Errorf("method %q parameter %q must be required", current.Name, param.Name)
			}
		}
		if len(current.Result.Schema.Type) == 0 && current.Result.Schema.Ref == "" {
			return nil, fmt.Errorf("method %q has no result schema", current.Name)
		}
	}
	if len(doc.Components.Schemas) == 0 {
		return nil, errors.New("OpenRPC components.schemas is empty")
	}
	if err := validateSchemaTypes(doc); err != nil {
		return nil, err
	}
	if err := validateReferences(doc); err != nil {
		return nil, err
	}

	var out strings.Builder
	out.WriteString("// Code generated by tools/internal/nzbgetgen; DO NOT EDIT.\n")
	out.WriteString("\n")
	out.WriteString("package nzbget\n")
	out.WriteString("\n")
	out.WriteString("import \"context\"\n")
	out.WriteString("\n")
	out.WriteString("// Invoker is implemented by the handwritten transport. Generated methods\n")
	out.WriteString("// only describe the typed wire call and never perform HTTP themselves.\n")
	out.WriteString("type Invoker interface {\n")
	out.WriteString("\tInvoke(context.Context, string, []any, any) error\n")
	out.WriteString("}\n")
	out.WriteString("\n")
	out.WriteString("// Methods exposes the four read-only positional NZBGet methods.\n")
	out.WriteString("type Methods struct {\n\tinvoker Invoker\n}\n\n")
	out.WriteString("// NewMethods binds generated wrappers to a transport.\n")
	out.WriteString("func NewMethods(invoker Invoker) Methods { return Methods{invoker: invoker} }\n\n")

	for _, current := range doc.Methods {
		out.WriteString(fmt.Sprintf("const Method%s = %q\n\n", operationName(current.Name), current.Name))
	}

	names := make([]string, 0, len(doc.Components.Schemas))
	for name := range doc.Components.Schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := emitStruct(&out, name, doc.Components.Schemas[name]); err != nil {
			return nil, err
		}
	}

	for _, current := range doc.Methods {
		if err := emitRequestAndMethod(&out, current); err != nil {
			return nil, err
		}
	}
	formatted, err := format.Source([]byte(out.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w", err)
	}
	return formatted, nil
}

func rejectOpenAPINullable(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode OpenRPC document for semantic validation: %w", err)
	}
	var walk func(any, string) error
	walk = func(current any, location string) error {
		switch typed := current.(type) {
		case map[string]any:
			if _, found := typed["nullable"]; found {
				return fmt.Errorf("unsupported OpenAPI nullable keyword at %s", location)
			}
			for name, child := range typed {
				if err := walk(child, location+"."+name); err != nil {
					return err
				}
			}
		case []any:
			for index, child := range typed {
				if err := walk(child, fmt.Sprintf("%s[%d]", location, index)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(value, "OpenRPC")
}

func validateSchemaTypes(doc document) error {
	var check func(schema, string) error
	check = func(value schema, location string) error {
		if value.Ref != "" {
			return nil
		}
		if len(value.Type) == 0 {
			return fmt.Errorf("schema at %s has no type", location)
		}
		nullable := false
		base := ""
		for _, current := range value.Type {
			if current == "null" {
				if nullable {
					return fmt.Errorf("schema at %s repeats null in type union", location)
				}
				nullable = true
				continue
			}
			if base != "" {
				return fmt.Errorf("schema at %s has more than one non-null type", location)
			}
			base = current
		}
		if base == "" {
			return fmt.Errorf("schema at %s has no non-null type", location)
		}
		if len(value.Type) > 2 {
			return fmt.Errorf("schema at %s has an unsupported type union", location)
		}
		switch base {
		case "array":
			if value.Items == nil {
				return fmt.Errorf("array schema at %s has no items", location)
			}
		case "object":
			for name, property := range value.Properties {
				if err := check(property, location+"."+name); err != nil {
					return err
				}
			}
		}
		if value.Items != nil {
			if err := check(*value.Items, location+".items"); err != nil {
				return err
			}
		}
		return nil
	}
	for name, value := range doc.Components.Schemas {
		if err := check(value, "components.schemas."+name); err != nil {
			return err
		}
	}
	for _, current := range doc.Methods {
		if err := check(current.Result.Schema, "methods."+current.Name+".result"); err != nil {
			return err
		}
		for _, param := range current.Params {
			if err := check(param.Schema, "methods."+current.Name+".params."+param.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateReferences(doc document) error {
	known := make(map[string]struct{}, len(doc.Components.Schemas))
	for name := range doc.Components.Schemas {
		known["#/components/schemas/"+name] = struct{}{}
	}
	var check func(schema, string) error
	check = func(value schema, location string) error {
		if value.Ref != "" {
			if _, ok := known[value.Ref]; !ok {
				return fmt.Errorf("unknown schema reference %q at %s", value.Ref, location)
			}
			return nil
		}
		if value.Items != nil {
			if err := check(*value.Items, location+".items"); err != nil {
				return err
			}
		}
		for name, property := range value.Properties {
			if err := check(property, location+"."+name); err != nil {
				return err
			}
		}
		return nil
	}
	for name, value := range doc.Components.Schemas {
		if err := check(value, "components.schemas."+name); err != nil {
			return err
		}
	}
	for _, current := range doc.Methods {
		if err := check(current.Result.Schema, "methods."+current.Name+".result"); err != nil {
			return err
		}
		for _, param := range current.Params {
			if err := check(param.Schema, "methods."+current.Name+".params."+param.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func emitStruct(out *strings.Builder, name string, value schema) error {
	goName := schemaName(name, value)
	if value.Type.primary() != "object" {
		fieldType, err := goType(value, true)
		if err != nil {
			return fmt.Errorf("schema %q: %w", name, err)
		}
		out.WriteString(fmt.Sprintf("// %s is the generated NZBGet wire representation of %s.\n", goName, name))
		out.WriteString(fmt.Sprintf("type %s %s\n\n", goName, fieldType))
		return nil
	}
	out.WriteString(fmt.Sprintf("// %s is the generated NZBGet wire representation of %s.\n", goName, name))
	out.WriteString(fmt.Sprintf("type %s struct {\n", goName))
	properties := orderedProperties(value)
	required := make(map[string]bool, len(value.Required))
	for _, item := range value.Required {
		required[item] = true
	}
	for _, propertyName := range properties {
		property := value.Properties[propertyName]
		fieldType, err := goType(property, required[propertyName])
		if err != nil {
			return fmt.Errorf("schema %q property %q: %w", name, propertyName, err)
		}
		out.WriteString(fmt.Sprintf("\t%s %s `json:%q`\n", exportName(propertyName), fieldType, propertyName))
	}
	out.WriteString("}\n\n")
	return nil
}

func orderedProperties(value schema) []string {
	seen := make(map[string]struct{}, len(value.Properties))
	result := make([]string, 0, len(value.Properties))
	for _, name := range value.GoOrder {
		if _, ok := value.Properties[name]; ok {
			result = append(result, name)
			seen[name] = struct{}{}
		}
	}
	remaining := make([]string, 0, len(value.Properties)-len(result))
	for name := range value.Properties {
		if _, ok := seen[name]; !ok {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)
	return append(result, remaining...)
}

func goType(value schema, required bool) (string, error) {
	var result string
	switch {
	case value.Ref != "":
		result = schemaName(strings.TrimPrefix(value.Ref, "#/components/schemas/"), schema{})
	case value.Type.primary() == "array":
		if value.Items == nil {
			return "", errors.New("array schema has no items")
		}
		itemType, err := goType(*value.Items, true)
		if err != nil {
			return "", err
		}
		result = "[]" + itemType
	case value.Type.primary() == "string":
		result = "string"
	case value.Type.primary() == "boolean":
		result = "bool"
	case value.Type.primary() == "integer":
		switch value.Format {
		case "uint32":
			result = "uint32"
		case "int32":
			result = "int32"
		case "", "int64":
			result = "int64"
		default:
			return "", fmt.Errorf("unsupported integer format %q", value.Format)
		}
	case value.Type.primary() == "number":
		result = "float64"
	case value.Type.primary() == "object":
		result = "map[string]any"
	default:
		return "", fmt.Errorf("unsupported schema type %q", value.Type.primary())
	}
	if value.Type.nullable() && result[0] != '[' {
		result = "*" + result
	}
	_ = required
	return result, nil
}

func emitRequestAndMethod(out *strings.Builder, current method) error {
	requestName := operationName(current.Name) + "Request"
	out.WriteString(fmt.Sprintf("// %s contains the exact positional arguments for %s.\n", requestName, current.Name))
	out.WriteString(fmt.Sprintf("type %s struct {\n", requestName))
	for _, param := range current.Params {
		fieldType, err := goType(param.Schema, true)
		if err != nil {
			return fmt.Errorf("method %q parameter %q: %w", current.Name, param.Name, err)
		}
		out.WriteString(fmt.Sprintf("\t%s %s\n", exportName(param.Name), fieldType))
	}
	out.WriteString("}\n\n")
	out.WriteString(fmt.Sprintf("// Params returns arguments in NZBGet's required by-position order.\nfunc (request %s) Params() []any {\n", requestName))
	out.WriteString("\treturn []any{")
	for index, param := range current.Params {
		if index > 0 {
			out.WriteString(", ")
		}
		out.WriteString("request." + exportName(param.Name))
	}
	out.WriteString("}\n}\n\n")

	resultType, err := methodResultType(current.Result.Schema)
	if err != nil {
		return fmt.Errorf("method %q result: %w", current.Name, err)
	}
	out.WriteString(fmt.Sprintf("// %s invokes %s through the bound transport.\n", operationName(current.Name), current.Name))
	out.WriteString(fmt.Sprintf("func (methods Methods) %s(ctx context.Context, request %s) (%s, error) {\n", operationName(current.Name), requestName, resultType))
	out.WriteString(fmt.Sprintf("\tvar result %s\n", resultType))
	out.WriteString(fmt.Sprintf("\tif err := methods.invoker.Invoke(ctx, Method%s, request.Params(), &result); err != nil {\n", operationName(current.Name)))
	out.WriteString(fmt.Sprintf("\t\treturn result, err\n\t}\n\treturn result, nil\n}\n\n"))
	return nil
}

func methodResultType(value schema) (string, error) {
	return goType(value, true)
}

func operationName(value string) string {
	switch value {
	case "version":
		return "Version"
	case "listgroups":
		return "ListGroups"
	case "listfiles":
		return "ListFiles"
	case "history":
		return "History"
	default:
		return exportName(value)
	}
}

func schemaName(name string, value schema) string {
	if value.GoName != "" {
		return value.GoName
	}
	return exportName(name)
}

func exportName(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ' '
	})
	if len(parts) == 1 {
		part := parts[0]
		return strings.ToUpper(part[:1]) + part[1:]
	}
	var result strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]))
		result.WriteString(part[1:])
	}
	return result.String()
}
