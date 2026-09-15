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
	digest := plan.Digest
	plan.Status = StatusReady
	plan.BlockingIssues = nil
	if err := plan.Validate(); err == nil {
		t.Fatal("blocking conflict was bypassed by mutable derived fields")
	}
	if plan.Digest != digest {
		t.Fatal("derived status mutation unexpectedly changed the original digest")
	}
	if err := plan.ValidateApproval(Approval{PlanID: plan.ID, Revision: plan.Revision, Digest: digest, At: planningNow}, planningNow); err == nil {
		t.Fatal("blocking conflict bypass reached approval")
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

func TestArrImportReferencesOnlyFlattenedExactManifestMembers(t *testing.T) {
	source := planningTarget(planningDownloadRoot, "incoming/Film.mkv")
	desired := NewImportPredicate(planningConnection, "movie-1", []ImportSelection{{
		Source:           planningTarget(planningDownloadRoot, "incoming/unapproved.mkv"),
		MovieOrEpisodeID: "movie-1",
		Confidence:       MappingExact,
	}}, "copy")
	state, err := NewDesiredState(desired)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(Request{
		ID: "plan-import-outside-manifest", Action: domain.ActionArrImport, Desired: state,
		Manifest: []domain.FileManifestEntry{planningFile(source, 10, "source-inode", planningNow)},
		Binding: Binding{
			SourceID: "discovery-1", SourceRevision: "manifest-1",
			ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"},
			MappingRevisions:    map[domain.ConfigID]string{"mapping-main": "map-1"},
			MappingScopes:       []MappingScope{planningMappingScope(planningDownloadRoot)},
		},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("outside-manifest import error = %v, want ErrInvalidPlan", err)
	}

	video := planningTarget(planningDownloadRoot, "incoming/pack/Film.mkv")
	subtitle := planningTarget(planningDownloadRoot, "incoming/pack/Film.pt.srt")
	directory := domain.FileManifestEntry{
		RootID: planningDownloadRoot, RelativePath: "incoming/pack", Type: domain.ManifestDirectory,
		FileIdentity: "pack-inode", Role: domain.RoleVideo, ObservedAt: planningNow,
		Children: []domain.FileManifestEntry{
			planningFile(video, 10, "video-inode", planningNow),
			planningSubtitleFile(subtitle, 5, "subtitle-inode", planningNow),
		},
	}
	importState, err := NewDesiredState(NewImportPredicate(planningConnection, "series-1", []ImportSelection{
		{Source: video, EpisodeIDs: []string{"episode-1"}, Confidence: MappingExact},
		{Source: subtitle, Subtitle: true, PairID: "pair-1", Language: "pt-BR", VideoPaths: []domain.FileTarget{video}, Confidence: MappingExact},
	}, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(Request{
		ID: "plan-import-directory-child", Action: domain.ActionArrImport, Desired: importState,
		Manifest: []domain.FileManifestEntry{directory},
		Binding: Binding{
			SourceID: "discovery-1", SourceRevision: "manifest-1",
			ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"},
			MappingRevisions:    map[domain.ConfigID]string{"mapping-main": "map-1"},
			MappingScopes:       []MappingScope{planningMappingScope(planningDownloadRoot)},
		},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("directory child import was rejected: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("directory child plan failed self-validation: %v", err)
	}

	missingVideoState, err := NewDesiredState(NewImportPredicate(planningConnection, "series-1", []ImportSelection{
		{Source: subtitle, Subtitle: true, PairID: "pair-1", Language: "pt-BR", VideoPaths: []domain.FileTarget{planningTarget(planningDownloadRoot, "incoming/pack/missing.mkv")}, Confidence: MappingExact},
	}, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(Request{
		ID: "plan-import-missing-video", Action: domain.ActionArrImport, Desired: missingVideoState,
		Manifest: []domain.FileManifestEntry{directory},
		Binding: Binding{
			SourceID: "discovery-1", SourceRevision: "manifest-1",
			ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"},
			MappingRevisions:    map[domain.ConfigID]string{"mapping-main": "map-1"},
			MappingScopes:       []MappingScope{planningMappingScope(planningDownloadRoot)},
		},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("nonexistent subtitle video error = %v, want ErrInvalidPlan", err)
	}
}

func TestTargetConnectionsNeedConfigurationFenceAndUnrelatedCurrentBindingsMayBeOmitted(t *testing.T) {
	registration := NewRegistrationPredicate(planningConnection, "tmdb-1", domain.MediaMovie, "movie-1", ports.RegistrationFields{})
	desired, err := NewDesiredState(registration)
	if err != nil {
		t.Fatal(err)
	}
	base := Request{ID: "plan-connection-fence", Action: domain.ActionArrRegistration, Desired: desired, Binding: Binding{SourceID: "discovery-1", SourceRevision: "catalog-1"}, CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour)}
	if _, err := Build(base); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("missing target connection fence error = %v, want ErrInvalidPlan", err)
	}
	base.Binding.ConnectionRevisions = map[domain.ConfigID]string{planningConnection: "cfg-1"}
	plan, err := Build(base)
	if err != nil {
		t.Fatal(err)
	}
	current := CurrentState{
		Binding: Binding{SourceID: "discovery-1", SourceRevision: "catalog-1", ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"}},
		Desired: desired, ObservedAt: planningNow.Add(time.Minute),
	}
	if err := plan.ValidateCurrent(current); err != nil {
		t.Fatalf("omitting unrelated current bindings invalidated plan: %v", err)
	}
	current.Binding.ConnectionRevisions = nil
	if err := plan.ValidateCurrent(current); !errors.Is(err, ErrPlanBindingChanged) {
		t.Fatalf("missing target current fence error = %v, want ErrPlanBindingChanged", err)
	}
}

func TestArrImportRequiresPathMappingRevisionFence(t *testing.T) {
	source := planningTarget(planningDownloadRoot, "incoming/Film.mkv")
	desired, err := NewDesiredState(NewImportPredicate(planningConnection, "movie-1", []ImportSelection{{Source: source, MovieOrEpisodeID: "movie-1", Confidence: MappingExact}}, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		ID: "plan-import-mapping-fence", Action: domain.ActionArrImport, Desired: desired,
		Manifest:  []domain.FileManifestEntry{planningFile(source, 10, "source-inode", planningNow)},
		Binding:   Binding{SourceID: "discovery-1", SourceRevision: "manifest-1", ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"}},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	}
	if _, err := Build(request); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("missing path mapping fence error = %v, want ErrInvalidPlan", err)
	}
	request.Binding.MappingRevisions = map[domain.ConfigID]string{"mapping-main": "map-1"}
	if _, err := Build(request); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("unscoped path mapping fence error = %v, want ErrInvalidPlan", err)
	}
	request.Binding.MappingScopes = []MappingScope{planningMappingScope(planningDownloadRoot)}
	if _, err := Build(request); err != nil {
		t.Fatalf("fenced Arr import rejected: %v", err)
	}
}

func TestSubtitleImportRejectsDirectoryRootButAcceptsEnumeratedChild(t *testing.T) {
	video := planningTarget(planningDownloadRoot, "incoming/pack/Film.mkv")
	subtitle := planningTarget(planningDownloadRoot, "incoming/pack/Film.en.srt")
	directory := domain.FileManifestEntry{
		RootID: planningDownloadRoot, RelativePath: "incoming/pack", Type: domain.ManifestDirectory,
		FileIdentity: "pack-inode", Role: domain.RoleSubtitle, ObservedAt: planningNow,
		Children: []domain.FileManifestEntry{
			planningFile(video, 10, "video-inode", planningNow),
			planningSubtitleFile(subtitle, 5, "subtitle-inode", planningNow),
		},
	}
	desired, err := NewDesiredState(NewImportPredicate(planningConnection, "series-1", []ImportSelection{
		{Source: directoryTarget(directory), Subtitle: true, PairID: "pair-1", Language: "en", VideoPaths: []domain.FileTarget{video}, Confidence: MappingExact},
	}, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(Request{
		ID: "plan-import-directory-subtitle", Action: domain.ActionArrImport, Desired: desired,
		Manifest: []domain.FileManifestEntry{directory},
		Binding: Binding{
			SourceID: "discovery-1", SourceRevision: "manifest-1",
			ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"},
			MappingRevisions:    map[domain.ConfigID]string{"mapping-main": "map-1"},
			MappingScopes:       []MappingScope{planningMappingScope(planningDownloadRoot)},
		},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("subtitle directory root error = %v, want ErrInvalidPlan", err)
	}
	standalone := NewSubtitlePredicate(planningConnection, directoryTarget(directory), []domain.FileTarget{video}, "pair-1", "en", false, false)
	standaloneDesired := DesiredState{Predicates: []Predicate{standalone}}
	if err := validateDesiredAgainstManifest(domain.ActionArrImport, standaloneDesired, []domain.FileManifestEntry{directory}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("standalone subtitle directory root error = %v, want ErrInvalidPlan", err)
	}

	childDesired, err := NewDesiredState(NewImportPredicate(planningConnection, "series-1", []ImportSelection{
		{Source: subtitle, Subtitle: true, PairID: "pair-1", Language: "en", VideoPaths: []domain.FileTarget{video}, Confidence: MappingExact},
	}, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(Request{
		ID: "plan-import-child-subtitle", Action: domain.ActionArrImport, Desired: childDesired,
		Manifest: []domain.FileManifestEntry{directory},
		Binding: Binding{
			SourceID: "discovery-1", SourceRevision: "manifest-1",
			ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"},
			MappingRevisions:    map[domain.ConfigID]string{"mapping-main": "map-1"},
			MappingScopes:       []MappingScope{planningMappingScope(planningDownloadRoot)},
		},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	}); err != nil {
		t.Fatalf("enumerated subtitle child was rejected: %v", err)
	}
}

func TestArrImportBindsTargetMappingScopeAndCurrentRevision(t *testing.T) {
	source := planningTarget(planningDownloadRoot, "incoming/Film.mkv")
	desired, err := NewDesiredState(NewImportPredicate(planningConnection, "movie-1", []ImportSelection{{Source: source, MovieOrEpisodeID: "movie-1", Confidence: MappingExact}}, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(Request{
		ID: "plan-import-current-mapping", Action: domain.ActionArrImport, Desired: desired,
		Manifest: []domain.FileManifestEntry{planningFile(source, 10, "source-inode", planningNow)},
		Binding: Binding{
			SourceID: "discovery-1", SourceRevision: "manifest-1",
			ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"},
			MappingRevisions: map[domain.ConfigID]string{
				"mapping-main": "map-1", "unrelated-library": "map-unrelated",
			},
			MappingScopes: []MappingScope{
				planningMappingScope(planningDownloadRoot),
				{MappingID: "unrelated-library", ConnectionID: "jellyfin-main", RootID: planningLibraryRoot},
			},
		},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	baseCurrent := CurrentState{
		Binding: Binding{
			SourceID: "discovery-1", SourceRevision: "manifest-1",
			ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"},
			MappingRevisions:    map[domain.ConfigID]string{"mapping-main": "map-1"},
			MappingScopes:       []MappingScope{planningMappingScope(planningDownloadRoot)},
		},
		Manifest: plan.Manifest, Desired: plan.Desired, ObservedAt: planningNow.Add(time.Minute),
	}
	if err := plan.ValidateCurrent(baseCurrent); err != nil {
		t.Fatalf("target mapping with unrelated current omission was rejected: %v", err)
	}
	changed := baseCurrent
	changed.Binding = cloneBinding(baseCurrent.Binding)
	changed.Binding.MappingRevisions["mapping-main"] = "map-2"
	if err := plan.ValidateCurrent(changed); !errors.Is(err, ErrPlanBindingChanged) {
		t.Fatalf("changed target mapping was accepted: %v", err)
	}
	omitted := baseCurrent
	omitted.Binding = cloneBinding(baseCurrent.Binding)
	omitted.Binding.MappingRevisions = nil
	omitted.Binding.MappingScopes = nil
	if err := plan.ValidateCurrent(omitted); !errors.Is(err, ErrPlanBindingChanged) {
		t.Fatalf("omitted target mapping was accepted: %v", err)
	}
	unrelated := baseCurrent
	unrelated.Binding = cloneBinding(baseCurrent.Binding)
	unrelated.Binding.MappingRevisions = map[domain.ConfigID]string{"unrelated-library": "map-unrelated"}
	unrelated.Binding.MappingScopes = []MappingScope{{MappingID: "unrelated-library", ConnectionID: "jellyfin-main", RootID: planningLibraryRoot}}
	if err := plan.ValidateCurrent(unrelated); !errors.Is(err, ErrPlanBindingChanged) {
		t.Fatalf("unrelated-only target mapping was accepted: %v", err)
	}
	ambiguous := baseCurrent
	ambiguous.Binding = cloneBinding(baseCurrent.Binding)
	ambiguous.Binding.MappingRevisions["mapping-extra"] = "map-extra"
	ambiguous.Binding.MappingScopes = append(ambiguous.Binding.MappingScopes, MappingScope{MappingID: "mapping-extra", ConnectionID: planningConnection, RootID: planningDownloadRoot})
	if err := plan.ValidateCurrent(ambiguous); !errors.Is(err, ErrPlanBindingChanged) {
		t.Fatalf("new same-scope mapping was accepted: %v", err)
	}
}

func TestConflictDigestCanonicalizesAuthorityFieldsAndIgnoresMessages(t *testing.T) {
	target := planningTarget(planningLibraryRoot, "Movies/Film.mkv")
	desired, err := NewDesiredState(NewFileContentPredicate(target, 10, planningDigest("a")))
	if err != nil {
		t.Fatal(err)
	}
	manifest := []domain.FileManifestEntry{planningFile(planningTarget(planningDownloadRoot, "incoming/Film.mkv"), 10, "source-inode", planningNow)}
	binding := Binding{SourceID: "discovery-1", SourceRevision: "manifest-1"}
	conflicts := []Conflict{
		{Code: "same-code", Field: "same-field", Target: "same-target", Message: "same-display", Evidence: []string{"authority-a"}, Blocking: true},
		{Code: "same-code", Field: "same-field", Target: "same-target", Message: "same-display", Evidence: []string{"authority-b"}, Blocking: false},
	}
	build := func(values []Conflict) Plan {
		plan, err := Build(Request{ID: "plan-conflict-stable", Action: domain.ActionFSCopy, Desired: desired, Manifest: manifest, Binding: binding, Conflicts: values, CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour)})
		if !errors.Is(err, ErrPlanConflict) {
			t.Fatalf("conflict build error = %v, want ErrPlanConflict", err)
		}
		return plan
	}
	forward := build(conflicts)
	reverse := build([]Conflict{conflicts[1], conflicts[0]})
	if forward.Digest != reverse.Digest {
		t.Fatalf("reversed authority conflict order changed digest: %s != %s", forward.Digest, reverse.Digest)
	}
	messageEdited := []Conflict{conflicts[1], conflicts[0]}
	messageEdited[0].Message = "different display copy"
	messageEdited[1].Message = "another display copy"
	edited := build(messageEdited)
	if edited.Digest != forward.Digest {
		t.Fatalf("display-only conflict edit changed digest: %s != %s", edited.Digest, forward.Digest)
	}
}

func TestDesiredConstructorsDeepCopyScalarPointersAcrossRevisions(t *testing.T) {
	monitored := false
	seasonFolder := true
	seasonNumber := 1
	absoluteNumber := 101
	registration := Predicate{Kind: PredicateRegistration, Registration: &RegistrationPredicate{
		ConnectionID: planningConnection, ProviderID: "tvdb-1", Kind: domain.MediaEpisode, ExternalID: "series-1",
		Fields: ports.RegistrationFields{Monitored: &monitored, SeasonFolder: &seasonFolder},
	}}
	episode := Predicate{Kind: PredicateEpisodeAssociation, Episode: &EpisodePredicate{
		ConnectionID: planningConnection, Source: planningTarget(planningDownloadRoot, "incoming/Show.S01E01.mkv"), SeriesID: "series-1",
		EpisodeIDs: []string{"episode-1"}, SeasonNumber: &seasonNumber, AbsoluteNumber: &absoluteNumber,
	}}
	desired, err := NewDesiredState(registration, episode)
	if err != nil {
		t.Fatal(err)
	}
	monitored = true
	seasonFolder = false
	seasonNumber = 9
	absoluteNumber = 999
	for _, predicate := range desired.Predicates {
		switch predicate.Kind {
		case PredicateRegistration:
			if predicate.Registration.Fields.Monitored == nil || *predicate.Registration.Fields.Monitored || predicate.Registration.Fields.SeasonFolder == nil || !*predicate.Registration.Fields.SeasonFolder {
				t.Fatalf("NewDesiredState retained registration scalar aliases: %#v", predicate.Registration.Fields)
			}
		case PredicateEpisodeAssociation:
			if predicate.Episode.SeasonNumber == nil || *predicate.Episode.SeasonNumber != 1 || predicate.Episode.AbsoluteNumber == nil || *predicate.Episode.AbsoluteNumber != 101 {
				t.Fatalf("NewDesiredState retained episode scalar aliases: %#v", predicate.Episode)
			}
		}
	}

	monitored = false
	plan, err := Build(Request{
		ID: "plan-pointer-copy", Action: domain.ActionArrRegistration, Desired: DesiredState{Predicates: []Predicate{registration}},
		Binding:   Binding{SourceID: "catalog", SourceRevision: "1", ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-1"}},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	monitored = true
	if plan.Desired.Predicates[0].Registration.Fields.Monitored == nil || *plan.Desired.Predicates[0].Registration.Fields.Monitored {
		t.Fatal("Build retained caller registration scalar pointer")
	}
	revised, err := NewRevision(plan, Request{
		Action: domain.ActionArrRegistration, Desired: DesiredState{Predicates: []Predicate{registration}},
		Binding:   Binding{SourceID: "catalog", SourceRevision: "2", ConnectionRevisions: map[domain.ConfigID]string{planningConnection: "cfg-2"}},
		CreatedAt: planningNow, ExpiresAt: planningNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if revised.Desired.Predicates[0].Registration.Fields.Monitored == plan.Desired.Predicates[0].Registration.Fields.Monitored {
		t.Fatal("revision reused the previous scalar pointer")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("previous plan was corrupted by revision construction: %v", err)
	}
}

func planningTarget(root domain.ConfigID, relative string) domain.FileTarget {
	return domain.FileTarget{RootID: root, RelativePath: relative}
}

func directoryTarget(entry domain.FileManifestEntry) domain.FileTarget {
	return planningTarget(entry.RootID, entry.RelativePath)
}

func planningMappingScope(root domain.ConfigID) MappingScope {
	return MappingScope{MappingID: "mapping-main", ConnectionID: planningConnection, RootID: root}
}

func planningFile(target domain.FileTarget, size int64, identity string, observedAt time.Time) domain.FileManifestEntry {
	return domain.FileManifestEntry{RootID: target.RootID, RelativePath: target.RelativePath, Type: domain.ManifestFile, Size: size, Digest: planningDigest("a"), FileIdentity: identity, Role: domain.RoleVideo, ObservedAt: observedAt}
}

func planningSubtitleFile(target domain.FileTarget, size int64, identity string, observedAt time.Time) domain.FileManifestEntry {
	return domain.FileManifestEntry{RootID: target.RootID, RelativePath: target.RelativePath, Type: domain.ManifestSubtitle, Size: size, Digest: planningDigest("a"), FileIdentity: identity, Role: domain.RoleSubtitle, ObservedAt: observedAt}
}

func planningDigest(value string) string {
	return "sha256:" + strings.Repeat(value, 64)
}

// Keep the ports import exercised by the registration predicate contract in
// this package's public test helpers.
var _ = ports.RegistrationFields{}
