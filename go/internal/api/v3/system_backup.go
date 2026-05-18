package v3

import (
	"crypto/sha1" //nolint:gosec // SHA1 used for stable wire-id derivation, not security
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
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
