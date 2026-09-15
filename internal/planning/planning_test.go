package planning

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	planningDownloadRoot domain.ConfigID = "downloads"
	planningLibraryRoot  domain.ConfigID = "library"
	planningConnection   domain.ConfigID = "sonarr-main"
)

var planningNow = time.Date(2026, time.June, 7, 8, 9, 10, 0, time.UTC)

func TestBuildCopyPlanBindsExactManifestAndDeterministicIntent(t *testing.T) {
	source := planningTarget(planningDownloadRoot, "incoming/Film.mkv")
	destination := planningTarget(planningLibraryRoot, "Movies/Film.mkv")
	manifest := []domain.FileManifestEntry{planningFile(source, 10, "source-inode", planningNow)}
	digest := planningDigest("a")
	desired, err := NewDesiredState(NewFileContentPredicate(destination, 10, digest))
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		ID:       "plan-copy-1",
		Action:   domain.ActionFSCopy,
		Desired:  desired,
		Manifest: manifest,
		Binding: Binding{
			SourceID: "discovery-1", SourceRevision: "manifest-4", SourceDigest: planningDigest("b"),
			ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-2"},
			MappingRevisions:    map[domain.ConfigID]string{"mapping-main": "map-3"},
		},
		Preconditions:    []Precondition{{Kind: "source_identity", Target: source.RelativePath, Expected: "source-inode", Required: true}},
		Impacts:          []Impact{{Kind: "jellyfin_availability", Target: "jellyfin-main/tmdb-1", Message: "initially stale"}},
		EstimatedBytes:   10,
		RequiredApproval: ApprovalAction,
		CreatedAt:        planningNow,
		ExpiresAt:        planningNow.Add(15 * time.Minute),
	}
	plan, err := Build(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusReady || plan.Digest == "" {
		t.Fatalf("plan status/digest = %q/%q", plan.Status, plan.Digest)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(plan.Manifest) != 1 || plan.Manifest[0].FileIdentity != "source-inode" {
		t.Fatalf("plan manifest lost exact identity: %#v", plan.Manifest)
	}
	// Predicate ordering and display prose are not authority-bearing. The
	// canonical plan digest remains stable when those presentation details vary.
	request.Impacts[0].Message = "availability observed later"
	second, err := Build(request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest != plan.Digest {
		t.Fatalf("display-only impact changed digest: %s != %s", second.Digest, plan.Digest)
	}
}

func TestNewRevisionIsImmutableAndBindsChangedSourceOrMapping(t *testing.T) {
	target := planningTarget(planningLibraryRoot, "Movies/Film.mkv")
	desired, err := NewDesiredState(NewFileContentPredicate(target, 10, planningDigest("a")))
	if err != nil {
		t.Fatal(err)
	}
	baseRequest := Request{
		ID: "plan-revision-1", Action: domain.ActionFSCopy, Desired: desired,
		Manifest:  []domain.FileManifestEntry{planningFile(planningTarget(planningDownloadRoot, "incoming/Film.mkv"), 10, "inode-1", planningNow)},
		Binding:   Binding{SourceID: "discovery-1", SourceRevision: "manifest-1", MappingRevisions: map[domain.ConfigID]string{"mapping-main": "map-1"}},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	}
	first, err := Build(baseRequest)
	if err != nil {
		t.Fatal(err)
	}
	changed := baseRequest
	changed.Binding = cloneBinding(baseRequest.Binding)
	changed.Binding.SourceRevision = "manifest-2"
	changed.Binding.MappingRevisions["mapping-main"] = "map-2"
	second, err := NewRevision(first, changed)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision+1 || second.ID != first.ID || second.Digest == first.Digest {
		t.Fatalf("revision = %#v, first = %#v", second, first)
	}
	if first.Revision != 1 || first.Binding.SourceRevision != "manifest-1" || first.Binding.MappingRevisions["mapping-main"] != "map-1" {
		t.Fatalf("previous plan was mutated: %#v", first)
	}
}

func TestBuildBlocksAmbiguousEpisodeAndUnmatchedSubtitle(t *testing.T) {
	source := planningTarget(planningDownloadRoot, "incoming/Show.S01E01.mkv")
	selection := ImportSelection{Source: source, EpisodeIDs: []string{"episode-1"}, Confidence: MappingAmbiguous}
	importPredicate := NewImportPredicate(planningConnection, "series-1", []ImportSelection{selection}, "copy")
	desired, err := NewDesiredState(importPredicate)
	if !errors.Is(err, ErrAmbiguousMapping) {
		t.Fatalf("ambiguous mapping error = %v, want ErrAmbiguousMapping", err)
	}
	if desired.Predicates != nil {
		t.Fatalf("invalid desired state should not be returned: %#v", desired)
	}

	subtitle := NewSubtitlePredicate(planningConnection, planningTarget(planningDownloadRoot, "incoming/Show.pt.srt"), nil, "pair-1", "pt-BR", true, false)
	if err := subtitle.Validate(); !errors.Is(err, ErrAmbiguousMapping) {
		t.Fatalf("unmatched subtitle error = %v, want ErrAmbiguousMapping", err)
	}
}

func TestImportReadBackIsPerFileAndPreservesSubtitleLabels(t *testing.T) {
	video := ImportSelection{Source: planningTarget(planningDownloadRoot, "incoming/Show.S01E01E02.mkv"), EpisodeIDs: []string{"episode-1", "episode-2"}, Confidence: MappingExact}
	subtitle := ImportSelection{Source: planningTarget(planningDownloadRoot, "incoming/Show.pt.forced.idx"), Subtitle: true, PairID: "idx-sub-1", Language: "pt-BR", Forced: true, HearingImpaired: true, VideoPaths: []domain.FileTarget{video.Source}, Confidence: MappingExact}
	desired, err := NewDesiredState(NewImportPredicate(planningConnection, "series-1", []ImportSelection{video, subtitle}, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := Evaluate(desired, ObservedState{Imports: []ImportObservation{{ConnectionID: planningConnection, RegisteredExternalID: "series-1", Present: true, Known: true, Files: []ImportSelection{video}}}})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.State != SatisfactionConflict || len(evaluation.Results) != 1 || evaluation.Results[0].State != SatisfactionConflict {
		t.Fatalf("partial import evaluation = %#v", evaluation)
	}
	if got := desired.Predicates[0].Import.Files[1].PairID; got != "idx-sub-1" {
		t.Fatalf("subtitle pair label was not retained: %q", got)
	}
	if !desired.Predicates[0].Import.Files[1].Forced || !desired.Predicates[0].Import.Files[1].HearingImpaired {
		t.Fatal("subtitle forced/SDH labels were not retained")
	}
}

func TestEvaluateDistinguishesCopyContentAndHardlinkIdentity(t *testing.T) {
	target := planningTarget(planningLibraryRoot, "Movies/Film.mkv")
	desiredCopy, err := NewDesiredState(NewFileContentPredicate(target, 10, planningDigest("a")))
	if err != nil {
		t.Fatal(err)
	}
	copyResult, err := Evaluate(desiredCopy, ObservedState{Files: []FileObservation{{Target: target, Known: true, Exists: true, Size: 10, Digest: planningDigest("b"), FileIdentity: "different-inode"}}})
	if err != nil {
		t.Fatal(err)
	}
	if copyResult.State != SatisfactionConflict {
		t.Fatalf("different bytes with same size evaluated as %q", copyResult.State)
	}

	source := planningTarget(planningDownloadRoot, "incoming/Film.mkv")
	desiredHardlink, err := NewDesiredState(NewHardlinkPredicate(source, target, "source-inode"))
	if err != nil {
		t.Fatal(err)
	}
	hardlinkResult, err := Evaluate(desiredHardlink, ObservedState{Files: []FileObservation{
		{Target: source, Known: true, Exists: true, Size: 10, Digest: planningDigest("a"), FileIdentity: "source-inode"},
		{Target: target, Known: true, Exists: true, Size: 10, Digest: planningDigest("a"), FileIdentity: "different-inode"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if hardlinkResult.State != SatisfactionConflict || hardlinkResult.Results[0].State == SatisfactionAlreadySatisfied {
		t.Fatalf("different inode incorrectly satisfied hardlink: %#v", hardlinkResult)
	}
	// Equal bytes are sufficient for a copy predicate, but never for a
	// hardlink predicate unless object identity also matches.
	equalBytes, err := Evaluate(desiredCopy, ObservedState{Files: []FileObservation{{Target: target, Known: true, Exists: true, Size: 10, Digest: planningDigest("a"), FileIdentity: "different-inode"}}})
	if err != nil {
		t.Fatal(err)
	}
	if equalBytes.State != SatisfactionAlreadySatisfied {
		t.Fatalf("equal content should satisfy copy: %#v", equalBytes)
	}
}

func TestPlanApprovalAndCurrentBindingRejectStaleOrForgedIntent(t *testing.T) {
	source := planningTarget(planningDownloadRoot, "incoming/Film.mkv")
	destination := planningTarget(planningLibraryRoot, "Movies/Film.mkv")
	desired, err := NewDesiredState(NewFileContentPredicate(destination, 10, planningDigest("a")))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(Request{ID: "plan-approval-1", Action: domain.ActionFSCopy, Desired: desired, Manifest: []domain.FileManifestEntry{planningFile(source, 10, "inode-1", planningNow)}, Binding: Binding{SourceID: "discovery-1", SourceRevision: "manifest-1"}, CreatedAt: planningNow, ExpiresAt: planningNow.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	approval := Approval{PlanID: plan.ID, Revision: plan.Revision, Digest: plan.Digest, At: planningNow.Add(time.Minute)}
	if err := plan.ValidateApproval(approval, planningNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	approval.Digest = planningDigest("f")
	if !errors.Is(plan.ValidateApproval(approval, planningNow.Add(time.Minute)), ErrPlanDigest) {
		t.Fatal("forged approval digest was accepted")
	}
	current := CurrentState{Binding: Binding{SourceID: "discovery-1", SourceRevision: "manifest-2"}, Manifest: plan.Manifest, Desired: plan.Desired, ObservedAt: planningNow.Add(2 * time.Minute)}
	conflicts := plan.CheckCurrent(current)
	if len(conflicts) == 0 || conflicts[0].Code != "source_changed" {
		t.Fatalf("source change conflicts = %#v", conflicts)
	}
	if !errors.Is(plan.ValidateCurrent(current), ErrPlanBindingChanged) {
		t.Fatal("changed source did not reject current plan")
	}
	if !errors.Is(plan.ValidateApproval(Approval{PlanID: plan.ID, Revision: plan.Revision, Digest: plan.Digest, At: plan.ExpiresAt}, plan.ExpiresAt), ErrPlanExpired) {
		t.Fatal("approval at expiry was accepted")
	}
}

func TestBuildReturnsInvalidPlanForExplicitConflictWithoutDispatchShape(t *testing.T) {
	target := planningTarget(planningLibraryRoot, "Movies/Film.mkv")
	desired, err := NewDesiredState(NewFileContentPredicate(target, 10, planningDigest("a")))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(Request{ID: "plan-conflict-1", Action: domain.ActionFSCopy, Desired: desired, Manifest: []domain.FileManifestEntry{planningFile(planningTarget(planningDownloadRoot, "incoming/Film.mkv"), 10, "inode-1", planningNow)}, Binding: Binding{SourceID: "discovery-1", SourceRevision: "manifest-1"}, Conflicts: []Conflict{{Code: "destination_conflict", Target: target.RelativePath, Message: "destination has different bytes", Blocking: true}}, CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour)})
	if !errors.Is(err, ErrPlanConflict) || plan.Status != StatusInvalid || len(plan.BlockingIssues) != 1 {
		t.Fatalf("conflicted plan/error = %#v/%v", plan, err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(plan.ValidateApproval(Approval{PlanID: plan.ID, Revision: plan.Revision, Digest: plan.Digest, At: planningNow}, planningNow), ErrPlanConflict) {
		t.Fatal("blocking plan could be approved")
	}
}

func TestPlanValidationDetectsAuthorityMutationButIgnoresDisplayText(t *testing.T) {
	target := planningTarget(planningLibraryRoot, "Movies/Film.mkv")
	desired, err := NewDesiredState(NewFileContentPredicate(target, 10, planningDigest("a")))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(Request{ID: "plan-mutation-1", Action: domain.ActionFSCopy, Desired: desired, Manifest: []domain.FileManifestEntry{planningFile(planningTarget(planningDownloadRoot, "incoming/Film.mkv"), 10, "inode-1", planningNow)}, Binding: Binding{SourceID: "discovery-1", SourceRevision: "manifest-1"}, Impacts: []Impact{{Kind: "availability", Target: "jellyfin-main/1", Message: "old display"}}, CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	plan.Impacts[0].Message = "new display"
	if err := plan.Validate(); err != nil {
		t.Fatalf("display-only mutation invalidated plan: %v", err)
	}
	plan.Binding.SourceRevision = "manifest-2"
	if !errors.Is(plan.Validate(), ErrPlanDigest) {
		t.Fatal("authority-bearing source revision mutation was not detected")
	}
}

func TestValidateManifestIsExactAndBounded(t *testing.T) {
	first := planningFile(planningTarget(planningDownloadRoot, "incoming/a.mkv"), 10, "inode-a", planningNow)
	second := planningFile(planningTarget(planningDownloadRoot, "incoming/a.mkv"), 10, "inode-b", planningNow)
	if err := ValidateManifest([]domain.FileManifestEntry{first, second}, true, ManifestLimits{}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("duplicate manifest path error = %v", err)
	}
	if err := ValidateManifest([]domain.FileManifestEntry{first}, true, ManifestLimits{MaxEntries: 1, MaxBytes: 9}); !errors.Is(err, ErrManifestLimit) {
		t.Fatalf("manifest byte bound error = %v", err)
	}
	directory := domain.FileManifestEntry{RootID: planningDownloadRoot, RelativePath: "incoming/pack", Type: domain.ManifestDirectory, FileIdentity: "dir-inode", Role: domain.RoleVideo, ObservedAt: planningNow}
	if err := ValidateManifest([]domain.FileManifestEntry{directory}, false, ManifestLimits{}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("directory without explicit children was accepted: %v", err)
	}
}

func TestRegistrationFieldsAreSemanticAndUnknownAvailabilityStaysUnknown(t *testing.T) {
	monitored := false
	registration := NewRegistrationPredicate(planningConnection, "tvdb-1", domain.MediaEpisode, "series-1", ports.RegistrationFields{RootFolder: "/tv", QualityProfileID: "standard", Monitored: &monitored})
	desired, err := NewDesiredState(registration, NewAvailabilityPredicate("jellyfin-main", "tvdb-1", "item-1", "present"))
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := Evaluate(desired, ObservedState{
		Registrations: []RegistrationObservation{{ConnectionID: planningConnection, ProviderID: "tvdb-1", Kind: domain.MediaEpisode, ExternalID: "series-1", Present: true, Known: true, Fields: ports.RegistrationFields{RootFolder: "/tv", QualityProfileID: "standard", Monitored: &monitored, SeriesType: "standard"}}},
		Services:      []ServiceObservation{{ConnectionID: "jellyfin-main", ProviderID: "tvdb-1", ExternalID: "item-1", Known: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.State != SatisfactionUnknown || evaluation.Results[0].State != SatisfactionAlreadySatisfied || evaluation.Results[1].State != SatisfactionUnknown {
		t.Fatalf("registration/availability evaluation = %#v", evaluation)
	}
}

func TestEvaluateMissingContentDigestDoesNotClaimCopy(t *testing.T) {
	target := planningTarget(planningLibraryRoot, "Movies/Film.mkv")
	desired, err := NewDesiredState(NewFileContentPredicate(target, 10, planningDigest("a")))
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := Evaluate(desired, ObservedState{Files: []FileObservation{{Target: target, Known: true, Exists: true, Size: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.State != SatisfactionUnknown {
		t.Fatalf("missing digest was treated as %q, want unknown", evaluation.State)
	}
}

func planningTarget(root domain.ConfigID, relative string) domain.FileTarget {
	return domain.FileTarget{RootID: root, RelativePath: relative}
}

func planningFile(target domain.FileTarget, size int64, identity string, observedAt time.Time) domain.FileManifestEntry {
	return domain.FileManifestEntry{RootID: target.RootID, RelativePath: target.RelativePath, Type: domain.ManifestFile, Size: size, Digest: planningDigest("a"), FileIdentity: identity, Role: domain.RoleVideo, ObservedAt: observedAt}
}

func planningDigest(value string) string {
	return "sha256:" + strings.Repeat(value, 64)
}

// Keep the ports import exercised by the registration predicate contract in
// this package's public test helpers.
var _ = ports.RegistrationFields{}
