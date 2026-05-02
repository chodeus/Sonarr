# Authentication & Security

**Source:** `src/NzbDrone.Core/Authentication/**`, `src/NzbDrone.Core/Security/**`, `src/Sonarr.Http/Authentication/**`, `src/NzbDrone.Common/Http/Dispatchers/ICertificationValidationService.cs`, `src/NzbDrone.Common/Options/AuthOptions.cs`, plus the wiring in `src/NzbDrone.Host/Startup.cs` / `src/NzbDrone.Host/Bootstrap.cs`.
**Target Go package:** `go/internal/auth/`, `go/internal/security/` (HTTP middleware likely in `go/internal/httpserver/middleware/`).
**Status:** drafted

## Purpose
Two related concerns live here: (1) **inbound authentication** — Sonarr's HTTP server gates the embedded React UI, the V3/V5 REST APIs, and the SignalR hub behind a single configurable login plus a server-wide API key; (2) **outbound TLS trust** — Sonarr makes thousands of HTTPS calls per day (indexers, download clients, metadata providers, notification webhooks, SMTP) through a single shared `HttpClient` whose `RemoteCertificateValidationCallback` is `X509CertificateValidationService.ShouldByPassValidationError`. Both surfaces are configured via a single global config file (`config.xml`) and exposed to the user through `Sonarr.Api.V3/Config/HostConfigController`.

There is exactly one local user; multiple authentication providers (basic / forms / external) are declared but only `Forms` and `None` are practically reachable. There is no OIDC, no OAuth, no LDAP, no Plex SSO for *Sonarr's own login* (Plex/Trakt OAuth in the notification providers is unrelated — see `notifications.md`).

## Public types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `User : ModelBase` | `src/NzbDrone.Core/Authentication/User.cs:6` | DB row | `Identifier (Guid)`, `Username`, `Password (base64 PBKDF2 hash)`, `Salt (base64)`, `Iterations (int)`. Stored in table `Users`. |
| `IUserService` / `UserService` | `src/NzbDrone.Core/Authentication/UserService.cs:14`, `:24` | service | `Add`, `Update`, `Upsert`, `FindUser()`, `FindUser(username,password)`, `FindUser(Guid)`. Also `IHandle<ApplicationStartedEvent>` to seed user from legacy `<Username>`/`<Password>` config-file elements (line 160). |
| `IUserRepository` / `UserRepository` | `src/NzbDrone.Core/Authentication/UserRepository.cs:8`, `:14` | repo | Plain Marr `BasicRepository<User>` with `FindUser(string username)` and `FindUser(Guid identifier)` lookups. |
| `AuthenticationType` enum | `src/NzbDrone.Core/Authentication/AuthenticationType.cs:5` | enum | `None=0`, `Basic=1` (`[Obsolete("Use Forms authentication instead")]`), `Forms=2`, `External=3`. |
| `AuthenticationRequiredType` enum | `src/NzbDrone.Core/Authentication/AuthenticationRequiredType.cs:3` | enum | `Enabled=0`, `DisabledForLocalAddresses=1`. |
| `CertificateValidationType` enum | `src/NzbDrone.Core/Security/CertificateValidationType.cs:3` | enum | `Enabled=0`, `DisabledForLocalAddresses=1`, `Disabled=2`. |
| `ICertificateValidationService` | `src/NzbDrone.Common/Http/Dispatchers/ICertificationValidationService.cs:6` | iface | Single method `ShouldByPassValidationError(sender, cert, chain, sslPolicyErrors) -> bool`. Misnamed — its semantics are *"this cert is acceptable"*, not "we're bypassing an error". |
| `X509CertificateValidationService` | `src/NzbDrone.Core/Security/X509CertificateValidationService.cs:12` | service | Implements `ICertificateValidationService`. Plugged into `ManagedHttpDispatcher` at line 160 and into `MailKit.SmtpClient.ServerCertificateValidationCallback` at `src/NzbDrone.Core/Notifications/Email/Email.cs:155`. |
| `ApiKeyAuthenticationOptions`, `ApiKeyAuthenticationHandler` | `src/Sonarr.Http/Authentication/ApiKeyAuthenticationHandler.cs:13`, `:24` | ASP.NET auth scheme | Two named instances: `"API"` and `"SignalR"` — both look up the same shared `IConfigFileProvider.ApiKey`. |
| `NoAuthenticationHandler` | `src/Sonarr.Http/Authentication/NoAuthenticationHandler.cs:12` | ASP.NET auth scheme | Always succeeds, builds `ClaimsPrincipal { user="Anonymous", AuthType=None }`. Used for `AuthenticationType.None` *and* `AuthenticationType.External` (see `AuthenticationBuilderExtensions.AddExternal`, line 28). |
| `BypassableDenyAnonymousAuthorizationRequirement` | `src/Sonarr.Http/Authentication/BypassableDenyAnonymousAuthorizationRequirement.cs:5` | requirement | Empty subclass of `DenyAnonymousAuthorizationRequirement`; the only purpose is to give `UiAuthorizationHandler` a unique requirement type to bind to so it can short-circuit the deny. |
| `UiAuthorizationHandler` | `src/Sonarr.Http/Authentication/UiAuthorizationHandler.cs:14` | handler + `IHandle<ConfigSavedEvent>` | Caches `AuthenticationRequiredType` in a *static* field (`_authenticationRequired`, line 17) and refreshes it on `ConfigSavedEvent` (line 43). Calls `context.Succeed` only when `DisabledForLocalAddresses` is set and the remote IP is local (or CGNAT and `TrustCgnatIpAddresses==true`). |
| `UiAuthorizationPolicyProvider` | `src/Sonarr.Http/Authentication/UiAuthorizationPolicyProvider.cs:9` | policy provider | Defines policy `"UI"`. Builds a policy with the *configured* auth scheme (`config.AuthenticationMethod.ToString()` — i.e. `"None"`, `"Forms"`, or `"External"`) plus the bypassable deny-anonymous requirement. Falls back to `DefaultAuthorizationPolicyProvider` for `"API"` / `"SignalR"`. |
| `IAuthenticationService` / `AuthenticationService` | `src/Sonarr.Http/Authentication/AuthenticationService.cs:9`, `:16` | service | `Login`, `Logout`, `LogUnauthorized`. Logs to a dedicated NLog logger named `"Auth"`. Caches `AuthenticationMethod` in a *static* field (`AUTH_METHOD`, line 21) — never refreshed on `ConfigSavedEvent`, see SEC-Q1. |
| `AuthenticationController` | `src/Sonarr.Http/Authentication/AuthenticationController.cs:20` | controller | `[AllowAnonymous]`. `POST /login` and `GET /logout`. Builds claims `{user, identifier (Guid), AuthType}` and calls `HttpContext.SignInAsync(AuthenticationType.Forms.ToString(), …, IsPersistent = RememberMe == "on")`. |
| `LoginResource` | `src/Sonarr.Http/Authentication/LoginResource.cs:3` | DTO | `Username`, `Password`, `RememberMe` (string — see AUTH-Q3). |
| `AuthenticationBuilderExtensions` | `src/Sonarr.Http/Authentication/AuthenticationBuilderExtensions.cs:14` | DI extension | The single source of truth for the scheme list. `AddAppAuthentication()` at line 33. |
| `AuthOptions` | `src/NzbDrone.Common/Options/AuthOptions.cs:3` | options POCO | Bound from config section `Sonarr:Auth` (env vars / external config). Fields: `ApiKey`, `Enabled` (bool? — legacy "is auth on" toggle), `Method`, `Required`, `TrustCgnatIpAddresses`. Wired in `Bootstrap.cs:110` and `:187`. |
| `ResetApiKeyCommand` | `src/NzbDrone.Core/Configuration/ResetApiKeyCommand.cs:5` | command | `SendUpdatesToClient = true`. Handled by `ConfigFileProvider.Execute` (`ConfigFileProvider.cs:510`) which calls `SetValue("ApiKey", GenerateApiKey())`. |
| `CleanupAdditionalUsers` | `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupAdditionalUsers.cs:6` | housekeeping task | `DELETE FROM Users WHERE Id NOT IN (SELECT Id FROM Users LIMIT 1)` — enforces the single-user invariant. |

## Public services

| Type | Methods | Side effects | Notes |
|------|---------|--------------|-------|
| `UserService` | `Add`, `Update`, `Upsert`, `FindUser`(3 overloads) | Inserts/updates `Users`, on first run reads legacy `<Username>`/`<Password>` from config XML and migrates | `FindUser(string,string)` (line 83) implements the upgrade path: if `Salt` is null and the stored `Password == sha256(input)`, it re-hashes with PBKDF2 and persists (lines 97–108). Returns `null` on mismatch — no exception. |
| `AuthenticationService` | `Login(request, username, password)`, `Logout(context)`, `LogUnauthorized(req)` | Writes auth log lines, never touches DB beyond delegation to `UserService` | When `AUTH_METHOD == None`, `Login` returns `null` *unconditionally* (line 31) — the `/login` POST will redirect back with `loginFailed=true`. Logged-in `User` is returned; the controller is responsible for emitting the cookie. |
| `X509CertificateValidationService` | `ShouldByPassValidationError(sender, cert, chain, sslPolicyErrors)` | Logs an error when a remote cert uses `md5RSA` (line 43) or when validation fails for a non-local address (line 72) | Always returns `true` when sender is *not* an `SslStream` or `string` (line 27) — that is, anything coming from a non-MailKit, non-`SocketsHttpHandler` callsite is silently trusted. SEC-Q3. |

## DB tables touched
- `Users` — read/write via `UserRepository`. Schema captured in migrations 076 (`src/NzbDrone.Core/Datastore/Migration/076_add_users_table.cs`) and 174 (`src/NzbDrone.Core/Datastore/Migration/174_add_salt_to_users.cs`):
  - `Id INTEGER PK`, `Identifier TEXT NOT NULL UNIQUE`, `Username TEXT NOT NULL UNIQUE`, `Password TEXT NOT NULL`, `Salt TEXT NULL`, `Iterations INT NULL`.
  - Mapped at `src/NzbDrone.Core/Datastore/TableMapping.cs:160` (`Mapper.Entity<User>("Users").RegisterModel()`).
- No dedicated table for sessions/cookies — sessions are signed/encrypted client-side cookies issued by ASP.NET's `CookieAuthenticationHandler` (state-less server side); the keys to do that are persisted to `<AppData>/asp/` via `services.AddDataProtection().PersistKeysToFileSystem(...)` (`src/NzbDrone.Host/Startup.cs:225-226`, key folder built in `src/NzbDrone.Common/Extensions/PathExtensions.cs:318-320` as `Path.Combine(AppDataPath, "asp")`).

## Password storage (the API-contract-sensitive part)

Algorithm and parameters hard-coded in `src/NzbDrone.Core/Authentication/UserService.cs:26-28`:

```
ITERATIONS    = 10000
SALT_SIZE     = 128 / 8 = 16 bytes
NUMBER_OF_BYTES = 256 / 8 = 32 bytes  (output length)
```

Hashing (`UserService.cs:142-150`):
- `KeyDerivation.Pbkdf2(password, salt, prf: KeyDerivationPrf.HMACSHA512, iterationCount: 10000, numBytesRequested: 32)` — the .NET 10 `Microsoft.AspNetCore.Cryptography.KeyDerivation` package (`src/NzbDrone.Core/Sonarr.Core.csproj:10`).
- Output is base64-encoded and stored in `Users.Password`.
- Salt is generated with `RandomNumberGenerator.Create().GetBytes(byte[16])` (line 137), base64-encoded, stored in `Users.Salt`.
- `Users.Iterations` always equals 10000 in code today; the column exists because the value is read back during verification (`VerifyHashedPassword`, line 152) so a future port can ramp iterations without breaking existing rows.

Verification (`UserService.cs:152-158`):
- `VerifyHashedPassword` re-runs PBKDF2 with the *stored* salt + iterations and compares the resulting base64 string to `user.Password` with **`==`** — i.e. ordinary `string.Equals`, **not** `CryptographicOperations.FixedTimeEquals`. Same for the API key (`ApiKeyAuthenticationHandler.cs:63`: `_apiKey == providedApiKey`). SEC-Q4.

Legacy upgrade path (`UserService.cs:97-108`):
- If `user.Salt` is null/whitespace, the stored password is treated as a hex SHA-256 of the cleartext (`Hashing.SHA256Hash(string)` at `src/NzbDrone.Core/Hashing.cs:9` — UTF-8 bytes, lowercased hex). On a successful match, the row is rewritten to PBKDF2 and saved.
- If the SHA-256 doesn't match, returns `null` *without* attempting PBKDF2. So a row created post-174 will never enter this branch. AUTH-Q1.
- `Hashing.SHA256Hash` is also reused for non-auth purposes (see grep hits in `Update/UpdateVerification.cs`, `MediaCover/MediaCoverProxy.cs`, `Notifications/Trakt/...`) so the helper itself stays.

Bootstrap migration of the pre-076 cleartext config-file password (`UserService.cs:160-188`):
- On first `ApplicationStartedEvent`, if `Users` is empty *and* the XML config has both `<Username>` and `<Password>` elements, it `Add()`s a user with PBKDF2-hashed password.
- The legacy XML password element is left untouched in `config.xml`. AUTH-Q2.

## API key

Generation (`src/NzbDrone.Core/Configuration/ConfigFileProvider.cs:498-501`):
```csharp
private string GenerateApiKey() => Guid.NewGuid().ToString().Replace("-", "");
```
- That is, the lowercase hex of a `Guid.NewGuid()` — 32 hex chars, **122 bits** of entropy (per RFC 4122 v4 GUID structure). Not cryptographically generic-random; sufficient for a shared-secret API key but worth noting in SEC-Q5.

Read path (`ConfigFileProvider.cs:194-208`):
- Order of precedence: `_authOptions.ApiKey` (env var / external config — read-only override) ⇒ `<ApiKey>` element in `config.xml` ⇒ generate-and-persist.
- If the config contains `<ApiKey></ApiKey>` (empty), a new key is generated and saved on next read.

Reset path (`ConfigFileProvider.cs:510-513`):
- `Execute(ResetApiKeyCommand)` writes a new GUID to `<ApiKey>`. Existing API key holders are *not* invalidated server-side — there is no token-revocation list — but every subsequent request must use the new key.
- `ResetApiKeyCommand.SendUpdatesToClient = true` (`ResetApiKeyCommand.cs:7`) so the SignalR `commands` event broadcasts the completion.

Where it is checked (`ApiKeyAuthenticationHandler.cs:37-79`):
1. Query string parameter — name comes from `Options.QueryName`.
2. Header — name comes from `Options.HeaderName`.
3. `Authorization: Bearer <key>` — final fallback, simple `Replace("Bearer ", "")` (no scheme validation, leaves any prefix string ahead of "Bearer " intact).

Two scheme registrations (`AuthenticationBuilderExtensions.cs:57-66`):
| Scheme name | Header | Query |
|-------------|--------|-------|
| `"API"`     | `X-Api-Key` | `apikey` |
| `"SignalR"` | `X-Api-Key` | `access_token` |

Both delegate to the same `ApiKeyAuthenticationHandler` (and therefore the same `_apiKey`). The reason for the second scheme is the SignalR JS client — it appends `?access_token=...` to the websocket URL.

`InitializeJsonController` exposes the API key to the UI bundle: `GET /initialize.json` (auth: `Policy="UI"`) returns a JSON blob containing `apiKey` (`src/Sonarr.Http/Frontend/InitializeJsonController.cs:48-49`). The browser then carries it on every API call as `X-Api-Key`.

## Forms / cookie auth

Wiring lives in `src/Sonarr.Http/Authentication/AuthenticationBuilderExtensions.cs:33-67`:

| Property | Value | Notes |
|----------|-------|-------|
| Scheme name | `"Forms"` (= `nameof(AuthenticationType.Forms)`) | Plain ASP.NET `AddCookie(...)` — no override of the cookie data format. |
| Cookie name | `"{instanceName}Auth"` | `instanceName` is `IConfigFileProvider.InstanceName`, run through `RemoveDiacritics()` and the regex `[^a-z0-9]+` (case-insensitive) replaced with `""` (lines 40-43). Default instance name is `"Sonarr"` ⇒ default cookie `"SonarrAuth"`. |
| `LoginPath` | `/login` | Browsers get a 302 redirect; XHRs (see below) get 401. |
| `AccessDeniedPath` | `/login?loginFailed=true` | 403 path. |
| `ExpireTimeSpan` | `TimeSpan.FromDays(7)` | Hard-coded — not configurable. |
| `SlidingExpiration` | `true` | Default ASP.NET sliding behaviour: refreshed when current age > half of `ExpireTimeSpan`. |
| `ReturnUrlParameter` | `"returnUrl"` | Honored by `Url.IsLocalUrl(returnUrl)` check in the controller. |
| `OnRedirectToLogin` | `EventOnRedirectCookiesLogin(ctx, 401)` | If `X-Requested-With: XMLHttpRequest` (header *or* query!) is present, sets `Location` header + 401 instead of redirecting. Lines 69-83. |
| `OnRedirectToAccessDenied` | `EventOnRedirectCookiesLogin(ctx, 403)` | Same XHR-aware behaviour with 403. |
| `IsPersistent` | from `LoginResource.RememberMe == "on"` | (`AuthenticationController.cs:54`) |
| Signing/encryption | ASP.NET DataProtection | Default protector. Keys persisted to `<AppData>/asp/` via `services.AddDataProtection().PersistKeysToFileSystem(...)` (`Startup.cs:225-226`). On `CryptographicException` containing an inner `XmlException` the controller logs "remove all XML files from {asp folder}" (`AuthenticationController.cs:62-66`) and returns 401 — i.e. corrupted DataProtection keys are *not* auto-rotated. |
| Cookie attributes | ASP.NET defaults (`HttpOnly=true`, `Secure=SameAsRequest`, `SameSite=Lax` in modern versions) | Not set explicitly. SEC-Q6. |

Login flow (`AuthenticationController.cs:35-86`):
1. `[AllowAnonymous] POST /login` with form body (`[FromForm] LoginResource`, content-type `application/x-www-form-urlencoded`) and `?returnUrl=` from query.
2. `_authService.Login(req, username, password)` runs PBKDF2 verify; null ⇒ `Redirect("~/login?returnUrl={returnUrl}&loginFailed=true")`.
3. Otherwise build claims `("user", username)`, `("identifier", Guid)`, `("AuthType", "Forms")` — `nameType="user"`, `roleType="identifier"` — and call `HttpContext.SignInAsync("Forms", principal, { IsPersistent = RememberMe })`.
4. Wrap `SignInAsync` in `try/catch (CryptographicException e)`; on `XmlException` inner exception (corrupted DataProtection keys), log and return 401.
5. Validate `returnUrl` with `Url.IsLocalUrl`; otherwise redirect to `UrlBase + "/"`.

Logout (`AuthenticationController.cs:88-94`): `_authService.Logout(...)` (logs only) then `HttpContext.SignOutAsync("Forms")` and 302 to `UrlBase + "/"`.

## Basic auth

`AuthenticationType.Basic` exists in the enum but is `[Obsolete]`. Both code paths that read `AuthenticationMethod` rewrite it to `Forms`:
- `ConfigFileProvider.cs:227-233` (read path)
- `ConfigFileProvider.cs:418-422` inside `MigrateConfigFile()` (one-shot rewrite of the XML)
- `Sonarr.Api.V3/Config/HostConfigController.cs:48-51`: validator rejects `AuthenticationMethod == Basic` with the message `'Basic' is no longer supported, switch to 'Forms' instead.`

There is **no Basic-auth challenge handler** — `ApiKeyAuthenticationHandler` parses `Authorization: Bearer ...` (note: not `Basic ...`). HTTP Basic from a downstream client is therefore not a supported auth path; it is only used *outbound* by `ManagedHttpDispatcher.GetResponseAsync` when `request.Credentials is BasicNetworkCredential` (see `src/NzbDrone.Common/Http/Dispatchers/ManagedHttpDispatcher.cs:74-79` — manual Base64-encoded `Authorization` header to skip the 401-challenge round trip).

## External authentication

`AuthenticationType.External` is registered (`AuthenticationBuilderExtensions.cs:55`) but `AddExternal()` (line 28) reuses `NoAuthenticationHandler` — i.e. it *unconditionally trusts* the upstream. The intent is that a reverse proxy in front of Sonarr enforces auth (e.g. Authelia / Authentik / cloudflared access) and Sonarr accepts whoever shows up. The principal it builds is the same `Anonymous` claim set the `None` scheme issues (`NoAuthenticationHandler.cs:23-31`). AUTH-Q4.

There is no OIDC / OAuth / SAML / LDAP integration. Trakt, Plex, Twitter OAuth flows in `src/NzbDrone.Core/Notifications/**` are **outbound** auth for those provider APIs (see `notifications.md`), not Sonarr login.

## Authorization policies

Configured in `src/NzbDrone.Host/Startup.cs:228-243`:
- Custom `IAuthorizationPolicyProvider`: `UiAuthorizationPolicyProvider` (line 228).
- Custom `IAuthorizationHandler`: `UiAuthorizationHandler` (line 229).
- `"SignalR"` policy (line 233-237): scheme `"SignalR"`, `RequireAuthenticatedUser()`. Applied via `MapHub<MessageHub>("/signalr/messages").RequireAuthorization("SignalR")` (line 374).
- Fallback policy (`FallbackPolicy`, line 240-242): scheme `"API"`, `RequireAuthenticatedUser()`. This is what every controller/action with no explicit `[Authorize]` falls back to — i.e. **every API V3/V5 endpoint requires an API key by default**; the only opt-outs are `[AllowAnonymous]` on individual actions.
- `"UI"` policy (`UiAuthorizationPolicyProvider.cs:27-37`): scheme = `config.AuthenticationMethod.ToString()` (`None`/`Forms`/`External`) plus `BypassableDenyAnonymousAuthorizationRequirement`. Applied via `[Authorize(Policy="UI")]` on `StaticResourceController` (`Frontend/StaticResourceController.cs:13`) and `InitializeJsonController` (line 11), and via `MapPost("/profiler/results", …).RequireAuthorization("UI")` (`Startup.cs:375`).

Bypass for local addresses (`UiAuthorizationHandler.cs:25-41`):
- Only when `AuthenticationRequiredType == DisabledForLocalAddresses`.
- IP comes from `httpContext.GetRemoteIP()` (an extension that consults `X-Forwarded-For` because `app.UseForwardedHeaders()` is configured at `Startup.cs:66-68, 337` with `XForwardedFor | XForwardedProto | XForwardedHost`). SEC-Q7 — there is no allowlist of trusted proxies; any request can spoof `X-Forwarded-For` if the deployment doesn't strip it upstream.
- "Local" = `IPAddress.IsLocalAddress()` (`src/NzbDrone.Common/Extensions/IpAddressExtensions.cs:8`):
  - IPv4: loopback, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16` (link-local).
  - IPv6: loopback, `IsIPv6LinkLocal`, `IsIPv6UniqueLocal`, `IsIPv6SiteLocal`.
- Optional CGNAT (`100.64.0.0/10`) bypass when `_configService.TrustCgnatIpAddresses` is set — this is the Tailscale escape hatch (`UiAuthorizationHandler.cs:33`, `IpAddressExtensions.cs:56`).

## Endpoints exempt from auth

The fallback `RequireAuthenticatedUser()` policy means **everything is gated** unless explicitly opted out. Inventory of opt-outs (grep `[AllowAnonymous]` across `src/Sonarr.Http/`, `src/Sonarr.Api.V3/`, `src/Sonarr.Api.V5/`, `src/NzbDrone.Host/`):

| Endpoint | File | Rationale |
|----------|------|-----------|
| `GET/HEAD /ping` | `src/Sonarr.Http/Ping/PingController.cs:23` | Health probe. Reads `IConfigRepository.All` through a 5-second cache and returns `{ "Status": "OK" }` or `Error`. |
| `GET /login` (page) | `src/Sonarr.Http/Frontend/StaticResourceController.cs:27-32` | Login HTML — served by `LoginHtmlMapper` (`src/Sonarr.Http/Frontend/Mappers/LoginHtmlMapper.cs:34`). |
| `GET /content/{**path}` (regex excludes `api/`) | `src/Sonarr.Http/Frontend/StaticResourceController.cs:35-40` | UI bundle assets — JS/CSS/images. CORS policy `"AllowGet"` (`Startup.cs:88-92` — `AllowAnyOrigin().WithMethods("GET","OPTIONS").AllowAnyHeader()`). |
| `POST /login`, `GET /logout` | `src/Sonarr.Http/Authentication/AuthenticationController.cs:18` | Whole controller is `[AllowAnonymous]`. |
| Default `Index` route `GET /` and `GET /{**path:regex(^(?!(api|feed)/).*)}` | `src/Sonarr.Http/Frontend/StaticResourceController.cs:42-47` | NOT `[AllowAnonymous]` — gated by `[Authorize(Policy="UI")]` on the controller, which becomes effectively anonymous when `AuthenticationMethod == None` (the policy's auth scheme is `"None"` ⇒ the no-op handler succeeds for everyone). |

What is **not** exempt (despite often being "open" in similar apps):
- `/MediaCover/**` — served by `MediaCoverMapper` (`src/Sonarr.Http/Frontend/Mappers/MediaCoverMapper.cs:46`). The mapper is invoked from `StaticResourceController.Index` which is `[Authorize(Policy="UI")]` — so the mapper gets the same UI-policy gate. UI cookie or API key required. SEC-Q8.
- `/initialize.json` — `[Authorize(Policy="UI")]`. Returns the API key (line 49); critical that the UI policy denies anonymous unless `None`/`External` is set.
- `/api/v3/system/status` — *not* `[AllowAnonymous]`. The fallback policy applies; the V3 frontend resolves the API key from `/initialize.json` first.
- `/signalr/messages` hub — `RequireAuthorization("SignalR")` (`Startup.cs:374`). API-key scheme.
- Swagger UI in debug builds — `app.UseSwagger(...)` at line 364, no `RequireAuthorization` decoration. Accessible without auth in `BuildInfo.IsDebug` only. SEC-Q9.

## Anti-CSRF

There is **no CSRF token machinery**. ASP.NET's antiforgery services are not registered (no `services.AddAntiforgery()` anywhere under `src/`), and no controller uses `[ValidateAntiForgeryToken]` or `[AutoValidateAntiforgeryToken]`. The defenses are:
1. The API requires `X-Api-Key` (or `?apikey=`/`Authorization: Bearer`). A naïve cross-site form post can append the query param, so the API-key-only API surface is effectively trusted under SameSite cookies but not under header-only auth. SEC-Q10.
2. The Forms cookie is the default ASP.NET cookie — `SameSite=Lax` in current versions — which mostly stops cross-origin POSTs. There is no double-submit token.
3. CORS for the API allows **any origin** with **any method** and **any header** (`Startup.cs:80-93`, `VersionedApiControllerAttribute.API_CORS_POLICY`). This effectively turns the API key into the only access control, even from browsers. SEC-Q11.

## TLS / cert validation

Inbound (Kestrel, `src/NzbDrone.Host/Bootstrap.cs:196-210`):
- `EnableSsl=true` + `SslCertPath` non-empty ⇒ `ConfigureHttpsDefaults(o => o.ServerCertificate = ValidateSslCertificate(sslCertPath, sslKeyPath, sslCertPassword))`.
- `MaxRequestBodySize = null` (unlimited), `AllowSynchronousIO = false`.
- No explicit `SslProtocols` setting; uses Kestrel/OS defaults (TLS 1.2+ on .NET 10). No cipher-suite restriction. SEC-Q12.
- The cert loader auto-detects PEM vs PKCS12 (`src/Sonarr.Api.V3/Config/CertificateValidator.cs:40-58`).

Outbound — single global validation callback installed on:
- `SocketsHttpHandler.SslOptions.RemoteCertificateValidationCallback` for every `HttpClient` cached by `ManagedHttpDispatcher` (`src/NzbDrone.Common/Http/Dispatchers/ManagedHttpDispatcher.cs:158-161`). One client per proxy-settings key (cached forever in `ICacheManager`).
- `MailKit.Net.Smtp.SmtpClient.ServerCertificateValidationCallback` (`src/NzbDrone.Core/Notifications/Email/Email.cs:155`).

Both call `X509CertificateValidationService.ShouldByPassValidationError`. Decision tree (`src/NzbDrone.Core/Security/X509CertificateValidationService.cs:23-75`):

```
if (sender is not SslStream && sender is not string)         return true   # SEC-Q3
if (sender is SslStream)  targetHostName = sender.TargetHostName
if (sender is string)     targetHostName = sender             # MailKit passes hostname as string
if (cert.SignatureAlgorithm.FriendlyName == "md5RSA")        log error   (line 43)
if (sslPolicyErrors == None)                                 return true
if (targetHostName is "localhost" or "127.0.0.1")            return true   # always trusts loopback hostnames, SEC-Q13
ipAddresses = resolve(targetHostName)
match _configService.CertificateValidation:
  Disabled                       => return true                      # global trust-all
  DisabledForLocalAddresses      => return ipAddresses.All(IsLocalAddress)
  Enabled (default)              => return false (after logging error)
```

Note the resolution path goes through the **system DNS resolver** (`Dns.GetHostEntry(host)` at line 84). If the host fails to resolve and fails validation, an exception is raised by `Dns.GetHostEntry` and propagates out of the callback as a transport failure (no `try/catch`). SEC-Q14.

There is no per-request override — the callback is *globally* the only validation policy. The Go port should use a single `tls.Config` shared across all `http.Transport`s with a custom `VerifyConnection` callback that mirrors this logic.

## Config keys

All read through `IConfigFileProvider` (`src/NzbDrone.Core/Configuration/ConfigFileProvider.cs`):

| Key | Default | Source priority | Notes |
|-----|---------|-----------------|-------|
| `ApiKey` | new GUID-hex on first read | `AuthOptions.ApiKey` env ⇒ `<ApiKey>` ⇒ generate-and-persist | line 194-208 |
| `AuthenticationMethod` | `None` (or `Forms` if legacy `AuthenticationEnabled=true`) | `AuthOptions.Method` env ⇒ legacy `AuthenticationEnabled` boolean ⇒ `<AuthenticationMethod>` | line 210-237. `Basic` is rewritten to `Forms` on read. |
| `AuthenticationRequired` | `Enabled` | `AuthOptions.Required` env ⇒ `<AuthenticationRequired>` | line 239-242 |
| `TrustCgnatIpAddresses` | `false` | `AuthOptions.TrustCgnatIpAddresses` env ⇒ `<TrustCgnatIpAddresses>` (`persist: false`) | line 244 |
| `Username` | empty | `<Username>` (legacy — only used to seed the `Users` table on first startup, see `UserService.Handle` line 160) | The runtime user lives in `Users`, not the XML. AUTH-Q2. |
| `Password` | empty | `<Password>` (legacy) | Same. |
| `EnableSsl`, `SslPort`, `SslCertPath`, `SslKeyPath`, `SslCertPassword` | `false` / 9898 / "" / "" / "" | `ServerOptions.*` env ⇒ XML | lines 188-190, 274-276 |
| `CertificateValidation` | `Enabled` | `<CertificateValidation>` via `IConfigService.CertificateValidation` (note: lives in `IConfigService`, not `IConfigFileProvider`) | Not in `ConfigFileProvider`; in `src/NzbDrone.Core/Configuration/ConfigService.cs`. |
| `dataProtectionFolder` | `<AppDataFolder>/asp` | `Bootstrap.cs:265` injects via in-memory config; `Startup.cs:226` reads via `Configuration["dataProtectionFolder"]` | Not user-visible. |

## Jobs / commands

| Command | Trigger | Frequency | Idempotent? |
|---------|---------|-----------|-------------|
| `ResetApiKeyCommand` | UI button (System → General → Reset API Key) and PUT `/api/v3/config/host`-style flows | on demand | yes — overwrites with a fresh GUID |
| `CleanupAdditionalUsers` (housekeeping) | nightly housekeeping schedule (registered alongside other `IHousekeepingTask` types) | daily | yes — keeps lowest `Id` only |

No scheduled job seeds users, expires sessions, or rotates DataProtection keys. ASP.NET DataProtection rotates its own keys (90-day default) inside `<AppData>/asp/`.

## Events raised
- `ResetApiKeyCommand` flows through the `Command` pipeline → `CommandUpdatedEvent` (SignalR `commands` push). Not a dedicated auth event.

## Events consumed

| Event | Handler | Behavior |
|-------|---------|----------|
| `ApplicationStartedEvent` | `UserService.Handle` (`UserService.cs:160`) | Seed `Users` from legacy `<Username>`/`<Password>` config XML when table is empty. |
| `ConfigSavedEvent` | `UiAuthorizationHandler.Handle` (`UiAuthorizationHandler.cs:43`) | Refresh static `_authenticationRequired` cache. |

`AuthenticationService.AUTH_METHOD` is **not** refreshed on `ConfigSavedEvent` — it is set once in the constructor (`AuthenticationService.cs:26`). Switching auth methods at runtime requires a Sonarr restart. SEC-Q1.

## External services
None. All authentication and crypto lives in-process. Outbound TLS validation depends on the system trust store (default .NET behaviour) — no bundled CA roots.

## HostConfigController surface (UI side)

`src/Sonarr.Api.V3/Config/HostConfigController.cs:21` — `V3ApiController("config/host")`. This is the only place the UI pokes auth settings.

- `GET /api/v3/config/host` (line 105) returns `HostConfigResource` containing `Username`, `Password = user.Password` (the **base64 PBKDF2 hash itself** — not stars), and `PasswordConfirmation = ""`. SEC-Q15.
- `PUT /api/v3/config/host` (line 120) writes everything back: `_configFileProvider.SaveConfigDictionary(dictionary)` ⇒ XML, `_configService.SaveConfigDictionary(dictionary)` ⇒ `Config` table. Then if `Username && Password` non-empty, `_userService.Upsert(username, password)` (line 132).
- `Upsert` (line 59 of `UserService.cs`) detects "no actual change" by comparing the incoming `password` to the current stored `user.Password` (line 68) — i.e. by string equality with the **hash**. So the round-trip pattern UI→PUT does *not* re-hash the unchanged hash; the `if (user.Password != password)` short-circuits and only the username changes. AUTH-Q5.
- `PasswordConfirmation` validation (line 53-54) calls `IsMatchingPassword(resource)` (line 83-98): returns true if `(user.Password == resource.Password)` (i.e. unchanged hash) **or** `resource.Password == resource.PasswordConfirmation`. Lets the UI submit without re-typing the password.

## Notable quirks / TODOs / HACKs found in source

- **API key compared with `==`** — `ApiKeyAuthenticationHandler.cs:63`. Not constant-time. Same for password hash compare (`UserService.cs:157`). At PBKDF2 cost 10000 iterations and 32-byte hashes the timing leak is small but non-zero.
- **API key from URL query** — `?apikey=...` exposes the key in access logs / browser history / `Referer`. The SignalR scheme additionally accepts `?access_token=` for the websocket handshake.
- **`Authorization: Bearer ...` parser** — `ApiKeyAuthenticationHandler.cs:51` does `Replace("Bearer ", "")` (substring replace). `Authorization: bearerXBearer Yfoo` would pass `Yfoo`. Fine for happy-path; weird against fuzzing.
- **Shared `_apiKey` captured in handler ctor** — `ApiKeyAuthenticationHandler.cs:34` captures `config.ApiKey` per `HandleAuthenticateAsync` instance; the handler is created per request by ASP.NET so this is actually fresh per request. Good.
- **Static fields `AUTH_METHOD` and `_authenticationRequired`** — `AuthenticationService.cs:21`, `UiAuthorizationHandler.cs:17`. Singleton-scoped behaviour smuggled through `static`; works only because both services are registered as singletons in the IoC graph.
- **`UiAuthorizationHandler` is *also* an `IAuthorizationRequirement`** (`UiAuthorizationHandler.cs:14`) — that interface declaration is unused; the actual requirement is `BypassableDenyAnonymousAuthorizationRequirement`. Looks vestigial.
- **`UiAuthorizationPolicyProvider` builds a fresh policy on every request** (`UiAuthorizationPolicyProvider.cs:31-34`) — no caching. ASP.NET has a `IAuthorizationPolicyProvider` cache but only when `AllowsCachingPolicies = true`, which the default policy provider does. The policy returned here is rebuilt every request when policy name is `"UI"`.
- **Cookie name regex strips diacritics + non-alphanumerics**, but does not lowercase. Default instance name `"Sonarr"` ⇒ `"SonarrAuth"`. A renamed instance like `"My Server (Anime)"` would yield `"MyServerAnimeAuth"`. AUTH-Q6 — collisions between instances? No — Sonarr is one process per port; the cookie is scoped by host/port anyway.
- **Forms scheme returns `Anonymous` claim set when `AuthenticationMethod == None`** — `UiAuthorizationPolicyProvider` builds the `"UI"` policy with the configured scheme, and `NoAuthenticationHandler` always succeeds with an anonymous principal. The `BypassableDenyAnonymousAuthorizationRequirement` is **never satisfied** by `Authenticate` (claim is anonymous) but **is** satisfied by the local-IP bypass — and on `None` mode there is no other bypass path. So `None` mode works because the requirement is `DenyAnonymous` and the principal **is** authenticated (just anonymously). Confusing but intentional. AUTH-Q7 — confirm `IsAuthenticated == true` for the anonymous principal.
- **`AuthenticationService.LogInvalidated`** (line 68) is a private logger that is never called.
- **`HostConfigController.GET` returns the password hash** to anyone with a valid API key / UI cookie. While that's already a privileged caller, exposing the hash widens the attack surface (offline crack on the 10000-iter PBKDF2 hash). SEC-Q15.
- **`X509CertificateValidationService` returns `true` for non-`SslStream`/non-`string` senders** (line 27) — meaning any other code path that plugs the callback in (e.g. tests, future `WebSocket`) silently bypasses validation. SEC-Q3.
- **md5RSA error log only** — no rejection of MD5-signed certs even when `CertificateValidation == Enabled` (line 43-46 logs but does not return false). The subsequent `sslPolicyErrors == None` check decides; .NET will report MD5 as a chain validation error on most platforms, so MD5 is rejected through that path, not this branch.
- **Legacy SHA-256 password upgrade is one-shot** — `UserService.cs:97-108` requires the *first* login attempt after migration 174 to re-hash. If the user never logs in again, the row stays in legacy form forever. AUTH-Q1.
- **No password length / strength validation** — `HostConfigController` only requires `NotEmpty` (line 45). A 1-character password hashed with 10000-iter PBKDF2-SHA512 is trivially crackable. SEC-Q16.
- **No login throttling / rate limit / account lockout** — `AuthenticationService.Login` returns null on bad credentials with no backoff. `_authLogger.Warn("Auth-Failure ip {ip} username {u}")` is the only side effect. SEC-Q17.
- **Login form is HTML-form-encoded, not JSON** — `[FromForm] LoginResource`. Ports must keep this content type for backwards compatibility with existing UI.
- **`RememberMe == "on"` string compare** — HTML checkbox value, not a boolean. AUTH-Q3.
- **Double-cache of `ApiKey` and `UrlBase` in `InitializeJsonController`** — set in ctor as static (`InitializeJsonController.cs:19,29-30`), so a `ResetApiKeyCommand` does not flush them; the static `_apiKey` keeps the *old* key in the in-memory render of `/initialize.json` until the controller instance is recycled. AUTH-Q8.
- **`CleanupAdditionalUsers` deletes by `Id NOT IN (SELECT Id FROM Users LIMIT 1)`** — relies on undefined `LIMIT` ordering for `SELECT ... LIMIT 1` without `ORDER BY`. On Postgres this can vary between runs. AUTH-Q9.

## Test files (the port must keep passing equivalents)

- `src/NzbDrone.Core.Test/Housekeeping/Housekeepers/CleanupAdditionalUsersFixture.cs` — single-user invariant.
- `src/NzbDrone.Common.Test/Http/HttpClientFixture.cs` — wires `X509CertificateValidationService` into `ManagedHttpDispatcher` for HTTP integration tests.
- `src/NzbDrone.Core.Test/Framework/CoreTest.cs:27-28` — sets up `ICertificateValidationService` + `IHttpDispatcher` defaults.

Coverage gaps (significant): there is **no fixture** for `UserService` (PBKDF2 round-trip, salt migration, legacy SHA-256 upgrade), `AuthenticationService`, `ApiKeyAuthenticationHandler`, `UiAuthorizationHandler`, or `X509CertificateValidationService`. The Go port should add table-driven tests covering at minimum: (a) PBKDF2 verify against existing real `Users` rows captured from a running install, (b) the legacy SHA-256 upgrade path, (c) API key precedence (query > header > Bearer), (d) local-IP bypass with IPv4/IPv6/CGNAT, (e) cert validator with each `CertificateValidationType` × local/non-local target. SEC-Q18.

## Cross-cutting concerns flagged for other cards

- **`Sonarr.Http` middleware/auth** is *not* a notification-style provider directory but a real ASP.NET wiring layer — it deserves its own subsystem card (likely `sonarr-http.md`) covering auth schemes/policies, CORS, forwarded headers, error pipeline, frontend mappers, and SignalR auth. The current card focuses on auth only; the rest of `Sonarr.Http` is uncovered.
- The "config keys" set above overlaps with the `system.md` card and the `ConfigFileProvider` story. Auth-related keys live in two stores: `config.xml` (`IConfigFileProvider`) **and** the `Config` DB table (`IConfigService`) — `CertificateValidation` is on the DB side. Confirm the `system.md` card covers this split.
- The `notifications.md` card already references `ICertificateValidationService.ShouldByPassValidationError` for SMTP. The Go port must wire the *same* trust callback into both the shared HTTP transport and any SMTP client.
- Migration ledger should record migrations 076 (`add_users_table`) and 174 (`add_salt_to_users`) with the password-hashing semantics noted above so the porter can validate Postgres parity.

## Open questions

### AUTH-* (login / users / cookies / API key)

- **AUTH-Q1**: `UserService.FindUser(username,password)` (`UserService.cs:97-108`) only upgrades a legacy SHA-256 row to PBKDF2 on a successful login. Should the Go port instead force-upgrade on next login *or* migrate all rows in a one-shot job? Status: unresolved. Blocks Phase 1 (`internal/auth/user_service`).
- **AUTH-Q2**: After the `<Username>`/`<Password>` seed runs (`UserService.cs:160-188`), the legacy XML elements remain. Should the Go port delete them after seeding? Status: unresolved. Blocks Phase 1.
- **AUTH-Q3**: `LoginResource.RememberMe` is a `string` compared with `== "on"` (HTML checkbox semantics). Keep that contract or normalize to a bool in v5? Status: unresolved. Blocks Phase 2 REST contract freeze.
- **AUTH-Q4**: `AuthenticationType.External` reuses the always-anonymous handler — it trusts the upstream proxy completely. Document this prominently in the Go port and consider requiring an `X-Forwarded-User` header to populate the principal name. Status: unresolved. Blocks Phase 1.
- **AUTH-Q5**: `UserService.Upsert` short-circuits when the *hash strings* match (`UserService.cs:68`). The HostConfigController therefore depends on `GET /api/v3/config/host` returning the **stored hash** so a subsequent PUT is idempotent. This is the contract that lets the UI not re-prompt for the password on every save. Confirm the Go port preserves this *exactly* — including returning the hash to the client. Status: unresolved. Blocks Phase 2.
- **AUTH-Q6**: Cookie name is derived from `InstanceName` after diacritic + non-alphanumeric stripping. Does the Go port need to reproduce the .NET `RemoveDiacritics` behaviour byte-for-byte for existing deployments to keep their session cookies? Status: unresolved. Blocks Phase 1.
- **AUTH-Q7**: `NoAuthenticationHandler` issues an `Anonymous` claims identity that is treated as `IsAuthenticated == true` by ASP.NET — that's why the `"UI"` policy with `DenyAnonymous` succeeds when `AuthenticationMethod == None`. Verify the Go port reproduces this semantic (an "authenticated anonymous" identity vs `nil`). Status: unresolved. Blocks Phase 1.
- **AUTH-Q8**: `InitializeJsonController` caches `_apiKey` in a `static` field set in the constructor (`InitializeJsonController.cs:19,29`). After `ResetApiKeyCommand` the cached HTML *content* is invalidated only when `RuntimeInfo.IsProduction == false` (line 41-43); in production the stale content is returned forever. Bug or accepted? Status: unresolved.
- **AUTH-Q9**: `CleanupAdditionalUsers.Clean` uses `LIMIT 1` without `ORDER BY` — undefined ordering on Postgres. Should the Go port deterministically pick the lowest `Id`? Status: unresolved. Blocks Phase 1.

### SEC-* (TLS / authorization / hardening)

- **SEC-Q1**: `AuthenticationService.AUTH_METHOD` (`AuthenticationService.cs:21`) is set once in the constructor and never refreshed — switching auth method at runtime requires a Sonarr restart. The companion `UiAuthorizationHandler._authenticationRequired` *is* refreshed via `IHandle<ConfigSavedEvent>`. Inconsistent — fix or document? Status: unresolved.
- **SEC-Q2**: API key and password hash comparisons use `==`, not constant-time. Switch to `crypto/subtle.ConstantTimeCompare` in the Go port? Status: unresolved. Blocks Phase 1.
- **SEC-Q3**: `X509CertificateValidationService.ShouldByPassValidationError` returns `true` when `sender` is neither `SslStream` nor `string` (line 27). Any future caller that doesn't match those types silently bypasses validation. Tighten in the Go port to *deny* unknown senders. Status: unresolved.
- **SEC-Q4**: PBKDF2-SHA512 with 10000 iterations is below current OWASP guidance (>= 600,000 for SHA-256, >= 210,000 for SHA-512 as of 2023). Should the Go port (a) keep 10000 for compatibility with imported DBs and ramp on next login by writing `Iterations = N`, or (b) ship a separate "rehash on login" pass? Note: `Users.Iterations` exists precisely to make (a) feasible. Status: unresolved. Blocks Phase 3 security review.
- **SEC-Q5**: `GenerateApiKey()` uses `Guid.NewGuid()` (122 bits effective, RFC 4122 v4). Switch to 32 bytes from `crypto/rand` (256 bits) in the Go port? Status: unresolved.
- **SEC-Q6**: `AddCookie` uses ASP.NET defaults — no explicit `HttpOnly`, `Secure`, `SameSite`. ASP.NET defaults to `HttpOnly=true`, `SameSite=Lax`, `Secure=SameAsRequest`. The Go port should set these explicitly. Confirm whether `Secure=Always` is acceptable when `EnableSsl=false` (it would prevent cookies on plaintext deployments). Status: unresolved.
- **SEC-Q7**: `app.UseForwardedHeaders()` accepts `X-Forwarded-For/Proto/Host` from any peer (`Startup.cs:66-68`). No `KnownNetworks` / `KnownProxies` allowlist — meaning a request directly hitting Sonarr from outside the LAN can spoof a "local" IP via `X-Forwarded-For` and *bypass auth* if `AuthenticationRequired == DisabledForLocalAddresses`. Status: unresolved. Blocks Phase 3 security review.
- **SEC-Q8**: `MediaCoverMapper` serves arbitrary jpg/png/gif from `<AppData>/MediaCover/...`. The mapper itself is invoked from `StaticResourceController.Index` which is `[Authorize(Policy="UI")]`. Confirm there is no other code path that exposes the same mapper without the policy gate. Status: unresolved.
- **SEC-Q9**: Swagger UI is mounted in debug builds with no `RequireAuthorization` (`Startup.cs:364-370`). Confirm release builds set `BuildInfo.IsDebug == false`. Status: unresolved.
- **SEC-Q10**: API-key-only auth + no CSRF token means a malicious site could (in theory) include `<img src="https://sonarr/api/v3/series/123?apikey=...">` if it learned the key — but it can't. The browser cookie is `SameSite=Lax`, so the bigger risk is direct CSRF *without* the API key when `AuthenticationMethod == None` and `DisabledForLocalAddresses` is on. Document the threat model. Status: unresolved.
- **SEC-Q11**: API CORS policy (`Startup.cs:80-86`) is `AllowAnyOrigin().AllowAnyMethod().AllowAnyHeader()`. Since the API requires the API key, this is fine for a server-to-server use case, but a misconfigured browser extension that leaks the key has full read/write. Should the Go port narrow this to a configurable allowlist? Status: unresolved.
- **SEC-Q12**: Kestrel TLS uses OS defaults — no `MinimumProtocolVersion`, no cipher allowlist (`Bootstrap.cs:196-205`). Match Go's `tls.VersionTLS12` minimum and a curated cipher list? Status: unresolved.
- **SEC-Q13**: Cert validator unconditionally trusts a remote whose `targetHostName` is the literal string `"localhost"` or `"127.0.0.1"` even when `CertificateValidation == Enabled` (line 53-56). DNS resolution is never consulted for those names. Reasonable but worth documenting. Status: unresolved.
- **SEC-Q14**: `Dns.GetHostEntry(host)` (line 84) is synchronous and can throw on resolution failures, inside a callback that has no surrounding `try/catch`. The exception bubbles out as a transport-level error. The Go port should resolve once and cache, returning false on resolve failure. Status: unresolved.
- **SEC-Q15**: `GET /api/v3/config/host` returns the stored PBKDF2 hash (`HostConfigController.cs:114`). Any client with a valid API key can offline-crack the password with 10000 iter / SHA-512 / 32-byte output. Sanitize in the Go port? (Likely breaks the round-trip pattern documented in AUTH-Q5.) Status: unresolved.
- **SEC-Q16**: No password complexity enforcement — `NotEmpty` validation only. Add a min-length / strength check in the Go port? Status: unresolved.
- **SEC-Q17**: No login throttling, lockout, or backoff. Status: unresolved. Blocks Phase 3 security review.
- **SEC-Q18**: Auth and security code has effectively zero unit-test coverage in the C# tree (only `CleanupAdditionalUsers` has a fixture). Phase 1 acceptance for this subsystem must include a comprehensive test corpus — minimum bar listed in the Test files section. Status: unresolved. Blocks Phase 1 sign-off.
