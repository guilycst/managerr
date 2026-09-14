// Package configuration owns Mastarr's effective configuration boundary.
//
// Startup YAML is parsed once and remains read-only. API-owned records are
// changed through validated, revision-checked operations. Both sources are
// merged into an immutable snapshot before workers can consume them.
package configuration

import (
	"bytes"
	"context"
	"crypto/hmac"
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
	ErrInvalidDocument              = errors.New("configuration document is invalid")
	ErrConfigSourceReadOnly         = errors.New("configuration source is read-only")
	ErrConfigSourceConflict         = errors.New("configuration source ownership conflicts")
	ErrRevisionMismatch             = errors.New("configuration revision does not match")
	ErrPreconditionRequired         = errors.New("configuration revision precondition is required")
	ErrResourceNotFound             = errors.New("configuration resource was not found")
	ErrResourceRetired              = errors.New("configuration resource is retired")
	ErrMappingAmbiguous             = errors.New("configuration mapping is ambiguous")
	ErrMappingInvalid               = errors.New("configuration mapping is invalid")
	ErrCredentialUnavailable        = errors.New("configuration credential is unavailable")
	ErrCredentialInvalid            = errors.New("configuration credential is invalid")
	ErrCredentialStoreRequired      = errors.New("managed credential store is required")
	ErrCredentialManagerNeeded      = errors.New("managed credential manager is required")
	ErrCredentialIdentityUnverified = errors.New("configuration credential target identity is unverified")
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

// IdentityVerification is the result of an optional target identity check.
// Credential-only changes are allowed to preserve approval intent only when
// this result is IdentityVerified. Unknown, failed, or errored checks fail
// closed when no invalidator is available, and otherwise invalidate the
// affected plans before activation.
type IdentityVerification string

const (
	IdentityUnknown  IdentityVerification = "unknown"
	IdentityVerified IdentityVerification = "verified"
	IdentityFailed   IdentityVerification = "failed"
)

// IdentityVerifier verifies that a connection credential-only change still
// addresses the same upstream target. It receives no credential plaintext.
type IdentityVerifier func(context.Context, domain.Connection) (IdentityVerification, error)

// TargetIdentityVerifier is an expressive alias for IdentityVerifier.
type TargetIdentityVerifier = IdentityVerifier

// CandidateCredentialReader provides an attempt-scoped view of the managed
// credential candidate to a target identity verifier. Values are decrypted
// only for the duration of the verifier call and are never included in a
// domain object, revision, snapshot, or persisted record. A verifier must not
// retain returned bytes and should zero them after use.
type CandidateCredentialReader func(field string) ([]byte, error)

// CandidateIdentityVerifier verifies a target using the exact managed
// credential candidate that is about to be committed. The reader is scoped to
// one authorization attempt and returns no value for fields outside that
// candidate set.
type CandidateIdentityVerifier func(context.Context, domain.Connection, CandidateCredentialReader) (IdentityVerification, error)

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
	// ManagedCredentialDigests binds each active API connection to the
	// complete encrypted envelope set persisted for it. New revisions carry
	// the same opaque digest, while this field lets storage adapters migrate
	// without exposing credential values.
	ManagedCredentialDigests map[domain.ConfigID]string
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

	APIState                  APIState
	CredentialManager         *credentials.Manager
	CredentialStore           ManagedCredentialStore
	KeySource                 string
	KeyPath                   string
	Invalidator               InvalidateFunc
	IdentityVerifier          IdentityVerifier
	CandidateIdentityVerifier CandidateIdentityVerifier
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
	staticBindings    map[CredentialKey]string
}

// Manager owns source records, historical tombstones and the effective view.
type Manager struct {
	mu           sync.RWMutex
	credentialMu sync.Mutex

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

	retiredConnections   map[domain.ConfigID]domain.Connection
	retiredRoots         map[domain.ConfigID]domain.StorageRoot
	retiredMappings      map[domain.ConfigID]domain.PathMapping
	staticCredentials    map[CredentialKey][]byte
	staticBindings       map[CredentialKey]string
	credentialQuarantine map[domain.ConfigID]struct{}
	pendingConnections   map[domain.ConfigID]struct{}
	generation           uint64
	bindingKey           []byte

	policy                    Policy
	snapshot                  domain.ConfigurationSnapshot
	credentialManager         *credentials.Manager
	credentialStore           ManagedCredentialStore
	keySource                 string
	keyPath                   string
	invalidator               InvalidateFunc
	identityVerifier          IdentityVerifier
	candidateIdentityVerifier CandidateIdentityVerifier
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
	bindingKey := stableBindingKey(documentID, options.CredentialManager)

	manager := &Manager{
		now:                       now,
		startupAt:                 startupAt,
		maxDocument:               maxDocument,
		documentID:                documentID,
		yamlConfigured:            len(options.YAML) != 0 || options.YAMLPath != "",
		secretResolver:            resolver,
		yamlConnections:           make(map[domain.ConfigID]domain.Connection),
		yamlRoots:                 make(map[domain.ConfigID]domain.StorageRoot),
		yamlMappings:              make(map[domain.ConfigID]domain.PathMapping),
		apiConnections:            make(map[domain.ConfigID]domain.Connection),
		apiRoots:                  make(map[domain.ConfigID]domain.StorageRoot),
		apiMappings:               make(map[domain.ConfigID]domain.PathMapping),
		managedFields:             make(map[domain.ConfigID]map[string]struct{}),
		managedDigests:            make(map[domain.ConfigID]string),
		retiredConnections:        make(map[domain.ConfigID]domain.Connection),
		retiredRoots:              make(map[domain.ConfigID]domain.StorageRoot),
		retiredMappings:           make(map[domain.ConfigID]domain.PathMapping),
		staticCredentials:         make(map[CredentialKey][]byte),
		staticBindings:            make(map[CredentialKey]string),
		credentialQuarantine:      make(map[domain.ConfigID]struct{}),
		pendingConnections:        make(map[domain.ConfigID]struct{}),
		generation:                1,
		bindingKey:                bindingKey,
		policy:                    Policy{TrashRetention: defaultTrashRetention, JanitorInterval: defaultJanitorInterval},
		credentialManager:         options.CredentialManager,
		credentialStore:           options.CredentialStore,
		keySource:                 options.KeySource,
		keyPath:                   options.KeyPath,
		invalidator:               options.Invalidator,
		identityVerifier:          options.IdentityVerifier,
		candidateIdentityVerifier: options.CandidateIdentityVerifier,
	}
	if err := manager.installAPIState(options.APIState); err != nil {
		return nil, err
	}
	if len(options.YAML) != 0 || options.YAMLPath != "" {
		var parsed ParsedYAML
		var err error
		if options.YAMLPath != "" {
			parsed, err = loadYAMLFile(options.YAMLPath, ParseOptions{
				DocumentID:       documentID,
				StartupAt:        startupAt,
				SecretResolver:   resolver,
				MaxDocumentBytes: maxDocument,
			}, manager.bindingKey)
		} else {
			parsed, err = parseYAML(options.YAML, ParseOptions{
				DocumentID:       documentID,
				StartupAt:        startupAt,
				SecretResolver:   resolver,
				MaxDocumentBytes: maxDocument,
			}, manager.bindingKey)
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
	manager.reconcileManagedCredentials(context.Background())
	return manager, nil
}

// Close releases package-owned cached static secret and encryption key bytes.
// Persistent stores own their own resources and are not closed by Manager.
func (manager *Manager) Close() error {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	for key, value := range manager.staticCredentials {
		zero(value)
		delete(manager.staticCredentials, key)
	}
	crypt := manager.credentialManager
	manager.credentialManager = nil
	zero(manager.bindingKey)
	manager.bindingKey = nil
	manager.mu.Unlock()
	manager.credentialMu.Lock()
	if crypt != nil {
		crypt.Close()
	}
	manager.credentialMu.Unlock()
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
	bindingKey := stableBindingKey(options.DocumentID, nil)
	defer zero(bindingKey)
	return parseYAML(data, options, bindingKey)
}

func parseYAML(data []byte, options ParseOptions, bindingKey []byte) (ParsedYAML, error) {
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
		staticBindings:    make(map[CredentialKey]string),
		DocumentBytes:     len(data),
	}
	for _, raw := range document.Connections {
		connection, values, err := raw.toDomain(yamlSource, resolver)
		if err != nil {
			parsed.clearSecrets()
			return ParsedYAML{}, err
		}
		if _, exists := findConnection(parsed.Snapshot.Connections, connection.ID); exists {
			zeroResolvedValues(values)
			parsed.clearSecrets()
			return ParsedYAML{}, fmt.Errorf("%w: duplicate connection id", ErrInvalidDocument)
		}
		bindings := make([]string, 0, len(values))
		parsed.Snapshot.Connections = append(parsed.Snapshot.Connections, connection)
		for field, value := range values {
			key := CredentialKey{ConnectionID: connection.ID, Field: field}
			parsed.StaticCredentials[key] = value
			binding := opaqueCredentialBinding(bindingKey, value)
			parsed.staticBindings[key] = binding
			bindings = append(bindings, field+"="+binding)
		}
		connection.Revision = connectionRevisionWithBindings(connection, bindings)
		parsed.Snapshot.Connections[len(parsed.Snapshot.Connections)-1] = connection
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
	if strings.TrimSpace(options.DocumentID) == "" {
		options.DocumentID = filepath.Base(strings.TrimSpace(filePath))
	}
	bindingKey := stableBindingKey(options.DocumentID, nil)
	defer zero(bindingKey)
	return loadYAMLFile(filePath, options, bindingKey)
}

func loadYAMLFile(filePath string, options ParseOptions, bindingKey []byte) (ParsedYAML, error) {
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
	return parseYAML(data, options, bindingKey)
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
	bindingKey := append([]byte(nil), manager.bindingKey...)
	manager.mu.RUnlock()
	parsed, err := parseYAML(data, ParseOptions{
		DocumentID:       documentID,
		StartupAt:        startupAt,
		SecretResolver:   resolver,
		MaxDocumentBytes: maxDocument,
	}, bindingKey)
	zero(bindingKey)
	if err != nil {
		return err
	}
	return manager.activateYAML(ctx, parsed)
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
	bindingKey := append([]byte(nil), manager.bindingKey...)
	manager.mu.RUnlock()
	parsed, err := loadYAMLFile(filePath, ParseOptions{
		DocumentID:       documentID,
		StartupAt:        startupAt,
		SecretResolver:   resolver,
		MaxDocumentBytes: maxDocument,
	}, bindingKey)
	zero(bindingKey)
	if err != nil {
		return err
	}
	return manager.activateYAML(ctx, parsed)
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
	for id, digest := range state.ManagedCredentialDigests {
		if !id.Valid() || !validCredentialEnvelopeDigest(digest) {
			return fmt.Errorf("%w: managed credential digest is invalid", ErrInvalidDocument)
		}
		connection, exists := manager.apiConnections[id]
		if !exists {
			return fmt.Errorf("%w: managed credential digest references unknown API connection", ErrInvalidDocument)
		}
		if embedded := managedDigestFromRevision(connection.Revision); embedded != "" && embedded != digest {
			return fmt.Errorf("%w: managed credential digest disagrees with connection revision", ErrInvalidDocument)
		}
		manager.managedDigests[id] = digest
	}
	for id, fields := range manager.managedFields {
		if _, active := manager.apiConnections[id]; !active || len(fields) == 0 {
			continue
		}
		if _, exists := manager.managedDigests[id]; exists {
			continue
		}
		if digest := managedDigestFromRevision(manager.apiConnections[id].Revision); digest != "" {
			manager.managedDigests[id] = digest
		}
	}
	return nil
}

type managedCredentialReconcile struct {
	id       domain.ConfigID
	revision string
	digest   string
}

// reconcileManagedCredentials makes the persisted encrypted envelope set a
// prerequisite for exposing an active managed field. A process can terminate
// after Replace and before the configuration CAS; on restart the old
// connection revision therefore remains authoritative and any different
// complete set is quarantined until an explicit repair changes both records.
// Store I/O is deliberately performed without manager.mu held.
func (manager *Manager) reconcileManagedCredentials(ctx context.Context) {
	manager.mu.RLock()
	store := manager.credentialStore
	candidates := make([]managedCredentialReconcile, 0, len(manager.managedFields))
	for id, fields := range manager.managedFields {
		if len(fields) == 0 {
			continue
		}
		connection, active := manager.apiConnections[id]
		if !active {
			continue
		}
		candidates = append(candidates, managedCredentialReconcile{id: id, revision: connection.Revision, digest: manager.managedDigests[id]})
	}
	manager.mu.RUnlock()
	if len(candidates) == 0 {
		return
	}
	for _, candidate := range candidates {
		matched := false
		if contextError(ctx) == nil && store != nil && candidate.digest != "" {
			actual, err := store.Load(ctx, candidate.id)
			matched = err == nil && credentialEnvelopeDigest(actual) == candidate.digest
		}
		manager.mu.Lock()
		current, active := manager.apiConnections[candidate.id]
		fields, managed := manager.managedFields[candidate.id]
		if active && managed && len(fields) != 0 && current.Revision == candidate.revision && !matched {
			manager.credentialQuarantine[candidate.id] = struct{}{}
		}
		manager.mu.Unlock()
	}
}

func (manager *Manager) activateYAML(ctx context.Context, parsed ParsedYAML) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextError(ctx); err != nil {
		parsed.clearSecrets()
		return err
	}
	manager.mu.Lock()
	prepared, err := manager.prepareYAMLActivationLocked(parsed)
	manager.mu.Unlock()
	if err != nil {
		parsed.clearSecrets()
		return err
	}
	if err := manager.runRevisionChanges(ctx, prepared.changes, prepared.connections); err != nil {
		parsed.clearSecrets()
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.generation != prepared.generation {
		parsed.clearSecrets()
		return ErrRevisionMismatch
	}
	return manager.commitYAMLActivationLocked(parsed, prepared)
}

type yamlActivation struct {
	generation  uint64
	connections map[domain.ConfigID]domain.Connection
	roots       map[domain.ConfigID]domain.StorageRoot
	mappings    map[domain.ConfigID]domain.PathMapping
	candidate   domain.ConfigurationSnapshot
	changes     []RevisionChange
}

func (manager *Manager) prepareYAMLActivationLocked(parsed ParsedYAML) (yamlActivation, error) {
	if err := validateMerged(parsed.Snapshot, manager.apiConnections, manager.apiRoots, manager.apiMappings); err != nil {
		return yamlActivation{}, err
	}
	for id, previous := range manager.retiredConnections {
		if _, active := findConnection(parsed.Snapshot.Connections, id); active && previous.Source.Source == domain.SourceAPI {
			return yamlActivation{}, fmt.Errorf("%w: retired API connection id %q", ErrConfigSourceConflict, id)
		}
	}
	for id, previous := range manager.retiredRoots {
		if _, active := findRoot(parsed.Snapshot.StorageRoots, id); active && previous.Source.Source == domain.SourceAPI {
			return yamlActivation{}, fmt.Errorf("%w: retired API storage root id %q", ErrConfigSourceConflict, id)
		}
	}
	for id, previous := range manager.retiredMappings {
		if _, active := findMapping(parsed.Snapshot.PathMappings, id); active && previous.Source.Source == domain.SourceAPI {
			return yamlActivation{}, fmt.Errorf("%w: retired API path mapping id %q", ErrConfigSourceConflict, id)
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
		return yamlActivation{}, err
	}
	return yamlActivation{
		generation:  manager.generation,
		connections: newConnections,
		roots:       newRoots,
		mappings:    newMappings,
		candidate:   candidate,
		changes:     manager.yamlChangesLocked(newConnections, newRoots, newMappings, parsed.staticBindings),
	}, nil
}

func (manager *Manager) commitYAMLActivationLocked(parsed ParsedYAML, prepared yamlActivation) error {
	retiredAt := manager.now().UTC()
	for id, old := range manager.yamlConnections {
		if _, exists := prepared.connections[id]; !exists {
			old.RetiredAt = &retiredAt
			manager.retiredConnections[id] = cloneConnection(old)
		}
	}
	for id, old := range manager.yamlRoots {
		if _, exists := prepared.roots[id]; !exists {
			old.RetiredAt = &retiredAt
			manager.retiredRoots[id] = cloneRoot(old)
		}
	}
	for id, old := range manager.yamlMappings {
		if _, exists := prepared.mappings[id]; !exists {
			manager.retiredMappings[id] = cloneMapping(old)
		}
	}
	for key, value := range manager.staticCredentials {
		zero(value)
		delete(manager.staticCredentials, key)
	}
	for key := range manager.staticBindings {
		delete(manager.staticBindings, key)
	}
	for key, value := range parsed.StaticCredentials {
		manager.staticCredentials[key] = append([]byte(nil), value...)
	}
	for key, binding := range parsed.staticBindings {
		manager.staticBindings[key] = binding
	}
	parsed.clearSecrets()
	for id := range prepared.connections {
		if retired, exists := manager.retiredConnections[id]; exists && retired.Source.Source == domain.SourceYAML {
			delete(manager.retiredConnections, id)
		}
	}
	for id := range prepared.roots {
		if retired, exists := manager.retiredRoots[id]; exists && retired.Source.Source == domain.SourceYAML {
			delete(manager.retiredRoots, id)
		}
	}
	for id := range prepared.mappings {
		if retired, exists := manager.retiredMappings[id]; exists && retired.Source.Source == domain.SourceYAML {
			delete(manager.retiredMappings, id)
		}
	}
	manager.yamlConnections = prepared.connections
	manager.yamlRoots = prepared.roots
	manager.yamlMappings = prepared.mappings
	manager.yamlConfigured = true
	manager.policy = parsed.Policy
	manager.snapshot = cloneSnapshot(prepared.candidate)
	manager.generation++
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
	return manager.snapshotForStateLocked(manager.apiConnections, manager.apiRoots, manager.apiMappings, yamlConnections, yamlRoots, yamlMappings, yamlConfigured)
}

func (manager *Manager) snapshotForStateLocked(apiConnections map[domain.ConfigID]domain.Connection, apiRoots map[domain.ConfigID]domain.StorageRoot, apiMappings map[domain.ConfigID]domain.PathMapping, yamlConnections map[domain.ConfigID]domain.Connection, yamlRoots map[domain.ConfigID]domain.StorageRoot, yamlMappings map[domain.ConfigID]domain.PathMapping, yamlConfigured bool) (domain.ConfigurationSnapshot, error) {
	connections := mergeConnections(apiConnections, yamlConnections)
	roots := mergeRoots(apiRoots, yamlRoots)
	mappings := mergeMappings(apiMappings, yamlMappings)
	source := manager.effectiveSource(connections, roots, mappings, yamlConfigured, len(apiConnections)+len(apiRoots)+len(apiMappings))
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

// yamlChangesLocked computes the deterministic external effects of replacing
// the YAML-owned records. It performs no callbacks or other external I/O.
func (manager *Manager) yamlChangesLocked(newConnections map[domain.ConfigID]domain.Connection, newRoots map[domain.ConfigID]domain.StorageRoot, newMappings map[domain.ConfigID]domain.PathMapping, newStaticBindings map[CredentialKey]string) []RevisionChange {
	changes := make([]RevisionChange, 0)
	for id, previous := range manager.yamlConnections {
		current, exists := newConnections[id]
		if !exists {
			changes = append(changes, RevisionChange{Kind: ResourceConnection, ID: id, PreviousRevision: previous.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true})
			continue
		}
		fields, authority := connectionChangedFieldsWithBindings(previous, current, manager.staticBindings, newStaticBindings)
		if len(fields) != 0 && (authority || containsAny(fields, "credentials")) {
			changes = append(changes, RevisionChange{Kind: ResourceConnection, ID: id, PreviousRevision: previous.Revision, Revision: current.Revision, ChangedFields: fields, AuthorityChanged: authority})
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
	return changes
}

func (manager *Manager) runRevisionChanges(ctx context.Context, changes []RevisionChange, newConnections map[domain.ConfigID]domain.Connection) error {
	return manager.runRevisionChangesWithCandidates(ctx, changes, newConnections, nil)
}

func (manager *Manager) runRevisionChangesWithCandidates(ctx context.Context, changes []RevisionChange, newConnections map[domain.ConfigID]domain.Connection, candidateCredentials map[domain.ConfigID]map[string]credentials.Envelope) error {
	for _, original := range changes {
		if err := contextError(ctx); err != nil {
			return err
		}
		change := original
		var connection *domain.Connection
		if change.Kind == ResourceConnection {
			if value, exists := newConnections[change.ID]; exists {
				copyValue := cloneConnection(value)
				connection = &copyValue
			}
		}
		var candidate map[string]credentials.Envelope
		if candidateCredentials != nil {
			candidate = candidateCredentials[change.ID]
		}
		if err := manager.authorizeRevisionChangeWithCandidate(ctx, change, connection, candidate); err != nil {
			return err
		}
	}
	return nil
}

func (manager *Manager) authorizeRevisionChange(ctx context.Context, change RevisionChange, connection *domain.Connection) error {
	return manager.authorizeRevisionChangeWithCandidate(ctx, change, connection, nil)
}

func (manager *Manager) authorizeRevisionChangeWithCandidate(ctx context.Context, change RevisionChange, connection *domain.Connection, candidate map[string]credentials.Envelope) error {
	manager.mu.RLock()
	verifier := manager.identityVerifier
	candidateVerifier := manager.candidateIdentityVerifier
	invalidator := manager.invalidator
	crypt := manager.credentialManager
	manager.mu.RUnlock()
	if change.Kind == ResourceConnection && connection != nil && !change.AuthorityChanged && containsAny(change.ChangedFields, "credentials") {
		status := IdentityUnknown
		var verifyErr error
		// Managed API credential changes must be checked with the exact
		// candidate set. The legacy verifier has no way to observe candidate
		// plaintext, so it is intentionally ignored for this path. YAML static
		// values remain internal to this package and retain the legacy seam.
		if candidate != nil && candidateVerifier != nil && crypt != nil {
			status, verifyErr = manager.verifyCandidateIdentity(ctx, *connection, candidate, crypt, candidateVerifier)
		} else if candidate == nil && verifier != nil {
			status, verifyErr = verifier(ctx, *connection)
		}
		if verifyErr == nil && status == IdentityVerified {
			return nil
		}
		change.AuthorityChanged = true
		if invalidator == nil {
			return ErrCredentialIdentityUnverified
		}
	}
	if change.AuthorityChanged && invalidator != nil {
		return invalidator(ctx, change)
	}
	return nil
}

func (manager *Manager) verifyCandidateIdentity(ctx context.Context, connection domain.Connection, candidate map[string]credentials.Envelope, crypt *credentials.Manager, verifier CandidateIdentityVerifier) (IdentityVerification, error) {
	if verifier == nil || crypt == nil {
		return IdentityUnknown, nil
	}
	var resolvedMu sync.Mutex
	resolved := make([][]byte, 0, len(candidate))
	defer func() {
		resolvedMu.Lock()
		deferred := resolved
		resolved = nil
		resolvedMu.Unlock()
		for _, value := range deferred {
			zero(value)
		}
	}()
	reader := CandidateCredentialReader(func(field string) ([]byte, error) {
		if validateCredentialField(field) != nil {
			return nil, ErrCredentialInvalid
		}
		envelope, exists := candidate[field]
		if !exists {
			return nil, ErrCredentialUnavailable
		}
		manager.credentialMu.Lock()
		value, err := crypt.Open(envelope, connection.ID.String(), field)
		manager.credentialMu.Unlock()
		if err != nil {
			return nil, ErrCredentialUnavailable
		}
		resolvedMu.Lock()
		resolved = append(resolved, value)
		resolvedMu.Unlock()
		return value, nil
	})
	return verifier(ctx, connection, reader)
}

func connectionChangedFields(previous, current domain.Connection) ([]string, bool) {
	return connectionChangedFieldsWithBindings(previous, current, nil, nil)
}

func connectionChangedFieldsWithBindings(previous, current domain.Connection, previousBindings, currentBindings map[CredentialKey]string) ([]string, bool) {
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
	if !credentialBindingsEqual(previous.ID, current.ID, previousBindings, currentBindings) && !containsAny(fields, "credentials") {
		fields = append(fields, "credentials")
	}
	return fields, authority
}

func credentialBindingsEqual(previousID, currentID domain.ConfigID, previous, current map[CredentialKey]string) bool {
	if previous == nil && current == nil {
		return true
	}
	fields := make(map[string]struct{})
	for key := range previous {
		if key.ConnectionID == previousID {
			fields[key.Field] = struct{}{}
		}
	}
	for key := range current {
		if key.ConnectionID == currentID {
			fields[key.Field] = struct{}{}
		}
	}
	for field := range fields {
		if previous[CredentialKey{ConnectionID: previousID, Field: field}] != current[CredentialKey{ConnectionID: currentID, Field: field}] {
			return false
		}
	}
	return true
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
	if !manager.credentialFieldActiveLocked(connectionID, field) {
		manager.mu.RUnlock()
		return nil, ErrCredentialUnavailable
	}
	if _, pending := manager.pendingConnections[connectionID]; pending {
		manager.mu.RUnlock()
		return nil, ErrCredentialUnavailable
	}
	if _, quarantined := manager.credentialQuarantine[connectionID]; quarantined {
		manager.mu.RUnlock()
		return nil, ErrCredentialUnavailable
	}
	if value, exists := manager.staticCredentials[key]; exists {
		copyValue := append([]byte(nil), value...)
		manager.mu.RUnlock()
		return copyValue, nil
	}
	store := manager.credentialStore
	crypt := manager.credentialManager
	manager.mu.RUnlock()
	if store == nil || crypt == nil {
		return nil, ErrCredentialUnavailable
	}
	envelopes, err := store.Load(ctx, connectionID)
	if err != nil {
		return nil, ErrCredentialUnavailable
	}
	observedDigest := credentialEnvelopeDigest(envelopes)
	envelope, exists := envelopes[field]
	if !exists {
		return nil, ErrCredentialUnavailable
	}
	manager.credentialMu.Lock()
	value, err := crypt.Open(envelope, connectionID.String(), field)
	manager.credentialMu.Unlock()
	if err != nil {
		return nil, ErrCredentialUnavailable
	}
	manager.mu.RLock()
	valid := manager.connectionActiveLocked(connectionID) && manager.credentialFieldActiveLocked(connectionID, field)
	_, pending := manager.pendingConnections[connectionID]
	_, quarantined := manager.credentialQuarantine[connectionID]
	valid = valid && !pending && !quarantined && manager.managedDigests[connectionID] == observedDigest
	manager.mu.RUnlock()
	if !valid {
		zero(value)
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
	if !manager.credentialFieldActiveLocked(connectionID, field) {
		manager.mu.RUnlock()
		return credentials.CredentialMetadata{}, ErrCredentialUnavailable
	}
	if _, pending := manager.pendingConnections[connectionID]; pending {
		manager.mu.RUnlock()
		return credentials.CredentialMetadata{}, ErrCredentialUnavailable
	}
	if _, quarantined := manager.credentialQuarantine[connectionID]; quarantined {
		manager.mu.RUnlock()
		return credentials.CredentialMetadata{}, ErrCredentialUnavailable
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
	observedDigest := credentialEnvelopeDigest(envelopes)
	manager.mu.RLock()
	valid := manager.connectionActiveLocked(connectionID) && manager.credentialFieldActiveLocked(connectionID, field)
	_, pending := manager.pendingConnections[connectionID]
	_, quarantined := manager.credentialQuarantine[connectionID]
	valid = valid && !pending && !quarantined && manager.managedDigests[connectionID] == observedDigest
	manager.mu.RUnlock()
	if !valid {
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
	if _, exists := manager.apiConnections[spec.ID]; exists {
		manager.mu.Unlock()
		return domain.Connection{}, fmt.Errorf("%w: connection already exists", ErrConfigSourceConflict)
	}
	if _, exists := manager.yamlConnections[spec.ID]; exists {
		manager.mu.Unlock()
		return domain.Connection{}, fmt.Errorf("%w: connection id is owned by YAML", ErrConfigSourceConflict)
	}
	if _, exists := manager.retiredConnections[spec.ID]; exists {
		manager.mu.Unlock()
		return domain.Connection{}, fmt.Errorf("%w: retired connection id cannot be reused", ErrResourceRetired)
	}
	if _, quarantined := manager.credentialQuarantine[spec.ID]; quarantined {
		manager.mu.Unlock()
		return domain.Connection{}, ErrCredentialUnavailable
	}
	if _, pending := manager.pendingConnections[spec.ID]; pending {
		manager.mu.Unlock()
		return domain.Connection{}, ErrConfigSourceConflict
	}
	manager.pendingConnections[spec.ID] = struct{}{}
	baseGeneration := manager.generation
	manager.mu.Unlock()
	defer manager.finishPendingConnection(spec.ID)

	preparedCredentials, err := manager.prepareManagedCredentials(ctx, spec.ID, spec.Credentials)
	if err != nil {
		return domain.Connection{}, err
	}
	var previousCredentials map[string]credentials.Envelope
	var store ManagedCredentialStore
	if preparedCredentials != nil {
		manager.mu.RLock()
		store = manager.credentialStore
		manager.mu.RUnlock()
		if store == nil {
			return domain.Connection{}, ErrCredentialStoreRequired
		}
		previousCredentials, err = store.Load(ctx, spec.ID)
		if err != nil {
			return domain.Connection{}, ErrCredentialUnavailable
		}
		if len(previousCredentials) != 0 {
			return domain.Connection{}, ErrCredentialUnavailable
		}
	}

	manager.mu.Lock()
	if manager.generation != baseGeneration || manager.connectionIDOwnedLocked(spec.ID) {
		manager.mu.Unlock()
		return domain.Connection{}, ErrConfigSourceConflict
	}
	connection := domain.Connection{
		ID:          spec.ID,
		Kind:        spec.Kind,
		Label:       spec.Label,
		Endpoint:    spec.Endpoint,
		Source:      manager.apiMetadata("connection", spec.ID, "pending"),
		Credentials: make(map[string]domain.CredentialReference),
	}
	fields := credentialFieldSet(spec.Credentials)
	digest := ""
	if preparedCredentials != nil {
		digest = credentialEnvelopeDigest(preparedCredentials)
	}
	connection.Revision = connectionRevisionForManaged(connection, fields, digest)
	connection.Source.Revision = connection.Revision
	candidateAPI := maps.Clone(manager.apiConnections)
	candidateAPI[spec.ID] = cloneConnection(connection)
	candidate, err := manager.snapshotForStateLocked(candidateAPI, manager.apiRoots, manager.apiMappings, manager.yamlConnections, manager.yamlRoots, manager.yamlMappings, manager.yamlConfigured)
	manager.mu.Unlock()
	if err != nil {
		return domain.Connection{}, err
	}
	if preparedCredentials != nil {
		outcome := writeCredentialSet(ctx, store, spec.ID, preparedCredentials, previousCredentials, true)
		if !outcome.matched {
			if outcome.unknown {
				manager.mu.Lock()
				manager.credentialQuarantine[spec.ID] = struct{}{}
				manager.mu.Unlock()
			}
			return domain.Connection{}, ErrCredentialUnavailable
		}
	}
	manager.mu.Lock()
	if manager.generation != baseGeneration || manager.connectionIDOwnedLocked(spec.ID) {
		if preparedCredentials != nil {
			manager.credentialQuarantine[spec.ID] = struct{}{}
		}
		manager.mu.Unlock()
		return domain.Connection{}, ErrRevisionMismatch
	}
	manager.apiConnections[spec.ID] = cloneConnection(connection)
	manager.managedFields[spec.ID] = cloneFieldSet(fields)
	if preparedCredentials != nil {
		manager.managedDigests[spec.ID] = digest
	} else {
		delete(manager.managedDigests, spec.ID)
	}
	manager.snapshot = cloneSnapshot(candidate)
	manager.generation++
	manager.mu.Unlock()
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
	current, exists := manager.apiConnections[id]
	if !exists {
		if _, yamlOwned := manager.yamlConnections[id]; yamlOwned {
			manager.mu.Unlock()
			return domain.Connection{}, ErrConfigSourceReadOnly
		}
		if _, retired := manager.retiredConnections[id]; retired {
			if manager.retiredConnections[id].Source.Source == domain.SourceYAML {
				manager.mu.Unlock()
				return domain.Connection{}, ErrConfigSourceReadOnly
			}
			manager.mu.Unlock()
			return domain.Connection{}, ErrResourceRetired
		}
		manager.mu.Unlock()
		return domain.Connection{}, ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		manager.mu.Unlock()
		return domain.Connection{}, ErrRevisionMismatch
	}
	if _, pending := manager.pendingConnections[id]; pending {
		manager.mu.Unlock()
		return domain.Connection{}, ErrConfigSourceConflict
	}
	manager.pendingConnections[id] = struct{}{}
	baseGeneration := manager.generation
	defer manager.finishPendingConnection(id)
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
	if len(changed) == 0 && patch.Credentials == nil {
		manager.mu.Unlock()
		return cloneConnection(current), nil
	}
	manager.mu.Unlock()

	var preparedCredentials map[string]credentials.Envelope
	var previousCredentials map[string]credentials.Envelope
	var store ManagedCredentialStore
	if patch.Credentials != nil {
		manager.mu.RLock()
		store = manager.credentialStore
		manager.mu.RUnlock()
		if store == nil {
			return domain.Connection{}, ErrCredentialStoreRequired
		}
		var err error
		previousCredentials, err = store.Load(ctx, id)
		if err != nil {
			return domain.Connection{}, ErrCredentialUnavailable
		}
		preparedCredentials, err = manager.prepareManagedCredentials(ctx, id, patch.Credentials)
		if err != nil {
			return domain.Connection{}, err
		}
		changed = append(changed, "credentials")
	}
	var fields map[string]struct{}
	digest := ""
	manager.mu.RLock()
	if patch.Credentials == nil {
		fields = cloneFieldSet(manager.managedFields[id])
		digest = manager.managedDigests[id]
	} else {
		fields = credentialFieldSet(patch.Credentials)
		digest = credentialEnvelopeDigest(preparedCredentials)
	}
	manager.mu.RUnlock()
	updated.Revision = connectionRevisionForManaged(updated, fields, digest)
	updated.Source.Revision = updated.Revision
	if err := updated.Validate(); err != nil {
		return domain.Connection{}, fmt.Errorf("%w: API connection: %v", ErrInvalidDocument, err)
	}
	change := RevisionChange{Kind: ResourceConnection, ID: id, PreviousRevision: current.Revision, Revision: updated.Revision, ChangedFields: slices.Clone(changed), AuthorityChanged: containsAny(changed, "endpoint")}
	manager.mu.Lock()
	if manager.generation != baseGeneration || manager.apiConnections[id].Revision != current.Revision {
		manager.mu.Unlock()
		return domain.Connection{}, ErrRevisionMismatch
	}
	candidateAPI := maps.Clone(manager.apiConnections)
	candidateAPI[id] = cloneConnection(updated)
	candidate, err := manager.snapshotForStateLocked(candidateAPI, manager.apiRoots, manager.apiMappings, manager.yamlConnections, manager.yamlRoots, manager.yamlMappings, manager.yamlConfigured)
	manager.mu.Unlock()
	if err != nil {
		return domain.Connection{}, err
	}
	var candidateCredentials map[domain.ConfigID]map[string]credentials.Envelope
	if patch.Credentials != nil {
		candidateCredentials = map[domain.ConfigID]map[string]credentials.Envelope{id: cloneEnvelopeSet(preparedCredentials)}
	}
	if err := manager.runRevisionChangesWithCandidates(ctx, []RevisionChange{change}, map[domain.ConfigID]domain.Connection{id: updated}, candidateCredentials); err != nil {
		return domain.Connection{}, err
	}
	if patch.Credentials != nil {
		outcome := writeCredentialSet(ctx, store, id, preparedCredentials, previousCredentials, true)
		if !outcome.matched {
			if outcome.unknown {
				manager.mu.Lock()
				manager.credentialQuarantine[id] = struct{}{}
				manager.mu.Unlock()
			}
			return domain.Connection{}, ErrCredentialUnavailable
		}
	}
	manager.mu.Lock()
	if manager.generation != baseGeneration || manager.apiConnections[id].Revision != current.Revision {
		if patch.Credentials != nil {
			manager.credentialQuarantine[id] = struct{}{}
		}
		manager.mu.Unlock()
		return domain.Connection{}, ErrRevisionMismatch
	}
	manager.apiConnections[id] = cloneConnection(updated)
	if patch.Credentials != nil {
		manager.managedFields[id] = cloneFieldSet(fields)
		manager.managedDigests[id] = digest
	}
	manager.snapshot = cloneSnapshot(candidate)
	manager.generation++
	manager.mu.Unlock()
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
	current, exists := manager.apiConnections[id]
	if !exists {
		if _, yamlOwned := manager.yamlConnections[id]; yamlOwned {
			manager.mu.Unlock()
			return ErrConfigSourceReadOnly
		}
		if _, retired := manager.retiredConnections[id]; retired {
			if manager.retiredConnections[id].Source.Source == domain.SourceYAML {
				manager.mu.Unlock()
				return ErrConfigSourceReadOnly
			}
			manager.mu.Unlock()
			return ErrResourceRetired
		}
		manager.mu.Unlock()
		return ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		manager.mu.Unlock()
		return ErrRevisionMismatch
	}
	if manager.connectionHasMappingsLocked(id) {
		manager.mu.Unlock()
		return fmt.Errorf("%w: connection is referenced by a path mapping", ErrConfigSourceConflict)
	}
	if _, pending := manager.pendingConnections[id]; pending {
		manager.mu.Unlock()
		return ErrConfigSourceConflict
	}
	manager.pendingConnections[id] = struct{}{}
	defer manager.finishPendingConnection(id)
	baseGeneration := manager.generation
	change := RevisionChange{Kind: ResourceConnection, ID: id, PreviousRevision: current.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true}
	candidateAPI := maps.Clone(manager.apiConnections)
	delete(candidateAPI, id)
	candidate, err := manager.snapshotForStateLocked(candidateAPI, manager.apiRoots, manager.apiMappings, manager.yamlConnections, manager.yamlRoots, manager.yamlMappings, manager.yamlConfigured)
	manager.mu.Unlock()
	if err != nil {
		return err
	}
	if err := manager.runRevisionChanges(ctx, []RevisionChange{change}, nil); err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	active, exists := manager.apiConnections[id]
	if !exists || active.Revision != current.Revision || manager.generation != baseGeneration {
		return ErrRevisionMismatch
	}
	retiredAt := manager.now().UTC()
	active.RetiredAt = &retiredAt
	manager.retiredConnections[id] = cloneConnection(active)
	delete(manager.apiConnections, id)
	manager.snapshot = cloneSnapshot(candidate)
	manager.generation++
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
	manager.generation++
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
	current, exists := manager.apiRoots[id]
	if !exists {
		if _, yamlOwned := manager.yamlRoots[id]; yamlOwned {
			manager.mu.Unlock()
			return domain.StorageRoot{}, ErrConfigSourceReadOnly
		}
		if _, retired := manager.retiredRoots[id]; retired {
			if manager.retiredRoots[id].Source.Source == domain.SourceYAML {
				manager.mu.Unlock()
				return domain.StorageRoot{}, ErrConfigSourceReadOnly
			}
			manager.mu.Unlock()
			return domain.StorageRoot{}, ErrResourceRetired
		}
		manager.mu.Unlock()
		return domain.StorageRoot{}, ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		manager.mu.Unlock()
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
			manager.mu.Unlock()
			return domain.StorageRoot{}, err
		}
		if interval != current.Watch.Interval {
			updated.Watch.Interval = interval
			changed = append(changed, "watch.interval")
		}
	}
	if len(changed) == 0 {
		manager.mu.Unlock()
		return cloneRoot(current), nil
	}
	updated.Revision = rootRevision(updated)
	updated.Source.Revision = updated.Revision
	if err := updated.Validate(); err != nil {
		manager.mu.Unlock()
		return domain.StorageRoot{}, fmt.Errorf("%w: API storage root: %v", ErrInvalidDocument, err)
	}
	if err := validateStorageRootPath(updated.Path); err != nil {
		manager.mu.Unlock()
		return domain.StorageRoot{}, err
	}
	for _, capability := range updated.Capabilities {
		if err := capability.Validate(); err != nil {
			manager.mu.Unlock()
			return domain.StorageRoot{}, fmt.Errorf("%w: API storage root capability", ErrInvalidDocument)
		}
	}
	change := RevisionChange{Kind: ResourceRoot, ID: id, PreviousRevision: current.Revision, Revision: updated.Revision, ChangedFields: slices.Clone(changed), AuthorityChanged: containsAny(changed, "purpose", "path", "readOnly", "capabilities")}
	baseGeneration := manager.generation
	candidateAPI := maps.Clone(manager.apiRoots)
	candidateAPI[id] = cloneRoot(updated)
	candidate, err := manager.snapshotForStateLocked(manager.apiConnections, candidateAPI, manager.apiMappings, manager.yamlConnections, manager.yamlRoots, manager.yamlMappings, manager.yamlConfigured)
	manager.mu.Unlock()
	if err != nil {
		return domain.StorageRoot{}, err
	}
	if err := manager.runRevisionChanges(ctx, []RevisionChange{change}, nil); err != nil {
		return domain.StorageRoot{}, err
	}
	manager.mu.Lock()
	active, exists := manager.apiRoots[id]
	if !exists || active.Revision != current.Revision || manager.generation != baseGeneration {
		manager.mu.Unlock()
		return domain.StorageRoot{}, ErrRevisionMismatch
	}
	manager.apiRoots[id] = cloneRoot(updated)
	manager.snapshot = cloneSnapshot(candidate)
	manager.generation++
	manager.mu.Unlock()
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
	current, exists := manager.apiRoots[id]
	if !exists {
		if _, yamlOwned := manager.yamlRoots[id]; yamlOwned {
			manager.mu.Unlock()
			return ErrConfigSourceReadOnly
		}
		if _, retired := manager.retiredRoots[id]; retired {
			if manager.retiredRoots[id].Source.Source == domain.SourceYAML {
				manager.mu.Unlock()
				return ErrConfigSourceReadOnly
			}
			manager.mu.Unlock()
			return ErrResourceRetired
		}
		manager.mu.Unlock()
		return ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		manager.mu.Unlock()
		return ErrRevisionMismatch
	}
	if manager.rootHasMappingsLocked(id) {
		manager.mu.Unlock()
		return fmt.Errorf("%w: storage root is referenced by a path mapping", ErrConfigSourceConflict)
	}
	baseGeneration := manager.generation
	change := RevisionChange{Kind: ResourceRoot, ID: id, PreviousRevision: current.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true}
	candidateAPI := maps.Clone(manager.apiRoots)
	delete(candidateAPI, id)
	candidate, err := manager.snapshotForStateLocked(manager.apiConnections, candidateAPI, manager.apiMappings, manager.yamlConnections, manager.yamlRoots, manager.yamlMappings, manager.yamlConfigured)
	manager.mu.Unlock()
	if err != nil {
		return err
	}
	if err := manager.runRevisionChanges(ctx, []RevisionChange{change}, nil); err != nil {
		return err
	}
	manager.mu.Lock()
	active, exists := manager.apiRoots[id]
	if !exists || active.Revision != current.Revision || manager.generation != baseGeneration {
		manager.mu.Unlock()
		return ErrRevisionMismatch
	}
	retiredAt := manager.now().UTC()
	active.RetiredAt = &retiredAt
	manager.retiredRoots[id] = cloneRoot(active)
	delete(manager.apiRoots, id)
	manager.snapshot = cloneSnapshot(candidate)
	manager.generation++
	manager.mu.Unlock()
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
	manager.generation++
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
	current, exists := manager.apiMappings[id]
	if !exists {
		if _, yamlOwned := manager.yamlMappings[id]; yamlOwned {
			manager.mu.Unlock()
			return domain.PathMapping{}, ErrConfigSourceReadOnly
		}
		if retired, retiredExists := manager.retiredMappings[id]; retiredExists {
			if retired.Source.Source == domain.SourceYAML {
				manager.mu.Unlock()
				return domain.PathMapping{}, ErrConfigSourceReadOnly
			}
			manager.mu.Unlock()
			return domain.PathMapping{}, ErrResourceRetired
		}
		manager.mu.Unlock()
		return domain.PathMapping{}, ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		manager.mu.Unlock()
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
		manager.mu.Unlock()
		return cloneMapping(current), nil
	}
	updated.Revision = mappingRevision(updated)
	updated.Source.Revision = updated.Revision
	if err := validateMappingShape(updated); err != nil {
		manager.mu.Unlock()
		return domain.PathMapping{}, err
	}
	change := RevisionChange{Kind: ResourceMapping, ID: id, PreviousRevision: current.Revision, Revision: updated.Revision, ChangedFields: slices.Clone(changed), AuthorityChanged: len(changed) != 0}
	baseGeneration := manager.generation
	candidateAPI := maps.Clone(manager.apiMappings)
	candidateAPI[id] = cloneMapping(updated)
	candidate, err := manager.snapshotForStateLocked(manager.apiConnections, manager.apiRoots, candidateAPI, manager.yamlConnections, manager.yamlRoots, manager.yamlMappings, manager.yamlConfigured)
	manager.mu.Unlock()
	if err != nil {
		return domain.PathMapping{}, err
	}
	if err := manager.runRevisionChanges(ctx, []RevisionChange{change}, nil); err != nil {
		return domain.PathMapping{}, err
	}
	manager.mu.Lock()
	active, exists := manager.apiMappings[id]
	if !exists || active.Revision != current.Revision || manager.generation != baseGeneration {
		manager.mu.Unlock()
		return domain.PathMapping{}, ErrRevisionMismatch
	}
	manager.apiMappings[id] = cloneMapping(updated)
	manager.snapshot = cloneSnapshot(candidate)
	manager.generation++
	manager.mu.Unlock()
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
	current, exists := manager.apiMappings[id]
	if !exists {
		if _, yamlOwned := manager.yamlMappings[id]; yamlOwned {
			manager.mu.Unlock()
			return ErrConfigSourceReadOnly
		}
		if retired, retiredExists := manager.retiredMappings[id]; retiredExists {
			if retired.Source.Source == domain.SourceYAML {
				manager.mu.Unlock()
				return ErrConfigSourceReadOnly
			}
			manager.mu.Unlock()
			return ErrResourceRetired
		}
		manager.mu.Unlock()
		return ErrResourceNotFound
	}
	if current.Revision != expectedRevision {
		manager.mu.Unlock()
		return ErrRevisionMismatch
	}
	baseGeneration := manager.generation
	change := RevisionChange{Kind: ResourceMapping, ID: id, PreviousRevision: current.Revision, ChangedFields: []string{"retired"}, AuthorityChanged: true}
	candidateAPI := maps.Clone(manager.apiMappings)
	delete(candidateAPI, id)
	candidate, err := manager.snapshotForStateLocked(manager.apiConnections, manager.apiRoots, candidateAPI, manager.yamlConnections, manager.yamlRoots, manager.yamlMappings, manager.yamlConfigured)
	manager.mu.Unlock()
	if err != nil {
		return err
	}
	if err := manager.runRevisionChanges(ctx, []RevisionChange{change}, nil); err != nil {
		return err
	}
	manager.mu.Lock()
	active, exists := manager.apiMappings[id]
	if !exists || active.Revision != current.Revision || manager.generation != baseGeneration {
		manager.mu.Unlock()
		return ErrRevisionMismatch
	}
	manager.retiredMappings[id] = cloneMapping(active)
	delete(manager.apiMappings, id)
	manager.snapshot = cloneSnapshot(candidate)
	manager.generation++
	manager.mu.Unlock()
	return nil
}

// DeletePathMapping is an alias retaining HTTP terminology.
func (manager *Manager) DeletePathMapping(ctx context.Context, id domain.ConfigID, expectedRevision string) error {
	return manager.RetirePathMapping(ctx, id, expectedRevision)
}

func (manager *Manager) prepareManagedCredentials(ctx context.Context, id domain.ConfigID, inputs map[string]CredentialInput) (map[string]credentials.Envelope, error) {
	if inputs == nil {
		return nil, nil
	}
	if len(inputs) == 0 {
		return map[string]credentials.Envelope{}, nil
	}
	manager.mu.RLock()
	store := manager.credentialStore
	crypt := manager.credentialManager
	manager.mu.RUnlock()
	if store == nil {
		return nil, ErrCredentialStoreRequired
	}
	if crypt == nil {
		return nil, ErrCredentialManagerNeeded
	}
	envelopes := make(map[string]credentials.Envelope, len(inputs))
	for field, input := range inputs {
		if validateCredentialField(field) != nil || input.Reference != nil || len(input.Value) == 0 {
			return nil, ErrCredentialInvalid
		}
		plaintext := append([]byte(nil), input.Value...)
		manager.credentialMu.Lock()
		envelope, err := crypt.Seal(id.String(), field, plaintext)
		manager.credentialMu.Unlock()
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

type credentialWriteOutcome struct {
	matched   bool
	unchanged bool
	unknown   bool
}

// writeCredentialSet treats a lost Replace response as an uncertain write.
// A read-back of the complete set is the only success proof; it never writes
// a rollback. If the old complete set is read back, the failed write is known
// to have had no effect. Any other set quarantines the connection at the
// caller because field-level access would otherwise expose ambiguous state.
func writeCredentialSet(ctx context.Context, store ManagedCredentialStore, id domain.ConfigID, desired, previous map[string]credentials.Envelope, previousKnown bool) credentialWriteOutcome {
	if store == nil {
		return credentialWriteOutcome{unknown: true}
	}
	_ = store.Replace(ctx, id, cloneEnvelopeSet(desired))
	actual, readErr := store.Load(ctx, id)
	if readErr != nil {
		return credentialWriteOutcome{unknown: true}
	}
	if envelopeSetsEqual(actual, desired) {
		return credentialWriteOutcome{matched: true}
	}
	if previousKnown && envelopeSetsEqual(actual, previous) {
		return credentialWriteOutcome{unchanged: true}
	}
	return credentialWriteOutcome{unknown: true}
}

func cloneEnvelopeSet(values map[string]credentials.Envelope) map[string]credentials.Envelope {
	if values == nil {
		return map[string]credentials.Envelope{}
	}
	copyValues := make(map[string]credentials.Envelope, len(values))
	for field, envelope := range values {
		copyValues[field] = cloneEnvelope(envelope)
	}
	return copyValues
}

func envelopeSetsEqual(left, right map[string]credentials.Envelope) bool {
	if len(left) != len(right) {
		return false
	}
	for field, expected := range right {
		actual, exists := left[field]
		if !exists || expected.Version != actual.Version || expected.KeyFingerprint != actual.KeyFingerprint || !bytes.Equal(expected.Nonce, actual.Nonce) || !bytes.Equal(expected.Ciphertext, actual.Ciphertext) {
			return false
		}
	}
	return true
}

func (manager *Manager) finishPendingConnection(id domain.ConfigID) {
	manager.mu.Lock()
	delete(manager.pendingConnections, id)
	manager.mu.Unlock()
}

func (manager *Manager) connectionIDOwnedLocked(id domain.ConfigID) bool {
	if _, exists := manager.apiConnections[id]; exists {
		return true
	}
	if _, exists := manager.yamlConnections[id]; exists {
		return true
	}
	if _, exists := manager.retiredConnections[id]; exists {
		return true
	}
	return false
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
	if parsed == nil {
		return
	}
	zeroCredentialValues(parsed.StaticCredentials)
}

func zeroCredentialValues(values map[CredentialKey][]byte) {
	for key, value := range values {
		zero(value)
		delete(values, key)
	}
}

func zeroResolvedValues(values map[string][]byte) {
	for key, value := range values {
		zero(value)
		delete(values, key)
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

// stableBindingKey derives the private HMAC key used for static secret
// bindings. A persistent credential key fingerprint scopes the binding when
// one is configured; the document identity is the stable fallback for YAML
// parsing before credential storage is wired. This keeps identical YAML and
// resolved bytes stable across restarts while a key rotation or document
// identity change produces a new binding namespace. The returned key is
// package-owned and must be zeroed when its owner closes.
func stableBindingKey(documentID string, crypt *credentials.Manager) []byte {
	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		documentID = defaultDocumentID
	}
	scope := "mastarr/static-credential-binding/v2\x00" + documentID
	if crypt != nil && crypt.Fingerprint() != "" {
		scope += "\x00credential-key:" + crypt.Fingerprint()
	} else {
		scope += "\x00document-only"
	}
	key := sha256.Sum256([]byte(scope))
	return append([]byte(nil), key[:]...)
}

func opaqueCredentialBinding(key, value []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return hex.EncodeToString(mac.Sum(nil))
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

func connectionRevisionWithBindings(connection domain.Connection, bindings []string) string {
	slices.Sort(bindings)
	return digestValue(struct {
		ID          domain.ConfigID
		Kind        domain.ConnectionKind
		Label       string
		Endpoint    string
		Credentials []string
		Bindings    []string
	}{connection.ID, connection.Kind, connection.Label, connection.Endpoint, sortedCredentialReferences(connection.Credentials), bindings})
}

func (manager *Manager) connectionRevisionLocked(connection domain.Connection) string {
	managed := make([]string, 0, len(manager.managedFields[connection.ID]))
	for field := range manager.managedFields[connection.ID] {
		managed = append(managed, field)
	}
	sort.Strings(managed)
	return connectionRevisionForManagedValues(connection, managed, manager.managedDigests[connection.ID])
}

func connectionRevisionForManaged(connection domain.Connection, fields map[string]struct{}, digest string) string {
	managed := make([]string, 0, len(fields))
	for field := range fields {
		managed = append(managed, field)
	}
	sort.Strings(managed)
	return connectionRevisionForManagedValues(connection, managed, digest)
}

func connectionRevisionForManagedValues(connection domain.Connection, managed []string, digest string) string {
	revision := digestValue(struct {
		ID          domain.ConfigID
		Kind        domain.ConnectionKind
		Label       string
		Endpoint    string
		Credentials []string
		Managed     []string
	}{connection.ID, connection.Kind, connection.Label, connection.Endpoint, sortedCredentialReferences(connection.Credentials), managed})
	if validCredentialEnvelopeDigest(digest) {
		return revision + "|managed=" + digest
	}
	return revision
}

func managedDigestFromRevision(revision string) string {
	const marker = "|managed="
	index := strings.LastIndex(revision, marker)
	if index < 0 || index+len(marker) == len(revision) {
		return ""
	}
	digest := revision[index+len(marker):]
	if !validCredentialEnvelopeDigest(digest) {
		return ""
	}
	return digest
}

func validCredentialEnvelopeDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil
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

func (manager *Manager) credentialFieldActiveLocked(id domain.ConfigID, field string) bool {
	if fields, exists := manager.managedFields[id]; exists {
		_, active := fields[field]
		return active
	}
	if connection, exists := manager.yamlConnections[id]; exists {
		_, active := connection.Credentials[field]
		return active
	}
	return false
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
		return "", errors.New("source prefix must be an absolute remote namespace")
	}
	// qBittorrent and NZBGet adapters currently expose POSIX remote paths.
	// Reject drive-letter forms at this boundary until all upstream adapters
	// share a canonical cross-platform namespace contract. This also prevents
	// C:/ and c:/ aliases from bypassing ambiguity checks.
	if !strings.HasPrefix(value, "/") || isWindowsAbsoluteNamespace(value) {
		return "", errors.New("source prefix must be an absolute remote namespace")
	}
	return cleanNamespace(value)
}

func cleanDestinationPrefix(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if isAbsoluteRemoteNamespace(value) {
		return "", errors.New("destination prefix must be root-relative")
	}
	return cleanNamespace(value)
}

func cleanNamespace(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") {
		return "", errors.New("namespace prefix is invalid")
	}
	if isWindowsAbsoluteNamespace(value) {
		drive := value[:2]
		rest := value[2:]
		cleanedRest := path.Clean(rest)
		if cleanedRest == "." {
			cleanedRest = "/"
		}
		if !strings.HasPrefix(cleanedRest, "/") {
			return "", errors.New("namespace prefix must be absolute")
		}
		cleaned := drive + cleanedRest
		if cleaned != value {
			return "", errors.New("namespace prefix is not canonical")
		}
		trimmedRest := strings.Trim(cleanedRest, "/")
		if trimmedRest != "" {
			for _, part := range strings.Split(trimmedRest, "/") {
				if part == "" || part == "." || part == ".." {
					return "", errors.New("namespace prefix contains unsafe component")
				}
			}
		}
		return cleaned, nil
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

func isWindowsAbsoluteNamespace(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' && value[2] == '/'
}

func isAbsoluteRemoteNamespace(value string) bool {
	return strings.HasPrefix(value, "/") || isWindowsAbsoluteNamespace(value)
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
