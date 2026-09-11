package main

import (
	"bytes"
	"encoding/json"
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

func repositoryPaths(t *testing.T) (string, string) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	return filepath.Join(repositoryRoot, "clients/nzbget/openrpc.json"), filepath.Join(repositoryRoot, "clients/nzbget/generated.go")
}
