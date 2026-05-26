// Package systembackup ports the v5 BackupController family at
// /api/v5/system/backup.
//
// Source: src/Sonarr.Api.V5/System/Backup/BackupController.cs
// (135 LOC) + BackupResource.cs (13 LOC).
//
// 4 endpoints (1 GET + 1 DELETE + 2 POST). Two divergences from v3:
//
//  1. DELETE /system/backup/{id} returns 204 (v5 NoContent) vs v3's
//     200 + `{}`. Status-only — wrapped via v5util.RewriteSuccessTo204.
//
//  2. POST /system/backup/restore/upload bad-extension path: v5
//     returns 400 + `{"error":"Invalid extension..."}` (TypedResults
//     .BadRequest<object>) whereas v3 throws UnsupportedMediaTypeException
//     mapping to 415 + standard error envelope. BOTH status AND body
//     shape differ — write a v5-specific handler from scratch rather
//     than rewrite v3's response.
//
// GET /system/backup is a pure path-alias (200 + identical envelope).
// POST /system/backup/restore/{id} is also pure path-alias (200 +
// `{"restartRequired":true}`, 404 on missing id — same as v3).
//
// BackupResource shape is byte-identical between v3 and v5 (6 fields:
// id/name/path/size/type/time). Type alias is wire-safe.
package systembackup

import (
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
	"sonarr.tv/sonarr/internal/httperr"
)

// Resource is the v5 BackupResource — wire-identical to v3.
type Resource = apiv3.BackupResource

// Admin is the read+admin surface for List/Delete/Restore handlers.
type Admin = apiv3.BackupAdmin

// Uploader is the surface for the RestoreUpload handler.
type Uploader = apiv3.BackupUploader

// ListHandler aliases the v3 list handler — identical wire shape
// (200 + descending Time sort + same Resource projection).
var ListHandler = apiv3.BackupListHandler

// RestoreHandler aliases the v3 restore-by-id handler — identical
// wire shape (200 + `{"restartRequired":true}`, 404 on missing id).
var RestoreHandler = apiv3.BackupRestoreHandler

// DeleteHandler wraps the v3 delete handler with the 200→204
// rewriter. v3 emits 200 + `{}`; v5 wants 204 + empty. The wrapper's
// predicate only matches 200 success responses; failure status
// codes (404 on missing id, 500 on disk error) pass through
// unchanged.
func DeleteHandler(svc Admin) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.BackupDeleteHandler(svc))
}

// validExtensions ports the C# allowlist (BackupController.cs:21).
var validExtensions = []string{".zip", ".db", ".xml"}

// uploadMaxBytes matches the C# `[RequestFormLimits(MultipartBodyLengthLimit = 5000000000)]`.
const uploadMaxBytes = 5_000_000_000

// uploadParseMemory matches the v3 helper — 32MB in-memory before
// spilling to disk (matches Go stdlib defaultMaxMemory).
const uploadParseMemory = 32 << 20

// RestoreUploadHandler returns POST /api/v5/system/backup/restore/upload.
// Mirrors `BackupController.RestoreUpload` at `:92-119`.
//
// Wire-shape divergence from v3: bad-extension path returns 400 with
// `{"error":"Invalid extension, must be one of: .zip, .db, .xml"}`
// (per v5 `TypedResults.BadRequest<object>(new { error = ... })`).
// v3 returns 415 with the standard envelope. Happy path identical:
// 200 + `{"restartRequired":true}`.
//
// Missing file → 400 "file must be provided" (envelope; matches both
// v3 and v5 — both throw BadRequestException for this case).
func RestoreUploadHandler(svc Uploader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, uploadMaxBytes)
		if err := r.ParseMultipartForm(uploadParseMemory); err != nil {
			httperr.BadRequest(w, "system/backup: "+err.Error())
			return
		}
		if r.MultipartForm == nil || len(r.MultipartForm.File) == 0 {
			httperr.BadRequest(w, "file must be provided")
			return
		}
		fh := firstFile(r.MultipartForm.File)
		if fh == nil {
			httperr.BadRequest(w, "file must be provided")
			return
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if !extensionAllowed(ext) {
			// v5-specific shape: 400 + `{"error":"..."}` (NOT the
			// standard envelope). Matches v5 C# TypedResults.BadRequest.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			buf, _ := json.Marshal(struct {
				Error string `json:"error"`
			}{Error: "Invalid extension, must be one of: " + strings.Join(validExtensions, ", ")})
			_, _ = w.Write(buf)
			return
		}
		src, err := fh.Open()
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/backup: open upload: %w", err), nil)
			return
		}
		defer func() { _ = src.Close() }()
		path := filepath.Join(svc.TempPath(), "sonarr_backup_restore"+ext)
		if err := svc.SaveStream(src, path); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/backup: save %q: %w", path, err), nil)
			return
		}
		if err := svc.Restore(path); err != nil {
			_ = svc.DeleteFile(path)
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/backup: restore %q: %w", path, err), nil)
			return
		}
		_ = svc.DeleteFile(path)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"restartRequired":true}`))
	})
}

func extensionAllowed(ext string) bool {
	for _, e := range validExtensions {
		if ext == e {
			return true
		}
	}
	return false
}

// firstFile returns the first *multipart.FileHeader across the form's
// field map. The Go stdlib's `*multipart.Form.File` is unordered, so
// pick whichever field has at least one file. Mirrors the v3 helper
// at `internal/api/v3/system_backup.go:239-249`.
func firstFile(files map[string][]*multipart.FileHeader) *multipart.FileHeader {
	for _, fhs := range files {
		if len(fhs) > 0 {
			return fhs[0]
		}
	}
	return nil
}
