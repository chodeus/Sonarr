package v3

import (
	"crypto/sha1" //nolint:gosec // SHA1 used for stable wire-id derivation, not security
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/backup"
	"sonarr.tv/sonarr/internal/httperr"
)

// BackupResource mirrors `src/Sonarr.Api.V3/System/Backup/BackupResource.cs`.
// 6 fields: id (derived from the SHA1-int31 of "backup-{type}-{name}"
// per C# HashConverter), name, path ("/backup/{type}/{name}"), size,
// type (camelCase enum: scheduled/manual/update), time.
type BackupResource struct {
	ID   int       `json:"id"`
	Name string    `json:"name"`
	Path string    `json:"path"`
	Size int64     `json:"size"`
	Type string    `json:"type"`
	Time time.Time `json:"time"`
}

// BackupAdmin is the read+admin surface for /api/v3/system/backup.
// Combines the service-level GetBackups + Restore with the
// filesystem-level FileExists + DeleteFile that the C# action calls
// against IDiskProvider directly.
type BackupAdmin interface {
	GetBackups() ([]backup.Backup, error)
	GetBackupFolderForType(t backup.Type) string
	Restore(backupFileName string) error
	FileExists(path string) bool
	DeleteFile(path string) error
}

// BackupListHandler returns GET /api/v3/system/backup. Mirrors
// `BackupController.GetBackupFiles` at `:34-50`. Sort: Time descending.
func BackupListHandler(svc BackupAdmin) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := svc.GetBackups()
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/backup: %w", err), nil)
			return
		}
		out := make([]BackupResource, 0, len(rows))
		for _, b := range rows {
			out = append(out, backupToResource(b))
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// BackupDeleteHandler returns DELETE /api/v3/system/backup/{id}.
// Mirrors `BackupController.DeleteBackup` at `:52-72`. 404 when the
// id doesn't match a known backup OR when the file is missing on
// disk (the C# action calls _diskProvider.FileExists explicitly
// before _diskProvider.DeleteFile and throws NotFound on miss).
func BackupDeleteHandler(svc BackupAdmin) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseBackupID(w, r)
		if !ok {
			return
		}
		b, ok, err := findBackupByID(svc, id)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/backup: list: %w", err), nil)
			return
		}
		if !ok {
			httperr.NotFound(w, fmt.Sprintf("backup %d not found", id))
			return
		}
		path := filepath.Join(svc.GetBackupFolderForType(b.Type), b.Name)
		if !svc.FileExists(path) {
			httperr.NotFound(w, fmt.Sprintf("backup file %q not found", path))
			return
		}
		if err := svc.DeleteFile(path); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/backup: delete %q: %w", path, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// BackupRestoreHandler returns POST /api/v3/system/backup/restore/{id}.
// Mirrors `BackupController.Restore` at `:74-92`. Returns
// `{"restartRequired":true}` on success — the user is expected to
// restart Sonarr to pick up the restored state.
func BackupRestoreHandler(svc BackupAdmin) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseBackupID(w, r)
		if !ok {
			return
		}
		b, ok, err := findBackupByID(svc, id)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/backup: list: %w", err), nil)
			return
		}
		if !ok {
			httperr.NotFound(w, fmt.Sprintf("backup %d not found", id))
			return
		}
		path := filepath.Join(svc.GetBackupFolderForType(b.Type), b.Name)
		if err := svc.Restore(path); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/backup: restore %q: %w", path, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(struct {
			RestartRequired bool `json:"restartRequired"`
		}{RestartRequired: true})
		_, _ = w.Write(buf)
	})
}

// BackupUploader is the admin surface for POST /system/backup/restore/upload.
// Kept separate from [BackupAdmin] so the list/delete/restore handlers
// don't grow a new TempPath/SaveStream dependency. Mirrors the four
// IDiskProvider + IAppFolderInfo + IBackupService method calls the C#
// `UploadAndRestore` action makes against its three injected services.
type BackupUploader interface {
	TempPath() string
	SaveStream(r io.Reader, path string) error
	Restore(backupFileName string) error
	DeleteFile(path string) error
}

// backupUploadMaxBytes mirrors the C# `[RequestFormLimits(MultipartBodyLengthLimit = 5000000000)]`
// attribute on `BackupController.UploadAndRestore` (`:95`). 5GB matches
// the C# literal; Kestrel's default multipart limit is 128MB so the
// attribute is the active cap.
const backupUploadMaxBytes = 5_000_000_000

// backupUploadParseMemory is the in-memory buffer threshold passed to
// [http.Request.ParseMultipartForm]. Above this, the multipart reader
// spills file parts to disk via [os.CreateTemp]. The C# pipeline does
// the same via Kestrel's `MultipartReader` defaults. 32MB matches Go's
// stdlib `defaultMaxMemory` constant.
const backupUploadParseMemory = 32 << 20

// backupValidExtensions ports the C# allowlist at
// `BackupController.cs:23`. Case-folded compare to match
// `Path.GetExtension` + `List<string>.Contains` (.NET defaults to
// case-insensitive on Windows; the Go port goes case-insensitive
// everywhere for cross-OS parity).
var backupValidExtensions = []string{".zip", ".db", ".xml"}

// BackupRestoreUploadHandler returns POST /api/v3/system/backup/restore/upload.
// Mirrors `BackupController.UploadAndRestore` at `:94-125`:
//
//   - 5GB total-body cap via [http.MaxBytesReader].
//   - First multipart file (any field name) is the backup archive.
//   - Extension must be one of .zip / .db / .xml (C# 415 path).
//   - SaveStream to `{TempPath}/sonarr_backup_restore{ext}`, then Restore,
//     then best-effort DeleteFile of the temp staging file.
//   - Returns `{"restartRequired":true}` on success.
//
// Error handling matches C# Sonarr's pipeline mappings:
// missing/empty file → 400 with "file must be provided"; bad extension
// → 415 with the allowlist; Restore() failure → 500 (and we still try
// to clean up the staged file).
func BackupRestoreUploadHandler(svc BackupUploader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, backupUploadMaxBytes)
		if err := r.ParseMultipartForm(backupUploadParseMemory); err != nil {
			// MaxBytesReader returns *http.MaxBytesError on overflow; the
			// stdlib wraps it via ParseMultipartForm. Tag the message but
			// keep the status at 400 — C# returns 400 on a malformed body
			// too (the framework's ModelBindingException → BadRequest).
			httperr.BadRequest(w, "system/backup: "+err.Error())
			return
		}
		fh := firstMultipartFile(r)
		if fh == nil {
			httperr.BadRequest(w, "file must be provided")
			return
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if !backupExtensionAllowed(ext) {
			httperr.Write(w, http.StatusUnsupportedMediaType,
				"Invalid extension, must be one of: "+strings.Join(backupValidExtensions, ", "),
				"", nil)
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
			// Best-effort cleanup before returning the error. Mirrors the
			// fact that the C# action does NOT clean up on Restore failure
			// (the `DeleteFile` call sits after Restore on the happy path)
			// — the Go port goes slightly stricter so a partial-recovery
			// retry doesn't trip over a stale staging file.
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

// firstMultipartFile returns the first uploaded file across all form
// fields, or nil if none. The C# action uses `Request.Form.Files[0]`
// which iterates the framework's `IFormFileCollection` in insertion
// order — multipart field names are NOT significant. The Go stdlib's
// `*multipart.Form.File` map is unordered, so we pick whichever field
// has at least one file. Tests using a single-file upload (the React
// UI's only call site) get deterministic behaviour either way.
func firstMultipartFile(r *http.Request) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	for _, fhs := range r.MultipartForm.File {
		if len(fhs) > 0 {
			return fhs[0]
		}
	}
	return nil
}

func backupExtensionAllowed(ext string) bool {
	for _, e := range backupValidExtensions {
		if ext == e {
			return true
		}
	}
	return false
}

func parseBackupID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		httperr.BadRequest(w, "system/backup: invalid id")
		return 0, false
	}
	return id, true
}

func findBackupByID(svc BackupAdmin, id int) (backup.Backup, bool, error) {
	rows, err := svc.GetBackups()
	if err != nil {
		return backup.Backup{}, false, err
	}
	for _, b := range rows {
		if backupID(b) == id {
			return b, true, nil
		}
	}
	return backup.Backup{}, false, nil
}

func backupToResource(b backup.Backup) BackupResource {
	return BackupResource{
		ID:   backupID(b),
		Name: b.Name,
		Path: fmt.Sprintf("/backup/%s/%s", b.Type.String(), b.Name),
		Size: b.Size,
		Type: b.Type.String(),
		Time: b.Time,
	}
}

// backupID ports C# `HashConverter.GetHashInt31($"backup-{type}-{name}")`:
// SHA1("backup-{type}-{name}") → first 4 bytes little-endian as
// int32 with the high bit cleared. The C# implementation uses
// `Encoding.Default.GetBytes` which on .NET 6+ is UTF-8; the Go port
// matches via string-byte casting (the bytes are UTF-8 too).
func backupID(b backup.Backup) int {
	h := sha1.Sum([]byte(fmt.Sprintf("backup-%s-%s", b.Type.String(), b.Name))) //nolint:gosec // stable wire id
	v := binary.LittleEndian.Uint32(h[:4])
	return int(v & 0x7fffffff)
}
