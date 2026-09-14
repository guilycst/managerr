package configuration

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
)

// ParseOptions controls one deterministic YAML parse. StartupAt is supplied by
// the process boundary so source metadata remains stable for the process.
type ParseOptions struct {
	DocumentID       string
	StartupAt        time.Time
	Environment      map[string]string
	SecretResolver   SecretResolver
	MaxDocumentBytes int
}

// SecretResolver resolves references permitted by startup YAML. API mutation
// methods never receive this interface, preventing arbitrary API-selected file
// reads.
type SecretResolver interface {
	Environment(name string) (string, bool)
	File(path string) ([]byte, error)
}

// EnvironmentResolver captures environment values once. File references use
// the host resolver's bounded regular-file implementation.
type EnvironmentResolver struct {
	values map[string]string
}

// NewEnvironmentResolver snapshots supplied values. A nil map means use the
// process environment at each lookup; callers should pass a map at startup to
// keep all references in one immutable source snapshot.
func NewEnvironmentResolver(values map[string]string) *EnvironmentResolver {
	var copyValues map[string]string
	if values != nil {
		copyValues = make(map[string]string, len(values))
		for key, value := range values {
			copyValues[key] = value
		}
	}
	return &EnvironmentResolver{values: copyValues}
}

func (resolver *EnvironmentResolver) Environment(name string) (string, bool) {
	if resolver == nil {
		return "", false
	}
	if resolver.values != nil {
		value, exists := resolver.values[name]
		return value, exists
	}
	return os.LookupEnv(name)
}

func (resolver *EnvironmentResolver) File(filePath string) ([]byte, error) {
	if resolver == nil {
		return nil, ErrCredentialUnavailable
	}
	file, err := openBoundedRegular(filePath, maxSecretFileBytes)
	if err != nil {
		return nil, ErrCredentialUnavailable
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maxSecretFileBytes+1))
	if err != nil || len(value) > maxSecretFileBytes {
		zero(value)
		return nil, ErrCredentialUnavailable
	}
	return value, nil
}

type yamlDocument struct {
	Version      int               `yaml:"version"`
	Connections  []yamlConnection  `yaml:"connections"`
	StorageRoots []yamlStorageRoot `yaml:"storageRoots"`
	PathMappings []yamlPathMapping `yaml:"pathMappings"`
	Trash        *yamlTrash        `yaml:"trash"`
}

type yamlConnection struct {
	ID          string                    `yaml:"id"`
	Kind        string                    `yaml:"kind"`
	Label       string                    `yaml:"label"`
	Endpoint    string                    `yaml:"endpoint"`
	Credentials map[string]yamlCredential `yaml:"credentials"`
}

type yamlCredential struct {
	Environment *string `yaml:"env"`
	File        *string `yaml:"file"`
}

type yamlStorageRoot struct {
	ID           string             `yaml:"id"`
	Label        string             `yaml:"label"`
	Purpose      string             `yaml:"purpose"`
	Path         string             `yaml:"path"`
	ReadOnly     bool               `yaml:"readOnly"`
	Capabilities []yamlCapability   `yaml:"capabilities"`
	Watch        *yamlWatchSettings `yaml:"watch"`
}

type yamlCapability struct {
	Name       string   `yaml:"name"`
	State      string   `yaml:"state"`
	Version    string   `yaml:"version"`
	Reason     string   `yaml:"reason"`
	Evidence   []string `yaml:"evidence"`
	ObservedAt string   `yaml:"observedAt"`
}

type yamlWatchSettings struct {
	Enabled  bool   `yaml:"enabled"`
	Interval string `yaml:"interval"`
}

type yamlPathMapping struct {
	ID                string `yaml:"id"`
	ConnectionID      string `yaml:"connectionId"`
	RootID            string `yaml:"rootId"`
	SourcePrefix      string `yaml:"sourcePrefix"`
	DestinationPrefix string `yaml:"destinationPrefix"`
	RelativePrefix    string `yaml:"relativePrefix"`
	ExternalPrefix    string `yaml:"externalPrefix"`
}

type yamlTrash struct {
	Retention       string `yaml:"retention"`
	JanitorInterval string `yaml:"janitorInterval"`
}

func (raw yamlConnection) toDomain(source domain.SourceMetadata, resolver SecretResolver) (connection domain.Connection, values map[string][]byte, err error) {
	defer func() {
		if err == nil {
			return
		}
		for key, value := range values {
			zero(value)
			delete(values, key)
		}
	}()
	id, err := domain.ParseConfigID(strings.TrimSpace(raw.ID))
	if err != nil {
		return domain.Connection{}, nil, fmt.Errorf("%w: connection id", ErrInvalidDocument)
	}
	connection = domain.Connection{ID: id, Kind: domain.ConnectionKind(raw.Kind), Label: raw.Label, Endpoint: raw.Endpoint, Source: source, Credentials: make(map[string]domain.CredentialReference, len(raw.Credentials))}
	values = make(map[string][]byte, len(raw.Credentials))
	for field, rawCredential := range raw.Credentials {
		rawField := field
		field = strings.TrimSpace(field)
		if err := validateCredentialField(rawField); err != nil {
			return domain.Connection{}, nil, fmt.Errorf("%w: credential field is empty", ErrCredentialInvalid)
		}
		if (rawCredential.Environment == nil) == (rawCredential.File == nil) {
			return domain.Connection{}, nil, fmt.Errorf("%w: credential %q must contain exactly one env or file reference", ErrCredentialInvalid, field)
		}
		var reference domain.CredentialReference
		var value []byte
		if rawCredential.Environment != nil {
			name := strings.TrimSpace(*rawCredential.Environment)
			if name == "" {
				return domain.Connection{}, nil, fmt.Errorf("%w: credential %q environment reference is empty", ErrCredentialInvalid, field)
			}
			reference = domain.CredentialReference{Kind: domain.CredentialFromEnvironment, Value: name}
			resolved, exists := resolver.Environment(name)
			if !exists || resolved == "" {
				return domain.Connection{}, nil, fmt.Errorf("%w: credential %q environment reference is unavailable", ErrCredentialUnavailable, field)
			}
			if len(resolved) > maxSecretFileBytes {
				return domain.Connection{}, nil, fmt.Errorf("%w: credential %q environment reference is too large", ErrCredentialInvalid, field)
			}
			value = []byte(resolved)
		} else {
			filePath := strings.TrimSpace(*rawCredential.File)
			reference = domain.CredentialReference{Kind: domain.CredentialFromFile, Value: filePath}
			if err := reference.Validate(); err != nil {
				return domain.Connection{}, nil, fmt.Errorf("%w: credential %q file reference", ErrCredentialInvalid, field)
			}
			resolved, resolveErr := resolver.File(filePath)
			if resolveErr != nil || len(resolved) == 0 || len(resolved) > maxSecretFileBytes {
				zero(resolved)
				return domain.Connection{}, nil, fmt.Errorf("%w: credential %q file reference is unavailable", ErrCredentialUnavailable, field)
			}
			value = resolved
		}
		connection.Credentials[field] = reference
		values[field] = value
	}
	connection.Revision = connectionRevision(connection)
	if err := connection.Validate(); err != nil {
		for key, value := range values {
			zero(value)
			delete(values, key)
		}
		return domain.Connection{}, nil, fmt.Errorf("%w: YAML connection", ErrInvalidDocument)
	}
	return connection, values, nil
}

func (raw yamlStorageRoot) toDomain(source domain.SourceMetadata) (domain.StorageRoot, error) {
	id, err := domain.ParseConfigID(strings.TrimSpace(raw.ID))
	if err != nil {
		return domain.StorageRoot{}, fmt.Errorf("%w: storage root id", ErrInvalidDocument)
	}
	root := domain.StorageRoot{ID: id, Label: raw.Label, Purpose: domain.StoragePurpose(raw.Purpose), Path: raw.Path, ReadOnly: raw.ReadOnly, Source: source}
	root.Watch = domain.WatchSettings{}
	if raw.Watch != nil {
		interval := time.Duration(0)
		if strings.TrimSpace(raw.Watch.Interval) != "" {
			parsedInterval, parseErr := time.ParseDuration(strings.TrimSpace(raw.Watch.Interval))
			if parseErr != nil || parsedInterval < 0 {
				return domain.StorageRoot{}, fmt.Errorf("%w: storage root watch interval", ErrInvalidDocument)
			}
			interval = parsedInterval
		}
		root.Watch = domain.WatchSettings{Enabled: raw.Watch.Enabled, Interval: interval}
	}
	for _, rawCapability := range raw.Capabilities {
		observedAt, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(rawCapability.ObservedAt))
		if parseErr != nil {
			return domain.StorageRoot{}, fmt.Errorf("%w: storage root capability timestamp", ErrInvalidDocument)
		}
		root.Capabilities = append(root.Capabilities, domain.Capability{Name: rawCapability.Name, State: domain.CapabilityState(rawCapability.State), Version: rawCapability.Version, Reason: rawCapability.Reason, Evidence: slicesClone(rawCapability.Evidence), ObservedAt: observedAt.UTC()})
	}
	root.Revision = rootRevision(root)
	if err := root.Validate(); err != nil {
		return domain.StorageRoot{}, fmt.Errorf("%w: YAML storage root", ErrInvalidDocument)
	}
	if err := validateStorageRootPath(root.Path); err != nil {
		return domain.StorageRoot{}, err
	}
	for _, capability := range root.Capabilities {
		if err := capability.Validate(); err != nil {
			return domain.StorageRoot{}, fmt.Errorf("%w: YAML storage root capability", ErrInvalidDocument)
		}
	}
	return root, nil
}

func (raw yamlPathMapping) toDomain(source domain.SourceMetadata) (domain.PathMapping, error) {
	id, err := domain.ParseConfigID(strings.TrimSpace(raw.ID))
	if err != nil {
		return domain.PathMapping{}, fmt.Errorf("%w: path mapping id", ErrInvalidDocument)
	}
	connectionID, err := domain.ParseConfigID(strings.TrimSpace(raw.ConnectionID))
	if err != nil {
		return domain.PathMapping{}, fmt.Errorf("%w: path mapping connection id", ErrInvalidDocument)
	}
	rootID, err := domain.ParseConfigID(strings.TrimSpace(raw.RootID))
	if err != nil {
		return domain.PathMapping{}, fmt.Errorf("%w: path mapping root id", ErrInvalidDocument)
	}
	// Canonical API names are sourcePrefix/destinationPrefix. The original
	// startup example used externalPrefix/relativePrefix, respectively, so
	// preserve those aliases without reversing their namespace semantics.
	sourcePrefix, err := chooseAlias(raw.SourcePrefix, raw.ExternalPrefix)
	if err != nil {
		return domain.PathMapping{}, fmt.Errorf("%w: path mapping source prefix", ErrInvalidDocument)
	}
	destinationPrefix, err := chooseAlias(raw.DestinationPrefix, raw.RelativePrefix)
	if err != nil {
		return domain.PathMapping{}, fmt.Errorf("%w: path mapping destination prefix", ErrInvalidDocument)
	}
	mapping := domain.PathMapping{ID: id, ConnectionID: connectionID, SourcePrefix: sourcePrefix, RootID: rootID, DestinationPrefix: destinationPrefix, Source: source}
	mapping.Revision = mappingRevision(mapping)
	if err := mapping.Validate(); err != nil {
		return domain.PathMapping{}, fmt.Errorf("%w: YAML path mapping", ErrInvalidDocument)
	}
	if err := validateMappingShape(mapping); err != nil {
		return domain.PathMapping{}, err
	}
	return mapping, nil
}

func (raw yamlTrash) policy() (Policy, error) {
	retention, err := time.ParseDuration(strings.TrimSpace(raw.Retention))
	if err != nil || retention <= 0 {
		return Policy{}, fmt.Errorf("%w: trash retention must be positive duration", ErrInvalidDocument)
	}
	janitor, err := time.ParseDuration(strings.TrimSpace(raw.JanitorInterval))
	if err != nil || janitor <= 0 {
		return Policy{}, fmt.Errorf("%w: janitor interval must be positive duration", ErrInvalidDocument)
	}
	return Policy{TrashRetention: retention, JanitorInterval: janitor}, nil
}

func chooseAlias(primary, alias string) (string, error) {
	if primary != "" && alias != "" && primary != alias {
		return "", errors.New("mapping aliases disagree")
	}
	if primary != "" {
		return primary, nil
	}
	return alias, nil
}

func slicesClone(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}
