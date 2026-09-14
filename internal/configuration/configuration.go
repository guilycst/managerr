// Package configuration owns Mastarr's effective configuration boundary.
//
// Startup YAML is parsed once and remains read-only. API-owned records are
// changed through validated, revision-checked operations. Both sources are
// merged into an immutable snapshot before workers can consume them.
package configuration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guilycst/mastarr/internal/credentials"
	"github.com/guilycst/mastarr/internal/domain"
	"gopkg.in/yaml.v3"
)

const (
	currentDocumentVersion = 1
	defaultDocumentID      = "config.yaml"
	defaultMaxDocumentSize = 4 << 20
	maxSecretFileBytes     = 64 << 10
	defaultTrashRetention  = 30 * 24 * time.Hour
	defaultJanitorInterval = time.Hour
)

var (
	ErrInvalidDocument         = errors.New("configuration document is invalid")
	ErrConfigSourceReadOnly    = errors.New("configuration source is read-only")
	ErrConfigSourceConflict    = errors.New("configuration source ownership conflicts")
	ErrRevisionMismatch        = errors.New("configuration revision does not match")
	ErrPreconditionRequired    = errors.New("configuration revision precondition is required")
	ErrResourceNotFound        = errors.New("configuration resource was not found")
	ErrResourceRetired         = errors.New("configuration resource is retired")
	ErrMappingAmbiguous        = errors.New("configuration mapping is ambiguous")
	ErrMappingInvalid          = errors.New("configuration mapping is invalid")
	ErrCredentialUnavailable   = errors.New("configuration credential is unavailable")
	ErrCredentialInvalid       = errors.New("configuration credential is invalid")
	ErrCredentialStoreRequired = errors.New("managed credential store is required")
	ErrCredentialManagerNeeded = errors.New("managed credential manager is required")
)

// ResourceKind identifies one source-owned configuration record.
type ResourceKind string

const (
	ResourceConnection ResourceKind = "connection"
	ResourceRoot       ResourceKind = "storage_root"
	ResourceMapping    ResourceKind = "path_mapping"
)

// CredentialKey identifies a field without carrying its value.
type CredentialKey struct {
	ConnectionID domain.ConfigID
	Field        string
}

// CredentialInput is accepted by API-owned connection operations. API values
// are always managed and encrypted. References are accepted only by startup
// YAML parsing; API callers cannot turn a path into an arbitrary secret read.
type CredentialInput struct {
	Value     []byte
	Reference *domain.CredentialReference
}

// ConnectionSpec describes a new API-owned connection.
type ConnectionSpec struct {
	ID          domain.ConfigID
	Kind        domain.ConnectionKind
	Label       string
	Endpoint    string
	Credentials map[string]CredentialInput
}

// ConnectionPatch updates an API-owned connection. A nil Credentials map
// preserves existing managed fields. A non-nil empty map clears them.
type ConnectionPatch struct {
	Label       *string
	Endpoint    *string
	Credentials map[string]CredentialInput
}

// StorageRootSpec describes a new API-owned storage root.
type StorageRootSpec struct {
	ID           domain.ConfigID
	Label        string
	Purpose      domain.StoragePurpose
	Path         string
	ReadOnly     bool
	Capabilities []domain.Capability
	Watch        domain.WatchSettings
}

// StorageRootPatch updates an API-owned storage root.
type StorageRootPatch struct {
	Label                *string
	Path                 *string
	ReadOnly             *bool
	Capabilities         *[]domain.Capability
	WatchEnabled         *bool
	WatchIntervalSeconds *int
}

// PathMappingSpec describes a new API-owned mapping.
type PathMappingSpec struct {
	ID                domain.ConfigID
	ConnectionID      domain.ConfigID
	SourcePrefix      string
	RootID            domain.ConfigID
	DestinationPrefix string
}

// PathMappingPatch updates mapping prefixes. References and IDs are stable.
type PathMappingPatch struct {
	SourcePrefix      *string
	DestinationPrefix *string
}

// RevisionChange is supplied to an invalidator when an authority-bearing
// configuration change makes unapproved plans unsafe to dispatch.
type RevisionChange struct {
	Kind             ResourceKind
	ID               domain.ConfigID
	PreviousRevision string
	Revision         string
	ChangedFields    []string
	AuthorityChanged bool
}

// InvalidateFunc is called before a mutation is committed. Returning an error
// keeps the old configuration active and prevents a partially applied change.
type InvalidateFunc func(context.Context, RevisionChange) error

// ManagedCredentialStore persists complete encrypted field sets atomically.
// Implementations normally translate this boundary to the storage/sqlc
// repository. Plaintext never enters this interface.
type ManagedCredentialStore interface {
	Load(ctx context.Context, connectionID domain.ConfigID) (map[string]credentials.Envelope, error)
	Replace(ctx context.Context, connectionID domain.ConfigID, values map[string]credentials.Envelope) error
}

// APIState is the persisted API-owned portion loaded before activation. Every
// record must already carry SourceAPI metadata and a valid resource revision.
type APIState struct {
	Connections             []domain.Connection
	StorageRoots            []domain.StorageRoot
	PathMappings            []domain.PathMapping
	ManagedCredentialFields map[domain.ConfigID][]string
}

// Options controls one configuration manager. YAMLPath and YAML are mutually
// exclusive. No watcher is installed; ApplyYAML is the explicit reload seam.
type Options struct {
	Now              func() time.Time
	YAMLPath         string
	YAML             []byte
	YAMLDocumentID   string
	Environment      map[string]string
	SecretResolver   SecretResolver
	MaxDocumentBytes int

	APIState          APIState
	CredentialManager *credentials.Manager
	CredentialStore   ManagedCredentialStore
	KeySource         string
	KeyPath           string
	Invalidator       InvalidateFunc
}

// Policy contains validated YAML policy values not yet represented in the
// frozen domain snapshot. It is exposed for configuration and worker wiring.
type Policy struct {
	TrashRetention  time.Duration
	JanitorInterval time.Duration
}

// ParsedYAML is an immutable candidate produced by ParseYAML.
type ParsedYAML struct {
	Snapshot          domain.ConfigurationSnapshot
	Policy            Policy
	StaticCredentials map[CredentialKey][]byte
	DocumentBytes     int
}

// Manager owns source records, historical tombstones and the effective view.
type Manager struct {
	mu sync.RWMutex

	now            func() time.Time
	startupAt      time.Time
	maxDocument    int
	documentID     string
	yamlConfigured bool
	secretResolver SecretResolver

	yamlConnections map[domain.ConfigID]domain.Connection
	yamlRoots       map[domain.ConfigID]domain.StorageRoot
	yamlMappings    map[domain.ConfigID]domain.PathMapping
	apiConnections  map[domain.ConfigID]domain.Connection
	apiRoots        map[domain.ConfigID]domain.StorageRoot
	apiMappings     map[domain.ConfigID]domain.PathMapping
	managedFields   map[domain.ConfigID]map[string]struct{}
	managedDigests  map[domain.ConfigID]string

	retiredConnections map[domain.ConfigID]domain.Connection
	retiredRoots       map[domain.ConfigID]domain.StorageRoot
	retiredMappings    map[domain.ConfigID]domain.PathMapping
	staticCredentials  map[CredentialKey][]byte

	policy            Policy
	snapshot          domain.ConfigurationSnapshot
	credentialManager *credentials.Manager
	credentialStore   ManagedCredentialStore
	keySource         string
	keyPath           string
	invalidator       InvalidateFunc
}

// New validates and activates API state and optional startup YAML as one
// transaction. An invalid candidate leaves no partially initialized manager.
func New(options Options) (*Manager, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	startupAt := now().UTC()
	if startupAt.IsZero() {
		return nil, fmt.Errorf("%w: startup time is required", ErrInvalidDocument)
	}
	maxDocument := options.MaxDocumentBytes
	if maxDocument <= 0 {
		maxDocument = defaultMaxDocumentSize
	}
	if maxDocument < 1 {
		return nil, fmt.Errorf("%w: document size limit is invalid", ErrInvalidDocument)
	}
	if options.YAMLPath != "" && len(options.YAML) != 0 {
		return nil, fmt.Errorf("%w: YAML path and bytes are mutually exclusive", ErrInvalidDocument)
	}
	documentID := strings.TrimSpace(options.YAMLDocumentID)
	if documentID == "" {
		documentID = defaultDocumentID
	}
	resolver := options.SecretResolver
	if resolver == nil {
		resolver = NewEnvironmentResolver(options.Environment)
	}

	manager := &Manager{
		now:                now,
		startupAt:          startupAt,
		maxDocument:        maxDocument,
		documentID:         documentID,
		yamlConfigured:     len(options.YAML) != 0 || options.YAMLPath != "",
		secretResolver:     resolver,
		yamlConnections:    make(map[domain.ConfigID]domain.Connection),
		yamlRoots:          make(map[domain.ConfigID]domain.StorageRoot),
		yamlMappings:       make(map[domain.ConfigID]domain.PathMapping),
		apiConnections:     make(map[domain.ConfigID]domain.Connection),
		apiRoots:           make(map[domain.ConfigID]domain.StorageRoot),
		apiMappings:        make(map[domain.ConfigID]domain.PathMapping),
		managedFields:      make(map[domain.ConfigID]map[string]struct{}),
		managedDigests:     make(map[domain.ConfigID]string),
		retiredConnections: make(map[domain.ConfigID]domain.Connection),
		retiredRoots:       make(map[domain.ConfigID]domain.StorageRoot),
		retiredMappings:    make(map[domain.ConfigID]domain.PathMapping),
		staticCredentials:  make(map[CredentialKey][]byte),
		policy:             Policy{TrashRetention: defaultTrashRetention, JanitorInterval: defaultJanitorInterval},
		credentialManager:  options.CredentialManager,
		credentialStore:    options.CredentialStore,
		keySource:          options.KeySource,
		keyPath:            options.KeyPath,
		invalidator:        options.Invalidator,
	}
	if err := manager.installAPIState(options.APIState); err != nil {
		return nil, err
	}
	if len(options.YAML) != 0 || options.YAMLPath != "" {
		var parsed ParsedYAML
		var err error
		if options.YAMLPath != "" {
			parsed, err = LoadYAMLFile(options.YAMLPath, ParseOptions{
				DocumentID:       documentID,
				StartupAt:        startupAt,
				SecretResolver:   resolver,
				MaxDocumentBytes: maxDocument,
			})
		} else {
			parsed, err = ParseYAML(options.YAML, ParseOptions{
				DocumentID:       documentID,
				StartupAt:        startupAt,
				SecretResolver:   resolver,
				MaxDocumentBytes: maxDocument,
			})
		}
		if err != nil {
			return nil, err
		}
		if err := manager.activateYAML(context.Background(), parsed); err != nil {
			return nil, err
		}
	} else if err := manager.rebuildLocked(); err != nil {
		return nil, err
	}
	return manager, nil
}

// Close releases package-owned cached static secret and encryption key bytes.
// Persistent stores own their own resources and are not closed by Manager.
func (manager *Manager) Close() error {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for key, value := range manager.staticCredentials {
		zero(value)
		delete(manager.staticCredentials, key)
	}
	if manager.credentialManager != nil {
		manager.credentialManager.Close()
		manager.credentialManager = nil
	}
	return nil
}

// Snapshot returns a deep copy of the current effective non-secret view.
func (manager *Manager) Snapshot(ctx context.Context) (domain.ConfigurationSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return domain.ConfigurationSnapshot{}, err
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return cloneSnapshot(manager.snapshot), nil
}

// Policy returns current validated policy values.
func (manager *Manager) Policy(ctx context.Context) (Policy, error) {
	if err := contextError(ctx); err != nil {
		return Policy{}, err
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.policy, nil
}

// ParseYAML strictly decodes one complete startup document and resolves every
// static credential reference before returning a candidate.
func ParseYAML(data []byte, options ParseOptions) (ParsedYAML, error) {
	maxDocument := options.MaxDocumentBytes
	if maxDocument <= 0 {
		maxDocument = defaultMaxDocumentSize
	}
	if len(data) == 0 || len(data) > maxDocument {
		return ParsedYAML{}, fmt.Errorf("%w: YAML document size is invalid", ErrInvalidDocument)
	}
	startupAt := options.StartupAt
	if startupAt.IsZero() {
		startupAt = time.Now().UTC()
	}
	documentID := strings.TrimSpace(options.DocumentID)
	if documentID == "" {
		documentID = defaultDocumentID
	}
	var document yamlDocument
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return ParsedYAML{}, fmt.Errorf("%w: decode YAML", ErrInvalidDocument)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ParsedYAML{}, fmt.Errorf("%w: multiple YAML documents are not supported", ErrInvalidDocument)
		}
		return ParsedYAML{}, fmt.Errorf("%w: decode trailing YAML", ErrInvalidDocument)
	}
	if document.Version != currentDocumentVersion {
		return ParsedYAML{}, fmt.Errorf("%w: unsupported document version", ErrInvalidDocument)
	}
	resolver := options.SecretResolver
	if resolver == nil {
		resolver = NewEnvironmentResolver(options.Environment)
	}
	documentRevision := digestValue(document)
	yamlSource := sourceMetadata(domain.SourceYAML, false, documentID, documentRevision, startupAt)
	parsed := ParsedYAML{
		Snapshot: domain.ConfigurationSnapshot{
			Source: yamlSource,
		},
		Policy:            Policy{TrashRetention: defaultTrashRetention, JanitorInterval: defaultJanitorInterval},
		StaticCredentials: make(map[CredentialKey][]byte),
		DocumentBytes:     len(data),
	}
	for _, raw := range document.Connections {
		connection, values, err := raw.toDomain(yamlSource, resolver)
		if err != nil {
			parsed.clearSecrets()
			return ParsedYAML{}, err
		}
		if _, exists := findConnection(parsed.Snapshot.Connections, connection.ID); exists {
			parsed.clearSecrets()
			return ParsedYAML{}, fmt.Errorf("%w: duplicate connection id", ErrInvalidDocument)
		}
		parsed.Snapshot.Connections = append(parsed.Snapshot.Connections, connection)
		for field, value := range values {
			parsed.StaticCredentials[CredentialKey{ConnectionID: connection.ID, Field: field}] = value
		}
	}
	for _, raw := range document.StorageRoots {
		root, err := raw.toDomain(yamlSource)
		if err != nil {
			parsed.clearSecrets()
			return ParsedYAML{}, err
		}
		if _, exists := findRoot(parsed.Snapshot.StorageRoots, root.ID); exists {
			parsed.clearSecrets()
			return ParsedYAML{}, fmt.Errorf("%w: duplicate storage root id", ErrInvalidDocument)
		}
		parsed.Snapshot.StorageRoots = append(parsed.Snapshot.StorageRoots, root)
	}
	for _, raw := range document.PathMappings {
		mapping, err := raw.toDomain(yamlSource)
		if err != nil {
			parsed.clearSecrets()
			return ParsedYAML{}, err
		}
		if _, exists := findMapping(parsed.Snapshot.PathMappings, mapping.ID); exists {
			parsed.clearSecrets()
			return ParsedYAML{}, fmt.Errorf("%w: duplicate path mapping id", ErrInvalidDocument)
		}
		parsed.Snapshot.PathMappings = append(parsed.Snapshot.PathMappings, mapping)
	}
	if document.Trash != nil {
		policy, err := document.Trash.policy()
		if err != nil {
			parsed.clearSecrets()
			return ParsedYAML{}, err
		}
		parsed.Policy = policy
	}
	if err := validateSnapshot(parsed.Snapshot); err != nil {
		parsed.clearSecrets()
		return ParsedYAML{}, err
	}
	return parsed, nil
}

// LoadYAML is a naming alias for callers that load bytes from their own
// startup boundary.
func LoadYAML(data []byte, options ParseOptions) (ParsedYAML, error) {
	return ParseYAML(data, options)
}

// LoadYAMLFile reads and validates one bounded regular file. It reads the
// selected descriptor once, so later file changes cannot alter this candidate.
func LoadYAMLFile(filePath string, options ParseOptions) (ParsedYAML, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" || !filepath.IsAbs(filePath) {
		return ParsedYAML{}, fmt.Errorf("%w: YAML file path must be absolute", ErrInvalidDocument)
	}
	file, err := openBoundedRegular(filePath, maxDocumentLimit(options.MaxDocumentBytes))
	if err != nil {
		return ParsedYAML{}, fmt.Errorf("%w: YAML file is unavailable", ErrInvalidDocument)
	}
	defer file.Close()
	limit := maxDocumentLimit(options.MaxDocumentBytes)
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return ParsedYAML{}, fmt.Errorf("%w: read YAML file", ErrInvalidDocument)
	}
	if len(data) > limit {
		return ParsedYAML{}, fmt.Errorf("%w: YAML document size is invalid", ErrInvalidDocument)
	}
	if options.DocumentID == "" {
		options.DocumentID = filepath.Base(filePath)
	}
	return ParseYAML(data, options)
}

// ApplyYAML explicitly replaces the YAML source snapshot. It is the only
// reload operation; no watcher or automatic secret-file reread exists.
func (manager *Manager) ApplyYAML(ctx context.Context, data []byte) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	manager.mu.RLock()
	resolver := manager.secretResolver
	maxDocument := manager.maxDocument
	documentID := manager.documentID
	startupAt := manager.startupAt
	manager.mu.RUnlock()
	parsed, err := ParseYAML(data, ParseOptions{
		DocumentID:       documentID,
		StartupAt:        startupAt,
		SecretResolver:   resolver,
		MaxDocumentBytes: maxDocument,
	})
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.activateYAMLLocked(ctx, parsed)
}

// ApplyYAMLFile explicitly reads and activates a startup file. Callers decide
// when to invoke it; Manager never polls or watches the path.
func (manager *Manager) ApplyYAMLFile(ctx context.Context, filePath string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	manager.mu.RLock()
	resolver := manager.secretResolver
	maxDocument := manager.maxDocument
	documentID := manager.documentID
	startupAt := manager.startupAt
	manager.mu.RUnlock()
	parsed, err := LoadYAMLFile(filePath, ParseOptions{
		DocumentID:       documentID,
		StartupAt:        startupAt,
		SecretResolver:   resolver,
		MaxDocumentBytes: maxDocument,
	})
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.activateYAMLLocked(ctx, parsed)
}

func (manager *Manager) installAPIState(state APIState) error {
	for id, fields := range state.ManagedCredentialFields {
		if !id.Valid() {
			return fmt.Errorf("%w: managed credential connection id", ErrInvalidDocument)
		}
		manager.managedFields[id] = make(map[string]struct{}, len(fields))
		for _, field := range fields {
			if err := validateCredentialField(field); err != nil {
				return fmt.Errorf("%w: managed credential field is empty", ErrInvalidDocument)
			}
			manager.managedFields[id][field] = struct{}{}
		}
	}
	for _, connection := range state.Connections {
		if connection.Source.Source != domain.SourceAPI || !connection.Source.Editable {
			return fmt.Errorf("%w: API connection source metadata is invalid", ErrInvalidDocument)
		}
		if err := connection.Validate(); err != nil {
			return fmt.Errorf("%w: API connection: %v", ErrInvalidDocument, err)
		}
		if _, exists := manager.apiConnections[connection.ID]; exists {
			return fmt.Errorf("%w: duplicate API connection id", ErrInvalidDocument)
		}
		if connection.RetiredAt != nil {
			manager.retiredConnections[connection.ID] = cloneConnection(connection)
		} else {
			manager.apiConnections[connection.ID] = cloneConnection(connection)
		}
	}
	for _, root := range state.StorageRoots {
		if root.Source.Source != domain.SourceAPI || !root.Source.Editable {
			return fmt.Errorf("%w: API root source metadata is invalid", ErrInvalidDocument)
		}
		if err := root.Validate(); err != nil {
			return fmt.Errorf("%w: API storage root: %v", ErrInvalidDocument, err)
		}
		if err := validateStorageRootPath(root.Path); err != nil {
			return fmt.Errorf("%w: API storage root path: %v", ErrInvalidDocument, err)
		}
		if _, exists := manager.apiRoots[root.ID]; exists {
			return fmt.Errorf("%w: duplicate API root id", ErrInvalidDocument)
		}
		if root.RetiredAt != nil {
			manager.retiredRoots[root.ID] = cloneRoot(root)
		} else {
			manager.apiRoots[root.ID] = cloneRoot(root)
		}
	}
	for _, mapping := range state.PathMappings {
		if mapping.Source.Source != domain.SourceAPI || !mapping.Source.Editable {
			return fmt.Errorf("%w: API mapping source metadata is invalid", ErrInvalidDocument)
		}
		if err := mapping.Validate(); err != nil {
			return fmt.Errorf("%w: API mapping: %v", ErrInvalidDocument, err)
		}
		if _, exists := manager.apiMappings[mapping.ID]; exists {
			return fmt.Errorf("%w: duplicate API mapping id", ErrInvalidDocument)
		}
		manager.apiMappings[mapping.ID] = cloneMapping(mapping)
	}
	for id := range manager.managedFields {
		if _, exists := manager.apiConnections[id]; !exists {
			return fmt.Errorf("%w: managed credentials reference unknown API connection", ErrInvalidDocument)
		}
	}
	return nil
}

func (manager *Manager) activateYAML(ctx context.Context, parsed ParsedYAML) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.activateYAMLLocked(ctx, parsed)
}

func (manager *Manager) activateYAMLLocked(ctx context.Context, parsed ParsedYAML) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextError(ctx); err != nil {
		parsed.clearSecrets()
		return err
	}
	if err := validateMerged(parsed.Snapshot, manager.apiConnections, manager.apiRoots, manager.apiMappings); err != nil {
		parsed.clearSecrets()
		return err
	}
	for id, previous := range manager.retiredConnections {
		if _, active := findConnection(parsed.Snapshot.Connections, id); active && previous.Source.Source == domain.SourceAPI {
			parsed.clearSecrets()
			return fmt.Errorf("%w: retired API connection id %q", ErrConfigSourceConflict, id)
		}
	}
	for id, previous := range manager.retiredRoots {
		if _, active := findRoot(parsed.Snapshot.StorageRoots, id); active && previous.Source.Source == domain.SourceAPI {
			parsed.clearSecrets()
			return fmt.Errorf("%w: retired API storage root id %q", ErrConfigSourceConflict, id)
		}
	}
	for id, previous := range manager.retiredMappings {
		if _, active := findMapping(parsed.Snapshot.PathMappings, id); active && previous.Source.Source == domain.SourceAPI {
			parsed.clearSecrets()
			return fmt.Errorf("%w: retired API path mapping id %q", ErrConfigSourceConflict, id)
		}
	}
	newConnections := make(map[domain.ConfigID]domain.Connection, len(parsed.Snapshot.Connections))
	newRoots := make(map[domain.ConfigID]domain.StorageRoot, len(parsed.Snapshot.StorageRoots))
	newMappings := make(map[domain.ConfigID]domain.PathMapping, len(parsed.Snapshot.PathMappings))
	for _, connection := range parsed.Snapshot.Connections {
		newConnections[connection.ID] = cloneConnection(connection)
	}
	for _, root := range parsed.Snapshot.StorageRoots {
		newRoots[root.ID] = cloneRoot(root)
	}
	for _, mapping := range parsed.Snapshot.PathMappings {
		newMappings[mapping.ID] = cloneMapping(mapping)
	}
	// Validate the complete effective candidate before invoking an invalidator
	// or changing any active maps. YAML can remove a resource still referenced
	// by an API-owned mapping, so validating the YAML document alone is not
	// sufficient.
	candidate, err := manager.snapshotForMapsLocked(newConnections, newRoots, newMappings, true)
	if err != nil {
		parsed.clearSecrets()
		return err
	}
	if err := manager.invalidateYAMLChangesLocked(ctx, newConnections, newRoots, newMappings); err != nil {
		parsed.clearSecrets()
		return err
	}
	retiredAt := manager.now().UTC()
	for id, old := range manager.yamlConnections {
		if _, exists := newConnections[id]; !exists {
			old.RetiredAt = &retiredAt
			manager.retiredConnections[id] = cloneConnection(old)
		}
	}
	for id, old := range manager.yamlRoots {
		if _, exists := newRoots[id]; !exists {
			old.RetiredAt = &retiredAt
			manager.retiredRoots[id] = cloneRoot(old)
		}
	}
	for id, old := range manager.yamlMappings {
		if _, exists := newMappings[id]; !exists {
			manager.retiredMappings[id] = cloneMapping(old)
		}
	}
	for key, value := range manager.staticCredentials {
		zero(value)
		delete(manager.staticCredentials, key)
	}
	for key, value := range parsed.StaticCredentials {
		manager.staticCredentials[key] = append([]byte(nil), value...)
	}
	parsed.clearSecrets()
	for id := range newConnections {
		if retired, exists := manager.retiredConnections[id]; exists && retired.Source.Source == domain.SourceYAML {
			delete(manager.retiredConnections, id)
		}
	}
	for id := range newRoots {
		if retired, exists := manager.retiredRoots[id]; exists && retired.Source.Source == domain.SourceYAML {
			delete(manager.retiredRoots, id)
		}
	}
	for id := range newMappings {
		if retired, exists := manager.retiredMappings[id]; exists && retired.Source.Source == domain.SourceYAML {
			delete(manager.retiredMappings, id)
		}
	}
	manager.yamlConnections = newConnections
	manager.yamlRoots = newRoots
	manager.yamlMappings = newMappings
	manager.yamlConfigured = true
	manager.policy = parsed.Policy
	manager.snapshot = cloneSnapshot(candidate)
	return nil
}

func (manager *Manager) rebuildLocked() error {
	snapshot, err := manager.snapshotForMapsLocked(manager.yamlConnections, manager.yamlRoots, manager.yamlMappings, manager.yamlConfigured)
	if err != nil {
		return err
	}
	manager.snapshot = cloneSnapshot(snapshot)
	return nil
}

func (manager *Manager) snapshotForMapsLocked(yamlConnections map[domain.ConfigID]domain.Connection, yamlRoots map[domain.ConfigID]domain.StorageRoot, yamlMappings map[domain.ConfigID]domain.PathMapping, yamlConfigured bool) (domain.ConfigurationSnapshot, error) {
	connections := mergeConnections(manager.apiConnections, yamlConnections)
	roots := mergeRoots(manager.apiRoots, yamlRoots)
	mappings := mergeMappings(manager.apiMappings, yamlMappings)
	source := manager.effectiveSource(connections, roots, mappings, yamlConfigured, len(manager.apiConnections)+len(manager.apiRoots)+len(manager.apiMappings))
	snapshot := domain.ConfigurationSnapshot{
		Source:          source,
		Connections:     connections,
		StorageRoots:    roots,
		PathMappings:    mappings,
		KeySource:       manager.keySource,
		KeyPath:         manager.keyPath,
		RestartRequired: true,
	}
	if err := validateSnapshot(snapshot); err != nil {
		return domain.ConfigurationSnapshot{}, err
	}
	return snapshot, nil
}

// invalidateYAMLChangesLocked informs the workflow boundary about authority
// changes before a new YAML snapshot becomes active. The comparison is kept
// deterministic so a caller can journal the exact sequence and retry safely.
func (manager *Manager) invalidateYAMLChangesLocked(ctx context.Context, newConnections map[domain.ConfigID]domain.Connection, newRoots map[domain.ConfigID]domain.StorageRoot, newMappings map[domain.ConfigID]domain.PathMapping) error {
	if manager.invalidator == nil {
		return nil
	}
	changes := make([]RevisionChange, 0)
	for id, previous := range manager.yamlConnections {
		current, exists := newConnections[id]
		if !exists {
			changes = append(changes, RevisionChange{Kind: ResourceConnection, ID: id, PreviousRevision: previous.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true})
			continue
		}
		fields, authority := connectionChangedFields(previous, current)
		if authority {
			changes = append(changes, RevisionChange{Kind: ResourceConnection, ID: id, PreviousRevision: previous.Revision, Revision: current.Revision, ChangedFields: fields, AuthorityChanged: true})
		}
	}
	for id, previous := range manager.yamlRoots {
		current, exists := newRoots[id]
		if !exists {
			changes = append(changes, RevisionChange{Kind: ResourceRoot, ID: id, PreviousRevision: previous.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true})
			continue
		}
		fields, authority := rootChangedFields(previous, current)
		if authority {
			changes = append(changes, RevisionChange{Kind: ResourceRoot, ID: id, PreviousRevision: previous.Revision, Revision: current.Revision, ChangedFields: fields, AuthorityChanged: true})
		}
	}
	for id, previous := range manager.yamlMappings {
		current, exists := newMappings[id]
		if !exists {
			changes = append(changes, RevisionChange{Kind: ResourceMapping, ID: id, PreviousRevision: previous.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true})
			continue
		}
		fields, authority := mappingChangedFields(previous, current)
		if authority {
			changes = append(changes, RevisionChange{Kind: ResourceMapping, ID: id, PreviousRevision: previous.Revision, Revision: current.Revision, ChangedFields: fields, AuthorityChanged: true})
		}
	}
	sort.Slice(changes, func(left, right int) bool {
		if changes[left].Kind != changes[right].Kind {
			return changes[left].Kind < changes[right].Kind
		}
		return changes[left].ID < changes[right].ID
	})
	for _, change := range changes {
		if err := manager.invalidator(ctx, change); err != nil {
			return err
		}
	}
	return nil
}

func connectionChangedFields(previous, current domain.Connection) ([]string, bool) {
	fields := make([]string, 0, 4)
	authority := false
	if previous.Kind != current.Kind {
		fields = append(fields, "kind")
		authority = true
	}
	if previous.Label != current.Label {
		fields = append(fields, "label")
	}
	if previous.Endpoint != current.Endpoint {
		fields = append(fields, "endpoint")
		authority = true
	}
	if !slices.Equal(sortedCredentialReferences(previous.Credentials), sortedCredentialReferences(current.Credentials)) {
		fields = append(fields, "credentials")
	}
	return fields, authority
}

func rootChangedFields(previous, current domain.StorageRoot) ([]string, bool) {
	fields := make([]string, 0, 6)
	authority := false
	if previous.Label != current.Label {
		fields = append(fields, "label")
	}
	if previous.Purpose != current.Purpose {
		fields = append(fields, "purpose")
		authority = true
	}
	if previous.Path != current.Path {
		fields = append(fields, "path")
		authority = true
	}
	if previous.ReadOnly != current.ReadOnly {
		fields = append(fields, "readOnly")
		authority = true
	}
	if !capabilitiesEqual(previous.Capabilities, current.Capabilities) {
		fields = append(fields, "capabilities")
		authority = true
	}
	if previous.Watch != current.Watch {
		fields = append(fields, "watch")
	}
	return fields, authority
}

func mappingChangedFields(previous, current domain.PathMapping) ([]string, bool) {
	fields := make([]string, 0, 4)
	if previous.ConnectionID != current.ConnectionID {
		fields = append(fields, "connectionId")
	}
	if previous.SourcePrefix != current.SourcePrefix {
		fields = append(fields, "sourcePrefix")
	}
	if previous.RootID != current.RootID {
		fields = append(fields, "rootId")
	}
	if previous.DestinationPrefix != current.DestinationPrefix {
		fields = append(fields, "destinationPrefix")
	}
	return fields, len(fields) != 0
}

func (manager *Manager) effectiveSource(connections []domain.Connection, roots []domain.StorageRoot, mappings []domain.PathMapping, yamlConfigured bool, apiResourceCount int) domain.SourceMetadata {
	type sourceRecord struct {
		Kind ResourceKind
		ID   domain.ConfigID
		Rev  string
	}
	values := make([]sourceRecord, 0, len(connections)+len(roots)+len(mappings))
	for _, item := range connections {
		values = append(values, sourceRecord{ResourceConnection, item.ID, item.Revision})
	}
	for _, item := range roots {
		values = append(values, sourceRecord{ResourceRoot, item.ID, item.Revision})
	}
	for _, item := range mappings {
		values = append(values, sourceRecord{ResourceMapping, item.ID, item.Revision})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Kind != values[j].Kind {
			return values[i].Kind < values[j].Kind
		}
		return values[i].ID < values[j].ID
	})
	source := domain.SourceYAML
	documentID := manager.documentID
	if !yamlConfigured || apiResourceCount > 0 {
		source = domain.SourceAPI
		documentID = "effective"
	}
	return sourceMetadata(source, source == domain.SourceAPI, documentID, digestValue(values), manager.startupAt)
}

// ListConnections returns active records unless includeRetired is true.
func (manager *Manager) ListConnections(ctx context.Context, includeRetired bool) ([]domain.Connection, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	items := make([]domain.Connection, 0, len(manager.apiConnections)+len(manager.yamlConnections))
	for _, item := range manager.apiConnections {
		items = append(items, cloneConnection(item))
	}
	for _, item := range manager.yamlConnections {
		items = append(items, cloneConnection(item))
	}
	if includeRetired {
		for _, item := range manager.retiredConnections {
			items = append(items, cloneConnection(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// GetConnection returns one active or historical connection.
func (manager *Manager) GetConnection(ctx context.Context, id domain.ConfigID, includeRetired bool) (domain.Connection, error) {
	if err := contextError(ctx); err != nil {
		return domain.Connection{}, err
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if value, exists := manager.apiConnections[id]; exists {
		return cloneConnection(value), nil
	}
	if value, exists := manager.yamlConnections[id]; exists {
		return cloneConnection(value), nil
	}
	if includeRetired {
		if value, exists := manager.retiredConnections[id]; exists {
			return cloneConnection(value), nil
		}
	}
	return domain.Connection{}, ErrResourceNotFound
}

// ListStorageRoots returns active records unless includeRetired is true.
func (manager *Manager) ListStorageRoots(ctx context.Context, includeRetired bool) ([]domain.StorageRoot, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	items := make([]domain.StorageRoot, 0, len(manager.apiRoots)+len(manager.yamlRoots))
	for _, item := range manager.apiRoots {
		items = append(items, cloneRoot(item))
	}
	for _, item := range manager.yamlRoots {
		items = append(items, cloneRoot(item))
	}
	if includeRetired {
		for _, item := range manager.retiredRoots {
			items = append(items, cloneRoot(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// GetStorageRoot returns one active or historical storage root.
func (manager *Manager) GetStorageRoot(ctx context.Context, id domain.ConfigID, includeRetired bool) (domain.StorageRoot, error) {
	if err := contextError(ctx); err != nil {
		return domain.StorageRoot{}, err
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if value, exists := manager.apiRoots[id]; exists {
		return cloneRoot(value), nil
	}
	if value, exists := manager.yamlRoots[id]; exists {
		return cloneRoot(value), nil
	}
	if includeRetired {
		if value, exists := manager.retiredRoots[id]; exists {
			return cloneRoot(value), nil
		}
	}
	return domain.StorageRoot{}, ErrResourceNotFound
}

// ListPathMappings returns active records unless includeRetired is true.
func (manager *Manager) ListPathMappings(ctx context.Context, includeRetired bool) ([]domain.PathMapping, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	items := make([]domain.PathMapping, 0, len(manager.apiMappings)+len(manager.yamlMappings))
	for _, item := range manager.apiMappings {
		items = append(items, cloneMapping(item))
	}
	for _, item := range manager.yamlMappings {
		items = append(items, cloneMapping(item))
	}
	if includeRetired {
		for _, item := range manager.retiredMappings {
			items = append(items, cloneMapping(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// GetPathMapping returns one active or historical path mapping.
func (manager *Manager) GetPathMapping(ctx context.Context, id domain.ConfigID, includeRetired bool) (domain.PathMapping, error) {
	if err := contextError(ctx); err != nil {
		return domain.PathMapping{}, err
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if value, exists := manager.apiMappings[id]; exists {
		return cloneMapping(value), nil
	}
	if value, exists := manager.yamlMappings[id]; exists {
		return cloneMapping(value), nil
	}
	if includeRetired {
		if value, exists := manager.retiredMappings[id]; exists {
			return cloneMapping(value), nil
		}
	}
	return domain.PathMapping{}, ErrResourceNotFound
}

// ResolveCredential returns a copy of one static or managed credential value.
// Callers must treat the returned bytes as sensitive and zero them after use.
func (manager *Manager) ResolveCredential(ctx context.Context, connectionID domain.ConfigID, field string) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !connectionID.Valid() || validateCredentialField(field) != nil {
		return nil, ErrCredentialInvalid
	}
	key := CredentialKey{ConnectionID: connectionID, Field: field}
	manager.mu.RLock()
	if !manager.connectionActiveLocked(connectionID) {
		manager.mu.RUnlock()
		return nil, ErrResourceNotFound
	}
	if value, exists := manager.staticCredentials[key]; exists {
		copyValue := append([]byte(nil), value...)
		manager.mu.RUnlock()
		return copyValue, nil
	}
	store := manager.credentialStore
	crypt := manager.credentialManager
	if store == nil || crypt == nil {
		manager.mu.RUnlock()
		return nil, ErrCredentialUnavailable
	}
	envelopes, err := store.Load(ctx, connectionID)
	if err != nil {
		manager.mu.RUnlock()
		return nil, ErrCredentialUnavailable
	}
	envelope, exists := envelopes[field]
	if !exists {
		manager.mu.RUnlock()
		return nil, ErrCredentialUnavailable
	}
	value, err := crypt.Open(envelope, connectionID.String(), field)
	manager.mu.RUnlock()
	if err != nil {
		return nil, ErrCredentialUnavailable
	}
	return value, nil
}

// CredentialMetadata returns redacted state for one connection field.
func (manager *Manager) CredentialMetadata(ctx context.Context, connectionID domain.ConfigID, field string) (credentials.CredentialMetadata, error) {
	if err := contextError(ctx); err != nil {
		return credentials.CredentialMetadata{}, err
	}
	if !connectionID.Valid() || validateCredentialField(field) != nil {
		return credentials.CredentialMetadata{}, ErrCredentialInvalid
	}
	manager.mu.RLock()
	if !manager.connectionActiveLocked(connectionID) {
		manager.mu.RUnlock()
		return credentials.CredentialMetadata{}, ErrResourceNotFound
	}
	if value, exists := manager.staticCredentials[CredentialKey{ConnectionID: connectionID, Field: field}]; exists {
		manager.mu.RUnlock()
		return credentials.CredentialMetadata{ConnectionID: connectionID.String(), Field: field, Configured: len(value) > 0}, nil
	}
	store := manager.credentialStore
	manager.mu.RUnlock()
	if store == nil {
		return credentials.CredentialMetadata{ConnectionID: connectionID.String(), Field: field}, nil
	}
	envelopes, err := store.Load(ctx, connectionID)
	if err != nil {
		return credentials.CredentialMetadata{}, ErrCredentialUnavailable
	}
	envelope, exists := envelopes[field]
	if !exists {
		return credentials.CredentialMetadata{ConnectionID: connectionID.String(), Field: field}, nil
	}
	metadata, err := credentials.Metadata(envelope, connectionID.String(), field)
	if err != nil {
		return credentials.CredentialMetadata{}, ErrCredentialUnavailable
	}
	return metadata, nil
}

// CreateConnection adds API-owned configuration atomically.
func (manager *Manager) CreateConnection(ctx context.Context, spec ConnectionSpec) (domain.Connection, error) {
	if err := contextError(ctx); err != nil {
		return domain.Connection{}, err
	}
	if err := spec.validate(); err != nil {
		return domain.Connection{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.apiConnections[spec.ID]; exists {
		return domain.Connection{}, fmt.Errorf("%w: connection already exists", ErrConfigSourceConflict)
	}
	if _, exists := manager.yamlConnections[spec.ID]; exists {
		return domain.Connection{}, fmt.Errorf("%w: connection id is owned by YAML", ErrConfigSourceConflict)
	}
	if _, exists := manager.retiredConnections[spec.ID]; exists {
		return domain.Connection{}, fmt.Errorf("%w: retired connection id cannot be reused", ErrResourceRetired)
	}
	connection := domain.Connection{
		ID:          spec.ID,
		Kind:        spec.Kind,
		Label:       spec.Label,
		Endpoint:    spec.Endpoint,
		Source:      manager.apiMetadata("connection", spec.ID, "pending"),
		Credentials: make(map[string]domain.CredentialReference),
	}
	preparedCredentials, err := manager.prepareManagedCredentialsLocked(ctx, spec.ID, spec.Credentials)
	if err != nil {
		return domain.Connection{}, err
	}
	manager.managedFields[spec.ID] = credentialFieldSet(spec.Credentials)
	if preparedCredentials != nil && manager.credentialStore != nil {
		manager.managedDigests[spec.ID] = credentialEnvelopeDigest(preparedCredentials)
	}
	connection.Revision = manager.connectionRevisionLocked(connection)
	connection.Source.Revision = connection.Revision
	manager.apiConnections[spec.ID] = cloneConnection(connection)
	if err := manager.rebuildLocked(); err != nil {
		delete(manager.apiConnections, spec.ID)
		delete(manager.managedFields, spec.ID)
		delete(manager.managedDigests, spec.ID)
		return domain.Connection{}, err
	}
	if preparedCredentials != nil && manager.credentialStore != nil {
		if err := manager.credentialStore.Replace(ctx, spec.ID, preparedCredentials); err != nil {
			delete(manager.apiConnections, spec.ID)
			delete(manager.managedFields, spec.ID)
			delete(manager.managedDigests, spec.ID)
			_ = manager.rebuildLocked()
			return domain.Connection{}, ErrCredentialUnavailable
		}
	}
	return cloneConnection(connection), nil
}

// UpdateConnection applies an API-owned patch with an If-Match-style revision.
func (manager *Manager) UpdateConnection(ctx context.Context, id domain.ConfigID, expectedRevision string, patch ConnectionPatch) (domain.Connection, error) {
	if err := contextError(ctx); err != nil {
		return domain.Connection{}, err
	}
	if !id.Valid() {
		return domain.Connection{}, ErrResourceNotFound
	}
	if strings.TrimSpace(expectedRevision) == "" {
		return domain.Connection{}, ErrPreconditionRequired
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, exists := manager.apiConnections[id]
	if !exists {
		if _, yamlOwned := manager.yamlConnections[id]; yamlOwned {
			return domain.Connection{}, ErrConfigSourceReadOnly
		}
		if _, retired := manager.retiredConnections[id]; retired {
			if manager.retiredConnections[id].Source.Source == domain.SourceYAML {
				return domain.Connection{}, ErrConfigSourceReadOnly
			}
			return domain.Connection{}, ErrResourceRetired
		}
		return domain.Connection{}, ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		return domain.Connection{}, ErrRevisionMismatch
	}
	var preparedCredentials map[string]credentials.Envelope
	var previousCredentials map[string]credentials.Envelope
	previousManagedFields := cloneFieldSet(manager.managedFields[id])
	previousManagedDigest := manager.managedDigests[id]
	updated := cloneConnection(current)
	changed := make([]string, 0, 2)
	if patch.Label != nil && *patch.Label != current.Label {
		updated.Label = *patch.Label
		changed = append(changed, "label")
	}
	if patch.Endpoint != nil && *patch.Endpoint != current.Endpoint {
		updated.Endpoint = *patch.Endpoint
		changed = append(changed, "endpoint")
	}
	if patch.Credentials != nil {
		if manager.credentialStore == nil {
			return domain.Connection{}, ErrCredentialStoreRequired
		}
		var err error
		previousCredentials, err = manager.credentialStore.Load(ctx, id)
		if err != nil {
			return domain.Connection{}, ErrCredentialUnavailable
		}
		preparedCredentials, err = manager.prepareManagedCredentialsLocked(ctx, id, patch.Credentials)
		if err != nil {
			return domain.Connection{}, err
		}
		changed = append(changed, "credentials")
		manager.managedFields[id] = credentialFieldSet(patch.Credentials)
		manager.managedDigests[id] = credentialEnvelopeDigest(preparedCredentials)
	}
	if len(changed) == 0 {
		manager.managedFields[id] = previousManagedFields
		manager.managedDigests[id] = previousManagedDigest
		return cloneConnection(current), nil
	}
	updated.Revision = manager.connectionRevisionLocked(updated)
	updated.Source.Revision = updated.Revision
	if err := updated.Validate(); err != nil {
		manager.managedFields[id] = previousManagedFields
		manager.managedDigests[id] = previousManagedDigest
		return domain.Connection{}, fmt.Errorf("%w: API connection: %v", ErrInvalidDocument, err)
	}
	change := RevisionChange{Kind: ResourceConnection, ID: id, PreviousRevision: current.Revision, Revision: updated.Revision, ChangedFields: slices.Clone(changed), AuthorityChanged: containsAny(changed, "endpoint")}
	if manager.invalidator != nil && change.AuthorityChanged {
		if err := manager.invalidator(ctx, change); err != nil {
			manager.managedFields[id] = previousManagedFields
			manager.managedDigests[id] = previousManagedDigest
			return domain.Connection{}, err
		}
	}
	manager.apiConnections[id] = updated
	if err := manager.rebuildLocked(); err != nil {
		manager.apiConnections[id] = current
		manager.managedFields[id] = previousManagedFields
		manager.managedDigests[id] = previousManagedDigest
		return domain.Connection{}, err
	}
	if patch.Credentials != nil {
		if err := manager.credentialStore.Replace(ctx, id, preparedCredentials); err != nil {
			_ = manager.credentialStore.Replace(context.Background(), id, previousCredentials)
			manager.apiConnections[id] = current
			manager.managedFields[id] = previousManagedFields
			manager.managedDigests[id] = previousManagedDigest
			_ = manager.rebuildLocked()
			return domain.Connection{}, ErrCredentialUnavailable
		}
	}
	return cloneConnection(updated), nil
}

// PatchConnection is an API-compatible alias for UpdateConnection.
func (manager *Manager) PatchConnection(ctx context.Context, id domain.ConfigID, expectedRevision string, patch ConnectionPatch) (domain.Connection, error) {
	return manager.UpdateConnection(ctx, id, expectedRevision, patch)
}

// RetireConnection removes an API-owned record from the active snapshot while
// retaining its historical tombstone.
func (manager *Manager) RetireConnection(ctx context.Context, id domain.ConfigID, expectedRevision string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(expectedRevision) == "" {
		return ErrPreconditionRequired
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, exists := manager.apiConnections[id]
	if !exists {
		if _, yamlOwned := manager.yamlConnections[id]; yamlOwned {
			return ErrConfigSourceReadOnly
		}
		if _, retired := manager.retiredConnections[id]; retired {
			if manager.retiredConnections[id].Source.Source == domain.SourceYAML {
				return ErrConfigSourceReadOnly
			}
			return ErrResourceRetired
		}
		return ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		return ErrRevisionMismatch
	}
	if manager.connectionHasMappingsLocked(id) {
		return fmt.Errorf("%w: connection is referenced by a path mapping", ErrConfigSourceConflict)
	}
	if manager.invalidator != nil {
		change := RevisionChange{Kind: ResourceConnection, ID: id, PreviousRevision: current.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true}
		if err := manager.invalidator(ctx, change); err != nil {
			return err
		}
	}
	retiredAt := manager.now().UTC()
	current.RetiredAt = &retiredAt
	manager.retiredConnections[id] = cloneConnection(current)
	delete(manager.apiConnections, id)
	if err := manager.rebuildLocked(); err != nil {
		delete(manager.retiredConnections, id)
		manager.apiConnections[id] = current
		return err
	}
	return nil
}

// DeleteConnection is an alias retaining HTTP terminology.
func (manager *Manager) DeleteConnection(ctx context.Context, id domain.ConfigID, expectedRevision string) error {
	return manager.RetireConnection(ctx, id, expectedRevision)
}

// CreateStorageRoot adds an API-owned storage root atomically.
func (manager *Manager) CreateStorageRoot(ctx context.Context, spec StorageRootSpec) (domain.StorageRoot, error) {
	if err := contextError(ctx); err != nil {
		return domain.StorageRoot{}, err
	}
	if err := spec.validate(); err != nil {
		return domain.StorageRoot{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.apiRoots[spec.ID]; exists {
		return domain.StorageRoot{}, fmt.Errorf("%w: storage root already exists", ErrConfigSourceConflict)
	}
	if _, exists := manager.yamlRoots[spec.ID]; exists {
		return domain.StorageRoot{}, fmt.Errorf("%w: storage root id is owned by YAML", ErrConfigSourceConflict)
	}
	if _, exists := manager.retiredRoots[spec.ID]; exists {
		return domain.StorageRoot{}, fmt.Errorf("%w: retired storage root id cannot be reused", ErrResourceRetired)
	}
	root := domain.StorageRoot{ID: spec.ID, Label: spec.Label, Purpose: spec.Purpose, Path: spec.Path, ReadOnly: spec.ReadOnly, Capabilities: slices.Clone(spec.Capabilities), Watch: spec.Watch, Source: manager.apiMetadata("storage-root", spec.ID, "pending")}
	root.Revision = rootRevision(root)
	root.Source.Revision = root.Revision
	manager.apiRoots[spec.ID] = cloneRoot(root)
	if err := manager.rebuildLocked(); err != nil {
		delete(manager.apiRoots, spec.ID)
		return domain.StorageRoot{}, err
	}
	return cloneRoot(root), nil
}

// UpdateStorageRoot updates an API-owned root with an ETag-style revision.
func (manager *Manager) UpdateStorageRoot(ctx context.Context, id domain.ConfigID, expectedRevision string, patch StorageRootPatch) (domain.StorageRoot, error) {
	if err := contextError(ctx); err != nil {
		return domain.StorageRoot{}, err
	}
	if strings.TrimSpace(expectedRevision) == "" {
		return domain.StorageRoot{}, ErrPreconditionRequired
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, exists := manager.apiRoots[id]
	if !exists {
		if _, yamlOwned := manager.yamlRoots[id]; yamlOwned {
			return domain.StorageRoot{}, ErrConfigSourceReadOnly
		}
		if _, retired := manager.retiredRoots[id]; retired {
			if manager.retiredRoots[id].Source.Source == domain.SourceYAML {
				return domain.StorageRoot{}, ErrConfigSourceReadOnly
			}
			return domain.StorageRoot{}, ErrResourceRetired
		}
		return domain.StorageRoot{}, ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		return domain.StorageRoot{}, ErrRevisionMismatch
	}
	updated := cloneRoot(current)
	changed := make([]string, 0, 5)
	if patch.Label != nil && *patch.Label != current.Label {
		updated.Label = *patch.Label
		changed = append(changed, "label")
	}
	if patch.Path != nil && *patch.Path != current.Path {
		updated.Path = *patch.Path
		changed = append(changed, "path")
	}
	if patch.ReadOnly != nil && *patch.ReadOnly != current.ReadOnly {
		updated.ReadOnly = *patch.ReadOnly
		changed = append(changed, "readOnly")
	}
	if patch.Capabilities != nil && !capabilitiesEqual(*patch.Capabilities, current.Capabilities) {
		updated.Capabilities = slices.Clone(*patch.Capabilities)
		changed = append(changed, "capabilities")
	}
	if patch.WatchEnabled != nil && *patch.WatchEnabled != current.Watch.Enabled {
		updated.Watch.Enabled = *patch.WatchEnabled
		changed = append(changed, "watch.enabled")
	}
	if patch.WatchIntervalSeconds != nil {
		interval, err := durationFromSeconds(*patch.WatchIntervalSeconds)
		if err != nil {
			return domain.StorageRoot{}, err
		}
		if interval != current.Watch.Interval {
			updated.Watch.Interval = interval
			changed = append(changed, "watch.interval")
		}
	}
	if len(changed) == 0 {
		return cloneRoot(current), nil
	}
	updated.Revision = rootRevision(updated)
	updated.Source.Revision = updated.Revision
	if err := updated.Validate(); err != nil {
		return domain.StorageRoot{}, fmt.Errorf("%w: API storage root: %v", ErrInvalidDocument, err)
	}
	if err := validateStorageRootPath(updated.Path); err != nil {
		return domain.StorageRoot{}, err
	}
	for _, capability := range updated.Capabilities {
		if err := capability.Validate(); err != nil {
			return domain.StorageRoot{}, fmt.Errorf("%w: API storage root capability", ErrInvalidDocument)
		}
	}
	change := RevisionChange{Kind: ResourceRoot, ID: id, PreviousRevision: current.Revision, Revision: updated.Revision, ChangedFields: slices.Clone(changed), AuthorityChanged: containsAny(changed, "purpose", "path", "readOnly", "capabilities")}
	if manager.invalidator != nil && change.AuthorityChanged {
		if err := manager.invalidator(ctx, change); err != nil {
			return domain.StorageRoot{}, err
		}
	}
	manager.apiRoots[id] = updated
	if err := manager.rebuildLocked(); err != nil {
		manager.apiRoots[id] = current
		return domain.StorageRoot{}, err
	}
	return cloneRoot(updated), nil
}

// PatchStorageRoot is an API-compatible alias for UpdateStorageRoot.
func (manager *Manager) PatchStorageRoot(ctx context.Context, id domain.ConfigID, expectedRevision string, patch StorageRootPatch) (domain.StorageRoot, error) {
	return manager.UpdateStorageRoot(ctx, id, expectedRevision, patch)
}

// RetireStorageRoot retires an API-owned root and preserves history.
func (manager *Manager) RetireStorageRoot(ctx context.Context, id domain.ConfigID, expectedRevision string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(expectedRevision) == "" {
		return ErrPreconditionRequired
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, exists := manager.apiRoots[id]
	if !exists {
		if _, yamlOwned := manager.yamlRoots[id]; yamlOwned {
			return ErrConfigSourceReadOnly
		}
		if _, retired := manager.retiredRoots[id]; retired {
			if manager.retiredRoots[id].Source.Source == domain.SourceYAML {
				return ErrConfigSourceReadOnly
			}
			return ErrResourceRetired
		}
		return ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		return ErrRevisionMismatch
	}
	if manager.rootHasMappingsLocked(id) {
		return fmt.Errorf("%w: storage root is referenced by a path mapping", ErrConfigSourceConflict)
	}
	if manager.invalidator != nil {
		change := RevisionChange{Kind: ResourceRoot, ID: id, PreviousRevision: current.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true}
		if err := manager.invalidator(ctx, change); err != nil {
			return err
		}
	}
	retiredAt := manager.now().UTC()
	current.RetiredAt = &retiredAt
	manager.retiredRoots[id] = cloneRoot(current)
	delete(manager.apiRoots, id)
	if err := manager.rebuildLocked(); err != nil {
		delete(manager.retiredRoots, id)
		manager.apiRoots[id] = current
		return err
	}
	return nil
}

// DeleteStorageRoot is an alias retaining HTTP terminology.
func (manager *Manager) DeleteStorageRoot(ctx context.Context, id domain.ConfigID, expectedRevision string) error {
	return manager.RetireStorageRoot(ctx, id, expectedRevision)
}

// CreatePathMapping adds an API-owned mapping after complete reference and
// ambiguity validation.
func (manager *Manager) CreatePathMapping(ctx context.Context, spec PathMappingSpec) (domain.PathMapping, error) {
	if err := contextError(ctx); err != nil {
		return domain.PathMapping{}, err
	}
	if err := spec.validate(); err != nil {
		return domain.PathMapping{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.apiMappings[spec.ID]; exists {
		return domain.PathMapping{}, fmt.Errorf("%w: path mapping already exists", ErrConfigSourceConflict)
	}
	if _, exists := manager.yamlMappings[spec.ID]; exists {
		return domain.PathMapping{}, fmt.Errorf("%w: path mapping id is owned by YAML", ErrConfigSourceConflict)
	}
	if _, exists := manager.retiredMappings[spec.ID]; exists {
		return domain.PathMapping{}, fmt.Errorf("%w: retired path mapping id cannot be reused", ErrResourceRetired)
	}
	mapping := domain.PathMapping{ID: spec.ID, ConnectionID: spec.ConnectionID, SourcePrefix: spec.SourcePrefix, RootID: spec.RootID, DestinationPrefix: spec.DestinationPrefix, Source: manager.apiMetadata("path-mapping", spec.ID, "pending")}
	mapping.Revision = mappingRevision(mapping)
	mapping.Source.Revision = mapping.Revision
	manager.apiMappings[spec.ID] = cloneMapping(mapping)
	if err := manager.rebuildLocked(); err != nil {
		delete(manager.apiMappings, spec.ID)
		return domain.PathMapping{}, err
	}
	return cloneMapping(mapping), nil
}

// UpdatePathMapping updates prefixes using optimistic concurrency.
func (manager *Manager) UpdatePathMapping(ctx context.Context, id domain.ConfigID, expectedRevision string, patch PathMappingPatch) (domain.PathMapping, error) {
	if err := contextError(ctx); err != nil {
		return domain.PathMapping{}, err
	}
	if strings.TrimSpace(expectedRevision) == "" {
		return domain.PathMapping{}, ErrPreconditionRequired
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, exists := manager.apiMappings[id]
	if !exists {
		if _, yamlOwned := manager.yamlMappings[id]; yamlOwned {
			return domain.PathMapping{}, ErrConfigSourceReadOnly
		}
		if retired, retiredExists := manager.retiredMappings[id]; retiredExists {
			if retired.Source.Source == domain.SourceYAML {
				return domain.PathMapping{}, ErrConfigSourceReadOnly
			}
			return domain.PathMapping{}, ErrResourceRetired
		}
		return domain.PathMapping{}, ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		return domain.PathMapping{}, ErrRevisionMismatch
	}
	updated := cloneMapping(current)
	changed := make([]string, 0, 2)
	if patch.SourcePrefix != nil && *patch.SourcePrefix != current.SourcePrefix {
		updated.SourcePrefix = *patch.SourcePrefix
		changed = append(changed, "sourcePrefix")
	}
	if patch.DestinationPrefix != nil && *patch.DestinationPrefix != current.DestinationPrefix {
		updated.DestinationPrefix = *patch.DestinationPrefix
		changed = append(changed, "destinationPrefix")
	}
	if len(changed) == 0 {
		return cloneMapping(current), nil
	}
	updated.Revision = mappingRevision(updated)
	updated.Source.Revision = updated.Revision
	if err := validateMappingShape(updated); err != nil {
		return domain.PathMapping{}, err
	}
	change := RevisionChange{Kind: ResourceMapping, ID: id, PreviousRevision: current.Revision, Revision: updated.Revision, ChangedFields: slices.Clone(changed), AuthorityChanged: len(changed) != 0}
	if manager.invalidator != nil && change.AuthorityChanged {
		if err := manager.invalidator(ctx, change); err != nil {
			return domain.PathMapping{}, err
		}
	}
	manager.apiMappings[id] = updated
	if err := manager.rebuildLocked(); err != nil {
		manager.apiMappings[id] = current
		return domain.PathMapping{}, err
	}
	return cloneMapping(updated), nil
}

// PatchPathMapping is an API-compatible alias for UpdatePathMapping.
func (manager *Manager) PatchPathMapping(ctx context.Context, id domain.ConfigID, expectedRevision string, patch PathMappingPatch) (domain.PathMapping, error) {
	return manager.UpdatePathMapping(ctx, id, expectedRevision, patch)
}

// RetirePathMapping retires an API-owned mapping and retains history.
func (manager *Manager) RetirePathMapping(ctx context.Context, id domain.ConfigID, expectedRevision string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(expectedRevision) == "" {
		return ErrPreconditionRequired
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, exists := manager.apiMappings[id]
	if !exists {
		if _, yamlOwned := manager.yamlMappings[id]; yamlOwned {
			return ErrConfigSourceReadOnly
		}
		if retired, retiredExists := manager.retiredMappings[id]; retiredExists {
			if retired.Source.Source == domain.SourceYAML {
				return ErrConfigSourceReadOnly
			}
			return ErrResourceRetired
		}
		return ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		return ErrRevisionMismatch
	}
	if manager.invalidator != nil {
		change := RevisionChange{Kind: ResourceMapping, ID: id, PreviousRevision: current.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true}
		if err := manager.invalidator(ctx, change); err != nil {
			return err
		}
	}
	manager.retiredMappings[id] = cloneMapping(current)
	delete(manager.apiMappings, id)
	if err := manager.rebuildLocked(); err != nil {
		delete(manager.retiredMappings, id)
		manager.apiMappings[id] = current
		return err
	}
	return nil
}

// DeletePathMapping is an alias retaining HTTP terminology.
func (manager *Manager) DeletePathMapping(ctx context.Context, id domain.ConfigID, expectedRevision string) error {
	return manager.RetirePathMapping(ctx, id, expectedRevision)
}

func (manager *Manager) prepareManagedCredentialsLocked(ctx context.Context, id domain.ConfigID, inputs map[string]CredentialInput) (map[string]credentials.Envelope, error) {
	if inputs == nil {
		return nil, nil
	}
	if len(inputs) == 0 {
		return map[string]credentials.Envelope{}, nil
	}
	if manager.credentialStore == nil {
		return nil, ErrCredentialStoreRequired
	}
	if manager.credentialManager == nil {
		return nil, ErrCredentialManagerNeeded
	}
	envelopes := make(map[string]credentials.Envelope, len(inputs))
	for field, input := range inputs {
		if validateCredentialField(field) != nil || input.Reference != nil || len(input.Value) == 0 {
			return nil, ErrCredentialInvalid
		}
		plaintext := append([]byte(nil), input.Value...)
		envelope, err := manager.credentialManager.Seal(id.String(), field, plaintext)
		zero(plaintext)
		if err != nil {
			return nil, ErrCredentialInvalid
		}
		envelopes[field] = envelope
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return envelopes, nil
}

func (manager *Manager) apiMetadata(kind string, id domain.ConfigID, revision string) domain.SourceMetadata {
	return sourceMetadata(domain.SourceAPI, true, "api", revision, manager.startupAt)
}

// StaticResolver is a deterministic resolver used by tests and embedded
// callers. Values are copied at construction and never mutated by callers.
type StaticResolver struct {
	environment map[string]string
	files       map[string][]byte
}

// NewStaticResolver creates a resolver from synthetic environment and files.
func NewStaticResolver(environment map[string]string, files map[string][]byte) *StaticResolver {
	resolver := &StaticResolver{environment: maps.Clone(environment), files: make(map[string][]byte, len(files))}
	for name, value := range files {
		resolver.files[name] = append([]byte(nil), value...)
	}
	return resolver
}

func (resolver *StaticResolver) Environment(name string) (string, bool) {
	if resolver == nil {
		return "", false
	}
	value, exists := resolver.environment[name]
	return value, exists
}

func (resolver *StaticResolver) File(filePath string) ([]byte, error) {
	if resolver == nil {
		return nil, ErrCredentialUnavailable
	}
	value, exists := resolver.files[filePath]
	if !exists {
		return nil, ErrCredentialUnavailable
	}
	return append([]byte(nil), value...), nil
}

type memoryCredentialStore struct {
	mu     sync.Mutex
	values map[domain.ConfigID]map[string]credentials.Envelope
}

// NewMemoryCredentialStore creates an atomic synthetic store useful for unit
// tests and for callers that have not yet wired the SQL repository adapter.
func NewMemoryCredentialStore() ManagedCredentialStore {
	return &memoryCredentialStore{values: make(map[domain.ConfigID]map[string]credentials.Envelope)}
}

func (store *memoryCredentialStore) Load(ctx context.Context, id domain.ConfigID) (map[string]credentials.Envelope, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	values := store.values[id]
	copyValues := make(map[string]credentials.Envelope, len(values))
	for field, envelope := range values {
		copyValues[field] = cloneEnvelope(envelope)
	}
	return copyValues, nil
}

func (store *memoryCredentialStore) Replace(ctx context.Context, id domain.ConfigID, values map[string]credentials.Envelope) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	copyValues := make(map[string]credentials.Envelope, len(values))
	for field, envelope := range values {
		copyValues[field] = cloneEnvelope(envelope)
	}
	store.values[id] = copyValues
	return nil
}

func (parsed *ParsedYAML) clearSecrets() {
	for key, value := range parsed.StaticCredentials {
		zero(value)
		delete(parsed.StaticCredentials, key)
	}
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func maxDocumentLimit(limit int) int {
	if limit <= 0 {
		return defaultMaxDocumentSize
	}
	return limit
}

func sourceMetadata(source domain.Source, editable bool, documentID, revision string, startupAt time.Time) domain.SourceMetadata {
	return domain.SourceMetadata{Source: source, Editable: editable, DocumentID: documentID, Revision: revision, StartupAt: startupAt.UTC(), ReloadPolicy: domain.ReloadOnRestart}
}

func digestValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "sha256:invalid"
	}
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func connectionRevision(connection domain.Connection) string {
	return digestValue(struct {
		ID          domain.ConfigID
		Kind        domain.ConnectionKind
		Label       string
		Endpoint    string
		Credentials []string
	}{connection.ID, connection.Kind, connection.Label, connection.Endpoint, sortedCredentialReferences(connection.Credentials)})
}

func (manager *Manager) connectionRevisionLocked(connection domain.Connection) string {
	managed := make([]string, 0, len(manager.managedFields[connection.ID]))
	for field := range manager.managedFields[connection.ID] {
		managed = append(managed, field)
	}
	sort.Strings(managed)
	return digestValue(struct {
		ID            domain.ConfigID
		Kind          domain.ConnectionKind
		Label         string
		Endpoint      string
		Credentials   []string
		Managed       []string
		ManagedDigest string
	}{connection.ID, connection.Kind, connection.Label, connection.Endpoint, sortedCredentialReferences(connection.Credentials), managed, manager.managedDigests[connection.ID]})
}

func rootRevision(root domain.StorageRoot) string {
	return digestValue(struct {
		ID           domain.ConfigID
		Label        string
		Purpose      domain.StoragePurpose
		Path         string
		ReadOnly     bool
		Capabilities []domain.Capability
		Watch        domain.WatchSettings
	}{root.ID, root.Label, root.Purpose, root.Path, root.ReadOnly, root.Capabilities, root.Watch})
}

func mappingRevision(mapping domain.PathMapping) string {
	return digestValue(struct {
		ID                domain.ConfigID
		ConnectionID      domain.ConfigID
		SourcePrefix      string
		RootID            domain.ConfigID
		DestinationPrefix string
	}{mapping.ID, mapping.ConnectionID, mapping.SourcePrefix, mapping.RootID, mapping.DestinationPrefix})
}

func sortedCredentialReferences(values map[string]domain.CredentialReference) []string {
	keys := make([]string, 0, len(values))
	for key, reference := range values {
		keys = append(keys, key+"="+reference.Kind+":"+reference.Value)
	}
	sort.Strings(keys)
	return keys
}

func credentialFieldSet(values map[string]CredentialInput) map[string]struct{} {
	if values == nil {
		return nil
	}
	fields := make(map[string]struct{}, len(values))
	for field := range values {
		fields[field] = struct{}{}
	}
	return fields
}

func cloneFieldSet(values map[string]struct{}) map[string]struct{} {
	if values == nil {
		return nil
	}
	copyValues := make(map[string]struct{}, len(values))
	for field := range values {
		copyValues[field] = struct{}{}
	}
	return copyValues
}

func credentialEnvelopeDigest(values map[string]credentials.Envelope) string {
	type item struct {
		Field          string
		Version        int64
		Nonce          []byte
		Ciphertext     []byte
		KeyFingerprint string
	}
	items := make([]item, 0, len(values))
	for field, envelope := range values {
		items = append(items, item{Field: field, Version: envelope.Version, Nonce: envelope.Nonce, Ciphertext: envelope.Ciphertext, KeyFingerprint: envelope.KeyFingerprint})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Field < items[j].Field })
	return digestValue(items)
}

func containsAny(values []string, wanted ...string) bool {
	for _, value := range values {
		for _, candidate := range wanted {
			if value == candidate {
				return true
			}
		}
	}
	return false
}

func findConnection(values []domain.Connection, id domain.ConfigID) (domain.Connection, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return domain.Connection{}, false
}

func (manager *Manager) connectionActiveLocked(id domain.ConfigID) bool {
	if _, exists := manager.apiConnections[id]; exists {
		return true
	}
	_, exists := manager.yamlConnections[id]
	return exists
}

func (manager *Manager) connectionHasMappingsLocked(id domain.ConfigID) bool {
	for _, mapping := range manager.apiMappings {
		if mapping.ConnectionID == id {
			return true
		}
	}
	for _, mapping := range manager.yamlMappings {
		if mapping.ConnectionID == id {
			return true
		}
	}
	return false
}

func (manager *Manager) rootHasMappingsLocked(id domain.ConfigID) bool {
	for _, mapping := range manager.apiMappings {
		if mapping.RootID == id {
			return true
		}
	}
	for _, mapping := range manager.yamlMappings {
		if mapping.RootID == id {
			return true
		}
	}
	return false
}

func findRoot(values []domain.StorageRoot, id domain.ConfigID) (domain.StorageRoot, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return domain.StorageRoot{}, false
}

func findMapping(values []domain.PathMapping, id domain.ConfigID) (domain.PathMapping, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return domain.PathMapping{}, false
}

func mergeConnections(api, yaml map[domain.ConfigID]domain.Connection) []domain.Connection {
	items := make([]domain.Connection, 0, len(api)+len(yaml))
	for _, value := range api {
		items = append(items, cloneConnection(value))
	}
	for _, value := range yaml {
		items = append(items, cloneConnection(value))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func mergeRoots(api, yaml map[domain.ConfigID]domain.StorageRoot) []domain.StorageRoot {
	items := make([]domain.StorageRoot, 0, len(api)+len(yaml))
	for _, value := range api {
		items = append(items, cloneRoot(value))
	}
	for _, value := range yaml {
		items = append(items, cloneRoot(value))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func mergeMappings(api, yaml map[domain.ConfigID]domain.PathMapping) []domain.PathMapping {
	items := make([]domain.PathMapping, 0, len(api)+len(yaml))
	for _, value := range api {
		items = append(items, cloneMapping(value))
	}
	for _, value := range yaml {
		items = append(items, cloneMapping(value))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func validateMerged(yaml domain.ConfigurationSnapshot, apiConnections map[domain.ConfigID]domain.Connection, apiRoots map[domain.ConfigID]domain.StorageRoot, apiMappings map[domain.ConfigID]domain.PathMapping) error {
	for _, item := range yaml.Connections {
		if _, exists := apiConnections[item.ID]; exists {
			return fmt.Errorf("%w: connection id %q", ErrConfigSourceConflict, item.ID)
		}
	}
	for _, item := range yaml.StorageRoots {
		if _, exists := apiRoots[item.ID]; exists {
			return fmt.Errorf("%w: storage root id %q", ErrConfigSourceConflict, item.ID)
		}
	}
	for _, item := range yaml.PathMappings {
		if _, exists := apiMappings[item.ID]; exists {
			return fmt.Errorf("%w: path mapping id %q", ErrConfigSourceConflict, item.ID)
		}
	}
	return nil
}

func validateSnapshot(snapshot domain.ConfigurationSnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDocument, err)
	}
	for _, root := range snapshot.StorageRoots {
		if err := validateStorageRootPath(root.Path); err != nil {
			return err
		}
	}
	for _, mapping := range snapshot.PathMappings {
		if err := validateMappingShape(mapping); err != nil {
			return err
		}
	}
	if err := validateMappingAmbiguity(snapshot.PathMappings); err != nil {
		return err
	}
	return nil
}

func validateStorageRootPath(value string) error {
	if value == "" || !filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%w: storage root path must be absolute", ErrInvalidDocument)
	}
	if filepath.Clean(value) != value {
		return fmt.Errorf("%w: storage root path must be canonical", ErrInvalidDocument)
	}
	return nil
}

func validateMappingShape(mapping domain.PathMapping) error {
	if _, err := cleanSourcePrefix(mapping.SourcePrefix); err != nil {
		return fmt.Errorf("%w: source prefix: %v", ErrMappingInvalid, err)
	}
	if _, err := cleanDestinationPrefix(mapping.DestinationPrefix); err != nil {
		return fmt.Errorf("%w: destination prefix: %v", ErrMappingInvalid, err)
	}
	return nil
}

func validateMappingAmbiguity(mappings []domain.PathMapping) error {
	for index, left := range mappings {
		leftPrefix, _ := cleanSourcePrefix(left.SourcePrefix)
		for _, right := range mappings[index+1:] {
			if left.ConnectionID != right.ConnectionID {
				continue
			}
			rightPrefix, _ := cleanSourcePrefix(right.SourcePrefix)
			if namespaceComponentCount(leftPrefix) != namespaceComponentCount(rightPrefix) {
				continue
			}
			if namespaceOverlaps(leftPrefix, rightPrefix) {
				return fmt.Errorf("%w: connection %q has equal-specificity mappings", ErrMappingAmbiguous, left.ConnectionID)
			}
		}
	}
	return nil
}

func cleanSourcePrefix(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return cleanNamespace(value)
}

func cleanDestinationPrefix(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "/") {
		return "", errors.New("destination prefix must be root-relative")
	}
	return cleanNamespace(value)
}

func cleanNamespace(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") {
		return "", errors.New("namespace prefix is invalid")
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." {
		return "", errors.New("namespace prefix is not canonical")
	}
	if cleaned == "/" {
		return cleaned, nil
	}
	parts := strings.Split(strings.Trim(cleaned, "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("namespace prefix contains unsafe component")
		}
	}
	return cleaned, nil
}

func namespaceComponentCount(value string) int {
	value = strings.Trim(value, "/")
	if value == "" {
		return 0
	}
	return len(strings.Split(value, "/"))
}

func namespaceOverlaps(left, right string) bool {
	if left == "" || right == "" || left == "/" || right == "/" {
		return true
	}
	return left == right
}

func openBoundedRegular(filePath string, limit int) (*os.File, error) {
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrInvalidDocument
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, ErrInvalidDocument
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrInvalidDocument
	}
	if openedInfo.Size() > int64(limit) {
		_ = file.Close()
		return nil, ErrInvalidDocument
	}
	return file, nil
}

func cloneSnapshot(snapshot domain.ConfigurationSnapshot) domain.ConfigurationSnapshot {
	copySnapshot := snapshot
	copySnapshot.Source = snapshot.Source
	copySnapshot.Connections = make([]domain.Connection, 0, len(snapshot.Connections))
	for _, item := range snapshot.Connections {
		copySnapshot.Connections = append(copySnapshot.Connections, cloneConnection(item))
	}
	copySnapshot.StorageRoots = make([]domain.StorageRoot, 0, len(snapshot.StorageRoots))
	for _, item := range snapshot.StorageRoots {
		copySnapshot.StorageRoots = append(copySnapshot.StorageRoots, cloneRoot(item))
	}
	copySnapshot.PathMappings = make([]domain.PathMapping, 0, len(snapshot.PathMappings))
	for _, item := range snapshot.PathMappings {
		copySnapshot.PathMappings = append(copySnapshot.PathMappings, cloneMapping(item))
	}
	return copySnapshot
}

func cloneConnection(value domain.Connection) domain.Connection {
	copyValue := value
	copyValue.Credentials = maps.Clone(value.Credentials)
	if value.RetiredAt != nil {
		retiredAt := *value.RetiredAt
		copyValue.RetiredAt = &retiredAt
	}
	return copyValue
}

func cloneRoot(value domain.StorageRoot) domain.StorageRoot {
	copyValue := value
	copyValue.Capabilities = cloneCapabilities(value.Capabilities)
	if value.RetiredAt != nil {
		retiredAt := *value.RetiredAt
		copyValue.RetiredAt = &retiredAt
	}
	return copyValue
}

func cloneCapabilities(values []domain.Capability) []domain.Capability {
	if values == nil {
		return nil
	}
	copyValues := make([]domain.Capability, len(values))
	for index, value := range values {
		copyValues[index] = value
		copyValues[index].Evidence = slices.Clone(value.Evidence)
	}
	return copyValues
}

func capabilitiesEqual(left, right []domain.Capability) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name || left[index].State != right[index].State || left[index].Version != right[index].Version || left[index].Reason != right[index].Reason || !left[index].ObservedAt.Equal(right[index].ObservedAt) || !slices.Equal(left[index].Evidence, right[index].Evidence) {
			return false
		}
	}
	return true
}

func cloneMapping(value domain.PathMapping) domain.PathMapping {
	return value
}

func cloneEnvelope(value credentials.Envelope) credentials.Envelope {
	value.Nonce = append([]byte(nil), value.Nonce...)
	value.Ciphertext = append([]byte(nil), value.Ciphertext...)
	return value
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
