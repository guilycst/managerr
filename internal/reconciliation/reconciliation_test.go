package reconciliation

import (
	"errors"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
)

const (
	reconciliationConnectionA domain.ConfigID = "radarr-main"
	reconciliationConnectionB domain.ConfigID = "radarr-backup"
)

var (
	reconciliationSourceID  domain.RuntimeID = "11111111-1111-4111-8111-111111111111"
	reconciliationCoverageA domain.RuntimeID = "22222222-2222-4222-8222-222222222222"
	reconciliationCoverageB domain.RuntimeID = "33333333-3333-4333-8333-333333333333"
)

func TestAggregateKeepsProviderIdentityScopedToConnection(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	result, err := AggregateRecords(Input{
		Now:         now,
		Connections: []domain.ConfigID{reconciliationConnectionB, reconciliationConnectionA},
		Records: []Record{
			{
				Identity:     Identity{Kind: domain.MediaMovie, ProviderID: "tmdb-42"},
				Title:        "Synthetic Film",
				DiscoveryIDs: []domain.RuntimeID{reconciliationSourceID},
				ObservedAt:   now,
				Tracking: []domain.TrackingObservation{
					trackingObservation(t, reconciliationConnectionA, domain.TrackingRegistration, domain.TrackingPresent, "arr-movie-a", now, ""),
					trackingObservation(t, reconciliationConnectionB, domain.TrackingRegistration, domain.TrackingAbsent, "", now, "b"),
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("got %d media aggregates, want one", len(result.Items))
	}
	item := result.Items[0]
	if item.Identity.ProviderID != "tmdb-42" || item.Identity.Kind != domain.MediaMovie {
		t.Fatalf("unexpected identity: %#v", item.Identity)
	}
	if len(item.Instances) != 2 {
		t.Fatalf("got %d instances, want two: %#v", len(item.Instances), item.Instances)
	}
	if item.Instances[0].ConnectionID != reconciliationConnectionB || item.Instances[1].ConnectionID != reconciliationConnectionA {
		t.Fatalf("instances are not deterministically sorted: %#v", item.Instances)
	}
	if got := item.Instances[1].Dimensions[0].Value; got != domain.TrackingPresent {
		t.Fatalf("radarr-main registration = %q, want present", got)
	}
	if got := item.Instances[0].Dimensions[0].Value; got != domain.TrackingAbsent {
		t.Fatalf("radarr-backup registration = %q, want absent", got)
	}
	if item.ConfirmedAbsent([]domain.ConfigID{reconciliationConnectionB}, domain.TrackingRegistration) != true {
		t.Fatal("complete absent evidence should support a filtered untracked view")
	}
	if item.ConfirmedAbsent([]domain.ConfigID{reconciliationConnectionA, reconciliationConnectionB}, domain.TrackingRegistration) {
		t.Fatal("present evidence in one selected instance must prevent a universal untracked result")
	}
}

func TestAggregateMissingAndUnknownEvidenceNeverBecomesAbsent(t *testing.T) {
	now := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	result, err := AggregateRecords(Input{
		Now:         now,
		Connections: []domain.ConfigID{reconciliationConnectionA, reconciliationConnectionB},
		Records: []Record{{
			Identity:   Identity{Kind: domain.MediaEpisode, ProviderID: "tvdb-7"},
			ObservedAt: now,
			Tracking: []domain.TrackingObservation{
				trackingObservation(t, reconciliationConnectionA, domain.TrackingImport, domain.TrackingUnknown, "", now, "client unavailable"),
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := result.Items[0]
	if item.ConfirmedAbsent([]domain.ConfigID{reconciliationConnectionA}, domain.TrackingImport) {
		t.Fatal("unknown evidence must not be filtered as absent")
	}
	if item.ConfirmedAbsent([]domain.ConfigID{reconciliationConnectionB}, domain.TrackingImport) {
		t.Fatal("missing connection evidence must not be filtered as absent")
	}
	for _, instance := range item.Instances {
		if instance.ConnectionID == reconciliationConnectionB && instance.Dimensions[1].Reason != "observation_missing" {
			t.Fatalf("missing import reason = %q", instance.Dimensions[1].Reason)
		}
	}
}

func TestAggregateContradictoryEvidenceIsRetainedAsConflict(t *testing.T) {
	now := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	first := trackingObservation(t, reconciliationConnectionA, domain.TrackingImport, domain.TrackingPresent, "arr-1", now, "first")
	second := trackingObservation(t, reconciliationConnectionA, domain.TrackingImport, domain.TrackingAbsent, "", now, "second")
	second.CoverageID = reconciliationCoverageA
	second.Coverage = completeCoverage(reconciliationConnectionA, reconciliationCoverageA, now)
	second.CoverageMaxAge = time.Hour
	result, err := AggregateRecords(Input{Now: now, Records: []Record{{
		Identity:   Identity{Kind: domain.MediaMovie, ProviderID: "tmdb-9"},
		ObservedAt: now,
		Tracking:   []domain.TrackingObservation{first, second},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || !result.Conflicts[0].Blocking {
		t.Fatalf("conflicts = %#v, want one blocking conflict", result.Conflicts)
	}
	if got := result.Items[0].Instances[0].Dimensions[1].Value; got != domain.TrackingUnknown {
		t.Fatalf("contradictory import summary = %q, want unknown", got)
	}
	if len(result.Items[0].Instances[0].Dimensions[1].Observations) != 2 {
		t.Fatal("both contradictory observations must remain available for review")
	}
}

func TestAggregateTitleDisagreementIsExplicitButDoesNotChangeIdentity(t *testing.T) {
	now := time.Date(2026, time.April, 5, 6, 7, 8, 0, time.UTC)
	result, err := AggregateRecords(Input{Now: now, Records: []Record{
		{Identity: Identity{Kind: domain.MediaMovie, ProviderID: "tmdb-10"}, Title: "First", ObservedAt: now},
		{Identity: Identity{Kind: domain.MediaMovie, ProviderID: "tmdb-10"}, Title: "Second", ObservedAt: now},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items[0].Titles) != 2 || result.Items[0].Title != "First" {
		t.Fatalf("titles = %#v, title = %q", result.Items[0].Titles, result.Items[0].Title)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].Code != "title_disagreement" || result.Conflicts[0].Blocking {
		t.Fatalf("title conflict = %#v", result.Conflicts)
	}
}

func TestAggregateRejectsProviderMismatchAndBounds(t *testing.T) {
	now := time.Date(2026, time.May, 6, 7, 8, 9, 0, time.UTC)
	bad := trackingObservation(t, reconciliationConnectionA, domain.TrackingRegistration, domain.TrackingPresent, "arr-1", now, "")
	bad.ProviderID = "tmdb-other"
	_, err := AggregateRecords(Input{Now: now, Records: []Record{{Identity: Identity{Kind: domain.MediaMovie, ProviderID: "tmdb-11"}, ObservedAt: now, Tracking: []domain.TrackingObservation{bad}}}})
	if !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("provider mismatch error = %v, want identity conflict", err)
	}
	_, err = AggregateRecords(Input{Now: now, MaxRecords: 1, Records: []Record{
		{Identity: Identity{Kind: domain.MediaMovie, ProviderID: "tmdb-11"}, ObservedAt: now},
		{Identity: Identity{Kind: domain.MediaMovie, ProviderID: "tmdb-12"}, ObservedAt: now},
	}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("record bound error = %v, want invalid input", err)
	}
}

func trackingObservation(t *testing.T, connectionID domain.ConfigID, dimension domain.TrackingDimension, value domain.TrackingValue, externalID string, observedAt time.Time, evidence string) domain.TrackingObservation {
	t.Helper()
	observation := domain.TrackingObservation{ConnectionID: connectionID, Dimension: dimension, Value: value, ExternalID: externalID, ObservedAt: observedAt}
	if evidence != "" {
		observation.Evidence = []string{evidence}
	}
	if value == domain.TrackingAbsent {
		coverageID := reconciliationCoverageB
		if connectionID == reconciliationConnectionA {
			coverageID = reconciliationCoverageA
		}
		observation.CoverageID = coverageID
		observation.Coverage = completeCoverage(connectionID, coverageID, observedAt)
		observation.CoverageMaxAge = time.Hour
	}
	return observation
}

func completeCoverage(connectionID domain.ConfigID, sourceID domain.RuntimeID, observedAt time.Time) *domain.Coverage {
	started := observedAt.Add(-time.Minute)
	completed := observedAt.Add(-time.Second)
	return &domain.Coverage{SourceID: sourceID, ConnectionID: connectionID, Completeness: domain.CompletenessComplete, ObservedCount: 1, StartedAt: &started, CompletedAt: &completed, ObservedAt: completed}
}
