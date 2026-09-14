// Package control implements the explicit qBittorrent mutation boundary.
//
// The adapter only acts on an exact, freshly observed torrent scope. Every
// mutation is followed by a read-back, and a response that cannot be
// reconciled is reported as unknown rather than retried. The standalone
// qBittorrent client currently exposes read methods only; Upstream is the
// narrow seam a later write-capable client can implement. Runtime writes stay
// disabled until versioned evidence enables the individual operation.
package control

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	native "github.com/guilycst/mastarr/clients/qbittorrent"
	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	defaultReconcileTimeout = 5 * time.Second
	maxExternalIDLength     = 128
	maxCapabilityText       = 256
)

const (
	operationObserve      = "qbit.control.observe"
	operationStop         = "qbit.control.stop"
	operationRelocate     = "qbit.control.relocate"
	operationRenameFile   = "qbit.control.rename_file"
	operationRenameFolder = "qbit.control.rename_folder"
	operationRemove       = "qbit.control.remove"
)

var errTorrentNotFound = errors.New("qBittorrent torrent record was not found")

// Upstream is the small, normalized seam required by this adapter. It keeps
// transport, authentication and upstream error construction inside the
// nested client module. The qBittorrent client in this checkout is read-only;
// a future write-capable version can satisfy this interface without exposing
// generated DTOs to Mastarr's domain or ports.
type Upstream interface {
	ListTorrents(context.Context, native.TorrentListOptions) ([]native.Torrent, error)
	GetTorrentFiles(context.Context, string) ([]native.TorrentFile, error)
	Stop(context.Context, string) error
	SetLocation(context.Context, string, string) error
	RenameFile(context.Context, string, string, string) error
	RenameFolder(context.Context, string, string, string) error
	Delete(context.Context, string, bool) error
}

// DestinationChecker supplies a root-confined vacancy observation for the
// final content path of a relocation. Implementations should use a reviewed
// filesystem read port; an absent checker keeps relocation blocked.
type DestinationChecker interface {
	CheckVacant(context.Context, domain.FileTarget) (bool, error)
}

// OperationCapability binds one control operation to its own versioned
// evidence. A stop probe cannot authorize relocation, renames or removal.
type OperationCapability struct {
	State    domain.CapabilityState
	Version  string
	Evidence []string
}

// ControlCapabilities keeps qBittorrent control gates independent. Each
// field defaults to unknown when omitted.
type ControlCapabilities struct {
	Stop         OperationCapability
	Relocate     OperationCapability
	RenameFile   OperationCapability
	RenameFolder OperationCapability
	Remove       OperationCapability
}

// Config configures one qBittorrent control instance. Each operation
// capability must remain unknown until its own versioned upstream fixture or
// product probe proves that operation. Supported capabilities require both a
// version and evidence string so callers cannot enable an operation by
// setting only a boolean-like state.
type Config struct {
	ConnectionID       domain.ConfigID
	Mappings           []domain.PathMapping
	Capabilities       ControlCapabilities
	DestinationChecker DestinationChecker
	ReconcileTimeout   time.Duration
}

// Client implements the qBittorrent control and capability ports.
type Client struct {
	connectionID       domain.ConfigID
	mappings           []mapping
	capabilities       ControlCapabilities
	destinationChecker DestinationChecker
	reconcileTimeout   time.Duration
	upstream           Upstream
}

var _ ports.DownloadControlPort = (*Client)(nil)
var _ ports.CapabilityPort = (*Client)(nil)

// New constructs a control adapter over a normalized upstream client. The
// adapter does not construct the read-only standalone client because that
// client deliberately has no mutation methods.
func New(config Config, upstream Upstream) (*Client, error) {
	if upstream == nil {
		return nil, errors.New("qBittorrent control upstream is required")
	}
	if !config.ConnectionID.Valid() {
		return nil, errors.New("qBittorrent control connection id is invalid")
	}
	capabilities, err := normalizeCapabilities(config.Capabilities)
	if err != nil {
		return nil, err
	}
	mappings, err := normalizeMappings(config.ConnectionID, config.Mappings)
	if err != nil {
		return nil, err
	}
	timeout := config.ReconcileTimeout
	if timeout == 0 {
		timeout = defaultReconcileTimeout
	}
	if timeout < 0 {
		return nil, errors.New("qBittorrent control reconcile timeout cannot be negative")
	}
	return &Client{
		connectionID:       config.ConnectionID,
		mappings:           mappings,
		capabilities:       capabilities,
		destinationChecker: config.DestinationChecker,
		reconcileTimeout:   timeout,
		upstream:           upstream,
	}, nil
}

func normalizeCapabilities(input ControlCapabilities) (ControlCapabilities, error) {
	stop, err := normalizeOperationCapability(operationStop, input.Stop)
	if err != nil {
		return ControlCapabilities{}, err
	}
	relocate, err := normalizeOperationCapability(operationRelocate, input.Relocate)
	if err != nil {
		return ControlCapabilities{}, err
	}
	renameFile, err := normalizeOperationCapability(operationRenameFile, input.RenameFile)
	if err != nil {
		return ControlCapabilities{}, err
	}
	renameFolder, err := normalizeOperationCapability(operationRenameFolder, input.RenameFolder)
	if err != nil {
		return ControlCapabilities{}, err
	}
	remove, err := normalizeOperationCapability(operationRemove, input.Remove)
	if err != nil {
		return ControlCapabilities{}, err
	}
	return ControlCapabilities{
		Stop: stop, Relocate: relocate, RenameFile: renameFile,
		RenameFolder: renameFolder, Remove: remove,
	}, nil
}

func normalizeOperationCapability(name string, input OperationCapability) (OperationCapability, error) {
	state := input.State
	if state == "" {
		state = domain.CapabilityUnknown
	}
	switch state {
	case domain.CapabilitySupported, domain.CapabilityUnsupported, domain.CapabilityUnknown:
	default:
		return OperationCapability{}, fmt.Errorf("qBittorrent capability %s state is invalid", name)
	}
	version, err := boundedCapabilityText(input.Version)
	if err != nil {
		return OperationCapability{}, fmt.Errorf("qBittorrent capability %s version: %w", name, err)
	}
	evidence := make([]string, 0, len(input.Evidence))
	for index, value := range input.Evidence {
		value, textErr := boundedCapabilityText(value)
		if textErr != nil {
			return OperationCapability{}, fmt.Errorf("qBittorrent capability %s evidence %d: %w", name, index, textErr)
		}
		if value != "" {
			evidence = append(evidence, value)
		}
	}
	if state == domain.CapabilitySupported && (version == "" || len(evidence) == 0) {
		return OperationCapability{}, fmt.Errorf("supported qBittorrent capability %s requires version and evidence", name)
	}
	return OperationCapability{State: state, Version: version, Evidence: evidence}, nil
}

// Capabilities reports the explicitly configured control gate. It does not
// probe or mutate qBittorrent; absent versioned evidence remains unknown.
func (client *Client) Capabilities(ctx context.Context, connectionID domain.ConfigID) ([]domain.Capability, error) {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if connectionID != client.connectionID {
		return nil, invalidInput(operationObserve, "connection scope is invalid")
	}
	now := time.Now().UTC()
	return []domain.Capability{
		client.capabilityObservation(operationStop, client.capabilities.Stop, now),
		client.capabilityObservation(operationRelocate, client.capabilities.Relocate, now),
		client.capabilityObservation(operationRenameFile, client.capabilities.RenameFile, now),
		client.capabilityObservation(operationRenameFolder, client.capabilities.RenameFolder, now),
		client.capabilityObservation(operationRemove, client.capabilities.Remove, now),
	}, nil
}

func (client *Client) capabilityObservation(name string, capability OperationCapability, observedAt time.Time) domain.Capability {
	reason := "qBittorrent operation capability is not verified"
	if capability.State == domain.CapabilityUnsupported {
		reason = "qBittorrent operation capability is unsupported"
	} else if capability.State == domain.CapabilitySupported {
		reason = "qBittorrent operation capability is enabled by versioned evidence"
	}
	return domain.Capability{
		Name: name, State: capability.State, Version: capability.Version,
		Reason: reason, Evidence: append([]string(nil), capability.Evidence...), ObservedAt: observedAt,
	}
}

// Observe returns one exact qBittorrent record and its mapped payload. It is
// read-only and remains available while write capability is unknown.
func (client *Client) Observe(ctx context.Context, ref ports.DownloadRef) (ports.DownloadObservation, error) {
	ctx = contextOrBackground(ctx)
	if err := validateRef(client.connectionID, ref, operationObserve); err != nil {
		return ports.DownloadObservation{}, err
	}
	snapshot, err := client.readSnapshot(ctx, ref, true, operationObserve)
	if err != nil {
		if errors.Is(err, errTorrentNotFound) {
			return ports.DownloadObservation{}, notFound(operationObserve)
		}
		return ports.DownloadObservation{}, err
	}
	capabilities, err := client.Capabilities(ctx, ref.ConnectionID)
	if err != nil {
		return ports.DownloadObservation{}, err
	}
	now := time.Now().UTC()
	payload := make([]domain.FileManifestEntry, len(snapshot.files))
	for index, file := range snapshot.files {
		payload[index] = file.manifest
	}
	return ports.DownloadObservation{
		Ref:          ref,
		State:        snapshot.torrent.State,
		Seeding:      isSeedingState(snapshot.torrent.State),
		Payload:      payload,
		ObservedAt:   now,
		Capabilities: capabilities,
	}, nil
}

// Stop stops one whole torrent. Zero upload speed never satisfies this
// operation; only an explicit stopped qBittorrent state does. Any dispatched
// write is reconciled with a detached, bounded read context.
func (client *Client) Stop(ctx context.Context, ref ports.DownloadRef) (ports.ClientEffect, error) {
	ctx = contextOrBackground(ctx)
	if err := validateRef(client.connectionID, ref, operationStop); err != nil {
		return ports.ClientEffect{}, err
	}
	snapshot, err := client.readSnapshot(ctx, ref, false, operationStop)
	if err != nil {
		if errors.Is(err, errTorrentNotFound) {
			return ports.ClientEffect{}, notFound(operationStop)
		}
		return ports.ClientEffect{}, err
	}
	if isStoppedState(snapshot.torrent.State) {
		return effect(operationStop, domain.OutcomeAlreadySatisfied, "state_read_back", "state_stopped"), nil
	}
	if err := ctx.Err(); err != nil {
		return ports.ClientEffect{}, err
	}
	if err := client.writeAllowed(operationStop, client.capabilities.Stop); err != nil {
		return ports.ClientEffect{}, err
	}
	writeErr := client.upstream.Stop(ctx, snapshot.torrent.Hash)
	reconciled, readErr := client.readAfterWrite(ctx, ref, false, operationStop)
	if readErr == nil && isStoppedState(reconciled.torrent.State) {
		return effect(operationStop, domain.OutcomeApplied, "state_read_back", "state_stopped"), nil
	}
	return ports.ClientEffect{}, unknownAfterWrite(operationStop, readErr, writeErr)
}

// Relocate invokes qBittorrent setLocation for the whole torrent. Destination
// paths are translated through an explicit configured mapping and exact
// content-path read-back is required before reporting success.
func (client *Client) Relocate(ctx context.Context, ref ports.DownloadRef, destination domain.FileTarget) (ports.ClientEffect, error) {
	ctx = contextOrBackground(ctx)
	if err := validateRef(client.connectionID, ref, operationRelocate); err != nil {
		return ports.ClientEffect{}, err
	}
	if err := destination.Validate(); err != nil {
		return ports.ClientEffect{}, invalidInput(operationRelocate, "destination is invalid")
	}
	desiredRemote, mapped, ambiguous := client.targetToRemote(destination)
	if ambiguous {
		return ports.ClientEffect{}, conflict(operationRelocate, "destination mapping is ambiguous")
	}
	if !mapped {
		return ports.ClientEffect{}, invalidInput(operationRelocate, "destination is not mapped")
	}
	snapshot, err := client.readSnapshot(ctx, ref, true, operationRelocate)
	if err != nil {
		if errors.Is(err, errTorrentNotFound) {
			return ports.ClientEffect{}, notFound(operationRelocate)
		}
		return ports.ClientEffect{}, err
	}
	if normalizeRemotePath(snapshot.torrent.ContentPath) == desiredRemote {
		return effect(operationRelocate, domain.OutcomeAlreadySatisfied, "content_path_read_back"), nil
	}
	if !isStoppedState(snapshot.torrent.State) {
		return ports.ClientEffect{}, conflict(operationRelocate, "torrent is not stopped")
	}
	if err := ctx.Err(); err != nil {
		return ports.ClientEffect{}, err
	}
	latest, err := client.readSnapshot(ctx, ref, true, operationRelocate)
	if err != nil {
		return ports.ClientEffect{}, err
	}
	if !samePayloadScope(snapshot, latest) || !isStoppedState(latest.torrent.State) {
		return ports.ClientEffect{}, conflict(operationRelocate, "torrent scope changed before relocation")
	}
	if err := client.writeAllowed(operationRelocate, client.capabilities.Relocate); err != nil {
		return ports.ClientEffect{}, err
	}
	if client.destinationChecker == nil {
		return ports.ClientEffect{}, upstreamFailure(domain.OutcomeUnsupported, operationRelocate, "destination vacancy evidence is unavailable")
	}
	vacant, vacancyErr := client.destinationChecker.CheckVacant(ctx, destination)
	if vacancyErr != nil {
		if errors.Is(vacancyErr, context.Canceled) || errors.Is(vacancyErr, context.DeadlineExceeded) {
			return ports.ClientEffect{}, vacancyErr
		}
		return ports.ClientEffect{}, upstreamFailure(domain.OutcomeUnavailable, operationRelocate, "destination vacancy could not be observed")
	}
	if !vacant {
		return ports.ClientEffect{}, conflict(operationRelocate, "destination is occupied")
	}
	// qBittorrent setLocation accepts the containing download directory. The
	// FileTarget above remains the final content path that read-back must prove.
	location := path.Dir(desiredRemote)
	writeErr := client.upstream.SetLocation(ctx, latest.torrent.Hash, location)
	reconciled, readErr := client.readAfterWrite(ctx, ref, true, operationRelocate)
	if readErr == nil && relocateReadBack(client, latest, reconciled, desiredRemote) {
		return effect(operationRelocate, domain.OutcomeApplied, "content_path_read_back"), nil
	}
	return ports.ClientEffect{}, unknownAfterWrite(operationRelocate, readErr, writeErr)
}

// RenameFile invokes qBittorrent renameFile for one exact observed file. A
// collision or missing source is rejected before dispatch; the native API is
// never given a wildcard or directory scope.
func (client *Client) RenameFile(ctx context.Context, ref ports.DownloadRef, source domain.FileTarget, newName string) (ports.ClientEffect, error) {
	ctx = contextOrBackground(ctx)
	if err := validateRef(client.connectionID, ref, operationRenameFile); err != nil {
		return ports.ClientEffect{}, err
	}
	if err := source.Validate(); err != nil || !validName(newName) {
		return ports.ClientEffect{}, invalidInput(operationRenameFile, "source or file name is invalid")
	}
	snapshot, err := client.readSnapshot(ctx, ref, true, operationRenameFile)
	if err != nil {
		if errors.Is(err, errTorrentNotFound) {
			return ports.ClientEffect{}, notFound(operationRenameFile)
		}
		return ports.ClientEffect{}, err
	}
	destination := domain.FileTarget{RootID: source.RootID, RelativePath: path.Join(path.Dir(source.RelativePath), newName)}
	if err := destination.Validate(); err != nil {
		return ports.ClientEffect{}, invalidInput(operationRenameFile, "destination file name is invalid")
	}
	sourceIndex, sourceFound := findTarget(snapshot.files, source)
	destinationIndex, destinationFound := findTarget(snapshot.files, destination)
	if !sourceFound {
		if destinationFound {
			return effect(operationRenameFile, domain.OutcomeAlreadySatisfied, "payload_read_back"), nil
		}
		return ports.ClientEffect{}, conflict(operationRenameFile, "source file was not observed")
	}
	if destinationFound && destinationIndex != sourceIndex {
		return ports.ClientEffect{}, conflict(operationRenameFile, "destination file already exists")
	}
	if source.RelativePath == destination.RelativePath {
		return effect(operationRenameFile, domain.OutcomeAlreadySatisfied, "payload_read_back"), nil
	}
	if !isStoppedState(snapshot.torrent.State) {
		return ports.ClientEffect{}, conflict(operationRenameFile, "torrent is not stopped")
	}
	if err := ctx.Err(); err != nil {
		return ports.ClientEffect{}, err
	}
	latest, err := client.readSnapshot(ctx, ref, true, operationRenameFile)
	if err != nil {
		return ports.ClientEffect{}, err
	}
	if !samePayloadScope(snapshot, latest) || !isStoppedState(latest.torrent.State) {
		return ports.ClientEffect{}, conflict(operationRenameFile, "torrent scope changed before rename")
	}
	sourceIndex, sourceFound = findTarget(latest.files, source)
	destinationIndex, destinationFound = findTarget(latest.files, destination)
	if !sourceFound {
		if destinationFound {
			return effect(operationRenameFile, domain.OutcomeAlreadySatisfied, "payload_read_back"), nil
		}
		return ports.ClientEffect{}, conflict(operationRenameFile, "source file was not observed")
	}
	if destinationFound && destinationIndex != sourceIndex {
		return ports.ClientEffect{}, conflict(operationRenameFile, "destination file already exists")
	}
	if err := client.writeAllowed(operationRenameFile, client.capabilities.RenameFile); err != nil {
		return ports.ClientEffect{}, err
	}
	oldPath := latest.files[sourceIndex].nativeName
	newPath := path.Join(path.Dir(oldPath), newName)
	writeErr := client.upstream.RenameFile(ctx, latest.torrent.Hash, oldPath, newPath)
	reconciled, readErr := client.readAfterWrite(ctx, ref, true, operationRenameFile)
	if readErr == nil && renameFileReadBack(reconciled.files, source, destination) {
		return effect(operationRenameFile, domain.OutcomeApplied, "payload_read_back"), nil
	}
	return ports.ClientEffect{}, unknownAfterWrite(operationRenameFile, readErr, writeErr)
}

// RenameFolder invokes qBittorrent renameFolder only when source contains the
// complete observed torrent payload. Native folder rename cannot represent a
// selected subset, so any file outside source blocks dispatch.
func (client *Client) RenameFolder(ctx context.Context, ref ports.DownloadRef, source domain.FileTarget, newName string) (ports.ClientEffect, error) {
	ctx = contextOrBackground(ctx)
	if err := validateRef(client.connectionID, ref, operationRenameFolder); err != nil {
		return ports.ClientEffect{}, err
	}
	if err := source.Validate(); err != nil || !validName(newName) {
		return ports.ClientEffect{}, invalidInput(operationRenameFolder, "source or folder name is invalid")
	}
	destination := domain.FileTarget{RootID: source.RootID, RelativePath: path.Join(path.Dir(source.RelativePath), newName)}
	if err := destination.Validate(); err != nil {
		return ports.ClientEffect{}, invalidInput(operationRenameFolder, "destination folder name is invalid")
	}
	snapshot, err := client.readSnapshot(ctx, ref, true, operationRenameFolder)
	if err != nil {
		if errors.Is(err, errTorrentNotFound) {
			return ports.ClientEffect{}, notFound(operationRenameFolder)
		}
		return ports.ClientEffect{}, err
	}
	selected, outside := folderScope(snapshot.files, source)
	if len(selected) == 0 {
		if folderScopeSatisfied(snapshot.files, destination) {
			return effect(operationRenameFolder, domain.OutcomeAlreadySatisfied, "payload_read_back"), nil
		}
		return ports.ClientEffect{}, conflict(operationRenameFolder, "source folder was not observed")
	}
	if outside {
		return ports.ClientEffect{}, conflict(operationRenameFolder, "native folder rename would expand beyond reviewed scope")
	}
	if source == destination {
		return effect(operationRenameFolder, domain.OutcomeAlreadySatisfied, "payload_read_back"), nil
	}
	if !isStoppedState(snapshot.torrent.State) {
		return ports.ClientEffect{}, conflict(operationRenameFolder, "torrent is not stopped")
	}
	if err := ctx.Err(); err != nil {
		return ports.ClientEffect{}, err
	}
	latest, err := client.readSnapshot(ctx, ref, true, operationRenameFolder)
	if err != nil {
		return ports.ClientEffect{}, err
	}
	if !samePayloadScope(snapshot, latest) || !isStoppedState(latest.torrent.State) {
		return ports.ClientEffect{}, conflict(operationRenameFolder, "torrent scope changed before rename")
	}
	selected, outside = folderScope(latest.files, source)
	if len(selected) == 0 {
		if folderScopeSatisfied(latest.files, destination) {
			return effect(operationRenameFolder, domain.OutcomeAlreadySatisfied, "payload_read_back"), nil
		}
		return ports.ClientEffect{}, conflict(operationRenameFolder, "source folder was not observed")
	}
	if outside {
		return ports.ClientEffect{}, conflict(operationRenameFolder, "native folder rename would expand beyond reviewed scope")
	}
	if source == destination {
		return effect(operationRenameFolder, domain.OutcomeAlreadySatisfied, "payload_read_back"), nil
	}
	oldPath, ok := client.nativeFolderPath(latest, source, selected)
	if !ok {
		return ports.ClientEffect{}, conflict(operationRenameFolder, "native folder scope is not exact")
	}
	newPath := path.Join(path.Dir(oldPath), newName)
	if oldPath == "." || newPath == "." || !nativeRenameHasNoCollision(latest.files, oldPath, newPath) {
		return ports.ClientEffect{}, conflict(operationRenameFolder, "native folder destination collides or expands scope")
	}
	if err := client.writeAllowed(operationRenameFolder, client.capabilities.RenameFolder); err != nil {
		return ports.ClientEffect{}, err
	}
	writeErr := client.upstream.RenameFolder(ctx, latest.torrent.Hash, oldPath, newPath)
	reconciled, readErr := client.readAfterWrite(ctx, ref, true, operationRenameFolder)
	if readErr == nil && renameFolderReadBack(reconciled.files, source, destination) {
		return effect(operationRenameFolder, domain.OutcomeApplied, "payload_read_back"), nil
	}
	return ports.ClientEffect{}, unknownAfterWrite(operationRenameFolder, readErr, writeErr)
}

// Remove removes only the qBittorrent metadata record. It refuses active
// torrents and always passes the literal false deleteFiles argument. It never
// re-adds or resumes a torrent after removal.
func (client *Client) Remove(ctx context.Context, ref ports.DownloadRef) (ports.ClientEffect, error) {
	ctx = contextOrBackground(ctx)
	if err := validateRef(client.connectionID, ref, operationRemove); err != nil {
		return ports.ClientEffect{}, err
	}
	snapshot, err := client.readSnapshot(ctx, ref, false, operationRemove)
	if err != nil {
		if errors.Is(err, errTorrentNotFound) {
			return effect(operationRemove, domain.OutcomeAlreadySatisfied, "record_read_back", "record_absent"), nil
		}
		return ports.ClientEffect{}, err
	}
	if !isStoppedState(snapshot.torrent.State) {
		return ports.ClientEffect{}, conflict(operationRemove, "torrent is not stopped")
	}
	if err := ctx.Err(); err != nil {
		return ports.ClientEffect{}, err
	}
	latest, err := client.readSnapshot(ctx, ref, false, operationRemove)
	if err != nil {
		if errors.Is(err, errTorrentNotFound) {
			return effect(operationRemove, domain.OutcomeAlreadySatisfied, "record_read_back", "record_absent"), nil
		}
		return ports.ClientEffect{}, err
	}
	if !isStoppedState(latest.torrent.State) {
		return ports.ClientEffect{}, conflict(operationRemove, "torrent resumed before metadata removal")
	}
	if err := client.writeAllowed(operationRemove, client.capabilities.Remove); err != nil {
		return ports.ClientEffect{}, err
	}
	// deleteFiles=false is deliberate and part of the adapter's safety
	// contract. No caller-provided flag can widen this operation.
	writeErr := client.upstream.Delete(ctx, latest.torrent.Hash, false)
	_, readErr := client.readSnapshotAfterWrite(ctx, ref, false, operationRemove)
	if errors.Is(readErr, errTorrentNotFound) {
		return effect(operationRemove, domain.OutcomeApplied, "record_read_back", "record_absent"), nil
	}
	return ports.ClientEffect{}, unknownAfterWrite(operationRemove, readErr, writeErr)
}

func (client *Client) readSnapshot(ctx context.Context, ref ports.DownloadRef, needFiles bool, operation string) (snapshot, error) {
	ctx = contextOrBackground(ctx)
	records, err := client.upstream.ListTorrents(ctx, native.TorrentListOptions{Hashes: []string{ref.ExternalID}, Limit: 1})
	if err != nil {
		return snapshot{}, translateNativeError(operation, err)
	}
	var selected native.Torrent
	found := false
	for _, record := range records {
		if !strings.EqualFold(record.Hash, ref.ExternalID) {
			continue
		}
		if found {
			return snapshot{}, upstreamFailure(domain.OutcomeUnknown, operation, "duplicate torrent identity")
		}
		selected, found = record, true
	}
	if !found {
		return snapshot{}, errTorrentNotFound
	}
	if strings.TrimSpace(selected.Hash) == "" {
		return snapshot{}, upstreamFailure(domain.OutcomeUnknown, operation, "torrent identity is missing")
	}
	result := snapshot{torrent: selected}
	if !needFiles {
		return result, nil
	}
	files, err := client.upstream.GetTorrentFiles(ctx, selected.Hash)
	if err != nil {
		return snapshot{}, translateNativeError(operation, err)
	}
	result.files, err = client.mapFiles(selected.Hash, selected.ContentPath, files, operation)
	if err != nil {
		return snapshot{}, err
	}
	return result, nil
}

func (client *Client) readSnapshotAfterWrite(ctx context.Context, ref ports.DownloadRef, needFiles bool, operation string) (snapshot, error) {
	reconcileContext, cancel := client.reconcileContext(ctx)
	defer cancel()
	return client.readSnapshot(reconcileContext, ref, needFiles, operation)
}

func (client *Client) readAfterWrite(ctx context.Context, ref ports.DownloadRef, needFiles bool, operation string) (snapshot, error) {
	return client.readSnapshotAfterWrite(ctx, ref, needFiles, operation)
}

func (client *Client) reconcileContext(parent context.Context) (context.Context, context.CancelFunc) {
	parent = contextOrBackground(parent)
	parent = context.WithoutCancel(parent)
	return context.WithTimeout(parent, client.reconcileTimeout)
}

func (client *Client) mapFiles(hash, contentPath string, files []native.TorrentFile, operation string) ([]observedFile, error) {
	if len(files) == 0 {
		return nil, upstreamFailure(domain.OutcomeUnknown, operation, "torrent payload is empty")
	}
	result := make([]observedFile, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	seenIndexes := make(map[int64]struct{}, len(files))
	for _, file := range files {
		if file.Index < 0 || file.Size < 0 || !validNativeName(file.Name) {
			return nil, upstreamFailure(domain.OutcomeUnknown, operation, "torrent payload path or metadata is invalid")
		}
		if _, exists := seenIndexes[file.Index]; exists {
			return nil, upstreamFailure(domain.OutcomeUnknown, operation, "torrent payload contains duplicate indexes")
		}
		seenIndexes[file.Index] = struct{}{}
		remote, ok := remoteFilePath(contentPath, file.Name, len(files))
		if !ok {
			return nil, upstreamFailure(domain.OutcomeUnknown, operation, "torrent payload path is unsafe")
		}
		target, mapped, ambiguous := client.remoteToTarget(remote)
		if ambiguous {
			return nil, upstreamFailure(domain.OutcomeUnknown, operation, "torrent payload mapping is ambiguous")
		}
		if !mapped {
			return nil, upstreamFailure(domain.OutcomeUnknown, operation, "torrent payload is not mapped")
		}
		key := target.RootID.String() + ":" + target.RelativePath
		if _, exists := seen[key]; exists {
			return nil, upstreamFailure(domain.OutcomeUnknown, operation, "torrent payload contains duplicate paths")
		}
		seen[key] = struct{}{}
		observedAt := time.Now().UTC()
		result = append(result, observedFile{
			nativeName: file.Name,
			remotePath: remote,
			target:     target,
			manifest: domain.FileManifestEntry{
				RootID: target.RootID, RelativePath: target.RelativePath,
				Type: manifestType(file.Name), Role: manifestRole(file.Name),
				Size: file.Size, FileIdentity: fmt.Sprintf("qbt:%s:%d", hash, file.Index),
				ObservedAt: observedAt,
			},
		})
	}
	return result, nil
}

func (client *Client) nativeFolderPath(snapshot snapshot, source domain.FileTarget, selected []observedFile) (string, bool) {
	remoteFolder, mapped, ambiguous := client.targetToRemote(source)
	if !mapped || ambiguous {
		return "", false
	}
	var candidate string
	for _, file := range selected {
		if !pathBoundaryMatch(file.remotePath, remoteFolder) || file.remotePath == remoteFolder {
			return "", false
		}
		suffix := strings.TrimPrefix(file.remotePath, remoteFolder+"/")
		if suffix == "" || !strings.HasSuffix(file.nativeName, suffix) {
			return "", false
		}
		prefix := strings.TrimSuffix(file.nativeName, suffix)
		prefix = strings.TrimSuffix(prefix, "/")
		if prefix == "" {
			return "", false
		}
		if candidate == "" {
			candidate = prefix
		} else if candidate != prefix {
			return "", false
		}
	}
	if candidate == "." || !validNativeRelativePath(candidate) {
		return "", false
	}
	return candidate, true
}

func (client *Client) writeAllowed(operation string, capability OperationCapability) error {
	if capability.State != domain.CapabilitySupported {
		return upstreamFailure(domain.OutcomeUnsupported, operation, "qBittorrent operation write capability is not enabled")
	}
	return nil
}

func (client *Client) remoteToTarget(remote string) (domain.FileTarget, bool, bool) {
	bestLength := -1
	var selected mapping
	ambiguous := false
	for _, candidate := range client.mappings {
		if !pathBoundaryMatch(remote, candidate.source) {
			continue
		}
		if len(candidate.source) > bestLength {
			bestLength = len(candidate.source)
			selected = candidate
			ambiguous = false
			continue
		}
		if len(candidate.source) == bestLength && (selected.rootID != candidate.rootID || selected.destination != candidate.destination) {
			ambiguous = true
		}
	}
	if bestLength < 0 || ambiguous {
		return domain.FileTarget{}, false, ambiguous
	}
	suffix := strings.TrimPrefix(remote, selected.source)
	suffix = strings.TrimPrefix(suffix, "/")
	relative := selected.destination
	if suffix != "" {
		if relative == "" {
			relative = suffix
		} else {
			relative = path.Join(relative, suffix)
		}
	}
	target := domain.FileTarget{RootID: selected.rootID, RelativePath: relative}
	if err := target.Validate(); err != nil {
		return domain.FileTarget{}, false, false
	}
	return target, true, false
}

func (client *Client) targetToRemote(target domain.FileTarget) (string, bool, bool) {
	bestLength := -1
	var selected mapping
	ambiguous := false
	for _, candidate := range client.mappings {
		if candidate.rootID != target.RootID || !pathBoundaryMatch(target.RelativePath, candidate.destination) {
			continue
		}
		if len(candidate.destination) > bestLength {
			bestLength = len(candidate.destination)
			selected = candidate
			ambiguous = false
			continue
		}
		if len(candidate.destination) == bestLength && selected.source != candidate.source {
			ambiguous = true
		}
	}
	if bestLength < 0 || ambiguous {
		return "", false, ambiguous
	}
	suffix := strings.TrimPrefix(target.RelativePath, selected.destination)
	suffix = strings.TrimPrefix(suffix, "/")
	remote := selected.source
	if suffix != "" {
		remote = path.Join(remote, suffix)
	}
	if !absoluteRemotePath(remote) {
		return "", false, false
	}
	return remote, true, false
}

type mapping struct {
	source      string
	rootID      domain.ConfigID
	destination string
}

type observedFile struct {
	nativeName string
	remotePath string
	target     domain.FileTarget
	manifest   domain.FileManifestEntry
}

type snapshot struct {
	torrent native.Torrent
	files   []observedFile
}

func normalizeMappings(connectionID domain.ConfigID, input []domain.PathMapping) ([]mapping, error) {
	result := make([]mapping, 0, len(input))
	for index, value := range input {
		if value.ConnectionID != connectionID {
			continue
		}
		source, ok := normalizeSourcePrefix(value.SourcePrefix)
		if !ok || !value.RootID.Valid() {
			return nil, fmt.Errorf("qBittorrent path mapping %d is invalid", index)
		}
		destination, ok := normalizeDestinationPrefix(value.DestinationPrefix)
		if !ok {
			return nil, fmt.Errorf("qBittorrent path mapping %d destination is invalid", index)
		}
		result = append(result, mapping{source: source, rootID: value.RootID, destination: destination})
	}
	return result, nil
}

func normalizeSourcePrefix(value string) (string, bool) {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) {
		return "", false
	}
	clean := path.Clean(value)
	if clean != value || !absoluteRemotePath(clean) {
		return "", false
	}
	return clean, true
}

func normalizeDestinationPrefix(value string) (string, bool) {
	if strings.ContainsRune(value, 0) || strings.Contains(value, `\`) {
		return "", false
	}
	value = strings.Trim(value, "/")
	if value == "" {
		return "", true
	}
	if path.Clean(value) != value || !validNativeRelativePath(value) {
		return "", false
	}
	return value, true
}

func validateRef(expected domain.ConfigID, ref ports.DownloadRef, operation string) error {
	if !expected.Valid() || ref.ConnectionID != expected || !ref.ConnectionID.Valid() {
		return invalidInput(operation, "connection scope is invalid")
	}
	if !validExternalID(ref.ExternalID) {
		return invalidInput(operation, "torrent identity is invalid")
	}
	return nil
}

func validExternalID(value string) bool {
	return value != "" && len(value) <= maxExternalIDLength && !strings.ContainsAny(value, "|\r\n\t") && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsSpace) < 0
}

func validName(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, `/\\`) && !strings.ContainsRune(value, 0) && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validNativeName(value string) bool {
	return validNativeRelativePath(value) && !strings.HasPrefix(value, "/")
}

func validNativeRelativePath(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) || strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." || strings.IndexFunc(component, unicode.IsControl) >= 0 {
			return false
		}
	}
	return utf8.ValidString(value)
}

func remoteFilePath(contentPath, name string, fileCount int) (string, bool) {
	if !validNativeName(name) {
		return "", false
	}
	contentPath = normalizeRemotePath(contentPath)
	if contentPath == "" {
		return name, true
	}
	contentBase := path.Base(contentPath)
	if fileCount == 1 && name == contentBase {
		return contentPath, true
	}
	if name == contentBase || strings.HasPrefix(name, contentBase+"/") {
		return path.Join(path.Dir(contentPath), name), true
	}
	return path.Join(contentPath, name), true
}

// relocateReadBack proves the final content path and every payload path. A
// qBittorrent location response is only sufficient when the native payload
// names observed before dispatch resolve to exactly the mapped final targets
// observed afterwards. This prevents a directory-level acknowledgement from
// being mistaken for a complete relocation.
func relocateReadBack(client *Client, before, after snapshot, desiredContentPath string) bool {
	if normalizeRemotePath(after.torrent.ContentPath) != desiredContentPath || len(before.files) != len(after.files) {
		return false
	}
	expected := make(map[string]string, len(before.files))
	for _, file := range before.files {
		remote, ok := remoteFilePath(desiredContentPath, file.nativeName, len(before.files))
		if !ok {
			return false
		}
		target, mapped, ambiguous := client.remoteToTarget(remote)
		if !mapped || ambiguous {
			return false
		}
		key := targetKey(target)
		if _, exists := expected[key]; exists {
			return false
		}
		expected[key] = remote
	}
	for _, file := range after.files {
		want, ok := expected[targetKey(file.target)]
		if !ok || want != file.remotePath {
			return false
		}
		delete(expected, targetKey(file.target))
	}
	return len(expected) == 0
}

func targetKey(target domain.FileTarget) string {
	return target.RootID.String() + ":" + target.RelativePath
}

func normalizeRemotePath(value string) string {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) {
		return ""
	}
	clean := path.Clean(value)
	if clean != value || !absoluteRemotePath(clean) {
		return ""
	}
	return clean
}

func absoluteRemotePath(value string) bool {
	return value != "" && strings.HasPrefix(value, "/") && !strings.ContainsRune(value, 0) && path.Clean(value) == value
}

func pathBoundaryMatch(value, prefix string) bool {
	if prefix == "" {
		return value != ""
	}
	if prefix == "/" {
		return strings.HasPrefix(value, "/")
	}
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func isStoppedState(state string) bool {
	switch state {
	case "paused", "pausedDL", "pausedUP", "stopped", "stoppedDL", "stoppedUP":
		return true
	default:
		return false
	}
}

func isSeedingState(state string) bool {
	switch state {
	case "uploading", "stalledUP", "queuedUP", "forcedUP":
		return true
	default:
		return false
	}
}

func manifestType(name string) domain.ManifestEntryType {
	switch strings.ToLower(path.Ext(name)) {
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx", ".sup", ".smi":
		return domain.ManifestSubtitle
	case ".nfo", ".jpg", ".jpeg", ".png", ".webp", ".txt":
		return domain.ManifestCompanion
	default:
		return domain.ManifestFile
	}
}

func manifestRole(name string) domain.ManifestRole {
	switch manifestType(name) {
	case domain.ManifestSubtitle:
		return domain.RoleSubtitle
	case domain.ManifestCompanion:
		return domain.RoleCompanion
	default:
		switch strings.ToLower(path.Ext(name)) {
		case ".mkv", ".mp4", ".m4v", ".avi", ".mov", ".wmv", ".webm", ".ts", ".m2ts":
			return domain.RoleVideo
		default:
			return ""
		}
	}
}

func folderScope(files []observedFile, source domain.FileTarget) (selected []observedFile, outside bool) {
	for _, file := range files {
		if file.target.RootID != source.RootID || !pathBoundaryMatch(file.target.RelativePath, source.RelativePath) || file.target.RelativePath == source.RelativePath {
			outside = true
			continue
		}
		selected = append(selected, file)
	}
	return selected, outside
}

func folderScopeSatisfied(files []observedFile, destination domain.FileTarget) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		if file.target.RootID != destination.RootID || !pathBoundaryMatch(file.target.RelativePath, destination.RelativePath) || file.target.RelativePath == destination.RelativePath {
			return false
		}
	}
	return true
}

func findTarget(files []observedFile, target domain.FileTarget) (int, bool) {
	for index, file := range files {
		if file.target == target {
			return index, true
		}
	}
	return -1, false
}

func samePayloadScope(left, right snapshot) bool {
	if normalizeRemotePath(left.torrent.ContentPath) != normalizeRemotePath(right.torrent.ContentPath) || len(left.files) != len(right.files) {
		return false
	}
	leftFiles := make(map[string]observedFile, len(left.files))
	for _, file := range left.files {
		leftFiles[file.nativeName] = file
	}
	for _, file := range right.files {
		other, ok := leftFiles[file.nativeName]
		if !ok || other.target != file.target || other.remotePath != file.remotePath {
			return false
		}
	}
	return true
}

func renameFileReadBack(files []observedFile, source, destination domain.FileTarget) bool {
	_, sourceFound := findTarget(files, source)
	_, destinationFound := findTarget(files, destination)
	return !sourceFound && destinationFound
}

func renameFolderReadBack(files []observedFile, source, destination domain.FileTarget) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		if file.target.RootID != destination.RootID || !pathBoundaryMatch(file.target.RelativePath, destination.RelativePath) || file.target.RelativePath == destination.RelativePath {
			return false
		}
		if pathBoundaryMatch(file.target.RelativePath, source.RelativePath) {
			return false
		}
	}
	return true
}

func nativeRenameHasNoCollision(files []observedFile, oldPath, newPath string) bool {
	predicted := make(map[string]struct{}, len(files))
	for _, file := range files {
		if !strings.HasPrefix(file.nativeName, oldPath+"/") {
			return false
		}
		name := path.Join(newPath, strings.TrimPrefix(file.nativeName, oldPath+"/"))
		if _, exists := predicted[name]; exists {
			return false
		}
		predicted[name] = struct{}{}
	}
	return len(predicted) == len(files)
}

func effect(operation string, outcome domain.EffectOutcome, evidence ...string) ports.ClientEffect {
	return ports.ClientEffect{
		OperationID: operation,
		Outcome:     outcome,
		ObservedAt:  time.Now().UTC(),
		Evidence:    append([]string(nil), evidence...),
	}
}

func invalidInput(operation, detail string) error {
	return upstreamFailure(domain.OutcomeInvalidInput, operation, detail)
}

func conflict(operation, detail string) error {
	return upstreamFailure(domain.OutcomeConflict, operation, detail)
}

func notFound(operation string) error {
	return upstreamFailure(domain.OutcomeUnavailable, operation, "torrent record was not found")
}

func upstreamFailure(code domain.UpstreamErrorCode, operation, detail string) error {
	return domain.UpstreamError{Code: code, Operation: operation, Detail: detail}
}

func unknownAfterWrite(operation string, readErr, writeErr error) error {
	// Once a mutating request was dispatched, even a typed transport error may
	// mean the upstream accepted it before the response was lost. Keep outcome
	// unknown until read-back proves the desired state; never expose raw error
	// text or blindly resubmit.
	status := 0
	retryable := false
	var nativeErr native.UpstreamError
	if errors.As(writeErr, &nativeErr) {
		status, retryable = nativeErr.Status, nativeErr.Retryable
	}
	var domainErr domain.UpstreamError
	if errors.As(writeErr, &domainErr) {
		status, retryable = domainErr.Status, domainErr.Retryable
	}
	if readErr != nil {
		var readDomain domain.UpstreamError
		if errors.As(readErr, &readDomain) && status == 0 {
			status, retryable = readDomain.Status, readDomain.Retryable
		}
	}
	return domain.UpstreamError{
		Code: domain.OutcomeUnknown, Status: status, Retryable: retryable,
		Operation: operation, Detail: "qBittorrent write outcome could not be reconciled",
	}
}

func translateNativeError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var upstream native.UpstreamError
	if !errors.As(err, &upstream) {
		return domain.UpstreamError{Code: domain.OutcomeUnknown, Operation: operation, Detail: "qBittorrent upstream request failed"}
	}
	code := domain.OutcomeUnknown
	switch upstream.Code {
	case native.ErrorUnavailable:
		code = domain.OutcomeUnavailable
	case native.ErrorRateLimited:
		code = domain.OutcomeRateLimited
	case native.ErrorUnauthorized:
		code = domain.OutcomeUnauthorized
	case native.ErrorInvalidInput:
		code = domain.OutcomeInvalidInput
	case native.ErrorConflict:
		code = domain.OutcomeConflict
	case native.ErrorUnsupported:
		code = domain.OutcomeUnsupported
	}
	return domain.UpstreamError{
		Code: code, Status: upstream.Status, Retryable: upstream.Retryable,
		Operation: operation, Detail: "qBittorrent upstream request failed",
	}
}

func boundedCapabilityText(value string) (string, error) {
	if len(value) > maxCapabilityText || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("capability evidence text is invalid")
	}
	return strings.TrimSpace(value), nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
