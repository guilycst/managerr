// Package reconciliation turns read-only connector observations into an
// instance-scoped view. It deliberately does not persist a universal
// "tracked" flag: registration, import, availability and request evidence
// remain separate facts for each configured connection.
package reconciliation

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
)

var (
	// ErrInvalidInput means that an observation cannot be safely interpreted.
	ErrInvalidInput = errors.New("invalid reconciliation input")
	// ErrIdentityConflict means observations disagree about one media identity
	// or one instance-scoped tracking fact.
	ErrIdentityConflict = errors.New("reconciliation identity conflict")
	// ErrNoConnections means that a filtered view did not identify any
	// connection whose evidence should be evaluated.
	ErrNoConnections = errors.New("reconciliation requires at least one connection")
)

// Identity is the stable media key used to combine observations. A title is
// display data only; provider ID and kind are the authority-bearing identity.
type Identity struct {
	Kind       domain.MediaKind
	ProviderID string
}

// Validate checks the fields used to correlate records from different
// upstream instances.
func (identity Identity) Validate() error {
	switch identity.Kind {
	case domain.MediaMovie, domain.MediaEpisode, domain.MediaSeason, domain.MediaAnime:
	default:
		return fmt.Errorf("%w: unsupported media kind %q", ErrInvalidInput, identity.Kind)
	}
	if strings.TrimSpace(identity.ProviderID) == "" {
		return fmt.Errorf("%w: provider id is required", ErrInvalidInput)
	}
	return nil
}

// Key is deterministic and keeps equal provider IDs in different media kinds
// separate. The separator cannot be confused with a valid provider ID in the
// key because it is not exposed as an API path.
func (identity Identity) Key() string {
	return string(identity.Kind) + "\x00" + identity.ProviderID
}

// Record is one read-only contribution to an aggregate. Connection scope is
// carried by each TrackingObservation, allowing a single discovered payload
// to be compared with several Arr, Jellyfin or Seerr instances.
type Record struct {
	Identity     Identity
	Title        string
	DiscoveryIDs []domain.RuntimeID
	Tracking     []domain.TrackingObservation
	ObservedAt   time.Time
}

// Validate checks every authority-bearing part of a record. Missing tracking
// dimensions are valid and remain unknown in the resulting instance view.
func (record Record) Validate() error {
	if err := record.Identity.Validate(); err != nil {
		return err
	}
	if record.ObservedAt.IsZero() {
		return fmt.Errorf("%w: record observation time is required", ErrInvalidInput)
	}
	seenDiscovery := make(map[domain.RuntimeID]struct{}, len(record.DiscoveryIDs))
	for _, id := range record.DiscoveryIDs {
		if !id.Valid() {
			return fmt.Errorf("%w: invalid discovery id %q", ErrInvalidInput, id)
		}
		if _, exists := seenDiscovery[id]; exists {
			return fmt.Errorf("%w: duplicate discovery id %q", ErrInvalidInput, id)
		}
		seenDiscovery[id] = struct{}{}
	}
	for index, observation := range record.Tracking {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("%w: tracking observation %d: %w", ErrInvalidInput, index, err)
		}
		if observation.ProviderID != "" && observation.ProviderID != record.Identity.ProviderID {
			return fmt.Errorf("%w: tracking observation %d has provider id %q for %q", ErrIdentityConflict, index, observation.ProviderID, record.Identity.ProviderID)
		}
		if observation.ObservedAt.After(record.ObservedAt) {
			return fmt.Errorf("%w: tracking observation %d is newer than record", ErrInvalidInput, index)
		}
	}
	return nil
}

// DimensionEvidence is the complete view of one tracking dimension for one
// configured connection. Value is a derived summary for filtering; the raw
// observations and their timestamps remain available for UI and audit.
type DimensionEvidence struct {
	Dimension    domain.TrackingDimension
	Value        domain.TrackingValue
	Known        bool
	Reason       string
	Observations []domain.TrackingObservation
}

// InstanceEvidence groups all dimensions for one configured connection. A
// connection with no observation still appears when requested by Input, with
// four explicit unknown dimensions and a reason of observation_missing.
type InstanceEvidence struct {
	ConnectionID domain.ConfigID
	Dimensions   []DimensionEvidence
}

// Aggregate contains all evidence for one media identity. There is
// intentionally no Tracked or Untracked field: callers choose a dimension and
// a set of connections and use ConfirmedAbsent for that filtered view.
type Aggregate struct {
	Identity     Identity
	Title        string
	Titles       []string
	DiscoveryIDs []domain.RuntimeID
	Instances    []InstanceEvidence
	ObservedAt   time.Time
	Conflicts    []Conflict
}

// Conflict describes contradictory evidence without discarding either side.
type Conflict struct {
	Code         string
	ConnectionID domain.ConfigID
	Dimension    domain.TrackingDimension
	Field        string
	Message      string
	Evidence     []string
	Blocking     bool
}

// Input controls one bounded aggregation operation.
type Input struct {
	Records         []Record
	Connections     []domain.ConfigID
	Now             time.Time
	MaxRecords      int
	MaxObservations int
}

const (
	defaultMaxRecords      = 10_000
	defaultMaxObservations = 100_000
)

// Result is a deterministic, sorted aggregate projection.
type Result struct {
	Items      []Aggregate
	Conflicts  []Conflict
	ObservedAt time.Time
}

// Aggregate combines a bounded set of read-only observations. Equal provider
// IDs from different connections never collide because tracking evidence is
// grouped by ConnectionID. Missing or partial evidence is represented as
// unknown and never inferred as absent.
func AggregateRecords(input Input) (Result, error) {
	if input.MaxRecords <= 0 {
		input.MaxRecords = defaultMaxRecords
	}
	if input.MaxObservations <= 0 {
		input.MaxObservations = defaultMaxObservations
	}
	if len(input.Records) > input.MaxRecords {
		return Result{}, fmt.Errorf("%w: record bound %d exceeded", ErrInvalidInput, input.MaxRecords)
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	input.Now = input.Now.UTC()
	expected, err := normalizeConnections(input.Connections)
	if err != nil {
		return Result{}, err
	}
	groups := make(map[string]*aggregateBuilder, len(input.Records))
	observationCount := 0
	for index, record := range input.Records {
		if err := record.Validate(); err != nil {
			return Result{}, fmt.Errorf("record %d: %w", index, err)
		}
		observationCount += len(record.Tracking)
		if observationCount > input.MaxObservations {
			return Result{}, fmt.Errorf("%w: observation bound %d exceeded", ErrInvalidInput, input.MaxObservations)
		}
		key := record.Identity.Key()
		builder := groups[key]
		if builder == nil {
			builder = &aggregateBuilder{identity: record.Identity, titles: make(map[string]struct{})}
			groups[key] = builder
		}
		builder.addRecord(record)
	}
	for _, builder := range groups {
		builder.ensureConnections(expected)
	}
	result := Result{ObservedAt: input.Now}
	result.Items = make([]Aggregate, 0, len(groups))
	for _, builder := range groups {
		item := builder.finish(input.Now)
		result.Items = append(result.Items, item)
		result.Conflicts = append(result.Conflicts, item.Conflicts...)
	}
	sort.Slice(result.Items, func(left, right int) bool {
		return result.Items[left].Identity.Key() < result.Items[right].Identity.Key()
	})
	sortConflicts(result.Conflicts)
	return result, nil
}

// New is a concise alias for AggregateRecords for callers that prefer a
// package constructor style.
func New(input Input) (Result, error) {
	return AggregateRecords(input)
}

type aggregateBuilder struct {
	identity     Identity
	titles       map[string]struct{}
	observations []domain.TrackingObservation
	discoveryIDs map[domain.RuntimeID]struct{}
	connections  map[domain.ConfigID]map[domain.TrackingDimension][]domain.TrackingObservation
	conflicts    []Conflict
	observedAt   time.Time
}

func (builder *aggregateBuilder) addRecord(record Record) {
	if builder.discoveryIDs == nil {
		builder.discoveryIDs = make(map[domain.RuntimeID]struct{})
	}
	if builder.connections == nil {
		builder.connections = make(map[domain.ConfigID]map[domain.TrackingDimension][]domain.TrackingObservation)
	}
	title := strings.TrimSpace(record.Title)
	if title != "" {
		for existing := range builder.titles {
			if existing != title {
				builder.conflicts = append(builder.conflicts, Conflict{
					Code:     "title_disagreement",
					Field:    "title",
					Message:  "observations for one provider identity have different display titles",
					Evidence: []string{existing, title},
					Blocking: false,
				})
			}
		}
		builder.titles[title] = struct{}{}
	}
	if builder.observedAt.IsZero() || record.ObservedAt.After(builder.observedAt) {
		builder.observedAt = record.ObservedAt
	}
	for _, id := range record.DiscoveryIDs {
		builder.discoveryIDs[id] = struct{}{}
	}
	for _, observation := range record.Tracking {
		builder.observations = append(builder.observations, cloneTrackingObservation(observation))
		byDimension := builder.connections[observation.ConnectionID]
		if byDimension == nil {
			byDimension = make(map[domain.TrackingDimension][]domain.TrackingObservation)
			builder.connections[observation.ConnectionID] = byDimension
		}
		byDimension[observation.Dimension] = append(byDimension[observation.Dimension], cloneTrackingObservation(observation))
	}
}

func (builder *aggregateBuilder) ensureConnections(expected []domain.ConfigID) {
	for _, connectionID := range expected {
		if builder.connections[connectionID] == nil {
			builder.connections[connectionID] = make(map[domain.TrackingDimension][]domain.TrackingObservation)
		}
	}
}

func (builder *aggregateBuilder) finish(now time.Time) Aggregate {
	item := Aggregate{Identity: builder.identity, ObservedAt: builder.observedAt}
	for title := range builder.titles {
		item.Titles = append(item.Titles, title)
	}
	sort.Strings(item.Titles)
	if len(item.Titles) > 0 {
		item.Title = item.Titles[0]
	}
	for id := range builder.discoveryIDs {
		item.DiscoveryIDs = append(item.DiscoveryIDs, id)
	}
	sort.Slice(item.DiscoveryIDs, func(left, right int) bool { return item.DiscoveryIDs[left] < item.DiscoveryIDs[right] })
	connections := make([]domain.ConfigID, 0, len(builder.connections))
	for connectionID := range builder.connections {
		connections = append(connections, connectionID)
	}
	sort.Slice(connections, func(left, right int) bool { return connections[left] < connections[right] })
	for _, connectionID := range connections {
		instance := InstanceEvidence{ConnectionID: connectionID}
		for _, dimension := range dimensions() {
			evidence := DimensionEvidence{Dimension: dimension, Value: domain.TrackingUnknown}
			observations := builder.connections[connectionID][dimension]
			if len(observations) == 0 {
				evidence.Reason = "observation_missing"
				instance.Dimensions = append(instance.Dimensions, evidence)
				continue
			}
			for _, observation := range observations {
				evidence.Observations = append(evidence.Observations, cloneTrackingObservation(observation))
			}
			sort.Slice(evidence.Observations, func(left, right int) bool {
				return trackingObservationKey(evidence.Observations[left]) < trackingObservationKey(evidence.Observations[right])
			})
			evidence.Value, evidence.Known = summarize(observations)
			if evidence.Value == domain.TrackingUnknown {
				evidence.Reason = "unknown_or_conflicting_observation"
			}
			if evidence.Value == domain.TrackingAbsent && !absenceEvidenceFresh(observations, now) {
				evidence.Value = domain.TrackingUnknown
				evidence.Known = false
				evidence.Reason = "absence_evidence_stale"
			}
			if conflict, ok := dimensionConflict(connectionID, dimension, observations); ok {
				builder.conflicts = append(builder.conflicts, conflict)
			}
			instance.Dimensions = append(instance.Dimensions, evidence)
		}
		item.Instances = append(item.Instances, instance)
	}
	item.Conflicts = append(item.Conflicts, dedupeConflicts(builder.conflicts)...)
	return item
}

func dimensions() []domain.TrackingDimension {
	return []domain.TrackingDimension{
		domain.TrackingRegistration,
		domain.TrackingImport,
		domain.TrackingAvailability,
		domain.TrackingRequest,
	}
}

func summarize(observations []domain.TrackingObservation) (domain.TrackingValue, bool) {
	if len(observations) == 0 {
		return domain.TrackingUnknown, false
	}
	value := observations[0].Value
	known := value != domain.TrackingUnknown
	for _, observation := range observations[1:] {
		if observation.Value != value {
			return domain.TrackingUnknown, false
		}
		known = known && observation.Value != domain.TrackingUnknown
	}
	if value == domain.TrackingUnknown {
		return domain.TrackingUnknown, false
	}
	return value, known
}

func dimensionConflict(connectionID domain.ConfigID, dimension domain.TrackingDimension, observations []domain.TrackingObservation) (Conflict, bool) {
	if len(observations) < 2 {
		return Conflict{}, false
	}
	var first *domain.TrackingObservation
	for index := range observations {
		observation := &observations[index]
		// Unknown evidence is an explicit lack of knowledge, not a claim that
		// contradicts a known value. It still makes the summary unknown.
		if observation.Value == domain.TrackingUnknown {
			continue
		}
		if first == nil {
			first = observation
			continue
		}
		if observation.Value != first.Value || observation.ExternalID != first.ExternalID || observation.ProviderID != first.ProviderID {
			return Conflict{
				Code:         "contradictory_tracking_evidence",
				ConnectionID: connectionID,
				Dimension:    dimension,
				Field:        "value_or_external_identity",
				Message:      "tracking observations for one connection and dimension disagree",
				Evidence:     []string{trackingObservationKey(*first), trackingObservationKey(*observation)},
				Blocking:     true,
			}, true
		}
	}
	return Conflict{}, false
}

func dedupeConflicts(conflicts []Conflict) []Conflict {
	result := make([]Conflict, 0, len(conflicts))
	seen := make(map[string]struct{}, len(conflicts))
	for _, conflict := range conflicts {
		evidence := append([]string(nil), conflict.Evidence...)
		sort.Strings(evidence)
		key := conflict.Code + "\x00" + string(conflict.ConnectionID) + "\x00" + string(conflict.Dimension) + "\x00" + conflict.Field + "\x00" + conflict.Message + "\x00" + strings.Join(evidence, "\x00")
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		conflict.Evidence = evidence
		result = append(result, conflict)
	}
	sortConflicts(result)
	return result
}

// ConfirmedAbsentAt reports whether every selected connection has a complete,
// non-conflicting absent observation for the requested dimension at now. The
// timestamp is explicit so a caller can make a deterministic filter decision
// and so a later filter cannot reuse an expired absence proof.
func (aggregate Aggregate) ConfirmedAbsentAt(now time.Time, connectionIDs []domain.ConfigID, dimension domain.TrackingDimension) bool {
	if now.IsZero() {
		return false
	}
	now = now.UTC()
	if !validDimension(dimension) || len(connectionIDs) == 0 {
		return false
	}
	seen := make(map[domain.ConfigID]struct{}, len(connectionIDs))
	for _, connectionID := range connectionIDs {
		if !connectionID.Valid() {
			return false
		}
		if _, duplicate := seen[connectionID]; duplicate {
			return false
		}
		seen[connectionID] = struct{}{}
		found := false
		for _, instance := range aggregate.Instances {
			if instance.ConnectionID != connectionID {
				continue
			}
			for _, evidence := range instance.Dimensions {
				if evidence.Dimension != dimension {
					continue
				}
				found = true
				if evidence.Value != domain.TrackingAbsent || !evidence.Known || len(evidence.Observations) == 0 || !absenceEvidenceFresh(evidence.Observations, now) {
					return false
				}
				for _, conflict := range aggregate.Conflicts {
					if conflict.ConnectionID == connectionID && conflict.Dimension == dimension && conflict.Blocking {
						return false
					}
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ConfirmedAbsent reports using the current UTC clock. Prefer
// ConfirmedAbsentAt when the caller already has an aggregation/filter clock.
func (aggregate Aggregate) ConfirmedAbsent(connectionIDs []domain.ConfigID, dimension domain.TrackingDimension) bool {
	return aggregate.ConfirmedAbsentAt(time.Now().UTC(), connectionIDs, dimension)
}

// FilterConfirmedAbsent returns the filtered view commonly shown as
// "untracked". It evaluates each selected dimension independently and keeps
// the original per-instance evidence intact.
func (result Result) FilterConfirmedAbsent(connectionIDs []domain.ConfigID, dimension domain.TrackingDimension) []Aggregate {
	return result.FilterConfirmedAbsentAt(time.Now().UTC(), connectionIDs, dimension)
}

// FilterConfirmedAbsentAt returns the filtered view at an explicit clock. Raw
// observations are retained even when a prior absence proof has aged out.
func (result Result) FilterConfirmedAbsentAt(now time.Time, connectionIDs []domain.ConfigID, dimension domain.TrackingDimension) []Aggregate {
	filtered := make([]Aggregate, 0)
	for _, item := range result.Items {
		if item.ConfirmedAbsentAt(now, connectionIDs, dimension) {
			filtered = append(filtered, cloneAggregate(item))
		}
	}
	return filtered
}

// ConfirmedAbsent is the result-level counterpart to Aggregate.ConfirmedAbsent
// and makes a no-results/invalid-connection decision explicit to callers.
func (result Result) ConfirmedAbsent(connectionIDs []domain.ConfigID, dimension domain.TrackingDimension) ([]Aggregate, error) {
	return result.ConfirmedAbsentAt(time.Now().UTC(), connectionIDs, dimension)
}

// ConfirmedAbsentAt is the result-level counterpart to
// Aggregate.ConfirmedAbsentAt and makes the filter clock explicit.
func (result Result) ConfirmedAbsentAt(now time.Time, connectionIDs []domain.ConfigID, dimension domain.TrackingDimension) ([]Aggregate, error) {
	if len(connectionIDs) == 0 {
		return nil, ErrNoConnections
	}
	for _, connectionID := range connectionIDs {
		if !connectionID.Valid() {
			return nil, fmt.Errorf("%w: invalid connection id %q", ErrInvalidInput, connectionID)
		}
	}
	if !validDimension(dimension) {
		return nil, fmt.Errorf("%w: unsupported tracking dimension %q", ErrInvalidInput, dimension)
	}
	if now.IsZero() {
		return nil, fmt.Errorf("%w: filter time is required", ErrInvalidInput)
	}
	return result.FilterConfirmedAbsentAt(now.UTC(), connectionIDs, dimension), nil
}

func validDimension(dimension domain.TrackingDimension) bool {
	switch dimension {
	case domain.TrackingRegistration, domain.TrackingImport, domain.TrackingAvailability, domain.TrackingRequest:
		return true
	default:
		return false
	}
}

// absenceEvidenceFresh applies the freshness bound at the time a result is
// consumed. TrackingObservation.Validate checks the bound relative to the old
// observation snapshot; that alone cannot keep a cached absence authoritative
// after the snapshot ages out.
func absenceEvidenceFresh(observations []domain.TrackingObservation, now time.Time) bool {
	if now.IsZero() || len(observations) == 0 {
		return false
	}
	now = now.UTC()
	for _, observation := range observations {
		if observation.Value != domain.TrackingAbsent || observation.Coverage == nil || observation.Coverage.Completeness != domain.CompletenessComplete || observation.CoverageMaxAge <= 0 || observation.ObservedAt.After(now) || observation.Coverage.ObservedAt.After(now) {
			return false
		}
		if observation.Coverage.CompletedAt == nil || observation.Coverage.CompletedAt.After(observation.ObservedAt) {
			return false
		}
		if age := now.Sub(observation.Coverage.ObservedAt); age < 0 || age > observation.CoverageMaxAge {
			return false
		}
	}
	return true
}

func normalizeConnections(connections []domain.ConfigID) ([]domain.ConfigID, error) {
	result := append([]domain.ConfigID(nil), connections...)
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	for index, connectionID := range result {
		if !connectionID.Valid() {
			return nil, fmt.Errorf("%w: invalid connection id %q", ErrInvalidInput, connectionID)
		}
		if index > 0 && result[index-1] == connectionID {
			return nil, fmt.Errorf("%w: duplicate connection id %q", ErrInvalidInput, connectionID)
		}
	}
	return result, nil
}

func trackingObservationKey(observation domain.TrackingObservation) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s/%s", observation.ConnectionID, observation.Dimension, observation.Value, observation.ExternalID, observation.ProviderID, observation.ObservedAt.UTC().Format(time.RFC3339Nano))
}

func sortConflicts(conflicts []Conflict) {
	sort.Slice(conflicts, func(left, right int) bool {
		leftKey := conflicts[left].Code + "\x00" + string(conflicts[left].ConnectionID) + "\x00" + string(conflicts[left].Dimension) + "\x00" + conflicts[left].Field + "\x00" + conflicts[left].Message
		rightKey := conflicts[right].Code + "\x00" + string(conflicts[right].ConnectionID) + "\x00" + string(conflicts[right].Dimension) + "\x00" + conflicts[right].Field + "\x00" + conflicts[right].Message
		return leftKey < rightKey
	})
}

func cloneTrackingObservation(observation domain.TrackingObservation) domain.TrackingObservation {
	clone := observation
	clone.Evidence = append([]string(nil), observation.Evidence...)
	if observation.Coverage != nil {
		coverage := *observation.Coverage
		coverage.ReasonCodes = append([]string(nil), observation.Coverage.ReasonCodes...)
		if observation.Coverage.StartedAt != nil {
			startedAt := *observation.Coverage.StartedAt
			coverage.StartedAt = &startedAt
		}
		if observation.Coverage.CompletedAt != nil {
			completedAt := *observation.Coverage.CompletedAt
			coverage.CompletedAt = &completedAt
		}
		clone.Coverage = &coverage
	}
	return clone
}

func cloneAggregate(aggregate Aggregate) Aggregate {
	clone := aggregate
	clone.Titles = append([]string(nil), aggregate.Titles...)
	clone.DiscoveryIDs = append([]domain.RuntimeID(nil), aggregate.DiscoveryIDs...)
	clone.Instances = make([]InstanceEvidence, len(aggregate.Instances))
	for index, instance := range aggregate.Instances {
		clone.Instances[index] = InstanceEvidence{ConnectionID: instance.ConnectionID, Dimensions: make([]DimensionEvidence, len(instance.Dimensions))}
		for dimensionIndex, evidence := range instance.Dimensions {
			clone.Instances[index].Dimensions[dimensionIndex] = DimensionEvidence{Dimension: evidence.Dimension, Value: evidence.Value, Known: evidence.Known, Reason: evidence.Reason}
			for _, observation := range evidence.Observations {
				clone.Instances[index].Dimensions[dimensionIndex].Observations = append(clone.Instances[index].Dimensions[dimensionIndex].Observations, cloneTrackingObservation(observation))
			}
		}
	}
	clone.Conflicts = append([]Conflict(nil), aggregate.Conflicts...)
	for index := range clone.Conflicts {
		clone.Conflicts[index].Evidence = append([]string(nil), aggregate.Conflicts[index].Evidence...)
	}
	return clone
}
