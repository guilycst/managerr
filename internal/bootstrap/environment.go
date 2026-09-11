// Package bootstrap owns process-start configuration. It parses environment
// variables once and keeps secret values out of validation errors.
package bootstrap

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

//go:generate sh -c "go tool -modfile=../../tools/go.mod github.com/g4s8/envdoc -output ../../docs/generated/environment.md -types=Environment && python3 -c \"from pathlib import Path; p = Path('../../docs/generated/environment.md'); p.write_text(p.read_text().rstrip(chr(10)) + chr(10))\""

// Environment is the process bootstrap contract. UI values are optional for
// the API binary and are required by ValidateUI when starting the BFF.
type Environment struct {
	// Persistent database, key, descriptor and journal directory.
	DataDir string `env:"MASTARR_DATA_DIR" envDefault:"/data"`
	// Optional startup-only YAML configuration file. Empty selects API-managed configuration.
	ConfigFile string `env:"MASTARR_CONFIG_FILE" envDefault:""`
	// API listener address.
	ListenAddr string `env:"MASTARR_LISTEN_ADDR" envDefault:":8080"`
	// Optional base64-encoded 32-byte credential encryption key. Never log this value.
	CredentialKey string `env:"MASTARR_CREDENTIAL_KEY"`
	// Optional file containing the same base64-encoded key. Mutually exclusive with CredentialKey.
	CredentialKeyFile string `env:"MASTARR_CREDENTIAL_KEY_FILE"`
	// Structured log level: info, debug, warn or error.
	LogLevel string `env:"MASTARR_LOG_LEVEL" envDefault:"info"`
	// API URL used by the BFF. Required when ValidateUI is called.
	UIAPIURL string `env:"MASTARR_UI_API_URL"`
	// BFF listener address.
	UIListenAddr string `env:"MASTARR_UI_LISTEN_ADDR" envDefault:":8081"`
	// Absolute public origin used for BFF metadata and origin checks. Required by ValidateUI.
	UIPublicOrigin string `env:"MASTARR_UI_PUBLIC_ORIGIN"`
}

// Parse parses a supplied environment map. The map seam keeps tests and
// embedding callers deterministic while Load performs the one OS read.
func Parse(values map[string]string) (Environment, error) {
	if values == nil {
		values = map[string]string{}
	}
	configuration, err := env.ParseAsWithOptions[Environment](env.Options{Environment: values})
	if err != nil {
		return Environment{}, sanitizeParseError(err)
	}
	if err := configuration.Validate(); err != nil {
		return Environment{}, err
	}
	return configuration, nil
}

// Load reads the operating system environment once and validates it.
func Load() (Environment, error) {
	return Parse(env.ToMap(os.Environ()))
}

func sanitizeParseError(err error) error {
	var parseErr env.ParseError
	if errors.As(err, &parseErr) {
		return fmt.Errorf("invalid environment field %q", parseErr.Name)
	}
	var missingErr env.VarIsNotSetError
	if errors.As(err, &missingErr) {
		return fmt.Errorf("required environment variable %q is not set", missingErr.Key)
	}
	var emptyErr env.EmptyVarError
	if errors.As(err, &emptyErr) {
		return fmt.Errorf("environment variable %q must not be empty", emptyErr.Key)
	}
	return errors.New("invalid environment configuration")
}

// Validate checks API bootstrap values. It never includes a secret value in
// an error string.
func (configuration Environment) Validate() error {
	if strings.TrimSpace(configuration.DataDir) == "" || !filepath.IsAbs(configuration.DataDir) {
		return errors.New("MASTARR_DATA_DIR must be a non-empty absolute path")
	}
	if configuration.ConfigFile != "" && !filepath.IsAbs(configuration.ConfigFile) {
		return errors.New("MASTARR_CONFIG_FILE must be an absolute path")
	}
	if err := validateListenAddr(configuration.ListenAddr); err != nil {
		return fmt.Errorf("MASTARR_LISTEN_ADDR: %w", err)
	}
	if err := validateListenAddr(configuration.UIListenAddr); err != nil {
		return fmt.Errorf("MASTARR_UI_LISTEN_ADDR: %w", err)
	}
	switch configuration.LogLevel {
	case "info", "debug", "warn", "error":
	default:
		return errors.New("MASTARR_LOG_LEVEL must be one of info, debug, warn or error")
	}
	if strings.TrimSpace(configuration.CredentialKey) != "" && strings.TrimSpace(configuration.CredentialKeyFile) != "" {
		return errors.New("MASTARR_CREDENTIAL_KEY and MASTARR_CREDENTIAL_KEY_FILE are mutually exclusive")
	}
	if strings.TrimSpace(configuration.CredentialKey) != "" {
		if _, err := DecodeCredentialKey(configuration.CredentialKey); err != nil {
			return errors.New("MASTARR_CREDENTIAL_KEY must be base64 for exactly 32 bytes")
		}
	}
	if strings.TrimSpace(configuration.CredentialKeyFile) != "" && !filepath.IsAbs(configuration.CredentialKeyFile) {
		return errors.New("MASTARR_CREDENTIAL_KEY_FILE must be an absolute path")
	}
	if configuration.UIAPIURL != "" {
		if err := validateServiceURL(configuration.UIAPIURL, false); err != nil {
			return fmt.Errorf("MASTARR_UI_API_URL: %w", err)
		}
	}
	if configuration.UIPublicOrigin != "" {
		if err := validateServiceURL(configuration.UIPublicOrigin, true); err != nil {
			return fmt.Errorf("MASTARR_UI_PUBLIC_ORIGIN: %w", err)
		}
	}
	return nil
}

// ValidateUI applies the BFF-only required fields after Validate.
func (configuration Environment) ValidateUI() error {
	if err := configuration.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(configuration.UIAPIURL) == "" {
		return errors.New("MASTARR_UI_API_URL is required for the BFF")
	}
	if strings.TrimSpace(configuration.UIPublicOrigin) == "" {
		return errors.New("MASTARR_UI_PUBLIC_ORIGIN is required for the BFF")
	}
	return nil
}

func validateListenAddr(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("listener address is required")
	}
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return errors.New("listener address must include a numeric port")
	}
	if port == "" {
		return errors.New("listener address must include a numeric port")
	}
	for _, char := range port {
		if char < '0' || char > '9' {
			return errors.New("listener port must be numeric")
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return errors.New("listener port must be between 0 and 65535")
	}
	return nil
}

func validateServiceURL(value string, origin bool) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return errors.New("must be an absolute HTTP(S) URL without credentials")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("must use http or https")
	}
	if origin && (parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/") {
		return errors.New("public origin cannot include a path, query or fragment")
	}
	return nil
}

// KeySource describes where the credential encryption key comes from.
type KeySource string

const (
	KeySourceEnvironment         KeySource = "environment"
	KeySourceSecretFile          KeySource = "secret_file"
	KeySourceGeneratedPersistent KeySource = "generated_persistent"
)

// CredentialKeySource applies the explicit key precedence rule without reading
// a secret file. The startup key loader owns file permissions and generation.
func (configuration Environment) CredentialKeySource() (KeySource, error) {
	hasEnv := strings.TrimSpace(configuration.CredentialKey) != ""
	hasFile := strings.TrimSpace(configuration.CredentialKeyFile) != ""
	if hasEnv && hasFile {
		return "", errors.New("credential key environment and file are mutually exclusive")
	}
	if hasEnv {
		if _, err := DecodeCredentialKey(configuration.CredentialKey); err != nil {
			return "", errors.New("credential key environment value is invalid")
		}
		return KeySourceEnvironment, nil
	}
	if hasFile {
		return KeySourceSecretFile, nil
	}
	return KeySourceGeneratedPersistent, nil
}

// DecodeCredentialKey validates the non-secret wire representation of a key.
// Callers should zero the returned bytes after constructing their cipher.
func DecodeCredentialKey(encoded string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("credential key must decode to 32 bytes")
	}
	return decoded, nil
}

// Bounds are the typed effective limits shared by scans and action workers.
// They deliberately use a small fixed default set until managed policy is
// implemented by the storage/configuration lanes.
type Bounds struct {
	MaxWorkers            int
	QueueCapacity         int
	MaxManifestEntries    int
	MaxManifestBytes      int64
	RequestTimeout        time.Duration
	ScanInterval          time.Duration
	FileStabilityInterval time.Duration
	StableObservations    int
	CoverageMaxAge        time.Duration
	TrashRetention        time.Duration
	JanitorInterval       time.Duration
}

// DefaultBounds returns the v0.0.1 worker and storage policy defaults.
func DefaultBounds() Bounds {
	return Bounds{
		MaxWorkers:            4,
		QueueCapacity:         256,
		MaxManifestEntries:    10_000,
		MaxManifestBytes:      1 << 30,
		RequestTimeout:        30 * time.Second,
		ScanInterval:          5 * time.Minute,
		FileStabilityInterval: 30 * time.Second,
		StableObservations:    2,
		CoverageMaxAge:        15 * time.Minute,
		TrashRetention:        30 * 24 * time.Hour,
		JanitorInterval:       time.Hour,
	}
}

// Validate rejects bounds that could disable safety admission or stability
// checks. The scan interval is never allowed below the selected 30-second floor.
func (bounds Bounds) Validate() error {
	if bounds.MaxWorkers < 1 || bounds.QueueCapacity < 1 || bounds.MaxManifestEntries < 1 || bounds.MaxManifestBytes < 1 {
		return errors.New("worker and manifest bounds must be positive")
	}
	if bounds.RequestTimeout <= 0 || bounds.ScanInterval < 30*time.Second || bounds.FileStabilityInterval < 30*time.Second || bounds.StableObservations < 2 || bounds.CoverageMaxAge <= 0 || bounds.TrashRetention <= 0 || bounds.JanitorInterval <= 0 {
		return errors.New("worker and storage durations do not meet safety minimums")
	}
	return nil
}
