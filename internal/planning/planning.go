// Package planning builds immutable, exact action plans from read-only
// evidence. Plans contain semantic desired predicates, bounded manifests and
// the configuration/source revisions that made the preview meaningful. The
// package has no persistence or upstream dependencies; callers hand a plan to
// the API/execution layers after review.
package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

var (
	// ErrInvalidPlan means the plan cannot be safely interpreted or dispatched.
	ErrInvalidPlan = errors.New("invalid action plan")
	// ErrPlanConflict means the plan contains an explicit blocking conflict.
	ErrPlanConflict = errors.New("action plan has blocking conflicts")
	// ErrPlanExpired means an otherwise valid plan is outside its approval
	// window.
	ErrPlanExpired = errors.New("action plan is expired")
	// ErrPlanRevision means a decision referred to a different immutable
	// revision.
	ErrPlanRevision = errors.New("action plan revision mismatch")
	// ErrPlanDigest means a decision did not bind the exact planned intent.
	ErrPlanDigest = errors.New("action plan digest mismatch")
	// ErrPlanBindingChanged means the source or relevant configuration changed
	// after a preview was created.
	ErrPlanBindingChanged = errors.New("action plan binding changed")
	// ErrManifestLimit means an exact manifest exceeded a configured bound.
	ErrManifestLimit = errors.New("action plan manifest limit exceeded")
	// ErrAmbiguousMapping means an exact title/episode/subtitle choice is still
	// unresolved and cannot be approved.
	ErrAmbiguousMapping = errors.New("media mapping is ambiguous")
)

// Status is the lifecycle of a plan revision before it becomes an approved
// execution input.
type Status string

const (
	StatusPreparing Status = "preparing"
	StatusReady     Status = "ready"
	StatusInvalid   Status = "invalid"
	StatusExpired   Status = "expired"
)

func (status Status) valid() bool {
	return status == StatusPreparing || status == StatusReady || status == StatusInvalid || status == StatusExpired
}

// ApprovalKind identifies which explicit review gate is required. A
// registration approval never implicitly approves an import predicate.
type ApprovalKind string

const (
	ApprovalNone         ApprovalKind = "none"
	ApprovalRegistration ApprovalKind = "registration"
	ApprovalImport       ApprovalKind = "import"
	ApprovalAction       ApprovalKind = "action"
)

func (kind ApprovalKind) valid() bool {
	return kind == ApprovalNone || kind == ApprovalRegistration || kind == ApprovalImport || kind == ApprovalAction
}

// Binding identifies the evidence and effective configuration revisions used
// to create a plan. SourceID/SourceRevision are required even for actions
// without a filesystem manifest: registration plans still bind the selected
// discovery/provider identity and target configuration.
type Binding struct {
	SourceID            string
	SourceRevision      string
	SourceDigest        string
	ConnectionRevisions map[domain.ConfigID]string
	MappingRevisions    map[domain.ConfigID]string
	MappingScopes       []MappingScope
}

// MappingScope identifies which connection and storage root a path mapping
// fence protects. MappingRevisions carries the authority-bearing revision for
// MappingID; keeping the scope beside it prevents an unrelated mapping from
// satisfying an Arr import's path fence.
type MappingScope struct {
	MappingID    domain.ConfigID
	ConnectionID domain.ConfigID
	RootID       domain.ConfigID
}

// Validate enforces that a plan cannot float free of the input that produced
// it. Map values are copied and sorted by the normalization path before the
// digest is calculated.
func (binding Binding) Validate() error {
	if strings.TrimSpace(binding.SourceID) == "" || strings.TrimSpace(binding.SourceRevision) == "" {
		return fmt.Errorf("%w: source identity and revision are required", ErrInvalidPlan)
	}
	if binding.SourceDigest != "" && !strongDigest(binding.SourceDigest) {
		return fmt.Errorf("%w: source digest must be a SHA-256 digest", ErrInvalidPlan)
	}
	for id, revision := range binding.ConnectionRevisions {
		if !id.Valid() || strings.TrimSpace(revision) == "" {
			return fmt.Errorf("%w: invalid connection revision for %q", ErrInvalidPlan, id)
		}
	}
	for id, revision := range binding.MappingRevisions {
		if !id.Valid() || strings.TrimSpace(revision) == "" {
			return fmt.Errorf("%w: invalid mapping revision for %q", ErrInvalidPlan, id)
		}
	}
	seenMappings := make(map[domain.ConfigID]struct{}, len(binding.MappingScopes))
	for _, scope := range binding.MappingScopes {
		if !scope.MappingID.Valid() || !scope.ConnectionID.Valid() || !scope.RootID.Valid() {
			return fmt.Errorf("%w: mapping scope references are invalid", ErrInvalidPlan)
		}
		if _, exists := seenMappings[scope.MappingID]; exists {
			return fmt.Errorf("%w: mapping %q has duplicate scopes", ErrInvalidPlan, scope.MappingID)
		}
		seenMappings[scope.MappingID] = struct{}{}
		if strings.TrimSpace(binding.MappingRevisions[scope.MappingID]) == "" {
			return fmt.Errorf("%w: mapping scope %q has no revision fence", ErrInvalidPlan, scope.MappingID)
		}
	}
	return nil
}

// RevisionBinding is the deterministic wire/digest representation of one
// configuration revision. It avoids map iteration order becoming part of a
// plan digest.
type RevisionBinding struct {
	ID       domain.ConfigID
	Revision string
}

// Precondition is a named read-only check that must still hold at execution.
// Expected is semantic data (for example a digest or upstream version), never
// a host path or an arbitrary command.
type Precondition struct {
	Kind     string
	Target   string
	Expected string
	Required bool
}

func (precondition Precondition) Validate() error {
	if strings.TrimSpace(precondition.Kind) == "" || strings.TrimSpace(precondition.Target) == "" {
		return fmt.Errorf("%w: precondition kind and target are required", ErrInvalidPlan)
	}
	if strings.TrimSpace(precondition.Expected) == "" {
		return fmt.Errorf("%w: precondition expected value is required", ErrInvalidPlan)
	}
	return nil
}

// Impact identifies a related item that the UI should show alongside an
// action. It does not widen the exact manifest or grant another mutation.
type Impact struct {
	Kind    string
	Target  string
	Message string
}

func (impact Impact) Validate() error {
	if strings.TrimSpace(impact.Kind) == "" || strings.TrimSpace(impact.Target) == "" {
		return fmt.Errorf("%w: impact kind and target are required", ErrInvalidPlan)
	}
	return nil
}

// Conflict is explicit review evidence. A blocking conflict makes a plan
// invalid and prevents any execution layer from treating it as approved.
type Conflict struct {
	Code     string
	Field    string
	Target   string
	Message  string
	Evidence []string
	Blocking bool
}

func (conflict Conflict) Validate() error {
	if strings.TrimSpace(conflict.Code) == "" || strings.TrimSpace(conflict.Message) == "" {
		return fmt.Errorf("%w: conflict code and message are required", ErrInvalidPlan)
	}
	if strings.TrimSpace(conflict.Target) == "" {
		return fmt.Errorf("%w: conflict target is required", ErrInvalidPlan)
	}
	return nil
}

// PredicateKind identifies a semantic desired state. Predicates are typed
// below so callers cannot accidentally turn a display title, size or name
// into proof that a side effect happened.
type PredicateKind string

const (
	PredicateFileContent         PredicateKind = "file_content"
	PredicateFileIdentity        PredicateKind = "file_identity"
	PredicateHardlink            PredicateKind = "hardlink"
	PredicateRegistration        PredicateKind = "arr_registration"
	PredicateImport              PredicateKind = "arr_import"
	PredicateAvailability        PredicateKind = "availability"
	PredicateRequest             PredicateKind = "request"
	PredicateEpisodeAssociation  PredicateKind = "episode_association"
	PredicateSubtitleAssociation PredicateKind = "subtitle_association"
)

// FilePredicate is used for exact content, identity and hardlink predicates.
// Source is populated only for hardlink predicates; Target is always the
// configured-root-relative destination.
type FilePredicate struct {
	Source       domain.FileTarget `json:"source,omitempty"`
	Target       domain.FileTarget `json:"target"`
	Size         int64             `json:"size,omitempty"`
	Digest       string            `json:"digest,omitempty"`
	FileIdentity string            `json:"fileIdentity,omitempty"`
}

// RegistrationPredicate is a field-level Arr upsert/read-back predicate.
// Empty optional fields preserve existing upstream settings.
type RegistrationPredicate struct {
	ConnectionID domain.ConfigID
	ProviderID   string
	Kind         domain.MediaKind
	ExternalID   string
	Fields       ports.RegistrationFields
}

// ImportSelection is one exact video or subtitle source association. It
// carries explicit episode IDs for multi-episode files and explicit subtitle
// pair/video targets for IDX/SUB and companion review.
type ImportSelection struct {
	Source           domain.FileTarget
	MovieOrEpisodeID string
	EpisodeIDs       []string
	Subtitle         bool
	Language         string
	Forced           bool
	HearingImpaired  bool
	PairID           string
	VideoPaths       []domain.FileTarget
	Confidence       MappingConfidence
}

// ImportPredicate binds an exact file set to one selected Arr instance and
// registration. It never means “all files in this directory”.
type ImportPredicate struct {
	ConnectionID         domain.ConfigID
	RegisteredExternalID string
	Files                []ImportSelection
	Transfer             string
}

// ServicePredicate is used for independent Jellyfin availability and Seerr
// request dimensions. A request predicate is still evaluated independently;
// this package does not issue Seerr writes.
type ServicePredicate struct {
	ConnectionID domain.ConfigID
	ProviderID   string
	ExternalID   string
	Value        string
}

// EpisodePredicate preserves exact selected title/episode IDs while keeping
// anime absolute-number hints available for review and audit.
type EpisodePredicate struct {
	ConnectionID   domain.ConfigID
	Source         domain.FileTarget
	SeriesID       string
	EpisodeIDs     []string
	SeasonNumber   *int
	AbsoluteNumber *int
}

// SubtitlePredicate preserves language, forced/SDH labels and explicit pair
// membership. VideoPaths must be explicit for a subtitle association.
type SubtitlePredicate struct {
	ConnectionID    domain.ConfigID
	Source          domain.FileTarget
	VideoPaths      []domain.FileTarget
	PairID          string
	Language        string
	Forced          bool
	HearingImpaired bool
}

// Predicate is a closed typed one-of. Exactly one payload must be populated.
type Predicate struct {
	Kind         PredicateKind
	File         *FilePredicate
	Registration *RegistrationPredicate
	Import       *ImportPredicate
	Service      *ServicePredicate
	Episode      *EpisodePredicate
	Subtitle     *SubtitlePredicate
}

// NewFileContentPredicate creates the semantic destination-content predicate
// used by copy plans and idempotent read-back.
func NewFileContentPredicate(target domain.FileTarget, size int64, digest string) Predicate {
	return Predicate{Kind: PredicateFileContent, File: &FilePredicate{Target: target, Size: size, Digest: digest}}
}

// NewFileIdentityPredicate creates a predicate for one exact filesystem
// object. It is intentionally distinct from content equality.
func NewFileIdentityPredicate(target domain.FileTarget, identity string) Predicate {
	return Predicate{Kind: PredicateFileIdentity, File: &FilePredicate{Target: target, FileIdentity: identity}}
}

// NewHardlinkPredicate creates a no-copy desired state requiring source and
// destination to name the same file object.
func NewHardlinkPredicate(source, target domain.FileTarget, identity string) Predicate {
	return Predicate{Kind: PredicateHardlink, File: &FilePredicate{Source: source, Target: target, FileIdentity: identity}}
}

// NewRegistrationPredicate creates an exact field-level Arr registration
// predicate. Upstream external ID may be empty for a new registration.
func NewRegistrationPredicate(connectionID domain.ConfigID, providerID string, kind domain.MediaKind, externalID string, fields ports.RegistrationFields) Predicate {
	fields = cloneRegistrationFields(fields)
	return Predicate{Kind: PredicateRegistration, Registration: &RegistrationPredicate{ConnectionID: connectionID, ProviderID: providerID, Kind: kind, ExternalID: externalID, Fields: fields}}
}

// NewImportPredicate creates an exact Arr import predicate.
func NewImportPredicate(connectionID domain.ConfigID, registeredExternalID string, files []ImportSelection, transfer string) Predicate {
	return Predicate{Kind: PredicateImport, Import: &ImportPredicate{ConnectionID: connectionID, RegisteredExternalID: registeredExternalID, Files: files, Transfer: transfer}}
}

// NewAvailabilityPredicate creates an independent media-server availability
// predicate. Value is normally present or absent from the selected server.
func NewAvailabilityPredicate(connectionID domain.ConfigID, providerID, externalID, value string) Predicate {
	return Predicate{Kind: PredicateAvailability, Service: &ServicePredicate{ConnectionID: connectionID, ProviderID: providerID, ExternalID: externalID, Value: value}}
}

// NewRequestPredicate creates an independent Seerr request-state predicate.
func NewRequestPredicate(connectionID domain.ConfigID, providerID, externalID, value string) Predicate {
	return Predicate{Kind: PredicateRequest, Service: &ServicePredicate{ConnectionID: connectionID, ProviderID: providerID, ExternalID: externalID, Value: value}}
}

// NewEpisodePredicate creates a selected series/episode association. An
// ambiguous or unresolved mapping is rejected by Validate.
func NewEpisodePredicate(connectionID domain.ConfigID, source domain.FileTarget, seriesID string, episodeIDs []string, seasonNumber, absoluteNumber *int) Predicate {
	return Predicate{Kind: PredicateEpisodeAssociation, Episode: &EpisodePredicate{ConnectionID: connectionID, Source: source, SeriesID: seriesID, EpisodeIDs: episodeIDs, SeasonNumber: cloneIntPointer(seasonNumber), AbsoluteNumber: cloneIntPointer(absoluteNumber)}}
}

// NewSubtitlePredicate creates an explicit subtitle association, retaining
// labels needed for forced, SDH and language review.
func NewSubtitlePredicate(connectionID domain.ConfigID, source domain.FileTarget, videoPaths []domain.FileTarget, pairID, language string, forced, hearingImpaired bool) Predicate {
	return Predicate{Kind: PredicateSubtitleAssociation, Subtitle: &SubtitlePredicate{ConnectionID: connectionID, Source: source, VideoPaths: videoPaths, PairID: pairID, Language: language, Forced: forced, HearingImpaired: hearingImpaired}}
}

// MappingConfidence is intentionally categorical; a suggested parser result
// becomes safe only after the user supplies explicit IDs.
type MappingConfidence string

const (
	MappingExact      MappingConfidence = "exact"
	MappingSuggested  MappingConfidence = "suggested"
	MappingAmbiguous  MappingConfidence = "ambiguous"
	MappingUnresolved MappingConfidence = "unresolved"
)

func (confidence MappingConfidence) valid() bool {
	return confidence == MappingExact || confidence == MappingSuggested || confidence == MappingAmbiguous || confidence == MappingUnresolved
}

// DesiredState is a set of semantic predicates. The set is canonicalized for
// digesting, while each predicate remains individually visible to execution
// and the UI.
type DesiredState struct {
	Predicates []Predicate
}

// NewDesiredState validates and copies a predicate set.
func NewDesiredState(predicates ...Predicate) (DesiredState, error) {
	desired := DesiredState{Predicates: append([]Predicate(nil), predicates...)}
	if err := desired.Validate(); err != nil {
		return DesiredState{}, err
	}
	return normalizeDesired(desired)
}

// Validate checks one-of shape, exact targets, selected IDs and duplicate
// predicates. An empty desired state cannot describe a safe mutation.
func (desired DesiredState) Validate() error {
	if len(desired.Predicates) == 0 {
		return fmt.Errorf("%w: desired state is empty", ErrInvalidPlan)
	}
	seen := make(map[string]struct{}, len(desired.Predicates))
	for index, predicate := range desired.Predicates {
		if err := predicate.Validate(); err != nil {
			return fmt.Errorf("%w: predicate %d: %w", ErrInvalidPlan, index, err)
		}
		key, err := predicateKey(predicate)
		if err != nil {
			return err
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate desired predicate", ErrInvalidPlan)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// SemanticDigest returns a digest of the desired predicates without display
// text. It is useful for detecting changed episode/subtitle intent.
func (desired DesiredState) SemanticDigest() (string, error) {
	normalized, err := normalizeDesired(desired)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("%w: encode desired state: %v", ErrInvalidPlan, err)
	}
	return digestBytes(payload), nil
}

// Validate checks one typed desired predicate.
func (predicate Predicate) Validate() error {
	if strings.TrimSpace(string(predicate.Kind)) == "" {
		return fmt.Errorf("%w: predicate kind is required", ErrInvalidPlan)
	}
	count := 0
	if predicate.File != nil {
		count++
	}
	if predicate.Registration != nil {
		count++
	}
	if predicate.Import != nil {
		count++
	}
	if predicate.Service != nil {
		count++
	}
	if predicate.Episode != nil {
		count++
	}
	if predicate.Subtitle != nil {
		count++
	}
	if count != 1 {
		return fmt.Errorf("%w: predicate must contain exactly one payload", ErrInvalidPlan)
	}
	switch predicate.Kind {
	case PredicateFileContent:
		if predicate.File == nil || !isZeroFileTarget(predicate.File.Source) {
			return fmt.Errorf("%w: file content predicate has an unexpected source", ErrInvalidPlan)
		}
		if err := predicate.File.Target.Validate(); err != nil {
			return fmt.Errorf("%w: file target: %v", ErrInvalidPlan, err)
		}
		if predicate.File.Size < 0 || !strongDigest(predicate.File.Digest) {
			return fmt.Errorf("%w: file content requires size and strong digest", ErrInvalidPlan)
		}
	case PredicateFileIdentity:
		if predicate.File == nil || !isZeroFileTarget(predicate.File.Source) {
			return fmt.Errorf("%w: file identity predicate has an unexpected source", ErrInvalidPlan)
		}
		if err := predicate.File.Target.Validate(); err != nil {
			return fmt.Errorf("%w: file target: %v", ErrInvalidPlan, err)
		}
		if strings.TrimSpace(predicate.File.FileIdentity) == "" {
			return fmt.Errorf("%w: file identity is required", ErrInvalidPlan)
		}
	case PredicateHardlink:
		if predicate.File == nil || isZeroFileTarget(predicate.File.Source) {
			return fmt.Errorf("%w: hardlink source is required", ErrInvalidPlan)
		}
		if err := predicate.File.Source.Validate(); err != nil {
			return fmt.Errorf("%w: hardlink source: %v", ErrInvalidPlan, err)
		}
		if err := predicate.File.Target.Validate(); err != nil {
			return fmt.Errorf("%w: hardlink target: %v", ErrInvalidPlan, err)
		}
		if predicate.File.Source.RootID == predicate.File.Target.RootID && predicate.File.Source.RelativePath == predicate.File.Target.RelativePath {
			return fmt.Errorf("%w: hardlink source and target are identical", ErrInvalidPlan)
		}
		if strings.TrimSpace(predicate.File.FileIdentity) == "" {
			return fmt.Errorf("%w: hardlink file identity is required", ErrInvalidPlan)
		}
	case PredicateRegistration:
		if predicate.Registration == nil {
			return fmt.Errorf("%w: registration payload is required", ErrInvalidPlan)
		}
		if err := validateRegistration(*predicate.Registration); err != nil {
			return err
		}
	case PredicateImport:
		if predicate.Import == nil {
			return fmt.Errorf("%w: import payload is required", ErrInvalidPlan)
		}
		if err := validateImport(*predicate.Import); err != nil {
			return err
		}
	case PredicateAvailability, PredicateRequest:
		if predicate.Service == nil {
			return fmt.Errorf("%w: service payload is required", ErrInvalidPlan)
		}
		if err := validateService(*predicate.Service); err != nil {
			return err
		}
	case PredicateEpisodeAssociation:
		if predicate.Episode == nil {
			return fmt.Errorf("%w: episode payload is required", ErrInvalidPlan)
		}
		if err := validateEpisode(*predicate.Episode); err != nil {
			return err
		}
	case PredicateSubtitleAssociation:
		if predicate.Subtitle == nil {
			return fmt.Errorf("%w: subtitle payload is required", ErrInvalidPlan)
		}
		if err := validateSubtitle(*predicate.Subtitle); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unsupported predicate kind %q", ErrInvalidPlan, predicate.Kind)
	}
	return nil
}

func validateRegistration(registration RegistrationPredicate) error {
	if !registration.ConnectionID.Valid() || strings.TrimSpace(registration.ProviderID) == "" {
		return fmt.Errorf("%w: registration connection and provider identity are required", ErrInvalidPlan)
	}
	if !validMediaKind(registration.Kind) {
		return fmt.Errorf("%w: registration media kind is invalid", ErrInvalidPlan)
	}
	fields := registration.Fields
	if strings.TrimSpace(registration.ExternalID) == "" && (strings.TrimSpace(fields.RootFolder) == "" || strings.TrimSpace(fields.QualityProfileID) == "") {
		return fmt.Errorf("%w: registration root folder and quality profile are required", ErrInvalidPlan)
	}
	if registration.Kind == domain.MediaEpisode || registration.Kind == domain.MediaSeason || registration.Kind == domain.MediaAnime {
		if strings.TrimSpace(registration.ExternalID) == "" && strings.TrimSpace(fields.SeriesType) == "" {
			return fmt.Errorf("%w: series registration type is required", ErrInvalidPlan)
		}
	}
	for _, season := range fields.Seasons {
		if strings.TrimSpace(season) == "" {
			return fmt.Errorf("%w: registration season id is empty", ErrInvalidPlan)
		}
	}
	return nil
}

func validateImport(importPredicate ImportPredicate) error {
	if !importPredicate.ConnectionID.Valid() || strings.TrimSpace(importPredicate.RegisteredExternalID) == "" {
		return fmt.Errorf("%w: import connection and registered external id are required", ErrInvalidPlan)
	}
	if len(importPredicate.Files) == 0 {
		return fmt.Errorf("%w: import file set is empty", ErrInvalidPlan)
	}
	seen := make(map[string]struct{}, len(importPredicate.Files))
	for index, selection := range importPredicate.Files {
		if err := selection.Validate(); err != nil {
			return fmt.Errorf("%w: import selection %d: %w", ErrInvalidPlan, index, err)
		}
		key := fileTargetKey(selection.Source)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: import source is repeated", ErrInvalidPlan)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateService(service ServicePredicate) error {
	if !service.ConnectionID.Valid() || strings.TrimSpace(service.ProviderID) == "" {
		return fmt.Errorf("%w: service connection and provider id are required", ErrInvalidPlan)
	}
	if strings.TrimSpace(service.Value) == "" {
		return fmt.Errorf("%w: service desired value is required", ErrInvalidPlan)
	}
	return nil
}

func validateEpisode(episode EpisodePredicate) error {
	if !episode.ConnectionID.Valid() || strings.TrimSpace(episode.SeriesID) == "" {
		return fmt.Errorf("%w: episode connection and series id are required", ErrInvalidPlan)
	}
	if err := episode.Source.Validate(); err != nil {
		return fmt.Errorf("%w: episode source: %v", ErrInvalidPlan, err)
	}
	if len(episode.EpisodeIDs) == 0 {
		return fmt.Errorf("%w: at least one exact episode id is required", ErrAmbiguousMapping)
	}
	for _, id := range episode.EpisodeIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%w: empty episode id", ErrInvalidPlan)
		}
	}
	if episode.SeasonNumber != nil && *episode.SeasonNumber < 0 {
		return fmt.Errorf("%w: season number is invalid", ErrInvalidPlan)
	}
	if episode.AbsoluteNumber != nil && *episode.AbsoluteNumber < 0 {
		return fmt.Errorf("%w: absolute number is invalid", ErrInvalidPlan)
	}
	return nil
}

func validateSubtitle(subtitle SubtitlePredicate) error {
	if !subtitle.ConnectionID.Valid() {
		return fmt.Errorf("%w: subtitle connection is invalid", ErrInvalidPlan)
	}
	if err := subtitle.Source.Validate(); err != nil {
		return fmt.Errorf("%w: subtitle source: %v", ErrInvalidPlan, err)
	}
	if strings.TrimSpace(subtitle.PairID) == "" || strings.TrimSpace(subtitle.Language) == "" {
		return fmt.Errorf("%w: subtitle pair and language are required", ErrAmbiguousMapping)
	}
	if len(subtitle.VideoPaths) == 0 {
		return fmt.Errorf("%w: subtitle must name at least one video", ErrAmbiguousMapping)
	}
	seen := make(map[string]struct{}, len(subtitle.VideoPaths))
	for _, video := range subtitle.VideoPaths {
		if err := video.Validate(); err != nil {
			return fmt.Errorf("%w: subtitle video: %v", ErrInvalidPlan, err)
		}
		key := fileTargetKey(video)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: subtitle video is repeated", ErrInvalidPlan)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// Validate checks one exact import selection.
func (selection ImportSelection) Validate() error {
	if err := selection.Source.Validate(); err != nil {
		return err
	}
	if selection.Confidence == "" {
		selection.Confidence = MappingExact
	}
	if !selection.Confidence.valid() {
		return fmt.Errorf("unsupported mapping confidence %q", selection.Confidence)
	}
	if selection.Confidence == MappingAmbiguous || selection.Confidence == MappingUnresolved {
		return ErrAmbiguousMapping
	}
	if selection.Subtitle {
		if strings.TrimSpace(selection.PairID) == "" || strings.TrimSpace(selection.Language) == "" || len(selection.VideoPaths) == 0 {
			return ErrAmbiguousMapping
		}
		if selection.MovieOrEpisodeID != "" || len(selection.EpisodeIDs) != 0 {
			return fmt.Errorf("subtitle selection cannot carry a movie or episode id")
		}
		for _, video := range selection.VideoPaths {
			if err := video.Validate(); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.TrimSpace(selection.PairID) != "" || len(selection.VideoPaths) != 0 {
		return fmt.Errorf("video selection cannot carry subtitle pairing")
	}
	if (strings.TrimSpace(selection.MovieOrEpisodeID) == "") == (len(selection.EpisodeIDs) == 0) {
		return fmt.Errorf("video selection requires exactly one movie or episode id set")
	}
	seen := make(map[string]struct{}, len(selection.EpisodeIDs))
	for _, id := range selection.EpisodeIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("episode id is empty")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("episode id is repeated")
		}
		seen[id] = struct{}{}
	}
	return nil
}

// Request is the complete input for one immutable plan revision.
type Request struct {
	ID               string
	Revision         int64
	Action           domain.ActionKind
	Desired          DesiredState
	Manifest         []domain.FileManifestEntry
	ManifestLimits   ManifestLimits
	Preconditions    []Precondition
	Binding          Binding
	Capabilities     []domain.Capability
	Conflicts        []Conflict
	Impacts          []Impact
	EstimatedBytes   int64
	RequiredApproval ApprovalKind
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

// Plan is an immutable-in-use representation of one reviewed revision. Its
// slices/maps are copied during Build and NewRevision; callers should pass the
// value through those constructors rather than mutate an approved instance.
type Plan struct {
	ID               string
	Revision         int64
	Digest           string
	Status           Status
	Action           domain.ActionKind
	Desired          DesiredState
	Manifest         []domain.FileManifestEntry
	ManifestLimits   ManifestLimits
	Preconditions    []Precondition
	Binding          Binding
	Capabilities     []domain.Capability
	Conflicts        []Conflict
	BlockingIssues   []Conflict
	Impacts          []Impact
	EstimatedBytes   int64
	RequiredApproval ApprovalKind
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

// DefaultManifestLimits returns bounded defaults suitable for a review-sized
// exact manifest. A larger operation must be deliberately batched or supplied
// explicit limits by its caller.
func DefaultManifestLimits() ManifestLimits {
	return ManifestLimits{MaxEntries: 10_000, MaxDepth: 64, MaxChildrenPerDirectory: 10_000, MaxBytes: 1 << 40}
}

// ManifestLimits bounds recursive exact manifests without truncating them.
type ManifestLimits struct {
	MaxEntries              int
	MaxDepth                int
	MaxChildrenPerDirectory int
	MaxBytes                int64
}

func (limits ManifestLimits) normalized() (ManifestLimits, error) {
	if limits.MaxEntries < 0 || limits.MaxDepth < 0 || limits.MaxChildrenPerDirectory < 0 || limits.MaxBytes < 0 {
		return ManifestLimits{}, fmt.Errorf("%w: manifest limits cannot be negative", ErrInvalidPlan)
	}
	defaults := DefaultManifestLimits()
	if limits.MaxEntries <= 0 {
		limits.MaxEntries = defaults.MaxEntries
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxChildrenPerDirectory <= 0 {
		limits.MaxChildrenPerDirectory = defaults.MaxChildrenPerDirectory
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	return limits, nil
}

// ValidateManifest validates an exact bounded manifest. It checks every child,
// identity and digest and rejects overlapping top-level entries. Directory
// manifests retain their explicit children; they are never wildcards.
func ValidateManifest(entries []domain.FileManifestEntry, requireDigest bool, limits ManifestLimits) error {
	var err error
	limits, err = limits.normalized()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("%w: manifest is empty", ErrInvalidPlan)
	}
	count := 0
	var bytes int64
	seen := make(map[string]struct{})
	var visit func(domain.FileManifestEntry, int) error
	visit = func(entry domain.FileManifestEntry, depth int) error {
		if depth > limits.MaxDepth {
			return fmt.Errorf("%w: manifest depth exceeds %d", ErrManifestLimit, limits.MaxDepth)
		}
		count++
		if count > limits.MaxEntries {
			return fmt.Errorf("%w: entries exceed %d", ErrManifestLimit, limits.MaxEntries)
		}
		if entry.Type != domain.ManifestDirectory {
			if entry.Size > limits.MaxBytes-bytes {
				return fmt.Errorf("%w: manifest bytes exceed %d", ErrManifestLimit, limits.MaxBytes)
			}
			bytes += entry.Size
		}
		if err := entry.ValidateAction(requireDigest); err != nil {
			return fmt.Errorf("%w: manifest entry: %v", ErrInvalidPlan, err)
		}
		key := fileTargetKey(domain.FileTarget{RootID: entry.RootID, RelativePath: entry.RelativePath})
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate manifest path %q", ErrInvalidPlan, entry.RelativePath)
		}
		seen[key] = struct{}{}
		if len(entry.Children) > limits.MaxChildrenPerDirectory {
			return fmt.Errorf("%w: children exceed %d", ErrManifestLimit, limits.MaxChildrenPerDirectory)
		}
		for _, child := range entry.Children {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, entry := range entries {
		if err := visit(entry, 0); err != nil {
			return err
		}
	}
	for left := 0; left < len(entries); left++ {
		for right := left + 1; right < len(entries); right++ {
			if manifestOverlap(entries[left], entries[right]) {
				return fmt.Errorf("%w: manifest entries overlap", ErrInvalidPlan)
			}
		}
	}
	return nil
}

// Build creates revision 1 when Request.Revision is zero. Explicit blocking
// conflicts return a populated invalid Plan plus ErrPlanConflict so the UI can
// render the evidence while execution still has a fail-closed result.
func Build(request Request) (Plan, error) {
	if request.Revision < 0 {
		return Plan{}, fmt.Errorf("%w: revision cannot be negative", ErrInvalidPlan)
	}
	if request.Revision == 0 {
		request.Revision = 1
	}
	if strings.TrimSpace(request.ID) == "" {
		id, err := domain.NewRuntimeID()
		if err != nil {
			return Plan{}, fmt.Errorf("%w: plan id: %v", ErrInvalidPlan, err)
		}
		request.ID = id.String()
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = time.Now().UTC()
	}
	request.CreatedAt = request.CreatedAt.UTC()
	if request.ExpiresAt.IsZero() {
		request.ExpiresAt = request.CreatedAt.Add(15 * time.Minute)
	}
	request.ExpiresAt = request.ExpiresAt.UTC()
	if request.ExpiresAt.Before(request.CreatedAt) || request.ExpiresAt.Equal(request.CreatedAt) {
		return Plan{}, fmt.Errorf("%w: expiry must follow creation", ErrInvalidPlan)
	}
	if !request.Action.Valid() {
		return Plan{}, fmt.Errorf("%w: unsupported action %q", ErrInvalidPlan, request.Action)
	}
	if request.RequiredApproval == "" {
		request.RequiredApproval = ApprovalAction
	}
	if !request.RequiredApproval.valid() {
		return Plan{}, fmt.Errorf("%w: unsupported approval kind %q", ErrInvalidPlan, request.RequiredApproval)
	}
	if request.EstimatedBytes < 0 {
		return Plan{}, fmt.Errorf("%w: estimated bytes cannot be negative", ErrInvalidPlan)
	}
	if err := request.Binding.Validate(); err != nil {
		return Plan{}, err
	}
	if err := request.Desired.Validate(); err != nil {
		return Plan{}, err
	}
	limits, err := request.ManifestLimits.normalized()
	if err != nil {
		return Plan{}, err
	}
	request.ManifestLimits = limits
	if err := validateManifestForAction(request.Action, request.Manifest, request.ManifestLimits); err != nil {
		return Plan{}, err
	}
	if err := validateDesiredForAction(request.Action, request.Desired); err != nil {
		return Plan{}, err
	}
	if err := validateBindingForDesired(request.Action, request.Desired, request.Binding); err != nil {
		return Plan{}, err
	}
	if err := validateDesiredAgainstManifest(request.Action, request.Desired, request.Manifest); err != nil {
		return Plan{}, err
	}
	for index, precondition := range request.Preconditions {
		if err := precondition.Validate(); err != nil {
			return Plan{}, fmt.Errorf("precondition %d: %v", index, err)
		}
	}
	for index, capability := range request.Capabilities {
		if err := capability.Validate(); err != nil {
			return Plan{}, fmt.Errorf("capability %d: %v", index, err)
		}
	}
	for index, conflict := range request.Conflicts {
		if err := conflict.Validate(); err != nil {
			return Plan{}, fmt.Errorf("conflict %d: %v", index, err)
		}
	}
	for index, impact := range request.Impacts {
		if err := impact.Validate(); err != nil {
			return Plan{}, fmt.Errorf("impact %d: %v", index, err)
		}
	}
	plan := Plan{
		ID: request.ID, Revision: request.Revision, Status: StatusPreparing,
		Action: request.Action, Desired: request.Desired, Manifest: request.Manifest, ManifestLimits: request.ManifestLimits,
		Preconditions: request.Preconditions, Binding: request.Binding,
		Capabilities: request.Capabilities, Conflicts: request.Conflicts,
		Impacts: request.Impacts, EstimatedBytes: request.EstimatedBytes,
		RequiredApproval: request.RequiredApproval, CreatedAt: request.CreatedAt,
		ExpiresAt: request.ExpiresAt,
	}
	plan = normalizePlan(plan)
	plan.BlockingIssues = canonicalBlockingIssues(plan.Conflicts)
	if len(plan.BlockingIssues) > 0 {
		plan.Status = StatusInvalid
	} else {
		plan.Status = StatusReady
	}
	plan.Digest, err = digestPlan(plan)
	if err != nil {
		return Plan{}, err
	}
	if len(plan.BlockingIssues) > 0 {
		return plan, ErrPlanConflict
	}
	return plan, nil
}

func validateManifestForAction(action domain.ActionKind, entries []domain.FileManifestEntry, limits ManifestLimits) error {
	requireManifest := action == domain.ActionFSCopy || action == domain.ActionFSHardlink || action == domain.ActionFSMove || action == domain.ActionFSRename || action == domain.ActionFSTrash || action == domain.ActionFSRestore || action == domain.ActionFSDelete || action == domain.ActionArrImport
	if !requireManifest && len(entries) == 0 {
		return nil
	}
	if len(entries) == 0 {
		return fmt.Errorf("%w: action %q requires an exact manifest", ErrInvalidPlan, action)
	}
	requireDigest := action == domain.ActionFSCopy || action == domain.ActionArrImport
	if err := ValidateManifest(entries, requireDigest, limits); err != nil {
		return err
	}
	if action == domain.ActionFSHardlink {
		var walk func(domain.FileManifestEntry) error
		walk = func(entry domain.FileManifestEntry) error {
			if entry.Type == domain.ManifestDirectory {
				return fmt.Errorf("%w: directory hardlink is unsupported", ErrInvalidPlan)
			}
			for _, child := range entry.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		}
		for _, entry := range entries {
			if err := walk(entry); err != nil {
				return err
			}
		}
	}
	if action == domain.ActionArrRegistration && len(entries) != 0 {
		return fmt.Errorf("%w: registration cannot widen into a file manifest", ErrInvalidPlan)
	}
	return nil
}

func validateDesiredForAction(action domain.ActionKind, desired DesiredState) error {
	hasRegistration := false
	hasImport := false
	hasFileContent := false
	hasHardlink := false
	for _, predicate := range desired.Predicates {
		switch predicate.Kind {
		case PredicateRegistration:
			hasRegistration = true
		case PredicateImport:
			hasImport = true
		case PredicateFileContent:
			hasFileContent = true
		case PredicateHardlink:
			hasHardlink = true
		}
	}
	switch action {
	case domain.ActionArrRegistration:
		if !hasRegistration || hasImport {
			return fmt.Errorf("%w: registration action requires a registration predicate and a separate import approval", ErrInvalidPlan)
		}
	case domain.ActionArrImport:
		if !hasImport || hasRegistration {
			return fmt.Errorf("%w: import action requires an exact import predicate after registration", ErrInvalidPlan)
		}
	case domain.ActionFSCopy:
		if !hasFileContent {
			return fmt.Errorf("%w: copy action requires file content predicates", ErrInvalidPlan)
		}
	case domain.ActionFSHardlink:
		if !hasHardlink {
			return fmt.Errorf("%w: hardlink action requires hardlink predicates", ErrInvalidPlan)
		}
	}
	return nil
}

// validateBindingForDesired makes the relevant configuration fences
// mandatory. A caller may omit unrelated connection or mapping revisions, but
// a desired predicate cannot target an unfenced connection. Arr imports also
// cross configured path mappings into the upstream namespace, so every
// connection/root scope used by the exact import must name one selected
// mapping and its revision.
func validateBindingForDesired(action domain.ActionKind, desired DesiredState, binding Binding) error {
	connections := desiredConnectionIDs(desired)
	for _, connectionID := range connections {
		if strings.TrimSpace(binding.ConnectionRevisions[connectionID]) == "" {
			return fmt.Errorf("%w: desired connection %q has no configuration revision fence", ErrInvalidPlan, connectionID)
		}
	}
	if action == domain.ActionArrImport {
		needs := desiredMappingNeeds(desired)
		if len(needs) == 0 {
			return fmt.Errorf("%w: Arr import has no source path mapping scope", ErrInvalidPlan)
		}
		for _, need := range needs {
			matches := mappingScopesForNeed(binding.MappingScopes, need)
			switch len(matches) {
			case 0:
				return fmt.Errorf("%w: Arr import source root %q for connection %q has no path mapping scope", ErrInvalidPlan, need.RootID, need.ConnectionID)
			case 1:
				if strings.TrimSpace(binding.MappingRevisions[matches[0].MappingID]) == "" {
					return fmt.Errorf("%w: Arr import mapping %q has no configuration revision fence", ErrInvalidPlan, matches[0].MappingID)
				}
			default:
				return fmt.Errorf("%w: Arr import source root %q for connection %q has ambiguous path mapping scopes", ErrInvalidPlan, need.RootID, need.ConnectionID)
			}
		}
	}
	return nil
}

type mappingNeed struct {
	ConnectionID domain.ConfigID
	RootID       domain.ConfigID
}

func desiredMappingNeeds(desired DesiredState) []mappingNeed {
	seen := make(map[mappingNeed]struct{})
	add := func(connectionID, rootID domain.ConfigID) {
		if connectionID.Valid() && rootID.Valid() {
			seen[mappingNeed{ConnectionID: connectionID, RootID: rootID}] = struct{}{}
		}
	}
	for _, predicate := range desired.Predicates {
		switch predicate.Kind {
		case PredicateImport:
			for _, selection := range predicate.Import.Files {
				add(predicate.Import.ConnectionID, selection.Source.RootID)
				for _, video := range selection.VideoPaths {
					add(predicate.Import.ConnectionID, video.RootID)
				}
			}
		case PredicateEpisodeAssociation:
			add(predicate.Episode.ConnectionID, predicate.Episode.Source.RootID)
		case PredicateSubtitleAssociation:
			add(predicate.Subtitle.ConnectionID, predicate.Subtitle.Source.RootID)
			for _, video := range predicate.Subtitle.VideoPaths {
				add(predicate.Subtitle.ConnectionID, video.RootID)
			}
		}
	}
	result := make([]mappingNeed, 0, len(seen))
	for need := range seen {
		result = append(result, need)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].ConnectionID != result[right].ConnectionID {
			return result[left].ConnectionID < result[right].ConnectionID
		}
		return result[left].RootID < result[right].RootID
	})
	return result
}

func mappingScopesForNeed(scopes []MappingScope, need mappingNeed) []MappingScope {
	result := make([]MappingScope, 0, len(scopes))
	for _, scope := range scopes {
		if scope.ConnectionID == need.ConnectionID && scope.RootID == need.RootID {
			result = append(result, scope)
		}
	}
	return result
}

func desiredConnectionIDs(desired DesiredState) []domain.ConfigID {
	seen := make(map[domain.ConfigID]struct{})
	for _, predicate := range desired.Predicates {
		var connectionID domain.ConfigID
		switch predicate.Kind {
		case PredicateRegistration:
			connectionID = predicate.Registration.ConnectionID
		case PredicateImport:
			connectionID = predicate.Import.ConnectionID
		case PredicateAvailability, PredicateRequest:
			connectionID = predicate.Service.ConnectionID
		case PredicateEpisodeAssociation:
			connectionID = predicate.Episode.ConnectionID
		case PredicateSubtitleAssociation:
			connectionID = predicate.Subtitle.ConnectionID
		}
		if connectionID != "" {
			seen[connectionID] = struct{}{}
		}
	}
	result := make([]domain.ConfigID, 0, len(seen))
	for connectionID := range seen {
		result = append(result, connectionID)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

// validateDesiredAgainstManifest keeps every path-bearing Arr association
// inside the flattened exact manifest. Directory entries are not wildcards;
// only their explicitly enumerated descendants are eligible references.
func validateDesiredAgainstManifest(action domain.ActionKind, desired DesiredState, manifest []domain.FileManifestEntry) error {
	if len(manifest) == 0 {
		return nil
	}
	members := flattenManifest(manifest)
	for predicateIndex, predicate := range desired.Predicates {
		switch predicate.Kind {
		case PredicateImport:
			for selectionIndex, selection := range predicate.Import.Files {
				entry, ok := members[fileTargetKey(selection.Source)]
				if !ok {
					return fmt.Errorf("%w: import predicate %d selection %d source is outside the exact manifest", ErrInvalidPlan, predicateIndex, selectionIndex)
				}
				if selection.Subtitle {
					if !isSubtitleManifestMember(entry) {
						return fmt.Errorf("%w: import predicate %d selection %d source is not an approved subtitle", ErrInvalidPlan, predicateIndex, selectionIndex)
					}
				} else if !isVideoManifestMember(entry) {
					return fmt.Errorf("%w: import predicate %d selection %d source is not an approved video", ErrInvalidPlan, predicateIndex, selectionIndex)
				}
				for videoIndex, video := range selection.VideoPaths {
					if err := validateManifestVideoReference(members, video, fmt.Sprintf("import predicate %d selection %d video %d", predicateIndex, selectionIndex, videoIndex)); err != nil {
						return err
					}
				}
			}
		case PredicateEpisodeAssociation:
			if err := validateManifestVideoReference(members, predicate.Episode.Source, fmt.Sprintf("episode predicate %d source", predicateIndex)); err != nil {
				return err
			}
		case PredicateSubtitleAssociation:
			entry, ok := members[fileTargetKey(predicate.Subtitle.Source)]
			if !ok {
				return fmt.Errorf("%w: subtitle predicate %d source is outside the exact manifest", ErrInvalidPlan, predicateIndex)
			}
			if !isSubtitleManifestMember(entry) {
				return fmt.Errorf("%w: subtitle predicate %d source is not an approved subtitle", ErrInvalidPlan, predicateIndex)
			}
			for videoIndex, video := range predicate.Subtitle.VideoPaths {
				if err := validateManifestVideoReference(members, video, fmt.Sprintf("subtitle predicate %d video %d", predicateIndex, videoIndex)); err != nil {
					return err
				}
			}
		}
	}
	_ = action // retained in the signature for action-specific future gates.
	return nil
}

func flattenManifest(entries []domain.FileManifestEntry) map[string]domain.FileManifestEntry {
	members := make(map[string]domain.FileManifestEntry)
	var visit func(domain.FileManifestEntry)
	visit = func(entry domain.FileManifestEntry) {
		members[fileTargetKey(domain.FileTarget{RootID: entry.RootID, RelativePath: entry.RelativePath})] = entry
		for _, child := range entry.Children {
			visit(child)
		}
	}
	for _, entry := range entries {
		visit(entry)
	}
	return members
}

func validateManifestVideoReference(members map[string]domain.FileManifestEntry, target domain.FileTarget, label string) error {
	entry, ok := members[fileTargetKey(target)]
	if !ok {
		return fmt.Errorf("%w: %s is outside the exact manifest", ErrInvalidPlan, label)
	}
	if !isVideoManifestMember(entry) {
		return fmt.Errorf("%w: %s is not an approved video", ErrInvalidPlan, label)
	}
	return nil
}

func isVideoManifestMember(entry domain.FileManifestEntry) bool {
	if entry.Type != domain.ManifestFile || entry.Role == domain.RoleSubtitle || entry.Role == domain.RoleCompanion {
		return false
	}
	return entry.Role == "" || entry.Role == domain.RoleVideo
}

func isSubtitleManifestMember(entry domain.FileManifestEntry) bool {
	if entry.Type == domain.ManifestSubtitle {
		return entry.Role == "" || entry.Role == domain.RoleSubtitle
	}
	return entry.Type == domain.ManifestFile && entry.Role == domain.RoleSubtitle
}

// NewRevision creates a new immutable revision without changing previous.
// The plan ID is retained and the revision is exactly previous.Revision+1.
func NewRevision(previous Plan, request Request) (Plan, error) {
	if err := previous.Validate(); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(request.ID) != "" && request.ID != previous.ID {
		return Plan{}, fmt.Errorf("%w: revision plan id changed", ErrPlanBindingChanged)
	}
	request.ID = previous.ID
	request.Revision = previous.Revision + 1
	return Build(request)
}

// Validate checks the self-consistency of a plan, including its digest.
func (plan Plan) Validate() error {
	if strings.TrimSpace(plan.ID) == "" || plan.Revision <= 0 || !plan.Action.Valid() || !plan.Status.valid() {
		return fmt.Errorf("%w: plan identity/state is invalid", ErrInvalidPlan)
	}
	if plan.Status == StatusPreparing {
		return fmt.Errorf("%w: plan is still preparing", ErrInvalidPlan)
	}
	if err := plan.Binding.Validate(); err != nil {
		return err
	}
	if err := plan.Desired.Validate(); err != nil {
		return err
	}
	if err := validateDesiredForAction(plan.Action, plan.Desired); err != nil {
		return err
	}
	if err := validateBindingForDesired(plan.Action, plan.Desired, plan.Binding); err != nil {
		return err
	}
	limits, err := plan.ManifestLimits.normalized()
	if err != nil {
		return err
	}
	plan.ManifestLimits = limits
	if err := validateManifestForAction(plan.Action, plan.Manifest, plan.ManifestLimits); err != nil {
		return err
	}
	if err := validateDesiredAgainstManifest(plan.Action, plan.Desired, plan.Manifest); err != nil {
		return err
	}
	if plan.CreatedAt.IsZero() || plan.ExpiresAt.IsZero() || !plan.ExpiresAt.After(plan.CreatedAt) {
		return fmt.Errorf("%w: plan timestamps are invalid", ErrInvalidPlan)
	}
	if !strongDigest(plan.Digest) {
		return fmt.Errorf("%w: plan digest is invalid", ErrInvalidPlan)
	}
	for _, conflict := range plan.Conflicts {
		if err := conflict.Validate(); err != nil {
			return err
		}
	}
	for _, precondition := range plan.Preconditions {
		if err := precondition.Validate(); err != nil {
			return err
		}
	}
	for _, capability := range plan.Capabilities {
		if err := capability.Validate(); err != nil {
			return err
		}
	}
	for _, impact := range plan.Impacts {
		if err := impact.Validate(); err != nil {
			return err
		}
	}
	canonicalBlocking := canonicalBlockingIssues(plan.Conflicts)
	canonicalStatus := StatusReady
	if len(canonicalBlocking) != 0 {
		canonicalStatus = StatusInvalid
	}
	if plan.Status != canonicalStatus {
		return fmt.Errorf("%w: plan status does not match canonical conflicts", ErrInvalidPlan)
	}
	if !sameConflictAuthorities(plan.BlockingIssues, canonicalBlocking) {
		return fmt.Errorf("%w: blocking issues do not match canonical conflicts", ErrInvalidPlan)
	}
	computedDigest, err := digestPlan(plan)
	if err != nil {
		return err
	}
	if computedDigest != plan.Digest {
		return ErrPlanDigest
	}
	return nil
}

// StatusAt reports expiry without mutating the plan.
func (plan Plan) StatusAt(now time.Time) Status {
	if plan.Status == StatusReady && !now.Before(plan.ExpiresAt) {
		return StatusExpired
	}
	return plan.Status
}

// Approval binds a decision to one exact plan revision and digest.
type Approval struct {
	PlanID   string
	Revision int64
	Digest   string
	At       time.Time
}

// ValidateApproval is the final immutable decision check before queueing.
func (plan Plan) ValidateApproval(approval Approval, now time.Time) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if plan.StatusAt(now.UTC()) == StatusExpired {
		return ErrPlanExpired
	}
	if plan.Status != StatusReady || len(plan.BlockingIssues) != 0 {
		return ErrPlanConflict
	}
	if approval.PlanID != plan.ID {
		return ErrPlanRevision
	}
	if approval.Revision != plan.Revision {
		return ErrPlanRevision
	}
	if approval.Digest != plan.Digest {
		return ErrPlanDigest
	}
	if approval.At.IsZero() || approval.At.Before(plan.CreatedAt) || !approval.At.Before(plan.ExpiresAt) {
		return ErrPlanExpired
	}
	return nil
}

// CurrentState is the read-only state captured immediately before approval or
// dispatch. Desired and manifest comparisons make changed episode mappings and
// forged/changed file evidence visible as conflicts.
type CurrentState struct {
	Binding      Binding
	Manifest     []domain.FileManifestEntry
	Desired      DesiredState
	Capabilities []domain.Capability
	ObservedAt   time.Time
}

// CheckCurrent returns every binding/manfiest/intent conflict, retaining all
// independent reasons for UI review.
func (plan Plan) CheckCurrent(current CurrentState) []Conflict {
	conflicts := make([]Conflict, 0)
	if current.ObservedAt.IsZero() {
		conflicts = append(conflicts, Conflict{Code: "current_observation_missing", Target: plan.ID, Message: "current state observation time is required", Blocking: true})
	}
	if plan.Binding.SourceID != current.Binding.SourceID || plan.Binding.SourceRevision != current.Binding.SourceRevision || normalizeDigest(plan.Binding.SourceDigest) != normalizeDigest(current.Binding.SourceDigest) {
		conflicts = append(conflicts, Conflict{Code: "source_changed", Field: "source", Target: plan.Binding.SourceID, Message: "the approved source identity or revision changed", Blocking: true})
	}
	if !sameRevisionSubset(plan.Binding.ConnectionRevisions, current.Binding.ConnectionRevisions) {
		conflicts = append(conflicts, Conflict{Code: "configuration_changed", Field: "connectionRevisions", Target: plan.ID, Message: "a relevant connection configuration revision changed", Blocking: true})
	}
	if !sameRevisionSubset(relevantMappingRevisions(plan), current.Binding.MappingRevisions) || !sameMappingScopeSubset(relevantMappingScopes(plan), current.Binding.MappingScopes) {
		conflicts = append(conflicts, Conflict{Code: "mapping_changed", Field: "mappingRevisions", Target: plan.ID, Message: "a relevant path mapping revision changed", Blocking: true})
	}
	if len(plan.Capabilities) != 0 && !sameCapabilities(plan.Capabilities, current.Capabilities) {
		conflicts = append(conflicts, Conflict{Code: "capability_changed", Field: "capabilities", Target: plan.ID, Message: "a capability used by the plan changed", Blocking: true})
	}
	if len(plan.Manifest) != 0 && (len(current.Manifest) == 0 || manifestFingerprint(plan.Manifest) != manifestFingerprint(current.Manifest)) {
		conflicts = append(conflicts, Conflict{Code: "manifest_changed", Field: "manifest", Target: plan.ID, Message: "the exact approved file manifest changed", Blocking: true})
	}
	planDesired, planErr := plan.Desired.SemanticDigest()
	currentDesired, currentErr := current.Desired.SemanticDigest()
	if planErr != nil || currentErr != nil || planDesired != currentDesired {
		conflicts = append(conflicts, Conflict{Code: "desired_state_changed", Field: "desired", Target: plan.ID, Message: "the selected semantic intent changed", Blocking: true})
	}
	if !plan.ExpiresAt.IsZero() && !current.ObservedAt.Before(plan.ExpiresAt) {
		conflicts = append(conflicts, Conflict{Code: "preview_expired", Field: "expiresAt", Target: plan.ID, Message: "the approved plan expired before validation", Blocking: true})
	}
	sortConflicts(conflicts)
	return conflicts
}

// ValidateCurrent returns a typed conflict error when current state no longer
// matches the immutable plan.
func (plan Plan) ValidateCurrent(current CurrentState) error {
	conflicts := plan.CheckCurrent(current)
	if len(conflicts) != 0 {
		return fmt.Errorf("%w: %s", ErrPlanBindingChanged, conflicts[0].Message)
	}
	return nil
}

// Satisfaction is the semantic result of evaluating one desired predicate.
type Satisfaction string

const (
	SatisfactionAlreadySatisfied Satisfaction = "already_satisfied"
	SatisfactionSatisfied        Satisfaction = SatisfactionAlreadySatisfied
	SatisfactionNeedsAction      Satisfaction = "needs_action"
	SatisfactionUnknown          Satisfaction = "unknown"
	SatisfactionConflict         Satisfaction = "conflict"
)

// FileObservation is a read-only identity/content observation. Known=false
// means the source was unavailable or coverage was incomplete.
type FileObservation struct {
	Target       domain.FileTarget
	Known        bool
	Exists       bool
	Size         int64
	Digest       string
	FileIdentity string
}

// RegistrationObservation is one instance-scoped Arr registration result.
type RegistrationObservation struct {
	ConnectionID domain.ConfigID
	ProviderID   string
	Kind         domain.MediaKind
	ExternalID   string
	Present      bool
	Known        bool
	Fields       ports.RegistrationFields
}

// ImportObservation is one instance-scoped Arr import result with exact file
// associations.
type ImportObservation struct {
	ConnectionID         domain.ConfigID
	RegisteredExternalID string
	Present              bool
	Known                bool
	Files                []ImportSelection
}

// ServiceObservation is independent Jellyfin/Seerr read-back evidence.
type ServiceObservation struct {
	ConnectionID domain.ConfigID
	ProviderID   string
	ExternalID   string
	Value        string
	Known        bool
}

// Fact is a generic fallback for future typed predicate families. It remains
// exact because the predicate itself is carried alongside the fact.
type Fact struct {
	Predicate Predicate
	Present   bool
	Known     bool
	Conflict  bool
	Reason    string
}

// ObservedState is the read-only input to Evaluate.
type ObservedState struct {
	Files         []FileObservation
	Registrations []RegistrationObservation
	Imports       []ImportObservation
	Services      []ServiceObservation
	Facts         []Fact
}

// PredicateResult keeps every per-predicate outcome so a partial Arr import
// or subtitle mapping cannot collapse into aggregate success.
type PredicateResult struct {
	Predicate Predicate
	State     Satisfaction
	Reason    string
}

// Evaluation is the aggregate semantic result. The aggregate state is a
// convenience; callers should render each PredicateResult as well.
type Evaluation struct {
	State   Satisfaction
	Results []PredicateResult
}

// Evaluate compares exact desired predicates with read-only observations.
// Size/name equality never proves copy success; content digest and, for
// hardlink, file identity are required.
func Evaluate(desired DesiredState, observed ObservedState) (Evaluation, error) {
	if err := desired.Validate(); err != nil {
		return Evaluation{}, err
	}
	evaluation := Evaluation{Results: make([]PredicateResult, 0, len(desired.Predicates))}
	for _, predicate := range desired.Predicates {
		result := evaluatePredicate(predicate, observed)
		evaluation.Results = append(evaluation.Results, result)
	}
	evaluation.State = SatisfactionAlreadySatisfied
	for _, result := range evaluation.Results {
		switch result.State {
		case SatisfactionConflict:
			evaluation.State = SatisfactionConflict
		case SatisfactionUnknown:
			if evaluation.State != SatisfactionConflict {
				evaluation.State = SatisfactionUnknown
			}
		case SatisfactionNeedsAction:
			if evaluation.State != SatisfactionConflict && evaluation.State != SatisfactionUnknown {
				evaluation.State = SatisfactionNeedsAction
			}
		}
	}
	return evaluation, nil
}

func evaluatePredicate(predicate Predicate, observed ObservedState) PredicateResult {
	switch predicate.Kind {
	case PredicateFileContent:
		return evaluateFileContent(predicate, observed.Files)
	case PredicateFileIdentity:
		return evaluateFileIdentity(predicate, observed.Files)
	case PredicateHardlink:
		return evaluateHardlink(predicate, observed.Files)
	case PredicateRegistration:
		return evaluateRegistration(predicate, observed.Registrations)
	case PredicateImport:
		return evaluateImport(predicate, observed.Imports)
	case PredicateAvailability, PredicateRequest:
		return evaluateService(predicate, observed.Services)
	default:
		for _, fact := range observed.Facts {
			if samePredicate(fact.Predicate, predicate) {
				if fact.Conflict {
					return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: fact.Reason}
				}
				if !fact.Known {
					return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: fact.Reason}
				}
				if fact.Present {
					return PredicateResult{Predicate: predicate, State: SatisfactionAlreadySatisfied, Reason: "exact predicate observed"}
				}
				return PredicateResult{Predicate: predicate, State: SatisfactionNeedsAction, Reason: "predicate absent"}
			}
		}
		return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "no exact observation"}
	}
}

func evaluateFileContent(predicate Predicate, observations []FileObservation) PredicateResult {
	target := predicate.File.Target
	matching, ok := fileObservation(target, observations)
	if !ok || !matching.Known {
		return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "file observation unavailable"}
	}
	if !matching.Exists {
		return PredicateResult{Predicate: predicate, State: SatisfactionNeedsAction, Reason: "destination is absent"}
	}
	if matching.Size != predicate.File.Size {
		return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "destination size differs from approved content"}
	}
	if matching.Digest == "" {
		return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "destination content digest is unavailable"}
	}
	if !strongDigest(matching.Digest) || !strings.EqualFold(normalizeDigest(matching.Digest), normalizeDigest(predicate.File.Digest)) {
		return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "destination content digest differs from approved content"}
	}
	return PredicateResult{Predicate: predicate, State: SatisfactionAlreadySatisfied, Reason: "destination content matches approved digest"}
}

func evaluateFileIdentity(predicate Predicate, observations []FileObservation) PredicateResult {
	matching, ok := fileObservation(predicate.File.Target, observations)
	if !ok || !matching.Known {
		return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "file observation unavailable"}
	}
	if !matching.Exists {
		return PredicateResult{Predicate: predicate, State: SatisfactionNeedsAction, Reason: "file is absent"}
	}
	if matching.FileIdentity != predicate.File.FileIdentity {
		return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "file identity differs from approved object"}
	}
	return PredicateResult{Predicate: predicate, State: SatisfactionAlreadySatisfied, Reason: "file identity matches approved object"}
}

func evaluateHardlink(predicate Predicate, observations []FileObservation) PredicateResult {
	source, sourceOK := fileObservation(predicate.File.Source, observations)
	target, targetOK := fileObservation(predicate.File.Target, observations)
	if !sourceOK || !source.Known || !targetOK || !target.Known {
		return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "hardlink source or destination observation unavailable"}
	}
	if !source.Exists {
		return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "approved hardlink source is absent"}
	}
	if source.FileIdentity != predicate.File.FileIdentity {
		return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "hardlink source identity changed"}
	}
	if !target.Exists {
		return PredicateResult{Predicate: predicate, State: SatisfactionNeedsAction, Reason: "hardlink destination is absent"}
	}
	if target.FileIdentity != predicate.File.FileIdentity || target.FileIdentity != source.FileIdentity {
		return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "destination is a different file object; hardlink cannot be inferred from equal bytes"}
	}
	return PredicateResult{Predicate: predicate, State: SatisfactionAlreadySatisfied, Reason: "source and destination name the approved file object"}
}

func evaluateRegistration(predicate Predicate, observations []RegistrationObservation) PredicateResult {
	registration := predicate.Registration
	var unknown bool
	for _, observation := range observations {
		if observation.ConnectionID != registration.ConnectionID || observation.ProviderID != registration.ProviderID || observation.Kind != registration.Kind {
			continue
		}
		if !observation.Known {
			unknown = true
			continue
		}
		if !observation.Present {
			return PredicateResult{Predicate: predicate, State: SatisfactionNeedsAction, Reason: "registration is absent"}
		}
		if registration.ExternalID != "" && observation.ExternalID != registration.ExternalID {
			return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "registration external identity differs"}
		}
		if !sameRegistrationFields(registration.Fields, observation.Fields) {
			return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "one or more explicitly selected registration fields differ"}
		}
		return PredicateResult{Predicate: predicate, State: SatisfactionAlreadySatisfied, Reason: "registration identity and selected fields match"}
	}
	if unknown {
		return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "registration observation is unknown"}
	}
	return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "no complete registration observation"}
}

func evaluateImport(predicate Predicate, observations []ImportObservation) PredicateResult {
	importPredicate := predicate.Import
	var unknown bool
	for _, observation := range observations {
		if observation.ConnectionID != importPredicate.ConnectionID || observation.RegisteredExternalID != importPredicate.RegisteredExternalID {
			continue
		}
		if !observation.Known {
			unknown = true
			continue
		}
		if !observation.Present {
			return PredicateResult{Predicate: predicate, State: SatisfactionNeedsAction, Reason: "import is absent"}
		}
		if !sameSelections(importPredicate.Files, observation.Files) {
			return PredicateResult{Predicate: predicate, State: SatisfactionConflict, Reason: "import read-back does not contain the exact approved file associations"}
		}
		return PredicateResult{Predicate: predicate, State: SatisfactionAlreadySatisfied, Reason: "exact import associations are present"}
	}
	if unknown {
		return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "import observation is unknown"}
	}
	return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "no complete import observation"}
}

func evaluateService(predicate Predicate, observations []ServiceObservation) PredicateResult {
	service := predicate.Service
	var unknown bool
	for _, observation := range observations {
		if observation.ConnectionID != service.ConnectionID || observation.ProviderID != service.ProviderID || (service.ExternalID != "" && observation.ExternalID != service.ExternalID) {
			continue
		}
		if !observation.Known {
			unknown = true
			continue
		}
		if observation.Value != service.Value {
			return PredicateResult{Predicate: predicate, State: SatisfactionNeedsAction, Reason: "service state does not satisfy desired value"}
		}
		return PredicateResult{Predicate: predicate, State: SatisfactionAlreadySatisfied, Reason: "service state matches desired value"}
	}
	if unknown {
		return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "service observation is unknown"}
	}
	return PredicateResult{Predicate: predicate, State: SatisfactionUnknown, Reason: "no service observation"}
}

func fileObservation(target domain.FileTarget, observations []FileObservation) (FileObservation, bool) {
	for _, observation := range observations {
		if observation.Target == target {
			return observation, true
		}
	}
	return FileObservation{}, false
}

func sameRegistrationFields(expected, actual ports.RegistrationFields) bool {
	if strings.TrimSpace(expected.RootFolder) != "" && expected.RootFolder != actual.RootFolder {
		return false
	}
	if strings.TrimSpace(expected.QualityProfileID) != "" && expected.QualityProfileID != actual.QualityProfileID {
		return false
	}
	if expected.Monitored != nil && (actual.Monitored == nil || *expected.Monitored != *actual.Monitored) {
		return false
	}
	if strings.TrimSpace(expected.SeriesType) != "" && expected.SeriesType != actual.SeriesType {
		return false
	}
	if expected.SeasonFolder != nil && (actual.SeasonFolder == nil || *expected.SeasonFolder != *actual.SeasonFolder) {
		return false
	}
	if len(expected.Seasons) != 0 && !sameStringSet(expected.Seasons, actual.Seasons) {
		return false
	}
	return true
}

func sameSelections(expected, actual []ImportSelection) bool {
	left := normalizeSelections(expected)
	right := normalizeSelections(actual)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if importSelectionKey(left[index]) != importSelectionKey(right[index]) {
			return false
		}
	}
	return true
}

// Build-time plan digest payload uses only normalized deterministic values.
type digestPayload struct {
	Version          int
	Revision         int64
	Action           domain.ActionKind
	Desired          DesiredState
	Manifest         []domain.FileManifestEntry
	ManifestLimits   ManifestLimits
	Preconditions    []Precondition
	Binding          digestBinding
	Capabilities     []domain.Capability
	Conflicts        []digestConflict
	Impacts          []digestImpact
	EstimatedBytes   int64
	RequiredApproval ApprovalKind
	CreatedAt        string
	ExpiresAt        string
}

type digestBinding struct {
	SourceID       string
	SourceRevision string
	SourceDigest   string
	Connections    []RevisionBinding
	Mappings       []RevisionBinding
	MappingScopes  []MappingScope `json:",omitempty"`
}

// digestConflict and digestImpact intentionally omit display prose. Codes,
// targets, evidence and blocking state are authority-bearing; a copy edit to
// a UI message must not create a new approved intent.
type digestConflict struct {
	Code     string
	Field    string
	Target   string
	Evidence []string
	Blocking bool
}

type digestImpact struct {
	Kind   string
	Target string
}

func digestPlan(plan Plan) (string, error) {
	payload := digestPayload{
		Version: 1, Revision: plan.Revision, Action: plan.Action,
		Desired: plan.Desired, Manifest: normalizeManifest(plan.Manifest), ManifestLimits: plan.ManifestLimits,
		Preconditions: normalizePreconditions(plan.Preconditions),
		Binding:       digestBinding{SourceID: plan.Binding.SourceID, SourceRevision: plan.Binding.SourceRevision, SourceDigest: normalizeDigest(plan.Binding.SourceDigest), Connections: revisionBindings(plan.Binding.ConnectionRevisions), Mappings: revisionBindings(plan.Binding.MappingRevisions), MappingScopes: normalizeMappingScopes(plan.Binding.MappingScopes)},
		Capabilities:  normalizeCapabilities(plan.Capabilities), Conflicts: digestConflicts(plan.Conflicts), Impacts: digestImpacts(plan.Impacts),
		EstimatedBytes: plan.EstimatedBytes, RequiredApproval: plan.RequiredApproval,
		CreatedAt: plan.CreatedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: plan.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("%w: encode plan digest: %v", ErrInvalidPlan, err)
	}
	return digestBytes(encoded), nil
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func strongDigest(value string) bool {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeDigest(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "sha256:") {
		return "sha256:" + strings.TrimPrefix(value, "sha256:")
	}
	return value
}

func normalizePlan(plan Plan) Plan {
	plan.Desired, _ = normalizeDesired(plan.Desired)
	plan.Manifest = normalizeManifest(plan.Manifest)
	plan.Preconditions = normalizePreconditions(plan.Preconditions)
	plan.Binding = cloneBinding(plan.Binding)
	plan.Capabilities = normalizeCapabilities(plan.Capabilities)
	plan.Conflicts = normalizeConflicts(plan.Conflicts)
	plan.Impacts = normalizeImpacts(plan.Impacts)
	plan.BlockingIssues = normalizeConflicts(plan.BlockingIssues)
	return plan
}

func normalizeDesired(desired DesiredState) (DesiredState, error) {
	if err := desired.Validate(); err != nil {
		return DesiredState{}, err
	}
	desired.Predicates = append([]Predicate(nil), desired.Predicates...)
	for index := range desired.Predicates {
		desired.Predicates[index] = normalizePredicate(desired.Predicates[index])
	}
	sort.Slice(desired.Predicates, func(left, right int) bool {
		leftKey, _ := predicateKey(desired.Predicates[left])
		rightKey, _ := predicateKey(desired.Predicates[right])
		return leftKey < rightKey
	})
	return desired, nil
}

func normalizePredicate(predicate Predicate) Predicate {
	clone := predicate
	if predicate.File != nil {
		file := *predicate.File
		file.Digest = normalizeDigest(file.Digest)
		clone.File = &file
	}
	if predicate.Registration != nil {
		registration := *predicate.Registration
		registration.Fields = cloneRegistrationFields(registration.Fields)
		clone.Registration = &registration
	}
	if predicate.Import != nil {
		importPredicate := *predicate.Import
		importPredicate.Files = normalizeSelections(importPredicate.Files)
		clone.Import = &importPredicate
	}
	if predicate.Service != nil {
		service := *predicate.Service
		clone.Service = &service
	}
	if predicate.Episode != nil {
		episode := *predicate.Episode
		episode.EpisodeIDs = sortedUniqueStrings(episode.EpisodeIDs)
		episode.SeasonNumber = cloneIntPointer(episode.SeasonNumber)
		episode.AbsoluteNumber = cloneIntPointer(episode.AbsoluteNumber)
		clone.Episode = &episode
	}
	if predicate.Subtitle != nil {
		subtitle := *predicate.Subtitle
		subtitle.VideoPaths = normalizeTargets(subtitle.VideoPaths)
		clone.Subtitle = &subtitle
	}
	return clone
}

func predicateKey(predicate Predicate) (string, error) {
	normalized := normalizePredicateShallow(predicate)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("%w: encode predicate: %v", ErrInvalidPlan, err)
	}
	return string(encoded), nil
}

func normalizePredicateShallow(predicate Predicate) Predicate {
	clone := predicate
	if predicate.File != nil {
		file := *predicate.File
		file.Digest = normalizeDigest(file.Digest)
		clone.File = &file
	}
	if predicate.Registration != nil {
		registration := *predicate.Registration
		registration.Fields = cloneRegistrationFields(registration.Fields)
		clone.Registration = &registration
	}
	if predicate.Import != nil {
		importPredicate := *predicate.Import
		importPredicate.Files = normalizeSelections(importPredicate.Files)
		clone.Import = &importPredicate
	}
	if predicate.Episode != nil {
		episode := *predicate.Episode
		episode.EpisodeIDs = sortedUniqueStrings(episode.EpisodeIDs)
		episode.SeasonNumber = cloneIntPointer(episode.SeasonNumber)
		episode.AbsoluteNumber = cloneIntPointer(episode.AbsoluteNumber)
		clone.Episode = &episode
	}
	if predicate.Subtitle != nil {
		subtitle := *predicate.Subtitle
		subtitle.VideoPaths = normalizeTargets(subtitle.VideoPaths)
		clone.Subtitle = &subtitle
	}
	return clone
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneRegistrationFields(fields ports.RegistrationFields) ports.RegistrationFields {
	fields.Monitored = cloneBoolPointer(fields.Monitored)
	fields.SeasonFolder = cloneBoolPointer(fields.SeasonFolder)
	fields.Seasons = sortedUniqueStrings(fields.Seasons)
	return fields
}

func normalizeSelections(selections []ImportSelection) []ImportSelection {
	result := make([]ImportSelection, len(selections))
	for index, selection := range selections {
		result[index] = selection
		if result[index].Confidence == "" {
			result[index].Confidence = MappingExact
		}
		result[index].EpisodeIDs = sortedUniqueStrings(selection.EpisodeIDs)
		result[index].VideoPaths = normalizeTargets(selection.VideoPaths)
	}
	sort.Slice(result, func(left, right int) bool {
		return importSelectionKey(result[left]) < importSelectionKey(result[right])
	})
	return result
}

func importSelectionKey(selection ImportSelection) string {
	encoded, _ := json.Marshal(selection)
	return string(encoded)
}

func normalizeTargets(targets []domain.FileTarget) []domain.FileTarget {
	result := append([]domain.FileTarget(nil), targets...)
	sort.Slice(result, func(left, right int) bool { return fileTargetKey(result[left]) < fileTargetKey(result[right]) })
	return result
}

func normalizeManifest(entries []domain.FileManifestEntry) []domain.FileManifestEntry {
	result := make([]domain.FileManifestEntry, len(entries))
	for index, entry := range entries {
		result[index] = entry
		result[index].Digest = normalizeDigest(entry.Digest)
		result[index].Children = normalizeManifest(entry.Children)
	}
	sort.Slice(result, func(left, right int) bool {
		return fileTargetKey(domain.FileTarget{RootID: result[left].RootID, RelativePath: result[left].RelativePath}) < fileTargetKey(domain.FileTarget{RootID: result[right].RootID, RelativePath: result[right].RelativePath})
	})
	return result
}

func normalizePreconditions(preconditions []Precondition) []Precondition {
	result := append([]Precondition(nil), preconditions...)
	sort.Slice(result, func(left, right int) bool {
		return result[left].Kind+"\x00"+result[left].Target+"\x00"+result[left].Expected < result[right].Kind+"\x00"+result[right].Target+"\x00"+result[right].Expected
	})
	return result
}

func normalizeCapabilities(capabilities []domain.Capability) []domain.Capability {
	result := make([]domain.Capability, len(capabilities))
	for index, capability := range capabilities {
		result[index] = capability
		result[index].Evidence = sortedUniqueStrings(capability.Evidence)
	}
	sort.Slice(result, func(left, right int) bool {
		return capabilityKey(result[left]) < capabilityKey(result[right])
	})
	return result
}

func capabilityKey(capability domain.Capability) string {
	evidence := sortedUniqueStrings(capability.Evidence)
	return capability.Name + "\x00" + capability.Version + "\x00" + string(capability.State) + "\x00" + capability.Reason + "\x00" + strings.Join(evidence, "\x00") + "\x00" + capability.ObservedAt.UTC().Format(time.RFC3339Nano)
}

func sameCapabilities(expected, actual []domain.Capability) bool {
	left := normalizeCapabilities(expected)
	right := normalizeCapabilities(actual)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if capabilityKey(left[index]) != capabilityKey(right[index]) || !left[index].ObservedAt.Equal(right[index].ObservedAt) {
			return false
		}
	}
	return true
}

func normalizeConflicts(conflicts []Conflict) []Conflict {
	result := make([]Conflict, len(conflicts))
	for index, conflict := range conflicts {
		result[index] = conflict
		result[index].Evidence = sortedUniqueStrings(conflict.Evidence)
	}
	sortConflicts(result)
	return result
}

func canonicalBlockingIssues(conflicts []Conflict) []Conflict {
	normalized := normalizeConflicts(conflicts)
	result := make([]Conflict, 0, len(normalized))
	for _, conflict := range normalized {
		if conflict.Blocking {
			result = append(result, cloneConflict(conflict))
		}
	}
	return result
}

// sameConflictAuthorities compares only the fields that determine whether a
// plan is safe. Message text is display-only and intentionally remains outside
// the plan digest and this authority comparison.
func sameConflictAuthorities(left, right []Conflict) bool {
	left = normalizeConflicts(left)
	right = normalizeConflicts(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Code != right[index].Code || left[index].Field != right[index].Field || left[index].Target != right[index].Target || left[index].Blocking != right[index].Blocking || !sameStringSet(left[index].Evidence, right[index].Evidence) {
			return false
		}
	}
	return true
}

func normalizeImpacts(impacts []Impact) []Impact {
	result := append([]Impact(nil), impacts...)
	sort.Slice(result, func(left, right int) bool {
		return result[left].Kind+"\x00"+result[left].Target < result[right].Kind+"\x00"+result[right].Target
	})
	return result
}

func digestConflicts(conflicts []Conflict) []digestConflict {
	result := make([]digestConflict, len(conflicts))
	for index, conflict := range conflicts {
		result[index] = digestConflict{Code: conflict.Code, Field: conflict.Field, Target: conflict.Target, Evidence: sortedUniqueStrings(conflict.Evidence), Blocking: conflict.Blocking}
	}
	sort.Slice(result, func(left, right int) bool {
		return digestConflictKey(result[left]) < digestConflictKey(result[right])
	})
	return result
}

func digestConflictKey(conflict digestConflict) string {
	encoded, _ := json.Marshal(conflict)
	return string(encoded)
}

func digestImpacts(impacts []Impact) []digestImpact {
	result := make([]digestImpact, len(impacts))
	for index, impact := range impacts {
		result[index] = digestImpact{Kind: impact.Kind, Target: impact.Target}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Kind+"\x00"+result[left].Target < result[right].Kind+"\x00"+result[right].Target
	})
	return result
}

func revisionBindings(values map[domain.ConfigID]string) []RevisionBinding {
	result := make([]RevisionBinding, 0, len(values))
	for id, revision := range values {
		result = append(result, RevisionBinding{ID: id, Revision: revision})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func cloneBinding(binding Binding) Binding {
	binding.ConnectionRevisions = cloneRevisionMap(binding.ConnectionRevisions)
	binding.MappingRevisions = cloneRevisionMap(binding.MappingRevisions)
	binding.MappingScopes = normalizeMappingScopes(binding.MappingScopes)
	binding.SourceDigest = normalizeDigest(binding.SourceDigest)
	return binding
}

func normalizeMappingScopes(scopes []MappingScope) []MappingScope {
	result := append([]MappingScope(nil), scopes...)
	sort.Slice(result, func(left, right int) bool {
		if result[left].MappingID != result[right].MappingID {
			return result[left].MappingID < result[right].MappingID
		}
		if result[left].ConnectionID != result[right].ConnectionID {
			return result[left].ConnectionID < result[right].ConnectionID
		}
		return result[left].RootID < result[right].RootID
	})
	return result
}

func cloneRevisionMap(values map[domain.ConfigID]string) map[domain.ConfigID]string {
	if values == nil {
		return nil
	}
	clone := make(map[domain.ConfigID]string, len(values))
	for id, revision := range values {
		clone[id] = revision
	}
	return clone
}

func sameRevisionSubset(expected, actual map[domain.ConfigID]string) bool {
	for id, revision := range expected {
		if actual[id] != revision {
			return false
		}
	}
	return true
}

func relevantMappingRevisions(plan Plan) map[domain.ConfigID]string {
	result := make(map[domain.ConfigID]string)
	for _, scope := range relevantMappingScopes(plan) {
		if revision := plan.Binding.MappingRevisions[scope.MappingID]; revision != "" {
			result[scope.MappingID] = revision
		}
	}
	return result
}

func relevantMappingScopes(plan Plan) []MappingScope {
	needs := desiredMappingNeeds(plan.Desired)
	result := make([]MappingScope, 0, len(needs))
	for _, need := range needs {
		matches := mappingScopesForNeed(plan.Binding.MappingScopes, need)
		if len(matches) == 1 {
			result = append(result, matches[0])
		}
	}
	return normalizeMappingScopes(result)
}

func sameMappingScopeSubset(expected, actual []MappingScope) bool {
	for _, required := range expected {
		mappingMatches := 0
		scopeMatches := 0
		for _, candidate := range actual {
			if candidate.MappingID == required.MappingID {
				mappingMatches++
				if candidate != required {
					return false
				}
			}
			if candidate.ConnectionID == required.ConnectionID && candidate.RootID == required.RootID {
				scopeMatches++
			}
		}
		if mappingMatches != 1 || scopeMatches != 1 {
			return false
		}
	}
	return true
}

func sameStringSet(left, right []string) bool {
	left = sortedUniqueStrings(left)
	right = sortedUniqueStrings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sortedUniqueStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	out := result[:0]
	for _, value := range result {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func sortConflicts(conflicts []Conflict) {
	sort.Slice(conflicts, func(left, right int) bool {
		leftKey := digestConflictKey(digestConflict{Code: conflicts[left].Code, Field: conflicts[left].Field, Target: conflicts[left].Target, Evidence: sortedUniqueStrings(conflicts[left].Evidence), Blocking: conflicts[left].Blocking}) + "\x00" + conflicts[left].Message
		rightKey := digestConflictKey(digestConflict{Code: conflicts[right].Code, Field: conflicts[right].Field, Target: conflicts[right].Target, Evidence: sortedUniqueStrings(conflicts[right].Evidence), Blocking: conflicts[right].Blocking}) + "\x00" + conflicts[right].Message
		return leftKey < rightKey
	})
}

func cloneConflict(conflict Conflict) Conflict {
	conflict.Evidence = append([]string(nil), conflict.Evidence...)
	return conflict
}

func manifestFingerprint(entries []domain.FileManifestEntry) string {
	encoded, _ := json.Marshal(normalizeManifest(entries))
	return string(encoded)
}

func manifestOverlap(left, right domain.FileManifestEntry) bool {
	if left.RootID != right.RootID {
		return false
	}
	if left.RelativePath == right.RelativePath {
		return true
	}
	return left.Type == domain.ManifestDirectory && strings.HasPrefix(right.RelativePath, left.RelativePath+"/") || right.Type == domain.ManifestDirectory && strings.HasPrefix(left.RelativePath, right.RelativePath+"/")
}

func fileTargetKey(target domain.FileTarget) string {
	return string(target.RootID) + "\x00" + target.RelativePath
}

func isZeroFileTarget(target domain.FileTarget) bool {
	return target.RootID == "" && target.RelativePath == ""
}

func validMediaKind(kind domain.MediaKind) bool {
	return kind == domain.MediaMovie || kind == domain.MediaEpisode || kind == domain.MediaSeason || kind == domain.MediaAnime
}

func samePredicate(left, right Predicate) bool {
	leftKey, leftErr := predicateKey(left)
	rightKey, rightErr := predicateKey(right)
	return leftErr == nil && rightErr == nil && leftKey == rightKey
}
