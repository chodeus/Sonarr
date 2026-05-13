// Package v3 mirrors `src/Sonarr.Api.V3/**` controller-by-controller.
// Each .go file ports a single C# controller; resources (DTOs) and
// the http.Handler that produces them live together in the same file
// for the small endpoints, splitting only when the resource or
// validator gets sizeable.
//
// Route templates come from the C# `[V3ApiController]` attribute
// (`src/Sonarr.Http/VersionedApiControllerAttribute.cs:29`), which
// produces `api/v3/{controller}` where the trailing segment is the
// type name minus the "Controller" suffix, lowercased.
//
// JSON serialisation follows the conventions baked into
// `NzbDrone.Common.Serializer.STJson.ApplySerializerSettings`:
//   - camelCase property names
//   - null fields omitted on write
//   - WriteIndented true (the harness compacts before diff so this is
//     immaterial for byte-equality)
//   - enums emit as camelCase strings
//
// Auth: every V3 controller is gated by the Startup FallbackPolicy
// unless it carries `[AllowAnonymous]`. The Go port mirrors that by
// registering all V3 routes with the host's default
// `Anonymous: false` flag so requireAPIKey wraps them.
package v3
