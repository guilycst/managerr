package read

// This file is the root-side translation seam for the standalone Sonarr and
// Radarr modules. The nested modules own HTTP, authentication, bounds and
// generated DTO decoding. Only their normalized public observations enter this
// package; the old decoder remains a compatibility fallback for response
// shapes that predate the frozen module contracts.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	radarrnative "github.com/guilycst/mastarr/clients/radarr"
	sonarrnative "github.com/guilycst/mastarr/clients/sonarr"
	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

type nativeCatalogRecord struct {
	ID      string
	Record  ports.MediaRecord
	Reasons []string
}

// List enters the standalone module for native-shaped catalog responses. A
// malformed legacy fixture is deliberately handled by listLegacy so existing
// partial-evidence behavior remains stable while production responses gain the
// module's strict validation and typed error boundary.
func (client *Client) List(ctx context.Context, connectionID domain.ConfigID, cursor string, requestedLimit int) (ports.Page[ports.MediaRecord], error) {
	if client.nativeAvailable() {
		result, err := client.listNative(ctx, connectionID, cursor, requestedLimit)
		if err == nil {
			return result, nil
		}
		if !nativeCompatibilityFallback(err) {
			return result, err
		}
	}
	return client.listLegacy(ctx, connectionID, cursor, requestedLimit)
}

func (client *Client) nativeAvailable() bool {
	return client != nil && (client.sonarr != nil || client.radarr != nil)
}

func nativeCompatibilityFallback(err error) bool {
	if err == nil {
		return false
	}
	if sonarrnative.IsCode(err, sonarrnative.ErrorMalformed) || radarrnative.IsCode(err, radarrnative.ErrorMalformed) {
		return true
	}
	var upstream domain.UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	// The adapter fallback exists only for the legacy response shapes covered
	// by the pre-module fixtures. Transport failures stay on the standalone
	// typed boundary; unsupported/malformed response classes can still be
	// decoded by the established root compatibility path.
	if strings.HasPrefix(upstream.Operation, "arr.import.observe") || strings.Contains(upstream.Operation, ".observe.file") {
		// These are already normalized observations with an explicit incomplete
		// identity/path result. Falling back would let a second decoder turn the
		// same native evidence into a false success.
		return false
	}
	return upstream.Code == domain.OutcomeUnknown || upstream.Code == domain.OutcomeUnsupported
}

func translateNativeError(err error, operation string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if upstream, ok := err.(sonarrnative.UpstreamError); ok {
		return domain.UpstreamError{Code: translateSonarrCode(upstream.Code), Status: upstream.Status, Retryable: upstream.Retryable, Operation: operation, Detail: "Sonarr upstream request failed"}
	}
	var sonarrErr sonarrnative.UpstreamError
	if errors.As(err, &sonarrErr) {
		return domain.UpstreamError{Code: translateSonarrCode(sonarrErr.Code), Status: sonarrErr.Status, Retryable: sonarrErr.Retryable, Operation: operation, Detail: "Sonarr upstream request failed"}
	}
	var radarrErr radarrnative.UpstreamError
	if errors.As(err, &radarrErr) {
		return domain.UpstreamError{Code: translateRadarrCode(radarrErr.Code), Status: radarrErr.Status, Retryable: radarrErr.Retryable, Operation: operation, Detail: "Radarr upstream request failed"}
	}
	return domain.UpstreamError{Code: domain.OutcomeUnavailable, Retryable: true, Operation: operation, Detail: "Arr upstream is unavailable"}
}

func translateSonarrCode(code sonarrnative.ErrorCode) domain.UpstreamErrorCode {
	switch code {
	case sonarrnative.ErrorUnavailable:
		return domain.OutcomeUnavailable
	case sonarrnative.ErrorRateLimited:
		return domain.OutcomeRateLimited
	case sonarrnative.ErrorUnauthorized, sonarrnative.ErrorForbidden:
		return domain.OutcomeUnauthorized
	case sonarrnative.ErrorInvalidInput:
		return domain.OutcomeInvalidInput
	case sonarrnative.ErrorConflict:
		return domain.OutcomeConflict
	case sonarrnative.ErrorUnsupported, sonarrnative.ErrorNotFound:
		return domain.OutcomeUnsupported
	default:
		return domain.OutcomeUnknown
	}
}

func translateRadarrCode(code radarrnative.ErrorCode) domain.UpstreamErrorCode {
	switch code {
	case radarrnative.ErrorUnavailable:
		return domain.OutcomeUnavailable
	case radarrnative.ErrorRateLimited:
		return domain.OutcomeRateLimited
	case radarrnative.ErrorUnauthorized, radarrnative.ErrorForbidden:
		return domain.OutcomeUnauthorized
	case radarrnative.ErrorInvalidInput:
		return domain.OutcomeInvalidInput
	case radarrnative.ErrorConflict:
		return domain.OutcomeConflict
	case radarrnative.ErrorUnsupported, radarrnative.ErrorNotFound:
		return domain.OutcomeUnsupported
	default:
		return domain.OutcomeUnknown
	}
}

func (client *Client) listNative(ctx context.Context, connectionID domain.ConfigID, cursor string, requestedLimit int) (ports.Page[ports.MediaRecord], error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return ports.Page[ports.MediaRecord]{}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.Page[ports.MediaRecord]{}, err
	}
	limit, err := client.pageLimit(requestedLimit)
	if err != nil {
		return ports.Page[ports.MediaRecord]{}, err
	}
	state, err := client.decodeCursor(cursor)
	if err != nil {
		return ports.Page[ports.MediaRecord]{}, err
	}
	if state.SourceID == "" {
		state.SourceID, err = domain.NewRuntimeID()
		if err != nil {
			return ports.Page[ports.MediaRecord]{}, errors.New("Arr inventory source identity unavailable")
		}
		state.StartedAt = nowUTC()
		state.PageSize = limit
		state.Collection = "inventory"
	} else if state.Collection != "inventory" {
		return ports.Page[ports.MediaRecord]{}, invalidInput("arr.inventory.cursor")
	} else if requestedLimit > 0 && state.PageSize != limit {
		return ports.Page[ports.MediaRecord]{}, invalidInput("arr.inventory.cursor")
	}
	if state.Page >= client.config.MaxPages || state.ObservedCount >= client.config.MaxRecords {
		return ports.Page[ports.MediaRecord]{}, invalidInput("arr.inventory.cursor")
	}

	records, err := client.nativeCatalog(ctx)
	if err != nil {
		return ports.Page[ports.MediaRecord]{}, err
	}
	revision, err := json.Marshal(records)
	if err != nil {
		return ports.Page[ports.MediaRecord]{}, malformed("arr.inventory.native")
	}
	currentRevision := digest(revision)
	if state.SnapshotRevision == "" {
		state.SnapshotRevision = currentRevision
	} else if state.SnapshotRevision != currentRevision {
		addReason(&state.Reasons, "catalog_snapshot_changed")
	}

	start := state.Offset
	if start < 0 {
		return ports.Page[ports.MediaRecord]{}, invalidInput("arr.inventory.cursor")
	}
	if start > len(records) {
		start = len(records)
	}
	remaining := client.config.MaxRecords - state.ObservedCount
	if remaining <= 0 {
		return ports.Page[ports.MediaRecord]{}, invalidInput("arr.inventory.cursor")
	}
	pageCount := minInt(limit, remaining)
	available := len(records) - start
	if pageCount > available {
		pageCount = available
	}
	selected := records[start : start+pageCount]
	hasMore := start+pageCount < len(records)
	if state.ObservedCount+pageCount >= client.config.MaxRecords && hasMore {
		hasMore = false
		addReason(&state.Reasons, "catalog_record_limit")
	}

	seen := make(map[string]struct{}, len(state.SeenIDs))
	for _, id := range state.SeenIDs {
		seen[id] = struct{}{}
	}
	pageSeen := make(map[string]struct{}, len(selected))
	items := make([]ports.MediaRecord, 0, len(selected))
	for _, item := range selected {
		if err := ctx.Err(); err != nil {
			return ports.Page[ports.MediaRecord]{}, err
		}
		for _, reason := range item.Reasons {
			addReason(&state.Reasons, reason)
		}
		if item.ID == "" {
			addReason(&state.Reasons, "record_missing_id")
			continue
		}
		if _, exists := seen[item.ID]; exists || identityFilterContains(state.SeenFilter, item.ID) {
			addReason(&state.Reasons, "catalog_overlap")
			continue
		}
		if _, exists := pageSeen[item.ID]; exists {
			addReason(&state.Reasons, "catalog_duplicate")
			continue
		}
		pageSeen[item.ID] = struct{}{}
		seen[item.ID] = struct{}{}
		rememberIdentity(&state, item.ID)
		items = append(items, item.Record)
	}
	state.Page++
	state.Offset = start + len(selected)
	state.ObservedCount += len(items)
	if len(selected) == 0 {
		hasMore = false
	}
	if hasMore && state.Page >= client.config.MaxPages {
		hasMore = false
		addReason(&state.Reasons, "catalog_page_limit")
	}
	if hasMore && state.ObservedCount >= client.config.MaxRecords {
		hasMore = false
		addReason(&state.Reasons, "catalog_record_limit")
	}
	now := nowUTC()
	coverage := domain.Coverage{SourceID: state.SourceID, ConnectionID: connectionID, Completeness: domain.CompletenessPartial, ReasonCodes: append([]string(nil), state.Reasons...), ObservedCount: int64(state.ObservedCount), SnapshotRevision: state.SnapshotRevision, StartedAt: timePtr(state.StartedAt), ObservedAt: now}
	if !hasMore {
		coverage.CompletedAt = &now
		if len(state.Reasons) == 0 {
			coverage.Completeness = domain.CompletenessComplete
		}
	} else {
		addReason(&coverage.ReasonCodes, "pagination_continues")
	}
	result := ports.Page[ports.MediaRecord]{Items: items, Coverage: coverage}
	if hasMore {
		result.NextCursor, err = client.encodeCursor(state)
		if err != nil {
			return ports.Page[ports.MediaRecord]{}, err
		}
	}
	return result, nil
}

func nowUTC() (resultTime time.Time) {
	return time.Now().UTC()
}

func (client *Client) nativeCatalog(ctx context.Context) ([]nativeCatalogRecord, error) {
	if client.config.Kind == domain.ConnectionRadarr {
		page, err := client.radarr.ListMovies(ctx)
		if err != nil {
			return nil, translateNativeError(err, "arr.inventory.list")
		}
		result := make([]nativeCatalogRecord, len(page.Items))
		for index, movie := range page.Items {
			record, reasons := client.nativeRadarrRecord(ctx, movie)
			result[index] = nativeCatalogRecord{ID: record.ExternalID, Record: record, Reasons: reasons}
		}
		return result, nil
	}
	page, err := client.sonarr.ListSeries(ctx)
	if err != nil {
		return nil, translateNativeError(err, "arr.inventory.list")
	}
	result := make([]nativeCatalogRecord, len(page.Items))
	for index, series := range page.Items {
		record, reasons := client.nativeSonarrRecord(ctx, series)
		result[index] = nativeCatalogRecord{ID: record.ExternalID, Record: record, Reasons: reasons}
	}
	return result, nil
}

func (client *Client) nativeRadarrRecord(ctx context.Context, movie radarrnative.Movie) (ports.MediaRecord, []string) {
	record := ports.MediaRecord{ExternalID: strconv.FormatInt(movie.ID, 10), Title: movie.Title, Kind: domain.MediaMovie, Monitored: movie.Monitored, ProviderID: nativeRadarrProvider(movie)}
	var reasons []string
	if movie.MovieFile != nil {
		file, reason := client.nativeRadarrFile(*movie.MovieFile, movie.ID)
		if reason != "" {
			reasons = append(reasons, reason)
		} else {
			record.Files = append(record.Files, file)
		}
		return record, reasons
	}
	files, err := client.radarr.ListMovieFiles(ctx, movie.ID)
	if err != nil {
		return record, append(reasons, "movie_files_unavailable")
	}
	for _, nativeFile := range files.Items {
		file, reason := client.nativeRadarrFile(nativeFile, movie.ID)
		if reason != "" {
			reasons = append(reasons, reason)
			continue
		}
		record.Files = append(record.Files, file)
	}
	return record, reasons
}

func nativeRadarrProvider(movie radarrnative.Movie) string {
	if movie.TMDBID != nil {
		return strconv.FormatInt(*movie.TMDBID, 10)
	}
	if movie.IMDBID != "" {
		return movie.IMDBID
	}
	for _, key := range []string{"tmdbId", "tmdbid", "imdbId", "imdbid", "tvdbId", "tvdbid"} {
		if value := strings.TrimSpace(movie.ProviderIDs[key]); value != "" {
			return value
		}
	}
	return ""
}

func (client *Client) nativeRadarrFile(nativeFile radarrnative.MovieFile, expectedMovieID int64) (ports.MediaFile, string) {
	file := ports.MediaFile{ExternalID: strconv.FormatInt(nativeFile.ID, 10), MovieID: strconv.FormatInt(nativeFile.MovieID, 10), Size: nativeFile.Size}
	if nativeFile.MovieID != expectedMovieID {
		return file, "movie_file_movie_identity_mismatch"
	}
	target, mapped, ambiguous := client.mapPath(nativeFile.Path)
	if ambiguous {
		return file, "movie_file_mapping_ambiguous"
	}
	if !mapped {
		return file, "movie_file_mapping_missing"
	}
	file.Path = target
	return file, ""
}

func (client *Client) nativeSonarrRecord(ctx context.Context, series sonarrnative.Series) (ports.MediaRecord, []string) {
	record := ports.MediaRecord{ExternalID: strconv.FormatInt(series.ID, 10), Title: series.Title, Kind: domain.MediaEpisode, Monitored: series.Monitored, ProviderID: nativeSonarrProvider(series)}
	episodes, err := client.sonarr.ListEpisodesWithFiles(ctx, series.ID)
	if err != nil {
		return record, []string{"episode_files_unavailable"}
	}
	reasons := make([]string, 0)
	fileIndex := make(map[int64]int)
	for _, episode := range episodes.Items {
		if episode.EpisodeFile == nil {
			continue
		}
		file, reason := client.nativeSonarrFile(*episode.EpisodeFile, series.ID)
		if reason != "" {
			reasons = append(reasons, reason)
			continue
		}
		position, exists := fileIndex[fileID(file.ExternalID)]
		if !exists {
			file.EpisodeIDs = []string{strconv.FormatInt(episode.ID, 10)}
			record.Files = append(record.Files, file)
			fileIndex[fileID(file.ExternalID)] = len(record.Files) - 1
			continue
		}
		if record.Files[position].Path != file.Path || record.Files[position].Size != file.Size {
			reasons = append(reasons, "episode_file_conflicting_details")
			continue
		}
		episodeID := strconv.FormatInt(episode.ID, 10)
		if !contains(record.Files[position].EpisodeIDs, episodeID) {
			record.Files[position].EpisodeIDs = append(record.Files[position].EpisodeIDs, episodeID)
		}
	}
	return record, reasons
}

func nativeSonarrProvider(series sonarrnative.Series) string {
	if series.TVDBID != nil {
		return strconv.FormatInt(*series.TVDBID, 10)
	}
	if series.IMDBID != "" {
		return series.IMDBID
	}
	for _, key := range []string{"tvdbId", "tvdbid", "imdbId", "imdbid", "tvmazeId", "tvmazeid"} {
		if value := strings.TrimSpace(series.ProviderIDs[key]); value != "" {
			return value
		}
	}
	return ""
}

func fileID(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func (client *Client) nativeSonarrFile(nativeFile sonarrnative.EpisodeFile, expectedSeriesID int64) (ports.MediaFile, string) {
	file := ports.MediaFile{ExternalID: strconv.FormatInt(nativeFile.ID, 10), Size: nativeFile.Size}
	if nativeFile.SeriesID != expectedSeriesID {
		return file, "episode_series_identity_mismatch"
	}
	target, mapped, ambiguous := client.mapPath(nativeFile.Path)
	if ambiguous {
		return file, "episode_file_mapping_ambiguous"
	}
	if !mapped {
		return file, "episode_file_mapping_missing"
	}
	file.Path = target
	return file, ""
}

func (client *Client) nativeOptions(ctx context.Context, connectionID domain.ConfigID) (ports.ManagerOptions, error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return ports.ManagerOptions{}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.ManagerOptions{}, err
	}
	result := ports.ManagerOptions{}
	if client.config.Kind == domain.ConnectionRadarr {
		roots, err := client.radarr.ListRootFolders(ctx)
		if err != nil {
			return result, translateNativeError(err, "arr.options.root_folders")
		}
		for _, root := range roots.Items {
			if strings.TrimSpace(root.Path) != "" {
				result.RootFolders = append(result.RootFolders, root.Path)
			}
		}
		profiles, err := client.radarr.ListQualityProfiles(ctx)
		if err != nil {
			return result, translateNativeError(err, "arr.options.quality_profiles")
		}
		for _, profile := range profiles.Items {
			result.QualityProfiles = append(result.QualityProfiles, ports.QualityProfile{ID: strconv.FormatInt(profile.ID, 10), Name: profile.Name})
		}
	} else {
		roots, err := client.sonarr.ListRootFolders(ctx)
		if err != nil {
			return result, translateNativeError(err, "arr.options.root_folders")
		}
		for _, root := range roots.Items {
			if strings.TrimSpace(root.Path) != "" {
				result.RootFolders = append(result.RootFolders, root.Path)
			}
		}
		profiles, err := client.sonarr.ListQualityProfiles(ctx)
		if err != nil {
			return result, translateNativeError(err, "arr.options.quality_profiles")
		}
		for _, profile := range profiles.Items {
			result.QualityProfiles = append(result.QualityProfiles, ports.QualityProfile{ID: strconv.FormatInt(profile.ID, 10), Name: profile.Name})
		}
	}
	result.ObservedAt = nowUTC()
	return result, nil
}

func (client *Client) nativeObserveImport(ctx context.Context, connectionID domain.ConfigID, externalID string) (ports.ImportObservation, error) {
	var result ports.ImportObservation
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	parsedID, err := parsePositiveInt(externalID)
	if err != nil {
		return result, invalidInput("arr.import.external_id")
	}
	var files []ports.MediaFile
	var reasons []string
	if client.config.Kind == domain.ConnectionRadarr {
		movie, err := client.radarr.GetMovie(ctx, int64(parsedID))
		if err != nil {
			return result, translateNativeError(err, "arr.movie.observe")
		}
		if movie.ID != int64(parsedID) {
			return result, observationIncomplete("arr.movie.observe", "movie_identity_mismatch")
		}
		if movie.MovieFile != nil {
			file, reason := client.nativeRadarrFile(*movie.MovieFile, movie.ID)
			if reason != "" {
				return result, observationIncomplete("arr.movie.observe.file", reason)
			}
			files = append(files, file)
		} else {
			movieFiles, fileErr := client.radarr.ListMovieFiles(ctx, movie.ID)
			if fileErr != nil {
				return result, translateNativeError(fileErr, "arr.movie.observe.files")
			}
			for _, nativeFile := range movieFiles.Items {
				file, reason := client.nativeRadarrFile(nativeFile, movie.ID)
				if reason != "" {
					return result, observationIncomplete("arr.movie.observe.file", reason)
				}
				files = append(files, file)
			}
		}
	} else {
		episodes, episodeErr := client.sonarr.ListEpisodesWithFiles(ctx, int64(parsedID))
		if episodeErr != nil {
			return result, translateNativeError(episodeErr, "arr.episode.observe")
		}
		fileIndex := make(map[int64]int)
		for _, episode := range episodes.Items {
			if episode.EpisodeFile == nil {
				continue
			}
			file, reason := client.nativeSonarrFile(*episode.EpisodeFile, int64(parsedID))
			if reason != "" {
				reasons = append(reasons, reason)
				continue
			}
			id := fileID(file.ExternalID)
			position, exists := fileIndex[id]
			if !exists {
				file.EpisodeIDs = []string{strconv.FormatInt(episode.ID, 10)}
				files = append(files, file)
				fileIndex[id] = len(files) - 1
				continue
			}
			episodeID := strconv.FormatInt(episode.ID, 10)
			if !contains(files[position].EpisodeIDs, episodeID) {
				files[position].EpisodeIDs = append(files[position].EpisodeIDs, episodeID)
			}
		}
	}
	if len(reasons) > 0 {
		return result, observationIncomplete("arr.import.observe", reasons[0])
	}
	if len(files) == 0 {
		return result, observationIncomplete("arr.import.observe", "import_file_missing")
	}
	result.ExternalID = externalID
	result.Files = files
	result.ObservedAt = nowUTC()
	return result, nil
}

func (client *Client) nativePreviewImport(ctx context.Context, connectionID domain.ConfigID, request ports.ImportPreviewRequest, downloadID string) (ports.ImportPreview, error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return ports.ImportPreview{}, err
	}
	if err := validateImportRequest(request, client.config.MaxFiles); err != nil {
		return ports.ImportPreview{}, err
	}
	groups := groupImportFiles(request.Files)
	preview := ports.ImportPreview{Files: make([]ports.ImportFile, 0, len(request.Files)), Rejections: make([]ports.ImportRejection, 0)}
	evidence := make([][]byte, 0, len(groups))
	for _, group := range groups {
		query, err := client.previewQuery(request.RegisteredExternalID, downloadID, group)
		if err != nil {
			return ports.ImportPreview{}, err
		}
		var accepted []ports.ImportFile
		var rejected []ports.ImportRejection
		var raw []byte
		if client.config.Kind == domain.ConnectionRadarr {
			nativeQuery := radarrnative.ManualImportQuery{Folder: query.Get("folder"), FilterExistingFiles: true, DownloadID: query.Get("downloadId")}
			if value := query.Get("movieId"); value != "" {
				movieID, parseErr := parsePositiveInt(value)
				if parseErr != nil {
					return ports.ImportPreview{}, invalidInput("arr.manual_import.preview.movie_id")
				}
				nativeQuery.MovieID = ptrInt64(int64(movieID))
			}
			page, nativeErr := client.radarr.PreviewManualImport(ctx, nativeQuery)
			if nativeErr != nil {
				return ports.ImportPreview{}, translateNativeError(nativeErr, "arr.manual_import.preview")
			}
			converted := make([]RadarrManualImportResource, len(page.Items))
			for index, item := range page.Items {
				converted[index] = radarrPreviewResource(item)
			}
			accepted, rejected, err = client.mapPreviewResponseForResources(converted, group, query.Get("folder"), request.RegisteredExternalID, nil, expectedDownloadsFor(group, downloadID), false)
			raw, _ = json.Marshal(converted)
		} else {
			nativeQuery := sonarrnative.ManualImportQuery{Folder: query.Get("folder"), FilterExistingFiles: true, DownloadID: query.Get("downloadId")}
			// Sonarr's native folder mode rejects seriesId. The adapter still
			// validates the returned series/episode associations below.
			page, nativeErr := client.sonarr.PreviewManualImport(ctx, nativeQuery)
			if nativeErr != nil {
				return ports.ImportPreview{}, translateNativeError(nativeErr, "arr.manual_import.preview")
			}
			converted := make([]SonarrManualImportResource, len(page.Items))
			for index, item := range page.Items {
				converted[index] = sonarrPreviewResource(item)
			}
			accepted, rejected, err = client.mapPreviewResponseForResources(converted, group, query.Get("folder"), request.RegisteredExternalID, nil, expectedDownloadsFor(group, downloadID), false)
			raw, _ = json.Marshal(converted)
		}
		if err != nil {
			return ports.ImportPreview{}, err
		}
		preview.Files = append(preview.Files, accepted...)
		preview.Rejections = append(preview.Rejections, rejected...)
		evidence = append(evidence, raw)
	}
	preview.Revision = client.previewRevision(request, downloadID, evidence)
	preview.ObservedAt = nowUTC()
	return preview, nil
}

func expectedDownloadsFor(files []ports.ImportFile, downloadID string) map[string]string {
	if strings.TrimSpace(downloadID) == "" {
		return nil
	}
	result := make(map[string]string, len(files))
	for _, file := range files {
		result[sourceKey(file.Source)] = strings.TrimSpace(downloadID)
	}
	return result
}

func ptrInt64(value int64) *int64 { return &value }

func radarrPreviewResource(value radarrnative.ManualImportCandidate) RadarrManualImportResource {
	result := RadarrManualImportResource{ID: rawInt64(value.ID), Path: value.Path, RelativePath: value.RelativePath, FolderName: value.FolderName, Name: value.Name, Size: value.Size, ReleaseGroup: value.ReleaseGroup, DownloadID: value.DownloadID, CustomFormatScore: valueValueInt32(value.CustomFormatScore), IndexerFlags: valueValueInt32(value.IndexerFlags)}
	if value.Movie != nil {
		result.Movie = &ArrMovieReference{ID: rawInt64(value.Movie.ID), Title: value.Movie.Title, TmdbID: rawOptionalInt64(value.Movie.TMDBID), ImdbID: rawString(value.Movie.IMDBID)}
	}
	if value.MovieFileID != nil {
		result.MovieFileID = rawInt64(*value.MovieFileID)
	}
	for _, language := range value.Languages {
		encoded, _ := json.Marshal(map[string]any{"id": language.ID, "name": language.Name, "isoCode": language.ISOCode})
		result.Languages = append(result.Languages, encoded)
	}
	if value.Quality != nil {
		result.Quality = nativeQualityJSON(value.Quality)
	}
	for _, rejection := range value.Rejections {
		result.Rejections = append(result.Rejections, ArrImportRejection{Code: rejection.Type, Reason: firstNonEmpty(rejection.Reason, rejection.Message)})
	}
	return result
}

func sonarrPreviewResource(value sonarrnative.ManualImportCandidate) SonarrManualImportResource {
	result := SonarrManualImportResource{ID: rawInt64(value.ID), Path: value.Path, RelativePath: value.RelativePath, FolderName: value.FolderName, Name: value.Name, Size: value.Size, ReleaseGroup: value.ReleaseGroup, DownloadID: value.DownloadID, ReleaseType: value.ReleaseType, CustomFormatScore: valueValueInt32(value.CustomFormatScore), IndexerFlags: valueValueInt32(value.IndexerFlags), SeasonNumber: cloneInt32AsInt(value.SeasonNumber)}
	if value.Series != nil {
		result.Series = &ArrSeriesReference{ID: rawInt64(value.Series.ID), Title: value.Series.Title, TvdbID: rawOptionalInt64(value.Series.TVDBID), TvMazeID: rawOptionalInt64(value.Series.TVMazeID)}
	}
	if value.EpisodeFileID != nil {
		result.EpisodeFileID = rawInt64(*value.EpisodeFileID)
	}
	for _, episode := range value.Episodes {
		result.Episodes = append(result.Episodes, ArrEpisodeReference{SeriesID: rawInt64(episode.SeriesID), ID: rawInt64(episode.ID), EpisodeFileID: rawOptionalInt64(episode.EpisodeFileID), SeasonNumber: int(episode.SeasonNumber), EpisodeNumber: int(episode.EpisodeNumber), AbsoluteEpisodeNumber: intValue(episode.AbsoluteEpisodeNumber), SceneAbsoluteEpisodeNumber: intValue(episode.SceneAbsoluteEpisodeNumber)})
	}
	if value.Language != nil {
		encoded, _ := json.Marshal(map[string]any{"id": value.Language.ID, "name": value.Language.Name})
		result.Languages = append(result.Languages, encoded)
	}
	for _, language := range value.Languages {
		encoded, _ := json.Marshal(map[string]any{"id": language.ID, "name": language.Name})
		result.Languages = append(result.Languages, encoded)
	}
	if value.Quality != nil {
		result.Quality = nativeQualityJSON(value.Quality)
	}
	if value.Forced != nil {
		copyValue := *value.Forced
		result.Forced = &copyValue
	}
	if value.HearingImpaired != nil {
		copyValue := *value.HearingImpaired
		result.HearingImpaired = &copyValue
	}
	for _, rejection := range value.Rejections {
		result.Rejections = append(result.Rejections, ArrImportRejection{Code: rejection.Type, Reason: firstNonEmpty(rejection.Reason, rejection.Message)})
	}
	return result
}

func (client *Client) mapPreviewResponseForResources(resources any, files []ports.ImportFile, folder, registeredID string, expectedEpisodeSets map[string][]string, expectedDownloadIDs map[string]string, retainNative bool) ([]ports.ImportFile, []ports.ImportRejection, error) {
	if client.config.Kind == domain.ConnectionRadarr {
		values, ok := resources.([]RadarrManualImportResource)
		if !ok {
			return nil, nil, malformed("arr.manual_import.preview.radarr")
		}
		accepted, _, rejected, err := client.mapPreviewResponseDetailedFromRadarr(values, files, folder, registeredID, expectedEpisodeSets, expectedDownloadIDs, retainNative)
		return accepted, rejected, err
	}
	values, ok := resources.([]SonarrManualImportResource)
	if !ok {
		return nil, nil, malformed("arr.manual_import.preview.sonarr")
	}
	accepted, _, rejected, err := client.mapPreviewResponseDetailedFromSonarr(values, files, folder, registeredID, expectedEpisodeSets, expectedDownloadIDs, retainNative)
	return accepted, rejected, err
}

// These two small wrappers keep the native conversion independent from the
// JSON decoder used by the legacy/reprocess bridge while sharing its reviewed
// exact-path and association checks.
func (client *Client) mapPreviewResponseDetailedFromRadarr(values []RadarrManualImportResource, files []ports.ImportFile, folder, registeredID string, expectedEpisodeSets map[string][]string, expectedDownloadIDs map[string]string, retainNative bool) ([]ports.ImportFile, []ReprocessFile, []ports.ImportRejection, error) {
	body, err := json.Marshal(values)
	if err != nil {
		return nil, nil, nil, err
	}
	return client.mapPreviewResponseDetailed(body, files, folder, registeredID, expectedEpisodeSets, expectedDownloadIDs, retainNative)
}

func (client *Client) mapPreviewResponseDetailedFromSonarr(values []SonarrManualImportResource, files []ports.ImportFile, folder, registeredID string, expectedEpisodeSets map[string][]string, expectedDownloadIDs map[string]string, retainNative bool) ([]ports.ImportFile, []ReprocessFile, []ports.ImportRejection, error) {
	body, err := json.Marshal(values)
	if err != nil {
		return nil, nil, nil, err
	}
	return client.mapPreviewResponseDetailed(body, files, folder, registeredID, expectedEpisodeSets, expectedDownloadIDs, retainNative)
}

func rawInt64(value int64) json.RawMessage { return json.RawMessage(strconv.FormatInt(value, 10)) }

func rawOptionalInt64(value *int64) json.RawMessage {
	if value == nil {
		return nil
	}
	return rawInt64(*value)
}

func rawString(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func valueValueInt32(value *int32) int {
	if value == nil {
		return 0
	}
	return int(*value)
}

func cloneInt32AsInt(value *int32) *int {
	if value == nil {
		return nil
	}
	copyValue := int(*value)
	return &copyValue
}

func intValue(value *int32) int {
	if value == nil {
		return 0
	}
	return int(*value)
}

func nativeQualityJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return encoded
}
