package domain

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// ConnectionKind identifies a supported upstream family.
type ConnectionKind string

const (
	ConnectionQBittorrent ConnectionKind = "qbittorrent"
	ConnectionNZBGet      ConnectionKind = "nzbget"
	ConnectionRadarr      ConnectionKind = "radarr"
	ConnectionSonarr      ConnectionKind = "sonarr"
	ConnectionJellyfin    ConnectionKind = "jellyfin"
	ConnectionSeerr       ConnectionKind = "seerr"
)

// StoragePurpose controls which actions may target a configured root.
type StoragePurpose string

const (
	StorageDownload   StoragePurpose = "download"
	StorageLibrary    StoragePurpose = "library"
	StorageDescriptor StoragePurpose = "descriptor"
	StorageTrash      StoragePurpose = "trash"
)

// CredentialReference points to a secret outside the public configuration
// document. The API never turns it into an arbitrary file read.
type CredentialReference struct {
	Kind  string
	Value string
}

// Connection is the non-secret effective configuration for one upstream.
type Connection struct {
	ID          ConfigID
	Kind        ConnectionKind
	Label       string
	Endpoint    string
	Source      SourceMetadata
	Revision    string
	Credentials map[string]CredentialReference
	RetiredAt   *time.Time
}

// Validate checks stable identity, endpoint shape and source metadata.
func (connection Connection) Validate() error {
	if !connection.ID.Valid() {
		return errors.New("connection has an invalid id")
	}
	switch connection.Kind {
	case ConnectionQBittorrent, ConnectionNZBGet, ConnectionRadarr, ConnectionSonarr, ConnectionJellyfin, ConnectionSeerr:
	default:
		return errors.New("unsupported connection kind")
	}
	if strings.TrimSpace(connection.Label) == "" {
		return errors.New("connection label is required")
	}
	parsed, err := url.Parse(connection.Endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return errors.New("connection endpoint must be an absolute URL without credentials")
	}
	if strings.TrimSpace(connection.Revision) == "" {
		return errors.New("connection revision is required")
	}
	return connection.Source.Validate()
}

// StorageRoot describes a host path known to the configuration boundary.
type StorageRoot struct {
	ID           ConfigID
	Label        string
	Purpose      StoragePurpose
	Path         string
	Source       SourceMetadata
	Revision     string
	ReadOnly     bool
	Capabilities []Capability
	Watch        WatchSettings
	RetiredAt    *time.Time
}

// WatchSettings controls one root's scheduled discovery scan.
type WatchSettings struct {
	Enabled  bool
	Interval time.Duration
}

func (root StorageRoot) Validate() error {
	if !root.ID.Valid() {
		return errors.New("storage root has an invalid id")
	}
	if strings.TrimSpace(root.Label) == "" || strings.TrimSpace(root.Path) == "" {
		return errors.New("storage root label and path are required")
	}
	switch root.Purpose {
	case StorageDownload, StorageLibrary, StorageDescriptor, StorageTrash:
	default:
		return errors.New("unsupported storage purpose")
	}
	if root.Watch.Enabled && root.Watch.Interval <= 0 {
		return errors.New("enabled storage watch requires a positive interval")
	}
	return root.Source.Validate()
}

// PathMapping translates one upstream namespace into one configured root.
type PathMapping struct {
	ID                ConfigID
	ConnectionID      ConfigID
	SourcePrefix      string
	RootID            ConfigID
	DestinationPrefix string
	Source            SourceMetadata
	Revision          string
}

func (mapping PathMapping) Validate() error {
	if !mapping.ID.Valid() || !mapping.ConnectionID.Valid() || !mapping.RootID.Valid() {
		return errors.New("path mapping has an invalid reference")
	}
	if strings.TrimSpace(mapping.Revision) == "" {
		return errors.New("path mapping revision is required")
	}
	return mapping.Source.Validate()
}

// ConfigurationSnapshot is the immutable effective view consumed by workers.
type ConfigurationSnapshot struct {
	Source          SourceMetadata
	Connections     []Connection
	StorageRoots    []StorageRoot
	PathMappings    []PathMapping
	KeySource       string
	KeyPath         string
	RestartRequired bool
}

func (snapshot ConfigurationSnapshot) Validate() error {
	if err := snapshot.Source.Validate(); err != nil {
		return err
	}
	seenConnections := make(map[ConfigID]struct{}, len(snapshot.Connections))
	for _, connection := range snapshot.Connections {
		if err := connection.Validate(); err != nil {
			return err
		}
		if _, exists := seenConnections[connection.ID]; exists {
			return errors.New("configuration contains duplicate connection id")
		}
		seenConnections[connection.ID] = struct{}{}
	}
	seenRoots := make(map[ConfigID]struct{}, len(snapshot.StorageRoots))
	for _, root := range snapshot.StorageRoots {
		if err := root.Validate(); err != nil {
			return err
		}
		if _, exists := seenRoots[root.ID]; exists {
			return errors.New("configuration contains duplicate storage root id")
		}
		seenRoots[root.ID] = struct{}{}
	}
	for _, mapping := range snapshot.PathMappings {
		if err := mapping.Validate(); err != nil {
			return err
		}
		if _, exists := seenConnections[mapping.ConnectionID]; !exists {
			return errors.New("path mapping references an unknown connection")
		}
		if _, exists := seenRoots[mapping.RootID]; !exists {
			return errors.New("path mapping references an unknown storage root")
		}
	}
	return nil
}
