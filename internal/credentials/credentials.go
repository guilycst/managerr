// Package credentials owns encryption and key lifecycle for managed upstream
// credentials. It deliberately has no dependency on configuration, storage or
// upstream packages so callers must translate their own records at the edge.
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// CurrentEnvelopeVersion is the only envelope format understood by the
	// v0.0.1 credential store.
	CurrentEnvelopeVersion int64 = 1
	keySize                      = 32
	maxKeyFileBytes              = 4 << 10
	keyDirectoryMode             = 0o700
	keyFileMode                  = 0o600
	redactedValue                = "[redacted]"
	keyCheckField                = "__mastarr_key_check__"
	keyCheckPlaintext            = "mastarr credential key check v1"
)

var (
	// ErrInvalidKey identifies missing, malformed or incorrectly sized key
	// material. The error never includes the supplied value.
	ErrInvalidKey = errors.New("credential key is invalid")
	// ErrKeySourceConflict means both explicit key sources were selected.
	ErrKeySourceConflict = errors.New("credential key sources are mutually exclusive")
	// ErrKeyUnavailable means an existing installation needs its original key,
	// but no key material was available and replacement is forbidden.
	ErrKeyUnavailable = errors.New("credential key material is unavailable")
	// ErrInvalidEnvelope identifies a malformed stored envelope.
	ErrInvalidEnvelope = errors.New("credential envelope is invalid")
	// ErrCredentialBinding identifies an invalid or mismatched field binding.
	ErrCredentialBinding = errors.New("credential field binding is invalid")
	// ErrAuthenticationFailed is returned for any authenticated decryption
	// failure, without exposing cipher or plaintext details.
	ErrAuthenticationFailed = errors.New("credential envelope authentication failed")
	// ErrPlaintextEmpty prevents creating a record which cannot represent a
	// configured credential value.
	ErrPlaintextEmpty = errors.New("credential value is empty")
)

// KeySource describes the selected key material source. Values are safe for
// configuration status and logs.
type KeySource string

const (
	KeySourceEnvironment         KeySource = "environment"
	KeySourceSecretFile          KeySource = "secret_file"
	KeySourceGeneratedPersistent KeySource = "generated_persistent"
)

// KeyOptions selects one key source. EnvironmentValue contains the base64
// wire format already read by the bootstrap boundary. EnvironmentProvided
// lets callers distinguish an explicitly empty environment value from an
// unset optional value. ExistingCredentialData blocks silent key generation
// when a database already contains encrypted data or a key-check record.
type KeyOptions struct {
	DataDir                string
	EnvironmentValue       string
	EnvironmentProvided    bool
	KeyFile                string
	KeyFileProvided        bool
	ExistingCredentialData bool
}

// Key contains validated key material and safe source metadata. Its bytes are
// private to this package and are copied before use by Manager.
type Key struct {
	material    []byte
	source      KeySource
	path        string
	fingerprint string
}

// Source reports where key material was selected from.
func (key *Key) Source() KeySource {
	if key == nil {
		return ""
	}
	return key.source
}

// Path reports the generated key path, or an empty string for an environment
// key. Explicit secret-file paths are returned because the path is already
// configuration metadata; secret bytes are never returned.
func (key *Key) Path() string {
	if key == nil {
		return ""
	}
	return key.path
}

// Fingerprint returns a SHA-256 fingerprint suitable for encrypted-record
// metadata. It is not key material.
func (key *Key) Fingerprint() string {
	if key == nil {
		return ""
	}
	return key.fingerprint
}

// Close zeroes key bytes. A Manager created from this key owns an independent
// copy and must be closed separately.
func (key *Key) Close() {
	if key == nil {
		return
	}
	zero(key.material)
	key.material = nil
}

// LoadKey selects and validates explicit key material, or creates/loads the
// persistent key at <data-dir>/keys/credentials.key. It never falls back to a
// generated key after an explicit source fails validation.
func LoadKey(options KeyOptions) (*Key, error) {
	environmentSet := options.EnvironmentProvided || strings.TrimSpace(options.EnvironmentValue) != ""
	fileSet := options.KeyFileProvided || strings.TrimSpace(options.KeyFile) != ""
	if environmentSet && fileSet {
		return nil, ErrKeySourceConflict
	}

	switch {
	case environmentSet:
		material, err := decodeBase64Key(options.EnvironmentValue)
		if err != nil {
			return nil, err
		}
		return newKey(material, KeySourceEnvironment, ""), nil
	case fileSet:
		material, err := readKeyFile(options.KeyFile)
		if err != nil {
			return nil, err
		}
		return newKey(material, KeySourceSecretFile, options.KeyFile), nil
	default:
		path, err := DefaultKeyPath(options.DataDir)
		if err != nil {
			return nil, err
		}
		material, err := loadOrCreatePersistentKey(path, options.ExistingCredentialData)
		if err != nil {
			return nil, err
		}
		return newKey(material, KeySourceGeneratedPersistent, path), nil
	}
}

// DefaultKeyPath returns the stable generated-key location for one data
// directory.
func DefaultKeyPath(dataDir string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return "", errors.New("credential data directory must be an absolute path")
	}
	return filepath.Join(dataDir, "keys", "credentials.key"), nil
}

// NewManager validates raw key material and copies it into a manager. Most
// callers should use Open so source selection and persistence are explicit.
func NewManager(material []byte) (*Manager, error) {
	validated, err := validateRawKey(material)
	if err != nil {
		return nil, err
	}
	return &Manager{
		material:    validated,
		fingerprint: fingerprint(validated),
	}, nil
}

// Open loads key material using options and creates an encryption manager.
func Open(options KeyOptions) (*Manager, *Key, error) {
	key, err := LoadKey(options)
	if err != nil {
		return nil, nil, err
	}
	manager, err := NewManager(key.material)
	if err != nil {
		key.Close()
		return nil, nil, err
	}
	return manager, key, nil
}

// Manager encrypts and decrypts credentials using AES-256-GCM.
type Manager struct {
	material    []byte
	fingerprint string
}

// Fingerprint returns the non-secret key fingerprint stored with envelopes.
func (manager *Manager) Fingerprint() string {
	if manager == nil {
		return ""
	}
	return manager.fingerprint
}

// Close zeroes managed key material. It is safe to call repeatedly.
func (manager *Manager) Close() {
	if manager == nil {
		return
	}
	zero(manager.material)
	manager.material = nil
	manager.fingerprint = ""
}

// Envelope is the database-neutral representation of one encrypted value.
// Nonce and Ciphertext are copied on creation and decryption; callers may
// serialize them into the storage columns without exposing plaintext.
type Envelope struct {
	Version        int64
	Nonce          []byte
	Ciphertext     []byte
	KeyFingerprint string
}

// Validate checks envelope shape without attempting decryption.
func (envelope Envelope) Validate() error {
	if envelope.Version != CurrentEnvelopeVersion {
		return fmt.Errorf("%w: unsupported version", ErrInvalidEnvelope)
	}
	if len(envelope.Nonce) != aesNonceSize() {
		return fmt.Errorf("%w: nonce length", ErrInvalidEnvelope)
	}
	if len(envelope.Ciphertext) <= gcmTagSize() {
		return fmt.Errorf("%w: ciphertext length", ErrInvalidEnvelope)
	}
	if !isFingerprint(envelope.KeyFingerprint) {
		return fmt.Errorf("%w: key fingerprint", ErrInvalidEnvelope)
	}
	return nil
}

// Seal encrypts plaintext bound to one connection and secret field name.
func (manager *Manager) Seal(connectionID, field string, plaintext []byte) (Envelope, error) {
	if err := manager.ready(); err != nil {
		return Envelope{}, err
	}
	if err := validateBinding(connectionID, field); err != nil {
		return Envelope{}, err
	}
	if len(plaintext) == 0 {
		return Envelope{}, ErrPlaintextEmpty
	}
	aead, err := manager.aead()
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, errors.New("generate credential nonce failed")
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, associatedData(connectionID, field))
	return Envelope{
		Version:        CurrentEnvelopeVersion,
		Nonce:          append([]byte(nil), nonce...),
		Ciphertext:     append([]byte(nil), ciphertext...),
		KeyFingerprint: manager.fingerprint,
	}, nil
}

// Open decrypts an envelope only when its key fingerprint and authenticated
// connection/field binding match. Failure never includes ciphertext or
// plaintext details.
func (manager *Manager) Open(envelope Envelope, connectionID, field string) ([]byte, error) {
	if err := manager.ready(); err != nil {
		return nil, err
	}
	if err := validateBinding(connectionID, field); err != nil {
		return nil, err
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	if envelope.KeyFingerprint != manager.fingerprint {
		return nil, ErrAuthenticationFailed
	}
	aead, err := manager.aead()
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData(connectionID, field))
	if err != nil {
		return nil, ErrAuthenticationFailed
	}
	return plaintext, nil
}

// NewKeyCheck creates a non-secret envelope used to validate the active key
// before readiness. Persist this record alongside the encrypted credential
// rows; its plaintext is intentionally private to this package.
func (manager *Manager) NewKeyCheck() (Envelope, error) {
	return manager.Seal("__mastarr__", keyCheckField, []byte(keyCheckPlaintext))
}

// VerifyKeyCheck authenticates a persisted key-check envelope. It does not
// return the check plaintext, and therefore cannot accidentally render it.
func (manager *Manager) VerifyKeyCheck(envelope Envelope) error {
	plaintext, err := manager.Open(envelope, "__mastarr__", keyCheckField)
	if err != nil {
		return err
	}
	defer zero(plaintext)
	if string(plaintext) != keyCheckPlaintext {
		return ErrAuthenticationFailed
	}
	return nil
}

// CredentialMetadata is safe to return from API/configuration reads. It
// contains no secret value or encrypted payload.
type CredentialMetadata struct {
	ConnectionID    string
	Field           string
	EnvelopeVersion int64
	KeyFingerprint  string
	Configured      bool
}

// Metadata returns redacted state for an encrypted envelope.
func Metadata(envelope Envelope, connectionID, field string) (CredentialMetadata, error) {
	if err := validateBinding(connectionID, field); err != nil {
		return CredentialMetadata{}, err
	}
	if err := envelope.Validate(); err != nil {
		return CredentialMetadata{}, err
	}
	return CredentialMetadata{
		ConnectionID:    connectionID,
		Field:           field,
		EnvelopeVersion: envelope.Version,
		KeyFingerprint:  envelope.KeyFingerprint,
		Configured:      true,
	}, nil
}

// Redact replaces a credential value with a stable marker. Empty values stay
// empty so optional fields remain distinguishable without exposing content.
func Redact(value string) string {
	if value == "" {
		return ""
	}
	return redactedValue
}

// RedactedValue is the marker used by Redact and safe diagnostics.
func RedactedValue() string { return redactedValue }

func (manager *Manager) ready() error {
	if manager == nil || len(manager.material) != keySize || !isFingerprint(manager.fingerprint) {
		return ErrInvalidKey
	}
	return nil
}

func (manager *Manager) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(manager.material)
	if err != nil {
		return nil, ErrInvalidKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("create credential cipher failed")
	}
	return aead, nil
}

func aesNonceSize() int { return 12 }

func gcmTagSize() int { return 16 }

func validateBinding(connectionID, field string) error {
	if strings.TrimSpace(connectionID) == "" || strings.TrimSpace(field) == "" {
		return ErrCredentialBinding
	}
	if strings.ContainsRune(connectionID, '\x00') || strings.ContainsRune(field, '\x00') {
		return ErrCredentialBinding
	}
	return nil
}

func associatedData(connectionID, field string) []byte {
	// Length-prefix each component so distinct IDs cannot produce the same
	// binding through concatenation ambiguity.
	data := make([]byte, 0, len(connectionID)+len(field)+32)
	data = append(data, "mastarr credential envelope v1"...)
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(connectionID)))
	data = append(data, length[:]...)
	data = append(data, connectionID...)
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	data = append(data, length[:]...)
	data = append(data, field...)
	return data
}

func validateRawKey(material []byte) ([]byte, error) {
	if len(material) != keySize {
		return nil, ErrInvalidKey
	}
	return append([]byte(nil), material...), nil
}

func decodeBase64Key(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, ErrInvalidKey
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidKey
	}
	return validateRawKey(decoded)
}

func newKey(material []byte, source KeySource, path string) *Key {
	return &Key{material: material, source: source, path: path, fingerprint: fingerprint(material)}
}

func fingerprint(material []byte) string {
	digest := sha256.Sum256(material)
	return hex.EncodeToString(digest[:])
}

func isFingerprint(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func readKeyFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return nil, ErrInvalidKey
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrKeyUnavailable
		}
		return nil, errors.New("read credential key file failed")
	}
	if !info.Mode().IsRegular() {
		return nil, ErrInvalidKey
	}
	contents, err := readBoundedFile(path)
	if err != nil {
		return nil, err
	}
	return decodeBase64Key(string(contents))
}

func loadOrCreatePersistentKey(path string, existingData bool) ([]byte, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, ErrInvalidKey
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("persistent credential key file permissions are too broad")
		}
		contents, err := readBoundedFile(path)
		if err != nil {
			return nil, err
		}
		return decodeBase64Key(string(contents))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("inspect persistent credential key failed")
	}
	if existingData {
		return nil, ErrKeyUnavailable
	}
	return createPersistentKey(path)
}

func createPersistentKey(path string) ([]byte, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, keyDirectoryMode); err != nil {
		return nil, errors.New("create credential key directory failed")
	}
	if err := os.Chmod(directory, keyDirectoryMode); err != nil {
		return nil, errors.New("secure credential key directory failed")
	}
	material := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, material); err != nil {
		return nil, errors.New("generate credential key failed")
	}
	encoded := base64.StdEncoding.EncodeToString(material) + "\n"
	temporary, err := os.CreateTemp(directory, ".credentials.key.*.tmp")
	if err != nil {
		zero(material)
		return nil, errors.New("create temporary credential key failed")
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(keyFileMode); err != nil {
		zero(material)
		return nil, errors.New("secure temporary credential key failed")
	}
	if _, err := io.WriteString(temporary, encoded); err != nil {
		zero(material)
		return nil, errors.New("write temporary credential key failed")
	}
	if err := temporary.Sync(); err != nil {
		zero(material)
		return nil, errors.New("sync temporary credential key failed")
	}
	if err := temporary.Close(); err != nil {
		zero(material)
		return nil, errors.New("close temporary credential key failed")
	}
	if err := os.Link(temporaryPath, path); err != nil {
		zero(material)
		if errors.Is(err, os.ErrExist) {
			return loadOrCreatePersistentKey(path, true)
		}
		return nil, errors.New("publish persistent credential key failed")
	}
	if err := syncDirectory(directory); err != nil {
		zero(material)
		return nil, err
	}
	return material, nil
}

func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrKeyUnavailable
		}
		return nil, errors.New("open credential key failed")
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
	if err != nil {
		return nil, errors.New("read credential key failed")
	}
	if len(contents) > maxKeyFileBytes {
		return nil, ErrInvalidKey
	}
	return contents, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return errors.New("open credential key directory for sync failed")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("sync credential key directory failed")
	}
	return nil
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
