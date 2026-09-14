package credentials

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoadKeyGeneratesStablePersistentKey(t *testing.T) {
	dataDir := t.TempDir()
	key, err := LoadKey(KeyOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if key.Source() != KeySourceGeneratedPersistent {
		t.Fatalf("source = %q", key.Source())
	}
	path, err := DefaultKeyPath(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if key.Path() != path || !isFingerprint(key.Fingerprint()) {
		t.Fatalf("key metadata = path %q fingerprint %q", key.Path(), key.Fingerprint())
	}
	key.Close()

	keyBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(keyBytes) != base64.StdEncoding.EncodedLen(keySize)+1 || keyBytes[len(keyBytes)-1] != '\n' {
		t.Fatalf("persistent key format = %q", keyBytes)
	}
	if _, err := decodeBase64Key(string(keyBytes)); err != nil {
		t.Fatalf("persistent key did not decode: %v", err)
	}
	keyInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := keyInfo.Mode().Perm(); got != keyFileMode {
		t.Fatalf("key mode = %o, want %o", got, keyFileMode)
	}
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := directoryInfo.Mode().Perm(); got != keyDirectoryMode {
		t.Fatalf("key directory mode = %o, want %o", got, keyDirectoryMode)
	}

	restarted, err := LoadKey(KeyOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.Fingerprint() != fingerprint(mustDecodeKey(t, string(keyBytes))) {
		t.Fatal("restart did not load the same key")
	}
}

func TestLoadKeyExplicitSourcesAndNoFallback(t *testing.T) {
	material := bytes.Repeat([]byte{0x42}, keySize)
	encoded := base64.StdEncoding.EncodeToString(material)

	fromEnvironment, err := LoadKey(KeyOptions{EnvironmentValue: encoded})
	if err != nil {
		t.Fatal(err)
	}
	if fromEnvironment.Source() != KeySourceEnvironment || fromEnvironment.Fingerprint() != fingerprint(material) {
		t.Fatalf("environment key metadata = %#v", fromEnvironment)
	}
	fromEnvironment.Close()

	keyFile := filepath.Join(t.TempDir(), "secret-key")
	if err := os.WriteFile(keyFile, []byte(encoded+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	fromFile, err := LoadKey(KeyOptions{KeyFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	if fromFile.Source() != KeySourceSecretFile || fromFile.Path() != keyFile {
		t.Fatalf("file key metadata = source %q path %q", fromFile.Source(), fromFile.Path())
	}
	fromFile.Close()

	if _, err := LoadKey(KeyOptions{EnvironmentValue: encoded, KeyFile: keyFile}); !errors.Is(err, ErrKeySourceConflict) {
		t.Fatalf("source conflict = %v", err)
	}
	if _, err := LoadKey(KeyOptions{EnvironmentValue: "wrong-value"}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid environment key = %v", err)
	}
	if _, err := LoadKey(KeyOptions{KeyFile: filepath.Join(t.TempDir(), "missing-key")}); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("missing explicit key = %v", err)
	}

	dataDir := t.TempDir()
	if _, err := LoadKey(KeyOptions{DataDir: dataDir, ExistingCredentialData: true}); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("existing data without key = %v", err)
	}
	if _, err := LoadKey(KeyOptions{DataDir: dataDir, EnvironmentProvided: true}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("empty explicit environment key = %v", err)
	}
	if _, err := LoadKey(KeyOptions{DataDir: dataDir, KeyFileProvided: true}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("empty explicit file path = %v", err)
	}
}

func TestPersistentKeyGenerationIsExclusive(t *testing.T) {
	dataDir := t.TempDir()
	const callers = 16
	keys := make(chan string, callers)
	errs := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer group.Done()
			key, err := LoadKey(KeyOptions{DataDir: dataDir})
			if err != nil {
				errs <- err
				return
			}
			keys <- key.Fingerprint()
			key.Close()
		}()
	}
	group.Wait()
	close(keys)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first string
	for keyFingerprint := range keys {
		if first == "" {
			first = keyFingerprint
			continue
		}
		if keyFingerprint != first {
			t.Fatalf("concurrent key fingerprints differ: %q and %q", first, keyFingerprint)
		}
	}
}

func TestEnvelopeBindsCredentialFieldAndAuthenticates(t *testing.T) {
	manager, err := NewManager(bytes.Repeat([]byte{0x17}, keySize))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	plaintext := []byte("synthetic-api-key")
	envelope, err := manager.Seal("radarr-main", "api_key", plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(envelope.Ciphertext, plaintext) || len(envelope.Nonce) != aesNonceSize() {
		t.Fatal("credential was not encrypted with a fresh nonce")
	}
	opened, err := manager.Open(envelope, "radarr-main", "api_key")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened plaintext = %q", opened)
	}

	for _, test := range []struct {
		name         string
		connectionID string
		field        string
		mutate       func(*Envelope)
		wantError    error
	}{
		{name: "wrong connection", connectionID: "sonarr-main", field: "api_key", wantError: ErrAuthenticationFailed},
		{name: "wrong field", connectionID: "radarr-main", field: "password", wantError: ErrAuthenticationFailed},
		{name: "ciphertext tamper", connectionID: "radarr-main", field: "api_key", mutate: func(value *Envelope) { value.Ciphertext[0] ^= 1 }, wantError: ErrAuthenticationFailed},
		{name: "fingerprint tamper", connectionID: "radarr-main", field: "api_key", mutate: func(value *Envelope) { value.KeyFingerprint = strings.Repeat("0", sha256SizeHex) }, wantError: ErrAuthenticationFailed},
		{name: "version tamper", connectionID: "radarr-main", field: "api_key", mutate: func(value *Envelope) { value.Version++ }, wantError: ErrInvalidEnvelope},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := envelope
			candidate.Nonce = append([]byte(nil), envelope.Nonce...)
			candidate.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			_, err := manager.Open(candidate, test.connectionID, test.field)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
		})
	}

	if _, err := manager.Seal("", "api_key", plaintext); !errors.Is(err, ErrCredentialBinding) {
		t.Fatalf("empty connection binding = %v", err)
	}
	if _, err := manager.Seal("radarr-main", "", plaintext); !errors.Is(err, ErrCredentialBinding) {
		t.Fatalf("empty field binding = %v", err)
	}
	if _, err := manager.Seal("radarr\x00main", "api_key", plaintext); !errors.Is(err, ErrCredentialBinding) {
		t.Fatalf("NUL connection binding = %v", err)
	}
	if _, err := manager.Seal("radarr-main", "api_key", nil); !errors.Is(err, ErrPlaintextEmpty) {
		t.Fatalf("empty credential = %v", err)
	}
}

func TestKeyCheckMetadataAndRedaction(t *testing.T) {
	manager, err := NewManager(bytes.Repeat([]byte{0xa5}, keySize))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	check, err := manager.NewKeyCheck()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.VerifyKeyCheck(check); err != nil {
		t.Fatal(err)
	}
	check.Ciphertext[0] ^= 1
	if err := manager.VerifyKeyCheck(check); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("tampered key check = %v", err)
	}

	envelope, err := manager.Seal("nzbget-main", "password", []byte("synthetic-secret"))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := Metadata(envelope, "nzbget-main", "password")
	if err != nil {
		t.Fatal(err)
	}
	if !metadata.Configured || metadata.ConnectionID != "nzbget-main" || metadata.Field != "password" || metadata.KeyFingerprint != manager.Fingerprint() {
		t.Fatalf("metadata = %#v", metadata)
	}
	if got := Redact("synthetic-secret"); got != redactedValue {
		t.Fatalf("redaction = %q", got)
	}
	if got := Redact(""); got != "" {
		t.Fatalf("empty redaction = %q", got)
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedMetadata), "synthetic-secret") {
		t.Fatal("credential value leaked through metadata")
	}
}

func TestInvalidPersistentKeyNeverRegenerates(t *testing.T) {
	dataDir := t.TempDir()
	path, err := DefaultKeyPath(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), keyDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-a-key\n"), keyFileMode); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(KeyOptions{DataDir: dataDir}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid persistent key = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid persistent key was replaced")
	}
}

const sha256SizeHex = 64

func mustDecodeKey(t *testing.T, value string) []byte {
	t.Helper()
	key, err := decodeBase64Key(value)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
