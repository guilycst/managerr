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
	for _, value := range []string{"", "-bad", "bad-", "UPPER", "a/b"} {
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
}

func TestActionTransitions(t *testing.T) {
	if !CanTransition(ActionQueued, ActionRunning) || !CanTransition(ActionRunning, ActionReconciling) {
		t.Fatal("expected active action transitions")
	}
	if CanTransition(ActionSucceeded, ActionRunning) {
		t.Fatal("terminal action must not be reopened")
	}
	if !CanTransitionWorkflow(WorkflowAwaitingApproval, WorkflowRunning) || !CanTransitionStep(StepBlocked, StepQueued) || !CanTransitionAttempt(AttemptRunning, AttemptReconciling) {
		t.Fatal("expected workflow, step and attempt transitions")
	}
}
