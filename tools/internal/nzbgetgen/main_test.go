package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGenerateMatchesCommittedOutputAndIsDeterministic(t *testing.T) {
	contractPath, generatedPath := repositoryPaths(t)
	contract, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Generate(contract)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(contract)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("generation is not deterministic")
	}
	committed, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, committed) {
		t.Fatal("committed generated.go differs from generator output")
	}
}

func TestGenerateRejectsMutationOrNamedParameterExpansion(t *testing.T) {
	contractPath, _ := repositoryPaths(t)
	contract, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(contract, &document); err != nil {
		t.Fatal(err)
	}
	methods, ok := document["methods"].([]any)
	if !ok {
		t.Fatal("methods missing")
	}
	methods = append(methods, map[string]any{"name": "editqueue", "paramStructure": "by-position"})
	document["methods"] = methods
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(mutated); err == nil {
		t.Fatal("generator accepted an expanded method set")
	}

	var named map[string]any
	if err := json.Unmarshal(contract, &named); err != nil {
		t.Fatal(err)
	}
	namedMethods := named["methods"].([]any)
	version := namedMethods[0].(map[string]any)
	version["paramStructure"] = "by-name"
	mutated, err = json.Marshal(named)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(mutated); err == nil {
		t.Fatal("generator accepted named parameters")
	}
}

func TestContractUsesPinnedOpenRPCAndDraft7RequiredParameters(t *testing.T) {
	contractPath, _ := repositoryPaths(t)
	contract, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(contract, &document); err != nil {
		t.Fatal(err)
	}
	if document["openrpc"] != "1.2.6" {
		t.Fatalf("OpenRPC version = %#v", document["openrpc"])
	}
	methods, ok := document["methods"].([]any)
	if !ok || len(methods) != len(expectedMethods) {
		t.Fatalf("methods = %#v", document["methods"])
	}
	for _, item := range methods {
		method := item.(map[string]any)
		for _, parameter := range method["params"].([]any) {
			param := parameter.(map[string]any)
			if required, ok := param["required"].(bool); !ok || !required {
				t.Fatalf("parameter is not required: %#v", param)
			}
		}
	}
	assertDraft7Schemas(t, document)
}

func TestGenerateRejectsUnsupportedVersionNullableAndInvalidUnion(t *testing.T) {
	contractPath, _ := repositoryPaths(t)
	contract, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(t *testing.T, change func(map[string]any)) {
		t.Helper()
		var document map[string]any
		if err := json.Unmarshal(contract, &document); err != nil {
			t.Fatal(err)
		}
		change(document)
		mutated, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Generate(mutated); err == nil {
			t.Fatal("generator accepted invalid contract")
		}
	}
	mutate(t, func(document map[string]any) { document["openrpc"] = "1.999" })
	mutate(t, func(document map[string]any) {
		methods := document["methods"].([]any)
		parameter := methods[1].(map[string]any)["params"].([]any)[0].(map[string]any)
		parameter["required"] = false
	})
	mutate(t, func(document map[string]any) {
		components := document["components"].(map[string]any)
		schemas := components["schemas"].(map[string]any)
		parameter := schemas["Parameter"].(map[string]any)
		value := parameter["properties"].(map[string]any)["Value"].(map[string]any)
		value["nullable"] = true
	})
	mutate(t, func(document map[string]any) {
		components := document["components"].(map[string]any)
		schemas := components["schemas"].(map[string]any)
		parameter := schemas["Parameter"].(map[string]any)
		value := parameter["properties"].(map[string]any)["Value"].(map[string]any)
		value["type"] = []any{"string", "integer"}
	})
}

func assertDraft7Schemas(t *testing.T, document map[string]any) {
	t.Helper()
	var walk func(any, string)
	walk = func(value any, location string) {
		switch typed := value.(type) {
		case map[string]any:
			if _, found := typed["nullable"]; found {
				t.Fatalf("OpenAPI nullable keyword at %s", location)
			}
			if rawType, found := typed["type"]; found {
				switch typeValue := rawType.(type) {
				case string:
					if typeValue == "" || typeValue == "null" {
						t.Fatalf("invalid Draft 7 type at %s: %#v", location, rawType)
					}
				case []any:
					if len(typeValue) != 2 || !containsString(typeValue, "null") || containsNonNullCount(typeValue) != 1 {
						t.Fatalf("invalid Draft 7 type union at %s: %#v", location, rawType)
					}
				default:
					t.Fatalf("invalid Draft 7 type value at %s: %#v", location, rawType)
				}
			}
			for key, child := range typed {
				walk(child, location+"."+key)
			}
		case []any:
			for index, child := range typed {
				walk(child, fmt.Sprintf("%s[%d]", location, index))
			}
		}
	}
	walk(document["components"], "components")
	walk(document["methods"], "methods")
}

func containsString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsNonNullCount(values []any) int {
	count := 0
	for _, value := range values {
		if value != "null" {
			count++
		}
	}
	return count
}

func repositoryPaths(t *testing.T) (string, string) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	return filepath.Join(repositoryRoot, "clients/nzbget/openrpc.json"), filepath.Join(repositoryRoot, "clients/nzbget/generated.go")
}
