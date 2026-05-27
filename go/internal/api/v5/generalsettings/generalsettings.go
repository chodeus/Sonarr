// Package generalsettings is the v5 wire surface for
// /api/v5/settings/general (the global "general" settings family — 37
// fields covering bind/port/SSL/auth/proxy/branch/update/backup state).
//
// Source: src/Sonarr.Api.V5/Settings/GeneralSettingsController.cs
// (114 LOC) + GeneralSettingsResource.cs (93 LOC).
//
// The C# v5 controller inherits from `SettingsController<TResource>`
// at SettingsController.cs:11-54 and produces the same status codes
// as the v3 HostConfigController:
//
//	GET  → 200 OK         (v3: same)
//	PUT  → 202 Accepted   (v3: same)
//
// Wire shape diverges from v3 by ONE field: v3 emits
// `trustCgnatIpAddresses` (bool, added to v3 only — newer than the v5
// branch fork); v5 does NOT carry this field. Otherwise the wire
// shape is byte-identical (37 v5 fields all present on v3's
// HostConfigResource with the same JSON tag spelling and types).
//
// Strategy: wrap the v3 handlers via a small response-rewriting
// middleware that strips the `trustCgnatIpAddresses` key from the
// JSON object body before forwarding to the client. v3's
// `DisallowUnknownFields` decoder accepts v5-shaped POST bodies
// because the only missing field is `trustCgnatIpAddresses` which
// defaults to false on v3's HostConfigResource (the absence is
// indistinguishable from `trustCgnatIpAddresses=false` on input).
//
// SaveSettings override: the C# v5 controller adds a user-upsert
// step (when Username + Password both set, _userService.Upsert is
// called) before falling through to base SaveSettings. v3
// HostConfigUpdateHandler does NOT include this user-upsert path
// — DEFERRED on the v5 surface as well; pending a IUserService Go
// port. Real v5 clients sending Username+Password will not have the
// user account persisted yet; tracked under
// `AUDIT_FIX_BACKLOG.md` (v5 follow-up: GeneralSettings user upsert).
//
// Validation chain in C# v5 (lines 27-72) is substantially richer
// than v3 Go-port's validateHostConfig: BindAddress IP validation
// (when not "*"/"localhost"), Port valid, UrlBase valid, InstanceName
// Sonarr-branding check, Username/Password conditional on Forms auth,
// AuthenticationMethod NotEqual(Basic), PasswordConfirmation match,
// SslPort valid + not equal Port when EnableSsl, SslCertPath chain
// of validators (FileExists + IsValidCertificate), SslKeyPath
// FileExists when set, LogSizeLimit ∈ [1,10], Branch not empty,
// UpdateScriptPath valid when Script, BackupFolder valid path,
// BackupInterval ∈ [1,7], BackupRetention ∈ [1,90]. v3 Go-port covers
// the subset that doesn't need filesystem-checking validators. Same
// DEFERRED state as the J282 MediaManagement deep validators —
// follow-up slice once the disk-validator adapter shims land.
package generalsettings

import (
	"encoding/json"
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// strippedField is the wire key removed from v3 responses before
// emitting on the v5 surface. v5 GeneralSettings doesn't carry this.
const strippedField = "trustCgnatIpAddresses"

// Getter is the read-side interface this package's handlers depend on.
// Aliased from v3 because the underlying service composition is
// identical — same hostConfigGetter adapter at host_wiring.go:1033
// satisfies both surfaces.
type Getter = apiv3.HostConfigGetter

// Writer is the read/write interface for the PUT handler. Aliased
// from v3.
type Writer = apiv3.HostConfigWriter

// GetHandler returns GET /api/v5/settings/general (and the /{id}
// variant). Wraps v3's HostConfigHandler with a field-strip
// middleware that removes the trustCgnatIpAddresses key from the
// JSON body. Other fields pass through untouched.
func GetHandler(svc Getter) http.Handler {
	return stripField(strippedField, apiv3.HostConfigHandler(svc))
}

// UpdateHandler returns PUT /api/v5/settings/general/{id}. Wraps v3's
// HostConfigUpdateHandler with the same field-strip middleware. v3's
// DisallowUnknownFields decoder accepts v5-shaped POST bodies
// (missing trustCgnatIpAddresses defaults to false — same as if the
// v3 client had explicitly sent the field). The response body has
// the trustCgnatIpAddresses key stripped before reaching the client.
func UpdateHandler(svc Writer) http.Handler {
	return stripField(strippedField, apiv3.HostConfigUpdateHandler(svc))
}

// stripField wraps the given handler and, on a JSON-object success
// response (200 / 202), removes the named top-level field before
// emitting. Error bodies (400/404/500) and non-JSON responses pass
// through unchanged.
func stripField(field string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := v5util.NewBufferingWriter()
		h.ServeHTTP(rec, r)
		bodyBytes := rec.Body()
		status := rec.Status()
		// Only strip on success status with a JSON object body. Other
		// shapes (errors / arrays / scalars) pass through verbatim.
		if (status == http.StatusOK || status == http.StatusAccepted) && len(bodyBytes) > 0 && bodyBytes[0] == '{' {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(bodyBytes, &obj); err == nil {
				delete(obj, field)
				buf, err := json.Marshal(obj)
				if err == nil {
					v5util.CopyHeaders(w.Header(), rec.Header())
					w.WriteHeader(status)
					_, _ = w.Write(buf)
					return
				}
			}
		}
		v5util.CopyHeaders(w.Header(), rec.Header())
		w.WriteHeader(status)
		_, _ = w.Write(bodyBytes)
	})
}
