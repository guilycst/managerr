package bootstrap

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestParseDefaults(t *testing.T) {
	configuration, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.DataDir != "/data" || configuration.ListenAddr != ":8080" || configuration.UIListenAddr != ":8081" || configuration.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", configuration)
	}
	if source, err := configuration.CredentialKeySource(); err != nil || source != KeySourceGeneratedPersistent {
		t.Fatalf("unexpected key source: %q, %v", source, err)
	}
}

func TestParseCredentialKeyRules(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 32))
	configuration, err := Parse(map[string]string{"MANAGERR_CREDENTIAL_KEY": encoded})
	if err != nil {
		t.Fatal(err)
	}
	if source, err := configuration.CredentialKeySource(); err != nil || source != KeySourceEnvironment {
		t.Fatalf("unexpected key source: %q, %v", source, err)
	}

	_, err = Parse(map[string]string{"MANAGERR_CREDENTIAL_KEY": "definitely-not-a-key"})
	if err == nil || strings.Contains(err.Error(), "definitely-not-a-key") {
		t.Fatalf("malformed key should fail without echoing the secret: %v", err)
	}
	_, err = Parse(map[string]string{
		"MANAGERR_CREDENTIAL_KEY":      encoded,
		"MANAGERR_CREDENTIAL_KEY_FILE": "/tmp/managerr-key",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected key source conflict, got %v", err)
	}
}

func TestValidateUIAndBounds(t *testing.T) {
	configuration, err := Parse(map[string]string{
		"MANAGERR_UI_API_URL":       "http://managerr-api:8080/api",
		"MANAGERR_UI_PUBLIC_ORIGIN": "https://managerr.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := configuration.ValidateUI(); err != nil {
		t.Fatal(err)
	}
	if err := DefaultBounds().Validate(); err != nil {
		t.Fatal(err)
	}
	tooFast := DefaultBounds()
	tooFast.ScanInterval = 29 * time.Second
	if err := tooFast.Validate(); err == nil {
		t.Fatal("expected scan interval floor")
	}

	if _, err := Parse(map[string]string{"MANAGERR_UI_PUBLIC_ORIGIN": "https://user:secret@example"}); err == nil {
		t.Fatal("expected credentials in public origin to fail")
	}
}
