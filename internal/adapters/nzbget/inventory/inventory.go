// Package inventory implements the read-only NZBGet JSON-RPC inventory
// boundary. It never edits the NZBGet queue, history or post-processing state.
package inventory

import (
	"bytes"
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	defaultMaxPageSize       = 200
	defaultMaxPages          = 100
	defaultMaxItems          = 10_000
	defaultMaxResponseBytes  = 8 << 20
	defaultMaxDescriptorSize = 4 << 20
	maxItemsCeiling          = 10_000
	maxCursorBytes           = 16 << 10
	maxEncodedCursorBytes    = 24 << 10
	maxReasonCodes           = 256
	maxVersionLength         = 128
	maxRPCMethodLength       = 64
	maxDroneLength           = 256
)

const rpcPath = "/jsonrpc"

// DescriptorMode controls optional lookup of an original retained NZB file.
// NZBGet history is never treated as an NZB descriptor.
type DescriptorMode string

const (
	DescriptorDisabled   DescriptorMode = "disabled"
	DescriptorBestEffort DescriptorMode = "best_effort"
)

// Config contains connection settings and bounded observation limits.
// Credentials are used only for HTTP Basic authentication and never appear in
// errors or observations.
type Config struct {
	ConnectionID domain.ConfigID
	Endpoint     string
	Username     string
	Password     string

	HTTPClient *http.Client
	Mappings   []domain.PathMapping

	MaxPageSize       int
	MaxPages          int
	MaxItems          int
	MaxResponseBytes  int64
	MaxDescriptorSize int64
	DescriptorMode    DescriptorMode
	// DescriptorDirectory is an explicitly configured mounted directory that
	// may contain retained original .nzb files. It is never scanned recursively.
	DescriptorDirectory string
}

// VersionObservation records NZBGet's read-only version response.
type VersionObservation struct {
	ConnectionID domain.ConfigID
	Version      string
	ObservedAt   time.Time
}

// ParameterObservation preserves one upstream post-processing parameter.
// Values are strings in NZBGet's public API. Names are retained as typed
// evidence, while values are redacted unless they are a validated exact-name
// drone correlation identifier. Malformed values stay absent and cause
// partial coverage rather than being guessed.
type ParameterObservation struct {
	Name  string
	Value string
}

// QueueObservation preserves NZBGet listgroups evidence, including native
// post-processing status and both identity aliases.
type QueueObservation struct {
	NZBID             int64
	DeprecatedID      int64
	Kind              string
	NZBFilename       string
	NZBName           string
	DestDir           string
	FinalDir          string
	Category          string
	Status            string
	FileSizeBytes     int64
	RemainingBytes    int64
	PausedBytes       int64
	FileCount         int64
	RemainingFiles    int64
	ActiveDownloads   int64
	TotalArticles     int64
	SuccessArticles   int64
	FailedArticles    int64
	Health            int64
	CriticalHealth    int64
	DownloadedBytes   int64
	Parameters        []ParameterObservation
	PostInfoText      string
	PostStageProgress int64
}

// HistoryObservation preserves NZBGet history evidence. ID is the deprecated
// alias of NZBID and is never treated as a second identity.
type HistoryObservation struct {
	NZBID           int64
	DeprecatedID    int64
	Kind            string
	NZBFilename     string
	NZBName         string
	Name            string
	URL             string
	HistoryTime     *time.Time
	DestDir         string
	FinalDir        string
	Category        string
	FileSizeBytes   int64
	DownloadedBytes int64
	FileCount       int64
	RemainingFiles  int64
	Health          int64
	CriticalHealth  int64
	Status          string
	ParStatus       string
	UnpackStatus    string
	ScriptStatus    string
	MoveStatus      string
	DeleteStatus    string
	MarkStatus      string
	URLStatus       string
	Parameters      []ParameterObservation
}

// DownloadObservation is a detailed NZBGet observation. MappedPath points at
// the final directory when an explicit path mapping exists. NZBGet history
// does not provide an exact filesystem manifest, so Item.Payload stays empty;
// callers must obtain an exact manifest from the filesystem boundary before
// any action.
type DownloadObservation struct {
	Item           ports.DownloadItem
	ScopedIdentity string
	NZBID          int64
	// HistoryID is the deprecated NZBGet ID alias. It is retained as evidence
	// but must equal NZBID when present and is never an independent identity.
	HistoryID     int64
	ArrDownloadID string
	Drone         string
	Kind          string
	NZBFilename   string
	NZBName       string
	DestDir       string
	FinalDir      string
	ContentPath   string
	MappedPath    *domain.FileTarget
	Queue         *QueueObservation
	History       *HistoryObservation

	// These fields retain exact-name parameter presence across queue/history
	// merging. An empty or unusable exact drone parameter must not turn back
	// into the numeric fallback merely because the other view lacks it.
	droneParameterPresent bool
	droneParameterUsable  bool
}

// DetailedPage is the adapter-specific bounded page returned by ListDetailed.
type DetailedPage struct {
	Items      []DownloadObservation
	NextCursor string
	Coverage   domain.Coverage
}

// Client is a read-only authenticated NZBGet JSON-RPC client.
type Client struct {
	config    Config
	endpoint  *url.URL
	http      *http.Client
	cursorKey []byte
	requestID atomic.Uint64
}

var _ ports.DownloadInventoryPort = (*Client)(nil)
var _ ports.CapabilityPort = (*Client)(nil)

// New validates connection settings without making a network request.
func New(config Config) (*Client, error) {
	if !config.ConnectionID.Valid() {
		return nil, errors.New("NZBGet connection id is invalid")
	}
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.MaxPageSize <= 0 {
		config.MaxPageSize = defaultMaxPageSize
	}
	if config.MaxPages <= 0 {
		config.MaxPages = defaultMaxPages
	}
	if config.MaxItems <= 0 {
		config.MaxItems = defaultMaxItems
	}
	if config.MaxItems > maxItemsCeiling {
		return nil, errors.New("NZBGet inventory item bound exceeds configured ceiling")
	}
	if config.MaxPageSize > config.MaxItems {
		config.MaxPageSize = config.MaxItems
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}
	if config.MaxDescriptorSize <= 0 {
		config.MaxDescriptorSize = defaultMaxDescriptorSize
	}
	if config.DescriptorMode == "" {
		config.DescriptorMode = DescriptorDisabled
	}
	if config.DescriptorMode != DescriptorDisabled && config.DescriptorMode != DescriptorBestEffort {
		return nil, errors.New("NZBGet descriptor mode is unsupported")
	}
	if config.DescriptorDirectory != "" {
		if !filepath.IsAbs(config.DescriptorDirectory) {
			return nil, errors.New("NZBGet descriptor directory must be absolute")
		}
		config.DescriptorDirectory = filepath.Clean(config.DescriptorDirectory)
	}
	if err := validateMappings(config.ConnectionID, config.Mappings); err != nil {
		return nil, err
	}
	config.Mappings = append([]domain.PathMapping(nil), config.Mappings...)

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	copyClient := *baseClient
	httpClient := &copyClient
	if httpClient.Timeout == 0 {
		httpClient.Timeout = 15 * time.Second
	}
	baseScheme, baseHost := endpoint.Scheme, endpoint.Host
	customRedirect := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != baseScheme || !strings.EqualFold(request.URL.Host, baseHost) {
			return http.ErrUseLastResponse
		}
		if customRedirect != nil {
			return customRedirect(request, via)
		}
		return nil
	}
	cursorKey := make([]byte, 32)
	if _, err := cryptorand.Read(cursorKey); err != nil {
		return nil, errors.New("NZBGet cursor key setup failed")
	}
	return &Client{config: config, endpoint: endpoint, http: httpClient, cursorKey: cursorKey}, nil
}

// NewClient is an explicit constructor alias.
func NewClient(config Config) (*Client, error) { return New(config) }

// ConnectionID returns stable configured instance scope.
func (client *Client) ConnectionID() domain.ConfigID { return client.config.ConnectionID }

// ScopedIdentity scopes a canonical NZBID by configured connection.
func (client *Client) ScopedIdentity(externalID string) string {
	if externalID == "" {
		return ""
	}
	return client.config.ConnectionID.String() + ":nzbget:" + externalID
}

// List implements ports.DownloadInventoryPort.
func (client *Client) List(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (ports.Page[ports.DownloadItem], error) {
	detailed, err := client.ListDetailed(ctx, connectionID, cursor, limit)
	if err != nil {
		return ports.Page[ports.DownloadItem]{}, err
	}
	items := make([]ports.DownloadItem, 0, len(detailed.Items))
	for _, item := range detailed.Items {
		items = append(items, item.Item)
	}
	if len(items) == 0 {
		items = nil
	}
	return ports.Page[ports.DownloadItem]{Items: items, NextCursor: detailed.NextCursor, Coverage: detailed.Coverage}, nil
}

// ListDetailed reads bounded queue and history arrays, then pages their local
// merged view. NZBGet's JSON-RPC arrays have no reliable server-side cursor.
func (client *Client) ListDetailed(ctx context.Context, connectionID domain.ConfigID, cursor string, requestedLimit int) (DetailedPage, error) {
	var result DetailedPage
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	limit, err := client.pageLimit(requestedLimit)
	if err != nil {
		return result, err
	}
	state, err := client.decodeCursor(cursor)
	if err != nil {
		return result, err
	}
	if state.SourceID != "" {
		if requestedLimit > 0 && limit != state.PageSize {
			return result, invalidInput("nzbget.inventory.cursor")
		}
		limit = state.PageSize
	}
	if state.SourceID == "" {
		state.SourceID, err = domain.NewRuntimeID()
		if err != nil {
			return result, errors.New("NZBGet inventory source identity unavailable")
		}
		state.StartedAt = time.Now().UTC()
		state.PageSize = limit
	}
	if state.ObservedCount >= int64(client.config.MaxItems) && cursor != "" {
		return result, invalidInput("nzbget.inventory.cursor")
	}

	snapshot, err := client.collect(ctx, connectionID)
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	result.Coverage = domain.Coverage{
		SourceID: state.SourceID, ConnectionID: connectionID,
		Completeness: domain.CompletenessComplete, ObservedCount: state.ObservedCount,
		SnapshotRevision: "sha256:" + snapshot.digest,
		StartedAt:        timePtr(state.StartedAt), ObservedAt: now,
	}
	for _, reason := range snapshot.reasons {
		addReason(&result.Coverage.ReasonCodes, reason)
	}
	if state.PriorPartial {
		addReason(&result.Coverage.ReasonCodes, "pagination_prior_partial")
	}
	snapshotChanged := state.SnapshotDigest != "" && state.SnapshotDigest != snapshot.digest
	if snapshotChanged {
		addReason(&result.Coverage.ReasonCodes, "pagination_snapshot_changed")
	}
	if state.Offset > len(snapshot.items) {
		addReason(&result.Coverage.ReasonCodes, "pagination_offset_changed")
		state.Offset = len(snapshot.items)
	}
	end := state.Offset + limit
	if end < state.Offset || end > len(snapshot.items) {
		end = len(snapshot.items)
	}
	result.Items = append([]DownloadObservation(nil), snapshot.items[state.Offset:end]...)
	state.Offset = end
	state.PageCount++
	state.ObservedCount += int64(len(result.Items))
	result.Coverage.ObservedCount = state.ObservedCount
	if state.ObservedCount > int64(client.config.MaxItems) {
		result.Items = result.Items[:client.config.MaxItems-int(state.ObservedCount-int64(len(result.Items)))]
		state.ObservedCount = int64(client.config.MaxItems)
		result.Coverage.ObservedCount = state.ObservedCount
		addReason(&result.Coverage.ReasonCodes, "items_limit")
	}

	more := state.Offset < len(snapshot.items)
	stop := !more || snapshotChanged || state.PageCount >= client.config.MaxPages || state.ObservedCount >= int64(client.config.MaxItems)
	if state.PageCount >= client.config.MaxPages && more {
		addReason(&result.Coverage.ReasonCodes, "pagination_limit")
	}
	if state.ObservedCount >= int64(client.config.MaxItems) && more {
		addReason(&result.Coverage.ReasonCodes, "items_limit")
	}
	if len(result.Coverage.ReasonCodes) > 0 {
		result.Coverage.Completeness = domain.CompletenessPartial
		state.PriorPartial = true
	}
	if more && !stop {
		state.SnapshotDigest = snapshot.digest
		result.NextCursor, err = client.encodeCursorChecked(state)
		if err != nil {
			result.NextCursor = ""
			stop = true
			addReason(&result.Coverage.ReasonCodes, "pagination_cursor_limit")
			result.Coverage.Completeness = domain.CompletenessPartial
		}
	}
	if !stop && result.NextCursor != "" {
		result.Coverage.Completeness = domain.CompletenessPartial
		addReason(&result.Coverage.ReasonCodes, "pagination_continues")
	} else {
		completed := time.Now().UTC()
		result.Coverage.CompletedAt = &completed
		result.Coverage.ObservedAt = completed
		if len(result.Coverage.ReasonCodes) > 0 {
			result.Coverage.Completeness = domain.CompletenessPartial
		}
	}
	return result, nil
}

type inventorySnapshot struct {
	items   []DownloadObservation
	digest  string
	reasons []string
}

func (client *Client) collect(ctx context.Context, connectionID domain.ConfigID) (inventorySnapshot, error) {
	var snapshot inventorySnapshot
	queueBody, queueErr := client.rpcCall(ctx, "listgroups", []any{0}, client.config.MaxResponseBytes)
	if queueErr != nil {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		if code, ok := upstreamCode(queueErr); ok && code == domain.OutcomeUnauthorized {
			return snapshot, queueErr
		}
		addReason(&snapshot.reasons, "queue_unavailable")
	}
	historyBody, historyErr := client.rpcCall(ctx, "history", []any{false}, client.config.MaxResponseBytes)
	if historyErr != nil {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		if code, ok := upstreamCode(historyErr); ok && code == domain.OutcomeUnauthorized {
			return snapshot, historyErr
		}
		addReason(&snapshot.reasons, "history_unavailable")
	}
	if queueErr != nil && historyErr != nil {
		return snapshot, queueErr
	}
	var queues []queueRecord
	if queueErr == nil {
		if err := decodeJSON(queueBody, &queues); err != nil {
			addReason(&snapshot.reasons, "queue_malformed")
			queues = nil
		} else if len(queues) > client.config.MaxItems {
			queues = queues[:client.config.MaxItems]
			addReason(&snapshot.reasons, "queue_limit")
		}
	}
	var histories []historyRecord
	if historyErr == nil {
		if err := decodeJSON(historyBody, &histories); err != nil {
			addReason(&snapshot.reasons, "history_malformed")
			histories = nil
		} else if len(histories) > client.config.MaxItems {
			histories = histories[:client.config.MaxItems]
			addReason(&snapshot.reasons, "history_limit")
		}
	}
	queueRaw, historyRaw := queueBody, historyBody
	hash := sha256.New()
	_, _ = hash.Write(queueRaw)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(historyRaw)
	snapshot.digest = hex.EncodeToString(hash.Sum(nil))

	seen := make(map[string]int, len(queues)+len(histories))
	queueSeen := make(map[string]struct{}, len(queues))
	for index := range queues {
		if err := ctx.Err(); err != nil {
			return inventorySnapshot{}, err
		}
		observation, reasons, observeErr := client.observeQueue(ctx, connectionID, queues[index])
		if observeErr != nil {
			return inventorySnapshot{}, observeErr
		}
		itemIndex := appendObservation(&snapshot, seen, observation)
		for _, reason := range reasons {
			addReason(&snapshot.reasons, fmt.Sprintf("item_%d_%s", itemIndex, reason))
		}
		if key := observation.NZBIDKey(); key != "" {
			if _, exists := queueSeen[key]; exists {
				addReason(&snapshot.reasons, "queue_duplicate_id")
			}
			queueSeen[key] = struct{}{}
		}
	}
	historySeen := make(map[string]struct{}, len(histories))
	for index := range histories {
		if err := ctx.Err(); err != nil {
			return inventorySnapshot{}, err
		}
		observation, reasons, observeErr := client.observeHistory(ctx, connectionID, histories[index])
		if observeErr != nil {
			return inventorySnapshot{}, observeErr
		}
		if key := observation.NZBIDKey(); key != "" {
			if previous, exists := seen[key]; exists && observationConflicts(snapshot.items[previous], observation) {
				addReason(&snapshot.reasons, "queue_history_conflict")
			}
		}
		itemIndex := appendObservation(&snapshot, seen, observation)
		for _, reason := range reasons {
			addReason(&snapshot.reasons, fmt.Sprintf("item_%d_%s", itemIndex, reason))
		}
		if key := observation.NZBIDKey(); key != "" {
			if _, exists := historySeen[key]; exists {
				addReason(&snapshot.reasons, "history_duplicate_id")
			}
			historySeen[key] = struct{}{}
		}
	}
	if len(snapshot.items) > client.config.MaxItems {
		snapshot.items = snapshot.items[:client.config.MaxItems]
		addReason(&snapshot.reasons, "items_limit")
	}
	client.reconcilePathReasons(&snapshot)
	return snapshot, nil
}

func appendObservation(snapshot *inventorySnapshot, seen map[string]int, observation DownloadObservation) int {
	key := observation.NZBIDKey()
	if key == "" {
		snapshot.items = append(snapshot.items, observation)
		return len(snapshot.items) - 1
	}
	if previous, ok := seen[key]; ok {
		mergeObservation(&snapshot.items[previous], observation)
		return previous
	}
	seen[key] = len(snapshot.items)
	snapshot.items = append(snapshot.items, observation)
	return len(snapshot.items) - 1
}

func mergeObservation(existing *DownloadObservation, incoming DownloadObservation) {
	if existing.History == nil && incoming.History != nil {
		existing.History = incoming.History
	}
	if existing.NZBFilename == "" {
		existing.NZBFilename = incoming.NZBFilename
	}
	if existing.NZBName == "" {
		existing.NZBName = incoming.NZBName
	}
	if existing.Kind == "" {
		existing.Kind = incoming.Kind
	}
	if existing.DestDir == "" {
		existing.DestDir = incoming.DestDir
	}
	finalMissing := existing.FinalDir == ""
	if finalMissing && incoming.FinalDir != "" {
		existing.FinalDir = incoming.FinalDir
		existing.ContentPath = incoming.ContentPath
		existing.MappedPath = cloneFileTarget(incoming.MappedPath)
	} else if existing.ContentPath == "" && incoming.ContentPath != "" {
		existing.ContentPath = incoming.ContentPath
		existing.MappedPath = cloneFileTarget(incoming.MappedPath)
	}
	mergeDroneEvidence(existing, &incoming)
	if existing.Item.Name == "" {
		existing.Item.Name = incoming.Item.Name
	}
	if existing.Item.Category == "" {
		existing.Item.Category = incoming.Item.Category
	}
	if existing.Item.CompletedAt == nil && incoming.Item.CompletedAt != nil {
		when := *incoming.Item.CompletedAt
		existing.Item.CompletedAt = &when
	}
	if existing.Item.Descriptor == nil || (!existing.Item.Descriptor.Available && incoming.Item.Descriptor != nil && incoming.Item.Descriptor.Available) {
		existing.Item.Descriptor = incoming.Item.Descriptor
	}
}

func mergeDroneEvidence(existing, incoming *DownloadObservation) {
	if !existing.droneParameterPresent && !incoming.droneParameterPresent {
		return
	}
	if existing.droneParameterPresent && incoming.droneParameterPresent {
		if !existing.droneParameterUsable || !incoming.droneParameterUsable || existing.Drone != incoming.Drone {
			existing.Drone = ""
			existing.ArrDownloadID = ""
			existing.droneParameterPresent = true
			existing.droneParameterUsable = false
			return
		}
		return
	}
	if !existing.droneParameterPresent {
		existing.droneParameterPresent = incoming.droneParameterPresent
		existing.droneParameterUsable = incoming.droneParameterUsable
		existing.Drone = incoming.Drone
		existing.ArrDownloadID = incoming.ArrDownloadID
	}
}

func cloneFileTarget(target *domain.FileTarget) *domain.FileTarget {
	if target == nil {
		return nil
	}
	copy := *target
	return &copy
}

func (client *Client) reconcilePathReasons(snapshot *inventorySnapshot) {
	filtered := snapshot.reasons[:0]
	for _, reason := range snapshot.reasons {
		if isItemPathReason(reason) {
			continue
		}
		filtered = append(filtered, reason)
	}
	snapshot.reasons = filtered
	for index := range snapshot.items {
		for _, reason := range client.mapObservationPath(&snapshot.items[index]) {
			addReason(&snapshot.reasons, fmt.Sprintf("item_%d_%s", index, reason))
		}
	}
}

func isItemPathReason(reason string) bool {
	if !strings.HasPrefix(reason, "item_") {
		return false
	}
	for _, suffix := range []string{
		"_final_path_unknown",
		"_final_path_unsafe",
		"_final_path_mapping_ambiguous",
		"_final_path_unmapped",
	} {
		if strings.HasSuffix(reason, suffix) {
			return true
		}
	}
	return false
}

func observationConflicts(existing, incoming DownloadObservation) bool {
	for _, values := range [][2]string{
		{existing.NZBFilename, incoming.NZBFilename},
		{existing.NZBName, incoming.NZBName},
		{existing.DestDir, incoming.DestDir},
		{existing.FinalDir, incoming.FinalDir},
		{existing.Drone, incoming.Drone},
	} {
		if values[0] != "" && values[1] != "" && values[0] != values[1] {
			return true
		}
	}
	return existing.Kind != "" && incoming.Kind != "" && existing.Kind != incoming.Kind
}

func (observation DownloadObservation) NZBIDKey() string {
	if observation.NZBID <= 0 {
		return ""
	}
	return strconv.FormatInt(observation.NZBID, 10)
}

func (client *Client) observeQueue(ctx context.Context, connectionID domain.ConfigID, record queueRecord) (DownloadObservation, []string, error) {
	name := firstNonEmpty(record.NZBName, record.NZBNicename, baseName(record.NZBFilename))
	effectiveID := effectiveNZBID(record.NZBID, record.DeprecatedID)
	id := nzbIDString(effectiveID)
	state, done, known := queueState(record.Status)
	if record.NZBID > 0 && record.DeprecatedID > 0 && record.NZBID != record.DeprecatedID {
		// NZBGet documents ID as an alias of NZBID. A disagreement is
		// contradictory upstream evidence, so it cannot prove completion or
		// provide a trustworthy lifecycle state.
		state, done, known = "unknown", false, false
	}
	if invalidIdentityValue(record.NZBID, record.DeprecatedID) {
		state, done, known = "unknown", false, false
	}
	progress, progressKnown := queueProgress(record)
	droneStatus := parameterValueStatus(record.Parameters, "drone")
	drone := usableParameterValue(droneStatus)
	arrDownloadID := id
	if droneStatus.Present {
		arrDownloadID = drone
	}
	observation := DownloadObservation{
		NZBID: effectiveID, HistoryID: record.DeprecatedID, Kind: record.Kind,
		NZBFilename: record.NZBFilename, NZBName: name, DestDir: record.DestDir,
		FinalDir: record.FinalDir, ContentPath: firstNonEmpty(record.FinalDir, record.DestDir),
		Drone: drone, ArrDownloadID: arrDownloadID,
		Queue: queueObservation(record), droneParameterPresent: droneStatus.Present, droneParameterUsable: droneStatus.Usable,
	}
	observation.ScopedIdentity = client.ScopedIdentity(id)
	observation.Item = ports.DownloadItem{ExternalID: id, Name: name, Protocol: "nzbget", State: state, Progress: progress, ProcessingDone: done, Category: record.Category}
	var reasons []string
	if effectiveID <= 0 {
		reasons = append(reasons, "identity_unknown")
	}
	if name == "" {
		reasons = append(reasons, "name_unknown")
	}
	if record.Kind == "" {
		reasons = append(reasons, "kind_unknown")
	}
	if record.NZBID > 0 && record.DeprecatedID > 0 && record.DeprecatedID != record.NZBID {
		reasons = append(reasons, "history_id_alias_mismatch")
	}
	if invalidIdentityValue(record.NZBID, record.DeprecatedID) {
		reasons = append(reasons, "identity_invalid")
	}
	if record.Status == "" || !known {
		reasons = append(reasons, "state_unknown")
	} else if !done && strings.HasPrefix(record.Status, "PP_") {
		reasons = append(reasons, "processing_incomplete")
	}
	if !progressKnown {
		reasons = append(reasons, "progress_unknown")
	}
	if observation.ArrDownloadID == "" {
		reasons = append(reasons, "arr_download_id_unknown")
	}
	if parametersMalformed(record.Parameters) || (droneStatus.Present && !droneStatus.Usable) {
		reasons = append(reasons, "parameters_malformed")
	}
	reasons = append(reasons, client.mapObservationPath(&observation)...)
	observation.Item.Descriptor, reasons = client.descriptor(ctx, effectiveID, record.NZBFilename, name, reasons)
	if err := ctx.Err(); err != nil {
		return DownloadObservation{}, nil, err
	}
	return observation, uniqueReasons(reasons), nil
}

func (client *Client) observeHistory(ctx context.Context, connectionID domain.ConfigID, record historyRecord) (DownloadObservation, []string, error) {
	name := firstNonEmpty(record.Name, record.NZBName, record.NZBNicename, baseName(record.NZBFilename))
	effectiveID := effectiveNZBID(record.NZBID, record.DeprecatedID)
	id := nzbIDString(effectiveID)
	state, done, known, statusReason := historyState(record)
	if record.NZBID > 0 && record.DeprecatedID > 0 && record.NZBID != record.DeprecatedID {
		// NZBGet documents ID as an alias of NZBID. A disagreement is
		// contradictory upstream evidence, so it cannot prove completion or
		// provide a trustworthy lifecycle state.
		state, done, known, statusReason = "unknown", false, false, "history_id_alias_mismatch"
	}
	if invalidIdentityValue(record.NZBID, record.DeprecatedID) {
		state, done, known, statusReason = "unknown", false, false, "identity_invalid"
	}
	progress := historyProgress(record, done)
	droneStatus := parameterValueStatus(record.Parameters, "drone")
	drone := usableParameterValue(droneStatus)
	arrDownloadID := id
	if droneStatus.Present {
		arrDownloadID = drone
	}
	observation := DownloadObservation{
		NZBID: effectiveID, HistoryID: record.DeprecatedID, Kind: record.Kind,
		NZBFilename: record.NZBFilename, NZBName: name, DestDir: record.DestDir,
		FinalDir: record.FinalDir, ContentPath: firstNonEmpty(record.FinalDir, record.DestDir),
		Drone: drone, ArrDownloadID: arrDownloadID,
		History: historyObservation(record), droneParameterPresent: droneStatus.Present, droneParameterUsable: droneStatus.Usable,
	}
	if record.HistoryTime > 0 {
		when, valid := timestampValue(record.HistoryTime)
		if valid {
			observation.History = historyObservation(record)
			observation.Item.CompletedAt = when
		}
	}
	observation.ScopedIdentity = client.ScopedIdentity(id)
	observation.Item = ports.DownloadItem{ExternalID: id, Name: name, Protocol: "nzbget", State: state, Progress: progress, ProcessingDone: done, Category: record.Category, CompletedAt: observation.Item.CompletedAt}
	var reasons []string
	if effectiveID <= 0 {
		reasons = append(reasons, "identity_unknown")
	}
	if name == "" {
		reasons = append(reasons, "name_unknown")
	}
	if record.NZBID > 0 && record.DeprecatedID > 0 && record.DeprecatedID != record.NZBID {
		reasons = append(reasons, "history_id_alias_mismatch")
	}
	if invalidIdentityValue(record.NZBID, record.DeprecatedID) {
		reasons = append(reasons, "identity_invalid")
	}
	if record.Kind == "" {
		reasons = append(reasons, "kind_unknown")
	}
	if !known {
		reasons = append(reasons, statusReason)
	} else if !done {
		reasons = append(reasons, statusReason)
	}
	if record.HistoryTime <= 0 {
		reasons = append(reasons, "history_time_unknown")
	}
	if observation.ArrDownloadID == "" {
		reasons = append(reasons, "arr_download_id_unknown")
	}
	if parametersMalformed(record.Parameters) || (droneStatus.Present && !droneStatus.Usable) {
		reasons = append(reasons, "parameters_malformed")
	}
	reasons = append(reasons, client.mapObservationPath(&observation)...)
	observation.Item.Descriptor, reasons = client.descriptor(ctx, effectiveID, record.NZBFilename, name, reasons)
	if err := ctx.Err(); err != nil {
		return DownloadObservation{}, nil, err
	}
	return observation, uniqueReasons(reasons), nil
}

func (client *Client) mapObservationPath(observation *DownloadObservation) []string {
	observation.MappedPath = nil
	if observation.ContentPath == "" {
		return []string{"final_path_unknown"}
	}
	if !absoluteRemotePath(observation.ContentPath) {
		return []string{"final_path_unsafe"}
	}
	target, ok, ambiguous := client.mapPath(observation.ContentPath)
	if ambiguous {
		return []string{"final_path_mapping_ambiguous"}
	}
	if !ok {
		return []string{"final_path_unmapped"}
	}
	observation.MappedPath = &target
	return nil
}

func queueObservation(record queueRecord) *QueueObservation {
	return &QueueObservation{
		NZBID: record.NZBID, DeprecatedID: record.DeprecatedID, Kind: record.Kind,
		NZBFilename: record.NZBFilename, NZBName: firstNonEmpty(record.NZBName, record.NZBNicename),
		DestDir: record.DestDir, FinalDir: record.FinalDir, Category: record.Category, Status: record.Status,
		FileSizeBytes: combinedSize(record.FileSizeHi, record.FileSizeLo), RemainingBytes: combinedSize(record.RemainingSizeHi, record.RemainingSizeLo),
		PausedBytes: combinedSize(record.PausedSizeHi, record.PausedSizeLo), FileCount: int64(record.FileCount), RemainingFiles: int64(record.RemainingFileCount),
		ActiveDownloads: int64(record.ActiveDownloads), TotalArticles: int64(record.TotalArticles), SuccessArticles: int64(record.SuccessArticles), FailedArticles: int64(record.FailedArticles),
		Health: int64(record.Health), CriticalHealth: int64(record.CriticalHealth), DownloadedBytes: combinedSize(record.DownloadedSizeHi, record.DownloadedSizeLo),
		Parameters: parameterObservations(record.Parameters), PostInfoText: redactSecretText(record.PostInfoText), PostStageProgress: int64(record.PostStageProgress),
	}
}

func historyObservation(record historyRecord) *HistoryObservation {
	var historyTime *time.Time
	if value, valid := timestampValue(record.HistoryTime); valid {
		historyTime = value
	}
	return &HistoryObservation{
		NZBID: record.NZBID, DeprecatedID: record.DeprecatedID, Kind: record.Kind, NZBFilename: record.NZBFilename,
		NZBName: firstNonEmpty(record.NZBName, record.NZBNicename), Name: record.Name, URL: sanitizeURL(record.URL), HistoryTime: historyTime,
		DestDir: record.DestDir, FinalDir: record.FinalDir, Category: record.Category,
		FileSizeBytes: combinedSize(record.FileSizeHi, record.FileSizeLo), DownloadedBytes: combinedSize(record.DownloadedSizeHi, record.DownloadedSizeLo),
		FileCount: int64(record.FileCount), RemainingFiles: int64(record.RemainingFileCount), Health: int64(record.Health), CriticalHealth: int64(record.CriticalHealth),
		Status: record.Status, ParStatus: record.ParStatus, UnpackStatus: record.UnpackStatus, ScriptStatus: record.ScriptStatus,
		MoveStatus: record.MoveStatus, DeleteStatus: record.DeleteStatus, MarkStatus: record.MarkStatus, URLStatus: record.URLStatus,
		Parameters: parameterObservations(record.Parameters),
	}
}

func parameterObservations(parameters []rpcParameter) []ParameterObservation {
	if len(parameters) == 0 {
		return nil
	}
	droneCount := 0
	for _, parameter := range parameters {
		if parameter.Name == "drone" {
			droneCount++
		}
	}
	result := make([]ParameterObservation, 0, len(parameters))
	for _, parameter := range parameters {
		value, ok := rawString(parameter.Value)
		if !ok {
			continue
		}
		result = append(result, ParameterObservation{Name: parameter.Name, Value: sanitizeParameterValue(parameter.Name, value, droneCount == 1)})
	}
	return result
}

type parameterValueObservation struct {
	Value   string
	Present bool
	Usable  bool
}

func parameterValueStatus(parameters []rpcParameter, name string) parameterValueObservation {
	var observation parameterValueObservation
	for _, parameter := range parameters {
		if parameter.Name != name {
			continue
		}
		if observation.Present {
			// Radarr's SingleOrDefault treats every second exact-name value as
			// ambiguous, including an equal duplicate.
			observation.Usable = false
			continue
		}
		observation.Present = true
		candidate, ok := rawString(parameter.Value)
		if !ok || candidate == "" {
			continue
		}
		candidate, ok = safeCorrelationValue(candidate)
		if !ok {
			continue
		}
		observation.Value = candidate
		observation.Usable = true
	}
	return observation
}

func usableParameterValue(observation parameterValueObservation) string {
	if !observation.Usable {
		return ""
	}
	return observation.Value
}

func parametersMalformed(parameters []rpcParameter) bool {
	for _, parameter := range parameters {
		if _, ok := rawString(parameter.Value); !ok {
			return true
		}
	}
	return false
}

func sanitizeParameterValue(name, value string, unique bool) string {
	if name == "drone" && unique {
		if safe, ok := safeCorrelationValue(value); ok {
			return safe
		}
	}
	return "[redacted]"
}

func containsSecretMarker(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"password", "passwd", "passphrase", "secret", "token", "apikey", "api_key",
		"authorization", "bearer ", "cookie",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func safeCorrelationValue(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	if len(value) > maxDroneLength || strings.TrimSpace(value) != value || containsSecretMarker(value) {
		return "", false
	}
	// Pinned Arr clients generate a compact, hyphenless GUID for drone. Keep a
	// bounded arr-* compatibility namespace for established integrations, but
	// reject arbitrary opaque/free-form values.
	if len(value) == 32 && isHexIdentifier(value) {
		return value, true
	}
	if strings.HasPrefix(value, "arr-") && safeIdentifier(value[4:]) {
		return value, true
	}
	return "", false
}

func isHexIdentifier(value string) bool {
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func safeIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("-_.", character)) {
			return false
		}
	}
	return true
}

func redactSecretText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "[redacted]"
}

func sanitizeURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[redacted]"
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Opaque = ""
	parsed.ForceQuery = false
	if containsSecretMarker(parsed.Host) {
		return "[redacted]"
	}
	return parsed.String()
}

func queueState(status string) (string, bool, bool) {
	switch status {
	case "QUEUED":
		return "queued", false, true
	case "PAUSED":
		return "paused", false, true
	case "DOWNLOADING", "FETCHING":
		return "downloading", false, true
	case "PP_QUEUED", "LOADING_PARS", "VERIFYING_SOURCES", "REPAIRING", "VERIFYING_REPAIRED", "RENAMING", "UNPACKING", "MOVING", "POST_UNPACK_RENAMING", "EXECUTING_SCRIPT":
		return "processing", false, true
	case "PP_FINISHED":
		return "completed", true, true
	default:
		return "unknown", false, false
	}
}

func historyState(record historyRecord) (string, bool, bool, string) {
	switch record.Status {
	case "SUCCESS/ALL", "SUCCESS/UNPACK", "SUCCESS/PAR", "SUCCESS/HEALTH", "SUCCESS/GOOD", "SUCCESS/MARK":
		if historyStatusesKnown(record) {
			return "completed", true, true, ""
		}
		return "completed", false, true, "processing_evidence_unknown"
	case "WARNING/SCRIPT", "WARNING/SPACE", "WARNING/PASSWORD", "WARNING/DAMAGED", "WARNING/REPAIRABLE", "WARNING/HEALTH":
		return "completed", false, true, "processing_warning"
	case "FAILURE/PAR", "FAILURE/UNPACK", "FAILURE/MOVE", "FAILURE/SCAN", "FAILURE/BAD", "FAILURE/HEALTH":
		return "failed", false, true, "processing_failed"
	case "DELETED/MANUAL", "DELETED/DUPE", "DELETED/COPY", "DELETED/GOOD":
		return "failed", false, true, "history_deleted"
	default:
		return "unknown", false, false, "state_unknown"
	}
}

func historyStatusesKnown(record historyRecord) bool {
	for _, value := range []struct {
		name  string
		value string
	}{
		{"par", record.ParStatus}, {"unpack", record.UnpackStatus}, {"script", record.ScriptStatus}, {"move", record.MoveStatus}, {"delete", record.DeleteStatus},
	} {
		if value.value == "" || value.value == "NONE" || value.value == "SUCCESS" || value.value == "MANUAL" {
			continue
		}
		switch value.name {
		case "par":
			if value.value != "REPAIR_POSSIBLE" {
				return false
			}
		case "unpack":
			return false
		case "script":
			return false
		case "move":
			if value.value != "SUCCESS" {
				return false
			}
		case "delete":
			return false
		}
	}
	return true
}

func queueProgress(record queueRecord) (float64, bool) {
	total := combinedSize(record.FileSizeHi, record.FileSizeLo)
	remaining := combinedSize(record.RemainingSizeHi, record.RemainingSizeLo)
	if total <= 0 || remaining < 0 || remaining > total {
		if record.Status == "PP_FINISHED" {
			return 1, true
		}
		return 0, false
	}
	progress := 1 - float64(remaining)/float64(total)
	if progress < 0 || progress > 1 || math.IsNaN(progress) || math.IsInf(progress, 0) {
		return 0, false
	}
	return progress, true
}

func historyProgress(record historyRecord, done bool) float64 {
	if done {
		return 1
	}
	total := combinedSize(record.FileSizeHi, record.FileSizeLo)
	downloaded := combinedSize(record.DownloadedSizeHi, record.DownloadedSizeLo)
	if total <= 0 || downloaded < 0 || downloaded > total {
		return 0
	}
	return float64(downloaded) / float64(total)
}

func combinedSize(hi, lo int64) int64 {
	if hi < 0 || lo < 0 || hi > math.MaxInt32 || lo > math.MaxUint32 {
		return -1
	}
	if hi > math.MaxInt64>>32 {
		return -1
	}
	value := (hi << 32) + lo
	if value < 0 {
		return -1
	}
	return value
}

func (client *Client) descriptor(ctx context.Context, nzbID int64, filename, name string, reasons []string) (*ports.DescriptorObservation, []string) {
	descriptor := &ports.DescriptorObservation{Kind: "nzb", Available: false, Unavailable: "not_requested"}
	if client.config.DescriptorMode != DescriptorBestEffort {
		return descriptor, reasons
	}
	if client.config.DescriptorDirectory == "" {
		descriptor.Unavailable = "not_configured"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	candidates := descriptorCandidates(nzbID, filename, name)
	if len(candidates) == 0 {
		descriptor.Unavailable = "identity_unavailable"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	var body []byte
	var found bool
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return descriptor, append(reasons, "descriptor_unavailable")
		}
		pathValue := filepath.Join(client.config.DescriptorDirectory, candidate)
		info, err := os.Lstat(pathValue)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			descriptor.Unavailable = "read_failed"
			return descriptor, append(reasons, "descriptor_unavailable")
		}
		if !info.Mode().IsRegular() {
			descriptor.Unavailable = "unsupported_file"
			return descriptor, append(reasons, "descriptor_unavailable")
		}
		body, err = readFileBounded(pathValue, client.config.MaxDescriptorSize)
		if err != nil {
			descriptor.Unavailable = "read_failed"
			return descriptor, append(reasons, "descriptor_unavailable")
		}
		found = true
		break
	}
	if !found {
		descriptor.Unavailable = "not_found"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	if !validNZBDescriptor(body) {
		descriptor.Unavailable = "malformed"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	identifier, err := domain.NewRuntimeID()
	if err != nil {
		descriptor.Unavailable = "descriptor_identity_unavailable"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	digest := sha256.Sum256(body)
	captured := time.Now().UTC()
	*descriptor = ports.DescriptorObservation{ID: identifier, Kind: "nzb", Available: true, Size: int64(len(body)), Digest: "sha256:" + hex.EncodeToString(digest[:]), CapturedAt: &captured, Source: "nzbget.retained_file"}
	return descriptor, reasons
}

func descriptorCandidates(nzbID int64, filename, name string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, 3)
	add := func(value string) {
		value = filepath.Base(strings.TrimSpace(value))
		if value == "" || value == "." || value == string(filepath.Separator)+"" || value == ".." || strings.Contains(value, string(filepath.Separator)) {
			return
		}
		if filepath.Ext(value) == "" {
			value += ".nzb"
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	add(filename)
	add(name)
	if nzbID > 0 {
		add(strconv.FormatInt(nzbID, 10) + ".nzb")
	}
	return result
}

func validNZBDescriptor(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(bytes.TrimSpace(data)))
	rootSeen := false
	rootClosed := false
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return rootSeen && rootClosed && depth == 0
		}
		if err != nil {
			return false
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if rootClosed {
					return false
				}
				if value.Name.Local != "nzb" {
					return false
				}
				rootSeen = true
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return false
			}
			depth--
			if depth == 0 {
				rootClosed = true
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return false
			}
		}
	}
}

func readFileBounded(name string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, errors.New("descriptor bound is invalid")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, errors.New("descriptor could not be read")
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("descriptor exceeds configured bound")
	}
	return data, nil
}

func (client *Client) Version(ctx context.Context, connectionID domain.ConfigID) (VersionObservation, error) {
	result := VersionObservation{ConnectionID: connectionID, ObservedAt: time.Now().UTC()}
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	body, err := client.rpcCall(ctx, "version", []any{}, 16<<10)
	if err != nil {
		return result, err
	}
	var value string
	if err := decodeJSON(body, &value); err != nil || !validVersion(value) {
		return result, upstreamMalformed("nzbget.version")
	}
	result.Version = strings.TrimSpace(value)
	return result, nil
}

func (client *Client) Capabilities(ctx context.Context, connectionID domain.ConfigID) ([]domain.Capability, error) {
	version, versionErr := client.Version(ctx, connectionID)
	if versionErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, versionErr
	}
	now := time.Now().UTC()
	capabilities := []domain.Capability{
		{Name: "nzbget.queue", State: domain.CapabilitySupported, Version: version.Version, ObservedAt: now},
		{Name: "nzbget.post_processing", State: domain.CapabilitySupported, Version: version.Version, ObservedAt: now},
		{Name: "nzbget.history", State: domain.CapabilitySupported, Version: version.Version, ObservedAt: now},
	}
	descriptor := domain.Capability{Name: "nzbget.descriptor", Version: version.Version, ObservedAt: now}
	if client.config.DescriptorMode == DescriptorBestEffort && client.config.DescriptorDirectory != "" {
		descriptor.State = domain.CapabilitySupported
	} else {
		descriptor.State = domain.CapabilityUnknown
		descriptor.Reason = "retained descriptor directory is not configured"
	}
	capabilities = append(capabilities, descriptor)
	return capabilities, nil
}

type queueRecord struct {
	NZBID              int64          `json:"NZBID"`
	DeprecatedID       int64          `json:"ID"`
	NZBNicename        string         `json:"NZBNicename"`
	NZBFilename        string         `json:"NZBFilename"`
	NZBName            string         `json:"NZBName"`
	Kind               string         `json:"Kind"`
	DestDir            string         `json:"DestDir"`
	FinalDir           string         `json:"FinalDir"`
	Category           string         `json:"Category"`
	FileSizeLo         int64          `json:"FileSizeLo"`
	FileSizeHi         int64          `json:"FileSizeHi"`
	RemainingSizeLo    int64          `json:"RemainingSizeLo"`
	RemainingSizeHi    int64          `json:"RemainingSizeHi"`
	PausedSizeLo       int64          `json:"PausedSizeLo"`
	PausedSizeHi       int64          `json:"PausedSizeHi"`
	FileCount          int            `json:"FileCount"`
	RemainingFileCount int            `json:"RemainingFileCount"`
	ActiveDownloads    int            `json:"ActiveDownloads"`
	TotalArticles      int            `json:"TotalArticles"`
	SuccessArticles    int            `json:"SuccessArticles"`
	FailedArticles     int            `json:"FailedArticles"`
	Health             int            `json:"Health"`
	CriticalHealth     int            `json:"CriticalHealth"`
	DownloadedSizeLo   int64          `json:"DownloadedSizeLo"`
	DownloadedSizeHi   int64          `json:"DownloadedSizeHi"`
	Status             string         `json:"Status"`
	Parameters         []rpcParameter `json:"Parameters"`
	PostInfoText       string         `json:"PostInfoText"`
	PostStageProgress  int            `json:"PostStageProgress"`
}

type historyRecord struct {
	NZBID              int64          `json:"NZBID"`
	DeprecatedID       int64          `json:"ID"`
	Kind               string         `json:"Kind"`
	NZBFilename        string         `json:"NZBFilename"`
	NZBName            string         `json:"NZBName"`
	NZBNicename        string         `json:"NZBNicename"`
	Name               string         `json:"Name"`
	URL                string         `json:"URL"`
	HistoryTime        int64          `json:"HistoryTime"`
	DestDir            string         `json:"DestDir"`
	FinalDir           string         `json:"FinalDir"`
	Category           string         `json:"Category"`
	FileSizeLo         int64          `json:"FileSizeLo"`
	FileSizeHi         int64          `json:"FileSizeHi"`
	DownloadedSizeLo   int64          `json:"DownloadedSizeLo"`
	DownloadedSizeHi   int64          `json:"DownloadedSizeHi"`
	FileCount          int            `json:"FileCount"`
	RemainingFileCount int            `json:"RemainingFileCount"`
	Health             int            `json:"Health"`
	CriticalHealth     int            `json:"CriticalHealth"`
	Status             string         `json:"Status"`
	ParStatus          string         `json:"ParStatus"`
	UnpackStatus       string         `json:"UnpackStatus"`
	ScriptStatus       string         `json:"ScriptStatus"`
	MoveStatus         string         `json:"MoveStatus"`
	DeleteStatus       string         `json:"DeleteStatus"`
	MarkStatus         string         `json:"MarkStatus"`
	URLStatus          string         `json:"UrlStatus"`
	Parameters         []rpcParameter `json:"Parameters"`
}

type rpcParameter struct {
	Name  string          `json:"Name"`
	Value json.RawMessage `json:"Value"`
}

type rpcRequest struct {
	Version string `json:"version"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      uint64 `json:"id"`
}

type rpcResponse struct {
	Version string          `json:"version"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (client *Client) rpcCall(ctx context.Context, method string, params []any, maxBytes int64) ([]byte, error) {
	if len(method) == 0 || len(method) > maxRPCMethodLength || strings.IndexFunc(method, unicode.IsControl) >= 0 {
		return nil, invalidInput("nzbget.rpc.method")
	}
	requestID := client.requestID.Add(1)
	requestBody, err := json.Marshal(rpcRequest{Version: "1.1", Method: method, Params: params, ID: requestID})
	if err != nil {
		return nil, errors.New("NZBGet request could not be encoded")
	}
	requestURL := *client.endpoint
	requestURL.Path = client.endpoint.Path
	if strings.Trim(requestURL.Path, "/") == "" {
		requestURL.Path = rpcPath
	}
	requestURL.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), bytes.NewReader(requestBody))
	if err != nil {
		return nil, errors.New("NZBGet request could not be created")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "managerr-nzbget-inventory/0.0.1")
	if client.config.Username != "" || client.config.Password != "" {
		request.SetBasicAuth(client.config.Username, client.config.Password)
	}
	response, err := client.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, domain.UpstreamError{Code: domain.OutcomeUnavailable, Retryable: true, Operation: "nzbget." + method, Detail: "upstream request timed out"}
		}
		return nil, domain.UpstreamError{Code: domain.OutcomeUnavailable, Retryable: true, Operation: "nzbget." + method, Detail: "upstream is unavailable"}
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, maxBytes)
	if err != nil {
		return nil, domain.UpstreamError{Code: domain.OutcomeUnknown, Status: response.StatusCode, Operation: "nzbget." + method, Detail: err.Error()}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, normalizeStatus("nzbget."+method, response.StatusCode)
	}
	var envelope rpcResponse
	if err := decodeJSON(body, &envelope); err != nil {
		return nil, upstreamMalformed("nzbget." + method)
	}
	if envelope.Version != "1.1" || !rpcResponseIDMatches(envelope.ID, requestID) {
		return nil, upstreamMalformed("nzbget." + method)
	}
	if envelope.Error != nil {
		return nil, normalizeRPCError(method, envelope.Error)
	}
	if len(envelope.Result) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Result), []byte("null")) {
		return nil, upstreamMalformed("nzbget." + method)
	}
	return envelope.Result, nil
}

func rpcResponseIDMatches(value json.RawMessage, expected uint64) bool {
	var actual uint64
	if len(value) == 0 || json.Unmarshal(value, &actual) != nil {
		return false
	}
	return actual == expected
}

func normalizeRPCError(method string, rpcErr *rpcError) error {
	code := domain.OutcomeUnknown
	switch rpcErr.Code {
	case -32600, -32602:
		code = domain.OutcomeInvalidInput
	case -32601:
		code = domain.OutcomeUnsupported
	}
	return domain.UpstreamError{Code: code, Operation: "nzbget." + method, UpstreamID: strconv.Itoa(rpcErr.Code), Detail: "JSON-RPC method failed"}
}

func (client *Client) encodeCursorChecked(state inventoryCursor) (string, error) {
	if len(client.cursorKey) == 0 || !state.SourceID.Valid() || state.StartedAt.IsZero() || state.PageSize <= 0 || state.PageSize > client.config.MaxPageSize || state.PageCount <= 0 || state.PageCount >= client.config.MaxPages || state.Offset <= 0 || state.Offset > client.config.MaxItems || state.ObservedCount < 0 || state.ObservedCount > int64(client.config.MaxItems) || len(state.SnapshotDigest) != sha256.Size*2 || normalizeHex(state.SnapshotDigest) != state.SnapshotDigest {
		return "", cursorEncodingError()
	}
	state.Version = 1
	state.ConnectionID = client.config.ConnectionID.String()
	payload, err := json.Marshal(state)
	if err != nil || len(payload) > maxCursorBytes {
		return "", cursorEncodingError()
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(payload)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	encodedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(encodedPayload)+1+len(encodedSignature) > maxEncodedCursorBytes {
		return "", cursorEncodingError()
	}
	return encodedPayload + "." + encodedSignature, nil
}

func (client *Client) decodeCursor(value string) (inventoryCursor, error) {
	if value == "" {
		return inventoryCursor{}, nil
	}
	if len(value) > maxEncodedCursorBytes || strings.Count(value, ".") != 1 {
		return inventoryCursor{}, invalidInput("nzbget.inventory.cursor")
	}
	payloadValue, signatureValue, ok := strings.Cut(value, ".")
	if !ok || payloadValue == "" || signatureValue == "" || len(client.cursorKey) == 0 {
		return inventoryCursor{}, invalidInput("nzbget.inventory.cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadValue)
	if err != nil || len(payload) == 0 || len(payload) > maxCursorBytes || base64.RawURLEncoding.EncodeToString(payload) != payloadValue {
		return inventoryCursor{}, invalidInput("nzbget.inventory.cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureValue)
	if err != nil || len(signature) != sha256.Size || base64.RawURLEncoding.EncodeToString(signature) != signatureValue {
		return inventoryCursor{}, invalidInput("nzbget.inventory.cursor")
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return inventoryCursor{}, invalidInput("nzbget.inventory.cursor")
	}
	var state inventoryCursor
	if err := json.Unmarshal(payload, &state); err != nil || state.Version != 1 || state.ConnectionID != client.config.ConnectionID.String() || !state.SourceID.Valid() || state.StartedAt.IsZero() || state.Offset <= 0 || state.PageCount <= 0 || state.PageCount >= client.config.MaxPages || state.PageSize <= 0 || state.PageSize > client.config.MaxPageSize || state.ObservedCount < 0 || state.ObservedCount > int64(client.config.MaxItems) || len(state.SnapshotDigest) != sha256.Size*2 || normalizeHex(state.SnapshotDigest) != state.SnapshotDigest {
		return inventoryCursor{}, invalidInput("nzbget.inventory.cursor")
	}
	return state, nil
}

type inventoryCursor struct {
	Version        int              `json:"v"`
	ConnectionID   string           `json:"connectionId"`
	SourceID       domain.RuntimeID `json:"sourceId"`
	StartedAt      time.Time        `json:"startedAt"`
	Offset         int              `json:"offset"`
	PageCount      int              `json:"pageCount"`
	PageSize       int              `json:"pageSize"`
	ObservedCount  int64            `json:"observedCount"`
	SnapshotDigest string           `json:"snapshotDigest"`
	PriorPartial   bool             `json:"priorPartial,omitempty"`
}

func cursorEncodingError() error {
	return domain.UpstreamError{Code: domain.OutcomeUnknown, Operation: "nzbget.inventory.cursor", Detail: "continuation cursor exceeds its configured bound"}
}

func (client *Client) pageLimit(requested int) (int, error) {
	if requested <= 0 {
		requested = client.config.MaxPageSize
	}
	if requested > client.config.MaxPageSize {
		return 0, invalidInput("nzbget.inventory.limit")
	}
	return requested, nil
}

func parseEndpoint(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("NZBGet endpoint must be an absolute URL without credentials or query")
	}
	return parsed, nil
}

func validateMappings(connectionID domain.ConfigID, mappings []domain.PathMapping) error {
	for _, mapping := range mappings {
		if mapping.ConnectionID != connectionID {
			continue
		}
		rawPrefix := strings.TrimSpace(mapping.SourcePrefix)
		if rawPrefix == "" || !absoluteRemotePath(rawPrefix) {
			return errors.New("NZBGet path mapping is invalid")
		}
		prefix := normalizeMappingPrefix(rawPrefix)
		if !mapping.RootID.Valid() || !absoluteRemotePath(prefix) {
			return errors.New("NZBGet path mapping is invalid")
		}
		if mapping.DestinationPrefix != "" {
			destination := strings.TrimSpace(mapping.DestinationPrefix)
			if strings.HasPrefix(destination, "/") || domain.ValidateRelativePath(strings.Trim(destination, "/")) != nil {
				return errors.New("NZBGet path mapping destination is invalid")
			}
		}
	}
	return nil
}

func (client *Client) mapPath(remote string) (domain.FileTarget, bool, bool) {
	if !absoluteRemotePath(remote) {
		return domain.FileTarget{}, false, false
	}
	bestLength := -1
	var selected domain.PathMapping
	ambiguous := false
	for _, mapping := range client.config.Mappings {
		if mapping.ConnectionID != client.config.ConnectionID {
			continue
		}
		prefix := normalizeMappingPrefix(mapping.SourcePrefix)
		if !pathBoundaryMatch(remote, prefix) {
			continue
		}
		if len(prefix) > bestLength {
			bestLength = len(prefix)
			selected = mapping
			ambiguous = false
			continue
		}
		if len(prefix) == bestLength && (selected.RootID != mapping.RootID || selected.DestinationPrefix != mapping.DestinationPrefix) {
			ambiguous = true
		}
	}
	if ambiguous || bestLength < 0 {
		return domain.FileTarget{}, false, ambiguous
	}
	prefix := normalizeMappingPrefix(selected.SourcePrefix)
	suffix := strings.TrimPrefix(remote, prefix)
	suffix = strings.TrimPrefix(suffix, "/")
	relative := strings.Trim(selected.DestinationPrefix, "/")
	if suffix != "" {
		if relative == "" {
			relative = suffix
		} else {
			relative = path.Join(relative, suffix)
		}
	}
	target := domain.FileTarget{RootID: selected.RootID, RelativePath: relative}
	if err := target.Validate(); err != nil {
		return domain.FileTarget{}, false, false
	}
	return target, true, false
}

func normalizeMappingPrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "/" {
		return "/"
	}
	return "/" + strings.Trim(value, "/")
}

func absoluteRemotePath(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "\x00") || strings.Contains(value, "//") {
		return false
	}
	for _, component := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if component == "." || component == ".." {
			return false
		}
	}
	return true
}

func pathBoundaryMatch(value, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(value, "/")
	}
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func baseName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.Base(value)
	value = strings.TrimSuffix(value, filepath.Ext(value))
	return value
}

func nzbIDString(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func effectiveNZBID(nzbID, deprecatedID int64) int64 {
	if nzbID > 0 {
		return nzbID
	}
	return deprecatedID
}

func invalidIdentityValue(nzbID, deprecatedID int64) bool {
	return nzbID < 0 || deprecatedID < 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func rawString(value json.RawMessage) (string, bool) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return "", false
	}
	var stringValue string
	if json.Unmarshal(value, &stringValue) == nil && len(value) > 0 && value[0] == '"' {
		return stringValue, true
	}
	return "", false
}

func timestampValue(seconds int64) (*time.Time, bool) {
	if seconds <= 0 {
		return nil, false
	}
	value := time.Unix(seconds, 0).UTC()
	if value.Year() < 1970 || value.Year() > 9999 {
		return nil, false
	}
	return &value, true
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func validVersion(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxVersionLength {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validateConnectionScope(expected, actual domain.ConfigID) error {
	if expected != actual {
		return invalidInput("nzbget.inventory.connection")
	}
	return nil
}

func decodeJSON(data []byte, target any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("empty JSON response")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON response data")
	}
	return nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, errors.New("response bound is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, errors.New("response could not be read")
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("response exceeds configured bound")
	}
	return data, nil
}

func normalizeStatus(operation string, status int) error {
	code := domain.OutcomeUnknown
	retryable := false
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = domain.OutcomeUnauthorized
	case status == http.StatusTooManyRequests:
		code, retryable = domain.OutcomeRateLimited, true
	case status == http.StatusBadRequest:
		code = domain.OutcomeInvalidInput
	case status == http.StatusConflict:
		code = domain.OutcomeConflict
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
		code = domain.OutcomeUnsupported
	case status == http.StatusRequestTimeout || status >= 500:
		code, retryable = domain.OutcomeUnavailable, true
	}
	return domain.UpstreamError{Code: code, Status: status, Retryable: retryable, Operation: operation, Detail: "upstream request failed"}
}

func upstreamMalformed(operation string) error {
	return domain.UpstreamError{Code: domain.OutcomeUnknown, Operation: operation, Detail: "upstream response was malformed"}
}

func invalidInput(operation string) error {
	return domain.UpstreamError{Code: domain.OutcomeInvalidInput, Operation: operation, Detail: "input is invalid"}
}

func upstreamCode(err error) (domain.UpstreamErrorCode, bool) {
	var upstream domain.UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Code, true
	}
	return "", false
}

func normalizeHex(value string) string {
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return ""
		}
	}
	return value
}

func addReason(reasons *[]string, reason string) {
	if reason == "" || len(*reasons) >= maxReasonCodes {
		return
	}
	for _, existing := range *reasons {
		if existing == reason {
			return
		}
	}
	*reasons = append(*reasons, reason)
}

func uniqueReasons(reasons []string) []string {
	result := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		addReason(&result, reason)
	}
	return result
}
