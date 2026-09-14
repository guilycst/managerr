package configuration

import (
	"fmt"
	"strings"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

var validationTime = time.Unix(1, 0).UTC()

func (spec ConnectionSpec) validate() error {
	id, err := domain.ParseConfigID(spec.ID.String())
	if err != nil || id != spec.ID {
		return fmt.Errorf("%w: connection id", ErrInvalidDocument)
	}
	connection := domain.Connection{
		ID:       spec.ID,
		Kind:     spec.Kind,
		Label:    spec.Label,
		Endpoint: spec.Endpoint,
		Source:   sourceMetadata(domain.SourceAPI, true, "api-connection-"+spec.ID.String(), "pending", validationTime),
		Revision: "pending",
	}
	if err := connection.Validate(); err != nil {
		return fmt.Errorf("%w: API connection: %v", ErrInvalidDocument, err)
	}
	if err := validateCredentialInputs(spec.Credentials); err != nil {
		return err
	}
	return nil
}

func (spec StorageRootSpec) validate() error {
	id, err := domain.ParseConfigID(spec.ID.String())
	if err != nil || id != spec.ID {
		return fmt.Errorf("%w: storage root id", ErrInvalidDocument)
	}
	root := domain.StorageRoot{ID: spec.ID, Label: spec.Label, Purpose: spec.Purpose, Path: spec.Path, ReadOnly: spec.ReadOnly, Capabilities: spec.Capabilities, Watch: spec.Watch, Source: sourceMetadata(domain.SourceAPI, true, "api-storage-root-"+spec.ID.String(), "pending", validationTime), Revision: "pending"}
	if root.Watch.Interval < 0 {
		return fmt.Errorf("%w: API storage root watch interval is negative", ErrInvalidDocument)
	}
	if err := root.Validate(); err != nil {
		return fmt.Errorf("%w: API storage root: %v", ErrInvalidDocument, err)
	}
	if err := validateStorageRootPath(root.Path); err != nil {
		return err
	}
	for _, capability := range spec.Capabilities {
		if err := capability.Validate(); err != nil {
			return fmt.Errorf("%w: API storage root capability", ErrInvalidDocument)
		}
	}
	return nil
}

func (spec PathMappingSpec) validate() error {
	id, err := domain.ParseConfigID(spec.ID.String())
	if err != nil || id != spec.ID {
		return fmt.Errorf("%w: path mapping id", ErrInvalidDocument)
	}
	if !spec.ConnectionID.Valid() || !spec.RootID.Valid() {
		return fmt.Errorf("%w: path mapping references", ErrMappingInvalid)
	}
	mapping := domain.PathMapping{ID: spec.ID, ConnectionID: spec.ConnectionID, RootID: spec.RootID, SourcePrefix: spec.SourcePrefix, DestinationPrefix: spec.DestinationPrefix, Revision: "pending", Source: sourceMetadata(domain.SourceAPI, true, "api-path-mapping-"+spec.ID.String(), "pending", validationTime)}
	if err := mapping.Validate(); err != nil {
		return fmt.Errorf("%w: API path mapping", ErrInvalidDocument)
	}
	return validateMappingShape(mapping)
}

func validateCredentialInputs(inputs map[string]CredentialInput) error {
	for field, input := range inputs {
		if err := validateCredentialField(field); err != nil || input.Reference != nil || len(input.Value) == 0 {
			return ErrCredentialInvalid
		}
	}
	return nil
}

func validateCredentialField(field string) error {
	if strings.TrimSpace(field) == "" || field != strings.TrimSpace(field) || strings.ContainsRune(field, '\x00') {
		return ErrCredentialInvalid
	}
	return nil
}

func durationFromSeconds(seconds int) (time.Duration, error) {
	if seconds < 0 {
		return 0, fmt.Errorf("%w: watch interval is negative", ErrInvalidDocument)
	}
	if int64(seconds) > int64((time.Duration(1<<63-1))/time.Second) {
		return 0, fmt.Errorf("%w: watch interval is too large", ErrInvalidDocument)
	}
	return time.Duration(seconds) * time.Second, nil
}

// Ensure Manager satisfies the frozen read port at compile time.
var _ ports.ConfigurationRepositoryPort = (*Manager)(nil)
