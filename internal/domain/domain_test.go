package domain

import (
	"strings"
	"testing"
	"time"
)

func TestRuntimeAndConfigIDs(t *testing.T) {
	id, err := NewRuntimeID()
	if err != nil || !id.Valid() {
		t.Fatalf("generated runtime id is invalid: %q, %v", id, err)
	}
	if _, err := ParseRuntimeID(strings.ToUpper(id.String())); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"radarr-main", "a.b_2", "0"} {
		if id, err := ParseConfigID(value); err != nil || !id.Valid() {
			t.Fatalf("valid config id %q rejected: %q, %v", value, id, err)
		}
	}
	for _, value := range []string{"", "-bad", "bad-", "UPPER", "a/b", " spaced"} {
		if _, err := ParseConfigID(value); err == nil {
			t.Fatalf("invalid config id %q accepted", value)
		}
	}
}

func TestManifestAndTrackingValidation(t *testing.T) {
	root, err := ParseConfigID("downloads")
	if err != nil {
		t.Fatal(err)
	}
	entry := FileManifestEntry{
		RootID:       root,
		RelativePath: "Example Film/movie.mkv",
		Type:         ManifestFile,
		Size:         10,
		ObservedAt:   time.Now(),
	}
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", "./movie.mkv", "/absolute", "movie//part", ""} {
		if err := ValidateRelativePath(path); err == nil {
			t.Fatalf("unsafe path %q accepted", path)
		}
	}
	observation := TrackingObservation{
		ConnectionID: root,
		Dimension:    TrackingRegistration,
		Value:        TrackingUnknown,
		ObservedAt:   time.Now(),
	}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Provenance{ClientItemID: "same-item"}).Validate(); err == nil {
		t.Fatal("client provenance without a connection was accepted")
	}
	if err := (Provenance{ConnectionID: root, ClientItemID: "same-item"}).Validate(); err != nil {
		t.Fatal(err)
	}

	coverageID, err := NewRuntimeID()
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Now()
	coverage := Coverage{
		SourceID:     coverageID,
		ConnectionID: root,
		Completeness: CompletenessComplete,
		CompletedAt: func() *time.Time {
			completed := observedAt.Add(-time.Second)
			return &completed
		}(),
		ObservedAt: observedAt.Add(-time.Second),
	}
	absent := TrackingObservation{
		ConnectionID:   root,
		Dimension:      TrackingRegistration,
		Value:          TrackingAbsent,
		ObservedAt:     observedAt,
		CoverageID:     coverageID,
		Coverage:       &coverage,
		CoverageMaxAge: time.Minute,
	}
	if err := absent.Validate(); err != nil {
		t.Fatal(err)
	}
	partial := coverage
	partial.Completeness = CompletenessPartial
	absent.Coverage = &partial
	if err := absent.Validate(); err == nil {
		t.Fatal("absent tracking accepted partial coverage")
	}
	absent.Coverage = nil
	if err := absent.Validate(); err == nil {
		t.Fatal("absent tracking accepted missing coverage")
	}
	otherRoot, err := ParseConfigID("other")
	if err != nil {
		t.Fatal(err)
	}
	wrongScope := coverage
	wrongScope.ConnectionID = otherRoot
	absent.Coverage = &wrongScope
	if err := absent.Validate(); err == nil {
		t.Fatal("absent tracking accepted coverage from another connection")
	}
	stale := coverage
	stale.ObservedAt = observedAt.Add(-2 * time.Minute)
	stale.CompletedAt = func() *time.Time {
		completed := stale.ObservedAt
		return &completed
	}()
	absent.Coverage = &stale
	if err := absent.Validate(); err == nil {
		t.Fatal("absent tracking accepted stale coverage")
	}
}

func TestActionTransitions(t *testing.T) {
	if !CanTransition(ActionQueued, ActionRunning) || !CanTransition(ActionRunning, ActionReconciling) {
		t.Fatal("expected active action transitions")
	}
	if CanTransition(ActionReconciling, ActionRunning) || CanTransition(ActionReconciling, ActionFailed) {
		t.Fatal("reconciliation must not dispatch or hide an uncertain write")
	}
	if CanTransition(ActionSucceeded, ActionRunning) {
		t.Fatal("terminal action must not be reopened")
	}
	if !CanTransitionWorkflow(WorkflowAwaitingApproval, WorkflowRunning) || !CanTransitionStep(StepBlocked, StepQueued) || !CanTransitionAttempt(AttemptRunning, AttemptReconciling) {
		t.Fatal("expected workflow, step and attempt transitions")
	}
}
