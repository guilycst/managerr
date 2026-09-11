// Package domain contains the values and state rules shared by the API,
// workers, storage and adapters.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// RuntimeID identifies a persisted runtime resource. It is an RFC 4122
// version 4 UUID and is intentionally separate from a configuration ID.
type RuntimeID string

func (id RuntimeID) String() string { return string(id) }

// NewRuntimeID returns a random version 4 UUID.
func NewRuntimeID() (RuntimeID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate runtime id: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	var encoded [36]byte
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return RuntimeID(encoded[:]), nil
}

// ParseRuntimeID validates and canonicalizes a UUID string.
func ParseRuntimeID(value string) (RuntimeID, error) {
	if strings.TrimSpace(value) != value {
		return "", errors.New("runtime id must be a UUID")
	}
	value = strings.ToLower(value)
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", errors.New("runtime id must be a UUID")
	}
	var raw [16]byte
	if _, err := hex.Decode(raw[0:4], []byte(value[0:8])); err != nil {
		return "", errors.New("runtime id must be a UUID")
	}
	if _, err := hex.Decode(raw[4:6], []byte(value[9:13])); err != nil {
		return "", errors.New("runtime id must be a UUID")
	}
	if _, err := hex.Decode(raw[6:8], []byte(value[14:18])); err != nil {
		return "", errors.New("runtime id must be a UUID")
	}
	if _, err := hex.Decode(raw[8:10], []byte(value[19:23])); err != nil {
		return "", errors.New("runtime id must be a UUID")
	}
	if _, err := hex.Decode(raw[10:16], []byte(value[24:])); err != nil {
		return "", errors.New("runtime id must be a UUID")
	}
	return RuntimeID(value), nil
}

// Valid reports whether id is a canonical UUID string.
func (id RuntimeID) Valid() bool {
	_, err := ParseRuntimeID(string(id))
	return err == nil
}

// ConfigID identifies a user-facing configuration resource. It is stable
// across restarts and is not a runtime UUID.
type ConfigID string

func (id ConfigID) String() string { return string(id) }

// ParseConfigID validates a stable configuration identifier.
func ParseConfigID(value string) (ConfigID, error) {
	if value == "" || len(value) > 63 {
		return "", errors.New("config id must contain 1 to 63 characters")
	}
	if !isConfigIDEdge(value[0]) {
		return "", errors.New("config id must start with a lowercase letter or digit")
	}
	if !isConfigIDEdge(value[len(value)-1]) {
		return "", errors.New("config id must end with a lowercase letter or digit")
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return "", errors.New("config id contains an unsupported character")
	}
	return ConfigID(value), nil
}

func isConfigIDEdge(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
}

// Valid reports whether id satisfies the stable configuration ID contract.
func (id ConfigID) Valid() bool {
	_, err := ParseConfigID(string(id))
	return err == nil
}

// ConfigRef binds a connection or root to the configuration revision used by
// an observation or an approved action.
type ConfigRef struct {
	ID       ConfigID
	Revision string
}

// Validate checks both the stable ID and its authority-bearing revision.
func (ref ConfigRef) Validate() error {
	if !ref.ID.Valid() {
		return errors.New("config reference has an invalid id")
	}
	if strings.TrimSpace(ref.Revision) == "" {
		return errors.New("config reference requires a revision")
	}
	return nil
}
