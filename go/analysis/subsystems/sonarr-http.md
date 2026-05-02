# sonarr-http

**Source:** `src/Sonarr.Http/**`, plus the wiring site in `src/NzbDrone.Host/Startup.cs`
**Target Go package:** `go/internal/http/` (router, middleware, error envelope, client-schema renderer, frontend mappers)
**Status:** drafted

## Purpose

`Sonarr.Http` is the HTTP plumbing that sits between Kestrel/ASP.NET Core and the V3/V5 controllers. It owns:

* the route-template attributes (`VersionedApiControllerAttribute`, `VersionedFeedControllerAttribute`) that bake `api/v{n}/` into every controller's URL — there is no global prefix middleware, the prefix is per-controller;
* the bespoke middleware pipeline (logging, URL-base redirect, version header, "starting up" 503, cache headers, conditional-GET, request-body buffering for command endpoints);
* the error envelope produced on every uncaught exception (`SonarrErrorPipeline` wired via `app.UseExceptionHandler`);
* the dynamic-form schema builder (`ClientSchema`) that converts settings classes annotated with `[FieldDefinition]` into a `Field[]` JSON array consumed by the React UI;
* the frontend asset pipeline — every static file the React app needs goes through `StaticResourceController` + a chain of `IMapHttpRequestsToDisk` mappers that perform `__URL_BASE__` / `__INSTANCE_NAME__` / `__MINI_PROFILER__` / `_THEME_` template substitution and per-request cache-buster hashing;
* the authentication/authorization wiring — schemes (`Forms`, `API`, `SignalR`, `None`, `External`), policies (`UI`, `API`, `SignalR`), and the bypassable `DenyAnonymous` requirement that implements "auth disabled for local addresses";
* the `RestController<T>` base that produces FluentValidation-driven 400 responses and the `RestControllerWithSignalR<T,M>` base that broadcasts CRUD changes onto the message hub;
* the FluentValidation `RuleBuilderExtensions` (`ValidId`, `NotBlank`, `HaveHttpProtocol`, `IsValidRssSyncInterval`, `EmptyCollection`) shared across V3 and V5 controllers;
* and the `/ping`, `/api`, `/initialize.json`, `/login`, `/logout` ad-hoc endpoints that don't fit the V3/V5 controller mould.

The full route inventory (per-version, per-controller) lives in `analysis/rest/inventory.md`; SignalR hub events live in `analysis/signalr/inventory.md`; provider-side `[FieldDefinition]` enumeration lives in `analysis/subsystems/thingiprovider.md` (running in parallel). This card focuses on the plumbing — what comes in, what goes out, in what order.

## Public types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `VersionedApiControllerAttribute` | `src/Sonarr.Http/VersionedApiControllerAttribute.cs:8` | route attribute | Implements `IRouteTemplateProvider`, `IEnableCorsAttribute`, `IApiBehaviorMetadata`. Constructs `Template = "api/v{version}/{resource}"`. `Order = 2`. `PolicyName = API_CORS_POLICY = "ApiCorsPolicy"`. Subclassed by `V3ApiControllerAttribute(:29)` and `V5ApiControllerAttribute(:37)`. |
| `VersionedFeedControllerAttribute` | `src/Sonarr.Http/VersionedFeedControllerAttribute.cs:6` | route attribute | `Template = "feed/v{Version}/{resource}"`. Used by RSS/iCal endpoints (Calendar feed). No CORS wiring (feeds are not browser-XHR). Subclassed `V3FeedControllerAttribute(:20)`, `V5FeedControllerAttribute(:28)`. |
| `RestController<TResource>` | `src/Sonarr.Http/REST/RestController.cs:20` | abstract controller base | Hosts `PostValidator` / `PutValidator` / `SharedValidator` / `IdValidator`. `OnActionExecuting` (line 74) runs the right validator depending on HTTP method, surfaces deprecated endpoints by writing `Deprecation: true` response header (line 121), and maps the route `id` onto a body resource when missing (line 99-103). |
| `RestControllerWithSignalR<TResource,TModel>` | `src/Sonarr.Http/REST/RestControllerWithSignalR.cs:10` | abstract controller base | Adds `IHandle<ModelEvent<TModel>>` so that any time the underlying model is created/updated/deleted/synced, a `SignalRMessage` envelope is broadcast. Filters to `V3` or `V5` namespace only (`:79`, `:102`) — i.e. SignalR hubs receive both versions concurrently. Cross-cite `analysis/signalr/inventory.md`. |
| `RestResource` | `src/Sonarr.Http/REST/RestResource.cs:5` | abstract DTO base | `Id` property tagged `[JsonIgnore(Condition = WhenWritingDefault)]` — IDs only appear in JSON when non-zero. `ResourceName` (line 11) lower-cases the type name and strips the `"resource"` suffix; this string ends up as `SignalRMessage.Name`. |
| `PagingRequestResource` / `PagingResource<T>` | `src/Sonarr.Http/PagingResource.cs:7`, `:17` | DTO | Paging contract: `Page` (default 1), `PageSize` (default 10), `SortKey`, `SortDirection`. `PagingResourceMapper.MapToPagingSpec` (line 41) gates `SortKey` against an allow-list and falls back to a default if missing/invalid. The full param-mapping spec (filter keys etc.) is documented in `analysis/rest/inventory.md`. |
| `ResourceChangeMessage<TResource>` | `src/Sonarr.Http/ResourceChangeMessage.cs:7` | DTO | Generic SignalR-message body; either `(resource, action)` or `(action)` for `Deleted`/`Sync` (where the resource is implied by `id`). Constructor at `:13` throws `InvalidOperationException` for any other action with no resource. |
| `Field` | `src/Sonarr.Http/ClientSchema/Field.cs:6` | DTO | The schema row sent to the UI for one settings property. Fields: `Order, Name, Label, Unit, HelpText, HelpTextWarning, HelpLink, Value, Type, Advanced, SelectOptions, SelectOptionsProviderAction, Section, Hidden, Privacy, Placeholder, IsFloat`. `Clone()` is a `MemberwiseClone` — used so per-instance `Value` overlay doesn't mutate the cached mapping. |
| `FieldMapping` | `src/Sonarr.Http/ClientSchema/FieldMapping.cs:5` | internal | `Field, PropertyType, GetterFunc, SetterFunc`. Cached per-type by `SchemaBuilder` (line 19). |
| `ErrorModel` | `src/Sonarr.Http/ErrorManagement/ErrorModel.cs:9` | DTO | Top-level error envelope: `Message`, `Description`, `Content`. Serialized via `STJson.SerializeAsync` (line 29) — therefore camelCase: `{message, description, content}`. |
| `ApiException` | `src/Sonarr.Http/Exceptions/ApiException.cs:6` | abstract exception | Carries an `HttpStatusCode` plus optional `Content`. Subclasses: `BadRequestException(:8)`, `NotFoundException(:8)`, `MethodNotAllowedException(:8)`, `UnsupportedMediaTypeException(:8)` — all in `src/Sonarr.Http/REST/`. |
| `InvalidApiKeyException` | `src/Sonarr.Http/Exceptions/InvalidApiKeyException.cs:5` | exception | Plain `Exception` subclass; *not* an `ApiException`. Reserved for the legacy `?apiKey=` query param paths. Doesn't actually appear to be thrown anywhere in the current tree (search shows only the type itself); kept as a TODO marker. SH-Q1. |
| `ApiInfoResource` | `src/Sonarr.Http/ApiInfoResource.cs:5` | DTO | `{ Current = "v5", Deprecated = ["v3"] }` — namespace is `NzbDrone.Http`, not `Sonarr.Http`. |
| `PingResource` | `src/Sonarr.Http/Ping/PingResource.cs:3` | DTO | `{ Status: "OK" }` or `{ Status: "Error" }`. Namespace `NzbDrone.Http.Ping`. |
| `LoginResource` | `src/Sonarr.Http/Authentication/LoginResource.cs:3` | DTO | `Username`, `Password`, `RememberMe`. `RememberMe == "on"` (line 53 of `AuthenticationController`) is the literal HTML-form checkbox value. |
| `BypassableDenyAnonymousAuthorizationRequirement` | `src/Sonarr.Http/Authentication/BypassableDenyAnonymousAuthorizationRequirement.cs:5` | requirement | Empty subclass of `DenyAnonymousAuthorizationRequirement`; exists so a custom handler can say `context.Succeed(requirement)` without short-circuiting other handlers. |
| `ApiKeyAuthenticationOptions` | `src/Sonarr.Http/Authentication/ApiKeyAuthenticationHandler.cs:13` | options | `HeaderName` and `QueryName` are configured per scheme (`API` uses `apikey`, `SignalR` uses `access_token`). |
| `IMapHttpRequestsToDisk` | `src/Sonarr.Http/Frontend/Mappers/IMapHttpRequestsToDisk.cs:7` | interface | Three methods: `Map(url) -> path`, `CanHandle(url) -> bool`, `GetResponse(ctx, url) -> IActionResult`. All concrete mappers are auto-DI-registered (composition root resolves `IEnumerable<IMapHttpRequestsToDisk>`). |
| `ICacheBreakerProvider` | `src/Sonarr.Http/Frontend/Mappers/CacheBreakerProvider.cs:10` | interface | `AddCacheBreakerToPath(url) -> url`. Returns `?t={ticks}` in dev, `?h={md5}` in prod. Skips `.ics` and `main` (line 47). |
| `ICacheableSpecification` | `src/Sonarr.Http/Middleware/CacheableSpecification.cs:8` | interface | Single rule: returns `false` for non-prod, `false` for `/api` (except `/api/.../MediaCover`), `false` for `/signalr`, `false` for `index.js`, `initialize.json`, `feed/`, `logfile/*.txt`, `updatelogfile/*.txt`, `true` otherwise. |

## Public services

| Type | Methods | Side effects | Notes |
|------|---------|--------------|-------|
| `SchemaBuilder` (static) | `ToSchema(model) -> List<Field>`, `ReadFromSchema(fields, type, model) -> object`, `GetFieldMappings(type) -> FieldMapping[]`, `Initialize(IContainer)` | Reads all properties of the model class, applies `[FieldDefinition]` attributes, and produces JSON-friendly `Field[]`. Localizes `Label`, `HelpText`, `HelpTextWarning` via `ILocalizationService` (resolved lazily at line 24). Caches per-type in static `Dictionary<Type, FieldMapping[]>` (line 19) — locked but not invalidated. | The cache lifetime is the process; if a settings class is loaded from a hot-reloaded provider the cache won't refresh. SH-Q2. |
| `SonarrErrorPipeline` | `HandleException(HttpContext)` | Writes the error envelope to the response body. Uses `STJson.SerializeAsync` for the `ErrorModel`; uses `STJson.ToJson(validationException.Errors)` for `ValidationException` (line 52). Logs at Warn / Fatal depending on exception type. | Wired in `Startup.cs:340-344` via `app.UseExceptionHandler` with `AllowStatusCode404Response = true` so 404s still get processed. |
| `IAuthenticationService` / `AuthenticationService` | `Login(req, user, pass)`, `Logout(ctx)`, `LogUnauthorized(req)` | Logs to a dedicated NLog logger named `"Auth"`. Static `AUTH_METHOD` cached at constructor (line 21). | Cross-cite `authentication-security.md` SEC-Q1 for the not-refreshed-on-config-save bug. |
| `UiAuthorizationPolicyProvider` | `GetPolicyAsync("UI") -> AuthorizationPolicy` | Returns a policy whose authentication scheme is `_config.AuthenticationMethod.ToString()` (Forms / Basic / None / External) and whose only requirement is `BypassableDenyAnonymousAuthorizationRequirement`. Falls back to the default provider for other names. | Registered as singleton at `Startup.cs:228`. The "UI" policy is the *only* policy that lives outside the static `AddAuthorization` call at line 231. |
| `UiAuthorizationHandler` | `HandleRequirementAsync` | When `AuthenticationRequired == DisabledForLocalAddresses`, succeeds the requirement if `httpContext.GetRemoteIP()` is local (or CGNAT when `TrustCgnatIpAddresses`). | Cached `_authenticationRequired` (static, line 17) is refreshed on `IHandle<ConfigSavedEvent>` (line 43) — *unlike* `AuthenticationService.AUTH_METHOD`. |
| `ApiKeyAuthenticationHandler` | `HandleAuthenticateAsync`, `HandleChallengeAsync (401)`, `HandleForbiddenAsync (403)` | Pulls API key from query (configurable name), header (configurable), or `Authorization: Bearer ...`. String-equals the configured key against the cached value. | Two scheme registrations: `API` (header `X-Api-Key`, query `apikey`) and `SignalR` (header `X-Api-Key`, query `access_token`). See `Authentication/AuthenticationBuilderExtensions.cs:57-66`. |
| `NoAuthenticationHandler` | `HandleAuthenticateAsync` | Always returns `AuthenticateResult.Success` with a `ClaimsPrincipal` carrying `user=Anonymous` and `AuthType=None`. | Wired to schemes `None` and `External` (the latter is documented at the auth card; identical implementation, different name). |
| `IDuplicateEndpointDetector` / `DuplicateEndpointDetector` | `GetDuplicateEndpoints(EndpointDataSource) -> Dictionary<string, List<string>>` | Reflects into ASP.NET's internal `DfaMatcherBuilder` to find route collisions. | Test-time helper only; not in the request pipeline. Used by route-uniqueness fixtures. |
| `CacheableSpecification` | `IsCacheable(HttpRequest)` | Pure function over `request.Path` + `request.Query["h"]`. | The `?h=` cache-buster is set by `CacheBreakerProvider`; presence forces cacheable, otherwise specific path patterns disable. |
| `LoggingMiddleware` | `InvokeAsync` | Writes `Req: ...` then `Res: ...` to NLog logger `"Http"`. For `/api/...` requests, also writes a single `Res:` line to NLog logger `"Api"`. Stamps `ApiRequestSequenceID` and `ApiRequestStartTime` into `HttpContext.Items`. | The error pipeline isn't actually consulted here despite the constructor taking one — the field is captured but never used. SH-Q3. |
| `BufferingMiddleware` | `InvokeAsync` | Calls `context.Request.EnableBuffering()` for paths under `/api/v3/command` or `/api/v5/command`. | The two paths are hardcoded in `Startup.cs:358`. Buffering is needed because `CommandController` reads the body twice (once to dispatch the typed command, once to log it). |
| `CacheHeaderMiddleware` | `InvokeAsync` | Sets either `Cache-Control: max-age=31536000, public` (cacheable) or `Cache-Control: no-cache, no-store; Expires: -1; Pragma: no-cache` (not cacheable). Skips `OPTIONS` requests. | Header values are defined in `Sonarr.Http/Extensions/RequestExtensions.cs:110-122`. |
| `IfModifiedMiddleware` | `InvokeAsync` | If the request is cacheable and carries `If-Modified-Since`, returns `304 Not Modified` with cache headers and a sniffed mime-type. **Does not actually compare timestamps** — any `If-Modified-Since` value at all triggers 304 for cacheable resources. | This works because the `?h={md5}` cache-buster makes URLs change when content changes; once the URL is static, 304 is correct. SH-Q4 — but is exploitable for cache-poisoning if a frontend asset is invalidated *without* the URL changing. |
| `StartingUpMiddleware` | `InvokeAsync` | While `IRuntimeInfo.IsStarting`, returns `503 Service Unavailable` with body `"Sonarr is starting up, please try again later"` (text/plain) or `{"errorMessage": "..."}` (json) depending on `request.IsApiRequest()`. | The JSON shape is **not** the standard `ErrorModel` envelope — it's a one-off `{ ErrorMessage = MESSAGE }` (camelCased to `errorMessage` by STJson). SH-Q5. |
| `UrlBaseMiddleware` | `InvokeAsync` | If `urlBase` is set and the request did *not* enter through the `PathBase`, replies `307 Temporary Redirect` to `{urlBase}{path}{query}`. | Runs *after* `app.UsePathBase(urlBase)` (`Startup.cs:339`), so requests that *did* hit the base have an empty `Request.Path` (everything moved into `PathBase`) and are passed through. |
| `VersionMiddleware` | `InvokeAsync` | Stamps `X-Application-Version: {BuildInfo.Version}` on every API response. | Only on `/api` requests (line 23). Header is set early in the pipeline so the controller can override if it wants. |

## Middleware pipeline

The pipeline is assembled in **`src/NzbDrone.Host/Startup.cs:287-378`**. The order is significant — once a middleware short-circuits, downstream entries do not run. The list below numbers the entries in the order they are added by `app.Use*`.

| # | Middleware | Source | What it does |
|---|------------|--------|--------------|
| 1 | `UseForwardedHeaders` | `Startup.cs:337` (configured at `:66-74`) | Reads `X-Forwarded-{For,Proto,Host}`. `KnownIPNetworks` is populated with RFC1918 `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` + IPv6 ULA `fc00::/7` + link-local `fe80::/10`. **`KnownProxies` is left empty** — ASP.NET's default `ForwardLimit = 1` means only the immediate peer's headers are honoured, but if that peer is on any of the trusted networks the spoof is accepted. The auth bypass (`DisabledForLocalAddresses`) consumes the rewritten `Connection.RemoteIpAddress`, which is exactly the spoof target. Cross-cite `authentication-security.md` SEC-Q7. |
| 2 | `LoggingMiddleware` | `Startup.cs:338`, code at `Middleware/LoggingMiddleware.cs:13` | Per-request sequence ID + Trace log of `Req: ...` / `Res: ...`. `/api/...` also logs to logger `"Api"` at Debug level with the elapsed milliseconds. |
| 3 | `UsePathBase(urlBase)` | `Startup.cs:339` | ASP.NET built-in. Strips the configured URL base off `request.Path` (and pushes it onto `request.PathBase`) so downstream routing matches without it. |
| 4 | `UseExceptionHandler(SonarrErrorPipeline.HandleException)` | `Startup.cs:340-344` | Wraps the rest of the pipeline. `AllowStatusCode404Response = true` so a 404 from below still serializes through the error pipeline. |
| 5 | `UseRouting` | `Startup.cs:346` | ASP.NET built-in. Picks the endpoint to dispatch to (controller action, MapHub, MapPost) — required *before* `UseAuthorization` so the policy is known. |
| 6 | `UseCors` | `Startup.cs:347` | Two named policies: `ApiCorsPolicy` (`AllowAnyOrigin / AllowAnyMethod / AllowAnyHeader`, configured at `:80-86`, attached to controllers via `VersionedApiControllerAttribute.PolicyName`) and `AllowGet` (`AllowAnyOrigin / WithMethods("GET", "OPTIONS") / AllowAnyHeader`, configured at `:88-92`, attached to `/content/...` via `[EnableCors("AllowGet")]` on `StaticResourceController.IndexContent` at `Frontend/StaticResourceController.cs:34`). |
| 7 | `UseAuthentication` | `Startup.cs:348` | Runs registered handlers (`Forms`, `Basic`, `None`, `External`, `API`, `SignalR`) per-request based on the policy chosen above. Cross-cite `authentication-security.md`. |
| 8 | `UseAuthorization` | `Startup.cs:349` | Enforces the controller's `[Authorize(Policy=...)]`. Default fallback policy (configured at `:240-242`) is "API + RequireAuthenticatedUser" — meaning *every* unmarked endpoint requires the API key. |
| 9 | `UseResponseCompression` | `Startup.cs:350` | ASP.NET default (gzip/brotli). |
| 10 | `VersionMiddleware` | `Startup.cs:353`, code at `Middleware/VersionMiddleware.cs:8` | `X-Application-Version` response header on `/api/...`. |
| 11 | `UrlBaseMiddleware(urlBase)` | `Startup.cs:354`, code at `Middleware/UrlBaseMiddleware.cs:7` | 307 redirect from un-prefixed path to prefixed path when `UrlBase` is set. |
| 12 | `StartingUpMiddleware` | `Startup.cs:355`, code at `Middleware/StartingUpMiddleware.cs:10` | 503 short-circuit while `IRuntimeInfo.IsStarting`. |
| 13 | `CacheHeaderMiddleware` | `Startup.cs:356`, code at `Middleware/CacheHeaderMiddleware.cs:7` | Sets cache headers based on `ICacheableSpecification`. |
| 14 | `IfModifiedMiddleware` | `Startup.cs:357`, code at `Middleware/IfModifiedMiddleware.cs:9` | 304 short-circuit for cacheable + `If-Modified-Since`. |
| 15 | `BufferingMiddleware(["/api/v3/command","/api/v5/command"])` | `Startup.cs:358`, code at `Middleware/BufferingMiddleware.cs:9` | `Request.EnableBuffering()` for the two command paths. |
| 16 | `UseWebSockets` | `Startup.cs:360` | ASP.NET built-in; required by SignalR. |
| 17 | `UseMiniProfiler` | `Startup.cs:361` | StackExchange profiler UI. Mounted at `/profiler` (configured at `Startup.cs:250`). The profiler `Results` POST is policy-gated to `UI` (`Startup.cs:375`). `IgnoredPaths.Add("/MediaCover")` at `:281` skips media cover requests from profiling. |
| 18 | `UseSwagger` (debug only) | `Startup.cs:364-370` | Mounts `/docs/{v3|v5}/openapi.json`. **No `RequireAuthorization`** — Swagger JSON is anonymous in debug builds. Cross-cite `authentication-security.md` SEC-Q9. |
| 19 | `UseEndpoints` | `Startup.cs:372-377` | Maps: `MessageHub` at `/signalr/messages` requiring policy `SignalR`; `POST /profiler/results` requiring policy `UI`; `MapControllers()` for everything else. |

`app.Properties["host.AppName"] = BuildInfo.AppName` is set at line 351 between `UseResponseCompression` and `VersionMiddleware` — this is purely for OWIN-style consumers; nothing in the request pipeline reads it.

### Routing precedence

Endpoints register at line 372-377; their effective precedence (highest first) is:

1. **Hub:** `/signalr/messages` (SignalR hub via `MapHub`).
2. **Profiler results:** `POST /profiler/results` (no-op `Task.CompletedTask`, but the route exists so MiniProfiler can write through).
3. **MVC controllers** registered via `MapControllers`. Within MVC, the route order is governed by attribute `Order` values:
   * `RestGetByIdAttribute.Order = 0` (most specific) — `GET api/v{n}/{resource}/{id:int}`.
   * `VersionedApiControllerAttribute.Order = 2` — `api/v{n}/{resource}` for the controller itself plus any non-`Order=0` actions.
   * `VersionedFeedControllerAttribute.Order = 2` — `feed/v{n}/{resource}`.
   * Catch-all attributes on `StaticResourceController` (line 36 `/content/...`, line 43 `/{**path:regex(^(?!(api|feed)/).*)}` — explicitly exclude `api` and `feed` so MVC's catch-all does not eat them).
4. **`StaticResourceController.Index`** (`Frontend/StaticResourceController.cs:42-43`) is the catchall fallback. It rejects paths that start with `api/` or `feed/`, and dispatches the rest to the registered `IMapHttpRequestsToDisk` mappers.

`MediaCover` is served by `MediaCoverMapper` (`Frontend/Mappers/MediaCoverMapper.cs:11`), which is invoked from inside `StaticResourceController.MapResource` — i.e. it runs *under* the controller's `[Authorize(Policy="UI")]` decorator (`StaticResourceController.cs:13`). The mapper resolves the path under `<AppData>/MediaCover/...`, and falls back to the un-resized `.jpg` when a `-{size}.jpg` does not exist (line 30-39).

`MediaCoverProxy` (`Frontend/Mappers/MediaCoverProxyMapper.cs:12`) is a different mapper that returns image bytes from an in-memory cache keyed by hash — used to proxy remote images during series/episode lookup before they're downloaded. The path pattern is `/MediaCoverProxy/{hash}/{filename}.{jpg|png|gif}` (line 14); the hash is the cache key handed out by `IMediaCoverProxy.RegisterUrl` upstream. Misses 404; hits stream the bytes with the inferred mime type.

`/login` is owned by `StaticResourceController.LoginPage` (line 27-31), explicitly `[AllowAnonymous]` and dispatched to `LoginHtmlMapper`. The HTML form there `POST`s to `/login`, which is owned by `AuthenticationController.Login` (`Authentication/AuthenticationController.cs:35`) — the two routes don't collide because the controller is `[ApiController]` and matches `POST /login`, while `StaticResourceController.LoginPage` is decorated with `[HttpGet("login")]`. ASP.NET's endpoint routing distinguishes the two by HTTP method.

### Path-collision surface map

The catchall regex `^(?!(api|feed)/).*` on `StaticResourceController.Index` (line 43) is the wall that keeps the SPA fallback from eating API routes. Anything that doesn't start with `api/` or `feed/` is fair game for the static dispatcher. The dispatcher then walks the mapper list with `SingleOrDefault` (line 53) — meaning two mappers claiming the same path causes a 500-level failure, not a silent first-match. `IndexHtmlMapper` is the broadest claim (no extension, not `/content`/`/mediacover`/`/login`); every other mapper carves out a more specific predicate. Adding a new mapper in the Go port requires checking `CanHandle` against the existing set to ensure no overlap.

The `/signalr/messages` route is registered explicitly in `UseEndpoints` and is not visible to MVC's `MapControllers`, so the catchall fallback never sees it.

## DB tables touched

None. `Sonarr.Http` is a stateless plumbing layer. The dependencies it carries (`IConfigFileProvider`, `IUserService`, etc.) are passed through to other subsystems.

## Events raised (IEventAggregator)

None directly. The `RestControllerWithSignalR<T,M>` base does broadcast `SignalRMessage` envelopes via `IBroadcastSignalRMessage`, but those are SignalR-protocol messages, not in-process `IEvent`s.

## Events consumed

| Event | Handler | Behavior |
|-------|---------|----------|
| `ConfigSavedEvent` | `UiAuthorizationHandler.Handle(ConfigSavedEvent)` (`Authentication/UiAuthorizationHandler.cs:43`) | Refreshes the cached `_authenticationRequired` so the `DisabledForLocalAddresses` toggle takes effect without restart. Cross-cite `authentication-security.md` SEC-Q1 — the sibling `AuthenticationService.AUTH_METHOD` is *not* refreshed. |
| `ModelEvent<TModel>` | `RestControllerWithSignalR<TResource,TModel>.Handle(ModelEvent<TModel>)` (`REST/RestControllerWithSignalR.cs:37`) | Broadcasts a `SignalRMessage` containing a `ResourceChangeMessage<TResource>` whenever the underlying model is created / updated / deleted / synced. For `Deleted` and `Sync`, an extra "no-resource" broadcast goes out before the per-id one. |

## Config keys

Read at startup via `IConfigFileProvider`:

* `UrlBase` — drives both `app.UsePathBase` and `UrlBaseMiddleware`. Empty string or `null` means "no base".
* `ApiKey` — cached by `ApiKeyAuthenticationHandler` (line 35) and `InitializeJsonController` (line 30).
* `InstanceName` — embedded into the cookie name (`AuthenticationBuilderExtensions.cs:43`) and into `manifest.json` as `__INSTANCE_NAME__` (`ManifestMapper.cs:41`).
* `AuthenticationMethod` — drives the `UI` policy's authentication scheme (`UiAuthorizationPolicyProvider.cs:31`).
* `AuthenticationRequired` — drives the bypass logic in `UiAuthorizationHandler`.
* `TrustCgnatIpAddresses` — extends the local-address bypass to `100.64.0.0/10`.
* `Theme` — substituted into `index.html` and `login.html` as `_THEME_` (`IndexHtmlMapper.cs:50`, `LoginHtmlMapper.cs:42`) and into MiniProfiler's color scheme (`Startup.cs:252-262`).
* `ProfilerEnabled` — gates the `__MINI_PROFILER__` substitution (`IndexHtmlMapper.cs:52-67`).
* `ProfilerPosition` — drives `MiniProfilerOptions.PopupRenderPosition` (`Startup.cs:265-279`).
* `LogDbEnabled` — read by `Startup.Configure` to decide whether to instantiate `logDatabaseFactory.Value` (line 317-321) — not strictly an HTTP concern but appears in this method.
* `Branch` — emitted into `initialize.json` (line 54).
* `UiFolder` — base folder for static assets (`StaticResourceMapper.cs:26`, `IndexHtmlMapper.cs:26`, `LoginHtmlMapper.cs:23`, `RobotsTxtMapper.cs:25`, `ManifestMapper.cs:19`).

Read by Bootstrap (`src/NzbDrone.Host/Bootstrap.cs:196-210`) and not Sonarr.Http itself but materially shapes the HTTP surface:

* `ServerOptions.AllowSynchronousIO = false` (line 208) — Kestrel rejects synchronous body reads.
* `ServerOptions.MaxRequestBodySize = null` (line 209) — no upload size limit. SH-Q6.

## Jobs / commands (`IExecute<TCommand>`)

None. `Sonarr.Http` is a plumbing layer; it dispatches commands but does not execute any.

## External services

None directly. Static assets are served from disk; uploads (profile images, etc.) are not handled in this layer.

## ClientSchema rendering pipeline

The "dynamic settings form" the React app displays for each provider is generated by the `ClientSchema` namespace. The flow:

1. **At startup**, `Startup.cs:323` calls `SchemaBuilder.Initialize(container)` so the static class can resolve `ILocalizationService` once.
2. **At request time**, the controller calls `SchemaBuilder.ToSchema(modelInstance)` (`SchemaBuilder.cs:27`) for an instance of the settings class (e.g. `SabnzbdSettings`).
3. `ToSchema` calls `GetFieldMappings(modelType)` (line 88), which is memoized in `_mappings` (line 19). On a cache miss, it calls `GetFieldMapping(type, "", v => v)` (line 109).
4. `GetFieldMapping` walks the type's properties via reflection (`GetProperties` at line 189). Each property must carry `[FieldDefinition]` (`src/NzbDrone.Core/Annotations/FieldDefinitionAttribute.cs:7`); properties without it are silently skipped. Properties are sorted by the attribute's `Order`.
5. For each property:
   * If the property type `IsSimpleType()` (primitive or string), a `Field` is created with localized `Label`, `HelpText`, `HelpTextWarning` (lines 119-130) and the `[FieldDefinition]` metadata copied through (lines 132-146).
   * Otherwise, the function recurses into the nested type with prefix `{propName}.` so the resulting `Field.Name` is dotted (e.g. `host.address`).
6. `Field.Type` is set to the `[FieldDefinition].Type` enum's name with first char lowercased (line 142). The full enum (`FieldDefinitionAttribute.cs:86-106`):

| `FieldType` | UI rendering |
|-------------|--------------|
| `Textbox` | plain text input |
| `Number` | numeric input (with `IsFloat=true` when property is `double`/`double?` — line 165-168) |
| `Password` | masked input (`Privacy=Password` separately controls round-trip behavior) |
| `Checkbox` | boolean toggle |
| `Select` | drop-down. `SelectOptions` from enum or `ISelectOptionsConverter` (line 216-263), or via `SelectOptionsProviderAction` for dynamic options (line 148-158). |
| `Path` | folder picker |
| `FilePath` | file picker |
| `Tag` | tag autocomplete |
| `Action` | button (`SelectOptionsProviderAction` is the action endpoint) |
| `Url` | URL input |
| `Captcha` | captcha widget |
| `OAuth` | OAuth button |
| `Device` | device picker (used by Pushover etc.) |
| `TagSelect` | tag dropdown with options from the same `SelectOptions` channel as `Select` |
| `RootFolder` | root-folder picker |
| `QualityProfile` | quality-profile picker |
| `SeriesTag` | series-tag picker |
| `KeyValueList` | key/value list editor |

7. For `Select` / `TagSelect`, `GetSelectOptions` (line 216) handles two cases:
   * **Enum** — every static field that isn't `[Obsolete]` is enumerated; `FieldOptionAttribute` provides labels and hints; the enum's underlying integer is the `Value`. The `Hint` defaults to `"({value})"` if not set explicitly (line 241).
   * **`ISelectOptionsConverter`** — instantiates the converter type, calls `GetSelectOptions()`. (Located in `src/NzbDrone.Core/Annotations/SelectOptionsConverter.cs`.)
8. **Privacy redaction** — at `SchemaBuilder.cs:40-43`, after `field.Value` is set from the model, fields with `Privacy == ApiKey` or `Privacy == Password` and a non-empty value are replaced with the literal `"********"` (line 18).
9. **Round-trip** — `ReadFromSchema(fields, type, model)` (line 52) converts a JSON field list back into an instance of the settings type. The redaction is reversed: if the incoming field value still equals `"********"` *and* an existing model was passed, the existing value is preserved (line 67-75). This is how the UI can submit settings without forcing the user to retype the API key.
10. **Value coercion** — `GetValueConverter` (line 265) handles `int`, `long`, `double` (and nullables), `IEnumerable<int>`, `IEnumerable<string>`, plus a default branch (line 327) that round-trips via `STJson.Deserialize(json, propertyType)`. Notably the `IEnumerable<int>` and `IEnumerable<string>` branches accept either a `JsonElement` array *or* a comma-separated string — the React UI submits JSON arrays, but the legacy comma-separated form is still accepted. SH-Q7.

### Hidden, Section, Placeholder, Unit

Three more attribute fields shape the UI:

* **`Hidden`** — `HiddenType.Visible | Hidden | HiddenIfNotSet` (`FieldDefinitionAttribute.cs:108-113`). Anything other than `Visible` is serialized as a lower-cased string into `Field.Hidden` (`SchemaBuilder.cs:160-163`) — i.e. the on-wire shape is the camelCase name (`hidden` or `hiddenIfNotSet`). The UI hides those rows unless their value is set.
* **`Section`** — free-form string used by the UI to group fields under a header (e.g. `"Authentication"`, `"Filtering"`).
* **`Placeholder`** — input placeholder text, copied through unmodified.
* **`Unit`** — a unit suffix shown next to the field (e.g. `"GB"`, `"days"`). Not localized.

### Localization

`Label`, `HelpText`, `HelpTextWarning` are passed through `_localizationService.GetLocalizedString` (`SchemaBuilder.cs:120-130`) with a token bag built from `[FieldToken]` attributes (`GetTokens`, line 198). A field-token attribute scopes a token name -> value to a particular `(label, TokenField)` tuple — i.e. the same `Label` key can have different tokens for the `Label`/`HelpText`/`HelpTextWarning` slot. Cross-cite `localization-validation.md` for the localization key conventions.

### `FieldOptionAttribute`

When `SelectOptions` points at an enum, each enum value can carry `[FieldOption(label: "X", Hint = "Y")]` (`FieldDefinitionAttribute.cs:31`). The label is localized; the hint defaults to `"({integerValue})"` if not provided (`SchemaBuilder.cs:241`). The order is the line number where the enum member is declared (`[CallerLineNumber]` at line 34) — meaning reordering enum members reorders the dropdown.

The full enumeration of which provider classes carry which `[FieldDefinition]` properties is deferred to `analysis/subsystems/thingiprovider.md` (running in parallel). This card stops at the rendering pipeline.

## Frontend asset pipeline

`StaticResourceController` (`Frontend/StaticResourceController.cs:15`) exposes three routes:

* `GET /login` (`[AllowAnonymous]`, line 27) — dispatches to `LoginHtmlMapper`.
* `GET /content/{**path}` (`[EnableCors("AllowGet")]` + `[AllowAnonymous]`, line 34-37) — dispatches to whichever mapper claims `/content/...`.
* `GET /` and `GET /{**path:regex(^(?!(api|feed)/).*)}` (line 42-43) — the catchall fallback for SPA routes; protected by the controller's class-level `[Authorize(Policy="UI")]`.

The dispatcher iterates `_requestMappers` (an `IEnumerable<IMapHttpRequestsToDisk>` injected by DI) and calls `m.CanHandle(path)` until exactly one matches. `SingleOrDefault` (line 53) — meaning if two mappers claim the same path, the request fails. The mapper that wins:

| Mapper | Path predicate | Source path | Substitutions |
|--------|----------------|-------------|---------------|
| `IndexHtmlMapper` | not under `/content`, `/mediacover`, `/login`; no `.` (no extension) — i.e. SPA routes | `<UI>/index.html` | `__URL_BASE__`, `_THEME_`, `__MINI_PROFILER__`, plus the regex-driven `href=`/`src=` rewrite that prepends `UrlBase` and appends `?h={md5}` cache-buster (`HtmlMapperBase.cs:15`, `:53-67`). |
| `LoginHtmlMapper` | `/login` | `<UI>/login.html` | `__URL_BASE__`, `_THEME_`. Same `href`/`src` cache-buster rewrite. |
| `StaticResourceMapper` | `/content/...` (except `manifest`/`browserconfig`), `*.js`, `*.map`, `*.css`, `*.ico` (except `/favicon.ico`), `*.swf`, `oauth.html` | `<UI>/{path}` | None. Raw file. |
| `FaviconMapper` | `/favicon.ico` | `<UI>/Content/Images/Icons/favicon.ico` (or `favicon-debug.ico` in debug) | None. |
| `RobotsTxtMapper` | `/robots.txt` | `<UI>/Content/robots.txt` | None. |
| `ManifestMapper` | `/Content/manifest...` | `<UI>/Content/manifest.json` | `__URL_BASE__`, `__INSTANCE_NAME__`. |
| `BrowserConfig` | `/Content/browserconfig...` | `<UI>/Content/browserconfig.xml` | `__URL_BASE__`. |
| `MediaCoverMapper` | `/MediaCover/...` | `<AppData>/MediaCover/...` | None. Falls back to base `.jpg` if a `-{size}.jpg` is missing. |
| `MediaCoverProxyMapper` | `/MediaCoverProxy/{hash}/{filename}` | (in-memory) | Returns image bytes from `IMediaCoverProxy`. |
| `BackupFileMapper` | `/backup/...` (matched against `BackupService.BackupFileRegex`) | `<BackupFolder>/...` | None. |
| `LogFileMapper` | `/logfile/...` ending in `.txt` | `<LogFolder>/{name}.txt` | None. |
| `UpdateLogFileMapper` | `/updatelogfile/...` ending in `.txt` | `<UpdateLogFolder>/{name}.txt` | None. |

The `__URL_BASE__` substitution and the `href=`/`src=` cache-buster rewriting both happen in `HtmlMapperBase.GetHtmlText` (`Frontend/Mappers/HtmlMapperBase.cs:43-74`). The cache-buster regex is:

```
(?:(?<attribute>href|src)="")(?<path>.*?(?<extension>css|js|png|ico|ics|svg|json))(?:"")(?:\s(?<nohash>data-no-hash))?
```

A `data-no-hash` attribute opts out of the hash. The hash is base64-of-MD5 (`CacheBreakerProvider.cs:40`), trimmed of `=` padding. In dev mode, ticks-since-epoch is used instead.

`StaticResourceController.MapResource` calls `Response.Headers.DisableCache()` (line 63) when the served result is `text/html` — even though `CacheHeaderMiddleware` already runs upstream. Belt-and-suspenders against an `index.html` ever being cached.

There is **no Content-Security-Policy header** emitted by the frontend pipeline. There is **no CSRF token** baked into the SPA — the API depends on the API-key header, the UI on the cookie + `SameSite=Lax`. Cross-cite `authentication-security.md` SEC-Q10.

### `initialize.json`

`InitializeJsonController` (`Frontend/InitializeJsonController.cs:14`) is a special endpoint at `GET /initialize.json` returning a hand-built JSON document (line 47-58). The fields:

```
{
  "apiRoot": "{urlBase}/api/v3",
  "apiKey": "{apiKey}",
  "release": "{BuildInfo.Release}",
  "version": "{BuildInfo.Version}",
  "instanceName": "{InstanceName}",
  "theme": "{Theme}",
  "branch": "{Branch.ToLower()}",
  "analytics": {true|false},
  "userHash": "{anonymous user token}",
  "urlBase": "{urlBase}",
  "isProduction": {true|false}
}
```

The endpoint is **`[Authorize(Policy = "UI")]`** — i.e. requires the UI cookie or API key. The constructor caches `_apiKey` and `_urlBase` into static fields (line 19-20) — this means any change to API key or URL base requires a restart for the SPA to pick it up. SH-Q8.

`apiRoot` is hardcoded to `v3` even on v5 builds. The SPA still talks to v3 routes for everything that exists in v3; v5-only endpoints are addressed directly. SH-Q9.

## ErrorManagement / Exceptions

### Pipeline

`SonarrErrorPipeline.HandleException` (`ErrorManagement/SonarrErrorPipeline.cs:24`) is the catchall for any exception escaping a controller. The dispatch:

| Exception type | Status code | Body shape |
|----------------|-------------|------------|
| `ApiException` (`BadRequestException`, `NotFoundException`, `MethodNotAllowedException`, `UnsupportedMediaTypeException`) | `apiException.StatusCode` | `ErrorModel { Message, Description = null, Content = exception.Content }` — **`Description` is dropped** when constructed via `new ErrorModel(apiException)` (line 43; constructor at `ErrorModel.cs:15`). |
| `FluentValidation.ValidationException` | `400 Bad Request` | `STJson.ToJson(validationException.Errors)` — i.e. the **raw `IList<ValidationFailure>`** with FluentValidation's own field shape (`PropertyName`, `ErrorMessage`, `AttemptedValue`, `Severity`, etc., all camelCased). **Not** wrapped in `ErrorModel`. SH-Q10. Cross-cite `localization-validation.md` VAL-Q4. |
| `NzbDroneClientException` | `clientException.StatusCode` | `ErrorModel { Message, Description }` (default constructor, both fields populated from the exception). |
| `ModelNotFoundException` | `404 Not Found` | `ErrorModel { Message, Description }`. |
| `ModelConflictException` | `409 Conflict` | `ErrorModel { Message, Description }`. |
| `SQLiteException` (only on PUT/POST containing "constraint failed") | `409 Conflict` | `ErrorModel { Message, Description }`. Logged at Error level. |
| Anything else | `500 Internal Server Error` | `ErrorModel { Message, Description = exception.ToString() }`. Logged at Fatal. |

`ErrorModel.WriteToResponse` (`ErrorManagement/ErrorModel.cs:25`) sets `ContentType = "application/json"` and uses `STJson.SerializeAsync(this, response.Body)` — meaning the on-wire field names are camelCase (`message`, `description`, `content`).

The validation error path is the *only* case where the body is **not** an `ErrorModel`; it's a JSON array of FluentValidation `ValidationFailure` objects. This shape is what V3/V5 clients depend on for displaying inline form errors. The exact JSON shape of `ValidationFailure` is the C# property set serialized through STJson camelCase: `{propertyName, errorMessage, attemptedValue, severity, errorCode, formattedMessagePlaceholderValues, customState}`. The `severity` field is the `Severity` enum serialized as a camelCase string (`error`, `warning`, `info`) per the global `JsonStringEnumConverter` policy. Cross-cite `localization-validation.md` for how `NzbDroneValidationFailure` extends this with `IsWarning`, `InfoLink`, etc. (VAL-Q4).

### `WriteAsync(STJson.ToJson(...))`

The Validation path (line 52) uses `response.WriteAsync` with the *string* form of `STJson.ToJson` rather than `STJson.SerializeAsync` directly to a stream. This double-allocates (string -> bytes) but is the only path that does so. SH-Q11.

## REST controller base

`RestController<TResource>` (`REST/RestController.cs:20`) is the validation gatekeeper for V3/V5 controllers. Behavior:

1. **`OnActionExecuting`** (line 74) inspects the current action's `[SkipValidation]` (`REST/Attributes/SkipValidationAttribute.cs:5`) — `Skip` opts out of POST/PUT validation, `SkipShared` opts out of `SharedValidator`.
2. For POST/PUT requests, every body argument that is `TResource` or `IEnumerable<TResource>` is validated; for PUT, route `id` is mapped onto `resource.Id` if not set in the body (line 100-103). Failures throw `FluentValidation.ValidationException(errors)` — caught by the pipeline above.
3. **`RestPutByIdAttribute`** (`REST/Attributes/RestPutByIdAttribute.cs:9`) extends `[HttpPut("{id:int?}")]` — so the route id is optional. **`RestDeleteByIdAttribute`** (`:9`) extends `[HttpDelete("{id:int}")]` — required. **`RestGetByIdAttribute`** (`:8`) is a custom `IRouteTemplateProvider` with `Template = "{id:int}"` and `Order = 0` (line 12) so the get-by-id route wins over the get-all route. **`RestPostByIdAttribute`** (`:7`) is just a marker that subclasses `[HttpPost]`.
4. **`Deprecation` header** (line 121) — if the action or controller carries `[Obsolete]`, the response gets `Deprecation: true` and a Warn log of the User-Agent that hit it. This is what powers the "deprecated endpoint" telemetry.
5. **`TypedResults.Ok / NotFound / Accepted / Created`** — the controller surfaces both Minimal-API-style `Results<>` returns and classic `ActionResult<>` returns; both helpers exist (line 161-185).

`RestControllerWithSignalR<TResource,TModel>` (`REST/RestControllerWithSignalR.cs:10`) layers SignalR broadcasting on top:

* On `IHandle<ModelEvent<TModel>>` (line 37), it calls `BroadcastResourceChange(action, id)` once per model event. For `Deleted` and `Sync` it *also* calls a no-id `BroadcastResourceChange(action)` first.
* The broadcast envelope is `SignalRMessage { Name = Resource, Body = ResourceChangeMessage<TResource>(...), Action = action, Version = Version }`. `Resource` and `Version` are scraped from the controller's `[VersionedApiControllerAttribute]` at construction time (line 23-33).
* The broadcast is **filtered to namespaces containing `"V3"` or `"V5"`** (line 79, 102). Anything else is dropped silently. SH-Q12 — this is brittle (what about a hypothetical `V6`?) but documents the current contract.

## Authentication / authorization wiring

The `AddAppAuthentication()` extension (`Authentication/AuthenticationBuilderExtensions.cs:33`) registers five schemes:

| Scheme name | Handler | Notes |
|-------------|---------|-------|
| `None` | `NoAuthenticationHandler` | Always succeeds; produces `user=Anonymous`, `AuthType=None`. |
| `External` | `NoAuthenticationHandler` (same class) | Identical behavior to `None`. Used when a reverse-proxy upstream is doing the auth and Sonarr is told to trust whatever comes through. |
| `Forms` | `CookieAuthenticationHandler` (built-in) | Cookie name is `{InstanceName}Auth` (sanitised — diacritics removed, non-word chars stripped, `:43`). `AccessDeniedPath = /login?loginFailed=true`, `LoginPath = /login`. `ExpireTimeSpan = 7 days`, `SlidingExpiration = true`. The `OnRedirectToLogin` / `OnRedirectToAccessDenied` events (line 49-50, function at `:69-83`) detect XHR (`X-Requested-With: XMLHttpRequest` header *or query*) and return 401/403 instead of a 302 redirect. |
| `API` | `ApiKeyAuthenticationHandler` | `HeaderName=X-Api-Key`, `QueryName=apikey`. Falls back to `Authorization: Bearer ...` (`ApiKeyAuthenticationHandler.cs:51`). |
| `SignalR` | `ApiKeyAuthenticationHandler` | `HeaderName=X-Api-Key`, `QueryName=access_token` (the SignalR JS client uses `access_token` for WebSocket query auth). |

**`Basic`** is *also* a valid `AuthenticationMethod` configured value, but not registered as an HTTP auth scheme here — it's instead registered in `NzbDrone.Http.Authentication` (the auth card). When `AuthenticationMethod = Basic`, `UiAuthorizationPolicyProvider.GetPolicyAsync("UI")` (line 31) will pass `"Basic"` as the policy's auth scheme, and the `Forms` handler is bypassed.

### Policies

| Policy | Where defined | Auth scheme | Requirement |
|--------|---------------|-------------|-------------|
| `UI` | `UiAuthorizationPolicyProvider.GetPolicyAsync` | `_config.AuthenticationMethod.ToString()` — i.e. `Forms` / `Basic` / `None` / `External` | `BypassableDenyAnonymousAuthorizationRequirement` (handled by `UiAuthorizationHandler`) |
| `API` | (default fallback policy) `Startup.cs:240-242` | `API` | `RequireAuthenticatedUser` |
| `SignalR` | `Startup.cs:233-237` | `SignalR` | `RequireAuthenticatedUser` |

The `UI` policy is the only one assembled dynamically per-request — because `AuthenticationMethod` can be reconfigured at runtime, the policy provider rebuilds the policy on every challenge. The default fallback (every controller without its own `[Authorize]`) is the `API` policy.

### `BypassableDenyAnonymous` mechanism

`UiAuthorizationHandler.HandleRequirementAsync` (`Authentication/UiAuthorizationHandler.cs:25`):

* If `AuthenticationRequired == DisabledForLocalAddresses`, succeeds the requirement when the remote IP is local *or* (when `TrustCgnatIpAddresses`) CGNAT (100.64/10).
* Otherwise, does *not* call `Succeed` — but *also* does not call `Fail`. The default `DenyAnonymousAuthorizationRequirement` handler (the parent class implements this implicitly) supplies the actual deny logic — the bypassable subclass just lets the local-IP path register a `Succeed` upstream.

This is subtle and easy to break in the Go port: the requirement *is* the deny, but the handler can succeed it.

## /ping, /api, /login, /logout

| Route | Controller | Auth | Methods | Body |
|-------|------------|------|---------|------|
| `GET /ping`, `HEAD /ping` | `PingController` (`Ping/PingController.cs:12`) | `[AllowAnonymous]` (line 23) | `GET`, `HEAD` | `200` `{ status: "OK" }` or `500` `{ status: "Error" }`. The ping checks the config DB (cached for 5s, line 31). |
| `GET /api` | `ApiInfoController` (`ApiInfoController.cs:6`) | (no auth attribute) | `GET` | `{ current: "v5", deprecated: ["v3"] }`. **Falls back to the default policy** — i.e. requires API key. SH-Q13 — this seems wrong; clients use `/api` to discover versions before authenticating. |
| `POST /login` | `AuthenticationController` (`Authentication/AuthenticationController.cs:35`) | `[AllowAnonymous]` (line 18) | `POST` (form-urlencoded) | Sets the `Forms` cookie, redirects to `returnUrl` or `urlBase + /`. On `CryptographicException` (corrupt DataProtection XML) returns `Unauthorized` (line 71). |
| `GET /logout` | `AuthenticationController.Logout` (line 88) | `[AllowAnonymous]` (controller) | `GET` | Clears cookie, redirects to `urlBase + /`. |

Both routes are HTML-flow only — they redirect, they don't return JSON. The XHR auth-redirect override (`AuthenticationBuilderExtensions.cs:69`) only triggers for the `OnRedirect*` events on the *cookie scheme*, not for these explicit redirect responses.

## JSON serialization (resolves Q-05 for this layer)

Sonarr.Http (and the rest of the v5 backend) is on **System.Text.Json**, not Newtonsoft. The settings are centralised in `STJson.ApplySerializerSettings` (`src/NzbDrone.Common/Serializer/System.Text.Json/STJson.cs:24`) and applied:

* to MVC body serialization at `Startup.cs:105-114` via both `AddJsonOptions` and `ConfigureHttpJsonOptions`;
* to SignalR payloads at `Startup.cs:218-223` via `AddJsonProtocol`;
* directly inside the error pipeline (`SonarrErrorPipeline.cs:52`, `ErrorModel.cs:29`, `StartingUpMiddleware.cs:27`);
* and inside the ClientSchema fallback converter (`SchemaBuilder.cs:339`).

The exact settings (`STJson.cs:26-37`):

```
AllowTrailingCommas = true
DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull
PropertyNameCaseInsensitive = true
DictionaryKeyPolicy = JsonNamingPolicy.CamelCase
PropertyNamingPolicy = JsonNamingPolicy.CamelCase
WriteIndented = true
Converters:
  JsonStringEnumConverter(JsonNamingPolicy.CamelCase, allowIntegerValues: true)
  STJVersionConverter        — System.Version <-> "1.2.3.4" string
  STJHttpUriConverter        — Uri / HttpUri custom converter
  STJTimeSpanConverter       — TimeSpan <-> "hh:mm:ss" via TimeSpan.ToString() (default "c" format)
  STJUtcConverter            — DateTime <-> "yyyy-MM-ddTHH:mm:ssZ" UTC string (line 16)
```

Concrete answers for Q-05 (the Newtonsoft-vs-encoding-json question):

* **Casing:** camelCase for both property names and dictionary keys. Deserialization is case-insensitive.
* **Enums:** strings, camelCase, integer values also accepted on read. (Note: this is NOT the `UnderscoreStringEnumConverter` from `src/NzbDrone.Common/Serializer/Newtonsoft.Json/UnderscoreStringEnumConverter.cs` — that converter is for the legacy *Newtonsoft* path which is now confined to a few internal serializers like the embedded-doc Dapper bridge. The HTTP layer is pure STJson.)
* **DateTime:** UTC, format `yyyy-MM-ddTHH:mm:ssZ` (no fractional seconds, no offset).
* **TimeSpan:** default .NET TimeSpan format (`hh:mm:ss` or `d.hh:mm:ss`).
* **Null handling:** `WhenWritingNull` — null properties are omitted from output. Reads accept null.
* **Indented:** yes (writes pretty-printed JSON; payloads are larger but human-readable).
* **Trailing commas:** allowed on read.

The Newtonsoft-vs-STJson divide remains: Newtonsoft is still in use inside `src/NzbDrone.Common/Serializer/Newtonsoft.Json/` for embedded-document Dapper round-trips, the parser regex corpus, and a handful of legacy paths. The HTTP wire is STJson-only. Cross-cite `analysis/questions.md#Q-05`.

## Notable quirks / TODOs / HACKs found in source

* `DuplicateEndpointDetector` (`Validation/DuplicateEndpointDetector.cs:30`) reflects into ASP.NET's internal `DfaMatcherBuilder` via `ImpromptuInterface` to enumerate routes — this is a test-time guard; the comment at line 19 acknowledges it's coupling to internal types.
* `IfModifiedMiddleware` (`Middleware/IfModifiedMiddleware.cs:23`) does **not** compare timestamps; any `If-Modified-Since` header on a cacheable resource produces a 304. This works because of the `?h={md5}` cache-buster, but is a footgun if a frontend asset is invalidated without changing the URL. SH-Q4.
* `LoggingMiddleware` (`Middleware/LoggingMiddleware.cs:22`) constructs and stores `_errorHandler` (line 26) but never uses it. Dead field. SH-Q3.
* `InvalidApiKeyException` (`Exceptions/InvalidApiKeyException.cs:5`) is defined but never thrown in the current tree. SH-Q1.
* `RestControllerWithSignalR` namespace check (`REST/RestControllerWithSignalR.cs:79, 102`) hardcodes `"V3"` / `"V5"` substrings — a future `V6` namespace would silently stop broadcasting.
* `InitializeJsonController` (`Frontend/InitializeJsonController.cs:19-20`) caches `_apiKey` and `_urlBase` into static fields. Changing API key or URL base requires a process restart for the SPA to see the new values. SH-Q8.
* `ApiInfoController` (`ApiInfoController.cs:6`) lacks `[AllowAnonymous]` — falls under the default `API`-policy fallback. This contradicts the apparent intent (clients use `/api` to discover versions before authenticating). SH-Q13.
* `BackupFileMapper` (`Frontend/Mappers/BackupFileMapper.cs:8`) serves any file from the backup folder that matches `BackupService.BackupFileRegex`. The path is built via `resourceUrl.Replace("/backup/", "")` then `Path.Combine` — this is potentially path-traversal-vulnerable if a `..` survives the regex. SH-Q14.
* `LogFileMapper` and `UpdateLogFileMapper` use `Path.GetFileName(path)` (`LogFileMapper.cs:22`, `UpdateLogFileMapper.cs:22`) which does prevent `../` traversal because `GetFileName` strips path separators. Good.
* `MediaCoverMapper.Map` (line 27-28) uses `Path.DirectorySeparatorChar` to translate URL slashes — meaning on Windows a request `/MediaCover/123/poster.jpg` becomes `123\poster.jpg`. Fine on .NET but the Go port must mind this.
* `ResponseCompression` is wired (`Startup.cs:78`, `:350`) with default settings — it doesn't enable HTTPS compression by default (BREACH consideration). Default is "compress only HTTP".
* The `host.AppName` property on `IApplicationBuilder` (`Startup.cs:351`) is set but no middleware reads it.
* `BufferingMiddleware` hardcodes the two command paths (`Startup.cs:358`). New endpoints needing buffering must update this list explicitly.
* `MaxRequestBodySize = null` (`Bootstrap.cs:209`) means the upload body is unlimited. SH-Q6.
* The `SignalR` policy **shares the API key** with the `API` scheme — the only difference is the query param name (`access_token` vs `apikey`). An attacker with the API key can connect to the hub and receive every model-change broadcast. Cross-cite `authentication-security.md` SEC-Q11.
* Swagger JSON is mounted in *debug* builds at `/docs/{version}/openapi.json` with no auth (`Startup.cs:364-370`). Cross-cite `authentication-security.md` SEC-Q9.
* `ResourceValidator<T>.RuleForField` (`REST/ResourceValidator.cs:13`) constructs FluentValidation `PropertyRule` directly via reflection-y `GetMember` — this is a port of FluentValidation's internals. The Go validator chain will need its own approach to "validate one entry of a `Field[]` by name".
* Cookie names are scoped per-instance via `{InstanceName}Auth` (`AuthenticationBuilderExtensions.cs:43`) so two Sonarr instances behind the same hostname don't share auth state. The instance name is sanitised by stripping diacritics + non-word chars (line 40-41).

## Cross-cutting concerns

A handful of behaviors only make sense when the whole pipeline is read together.

* **URL base double-handling.** `app.UsePathBase` (`Startup.cs:339`) strips the prefix off `Request.Path` and shifts it to `Request.PathBase`; `UrlBaseMiddleware` (line 354) catches the case where the prefix was *not* present and 307-redirects. The frontend mappers `IndexHtmlMapper` and `LoginHtmlMapper` then re-stamp the prefix into the served HTML via `__URL_BASE__` substitution and the regex-driven `href=`/`src=` rewrite. Three different layers participate in the URL-base story; the Go port must port all three to match the existing behavior under reverse-proxy paths like `https://example.com/sonarr/`.
* **Cache-buster + 304 contract.** `CacheBreakerProvider` produces `?h={md5}` query strings on asset URLs in production; `CacheableSpecification.IsCacheable` looks for the `h` query key (line 22) to mark "always cacheable"; `CacheHeaderMiddleware` writes a one-year `Cache-Control` for cacheable responses; `IfModifiedMiddleware` 304s any cacheable request that carries `If-Modified-Since`. The four pieces work together — change one and the rest break. The Go port must keep the contract intact.
* **Static-field caches.** Several singletons cache config values into static fields and don't refresh on `ConfigSavedEvent`: `AuthenticationService.AUTH_METHOD` (cross-cite SEC-Q1), `InitializeJsonController._apiKey/_urlBase` (SH-Q8), `VersionMiddleware._version`. The Go port should consistently subscribe to the equivalent reload signal or read live, not partially.
* **Body buffering only on command paths.** `BufferingMiddleware` is the only place where `Request.Body` becomes seekable. `CommandController` reads the body twice (typed dispatch + raw log). Any future endpoint that needs to re-read its body must be added to the allowlist (or refactored to read once).
* **CORS + auth.** The `ApiCorsPolicy` is `AllowAnyOrigin / AllowAnyMethod / AllowAnyHeader`, which means the API key is the *only* access control. Browser-side cookie auth is unaffected because cookies are not sent cross-origin under `SameSite=Lax`. Cross-cite `authentication-security.md` SEC-Q11.
* **Mini-profiler reach.** MiniProfiler is wired in production builds too (it's gated by `_configFileProvider.ProfilerEnabled`, not `BuildInfo.IsDebug`). The `IndexHtmlMapper` only injects the profiler script when the config flag is on (line 52-67), but the `/profiler` route handler itself is always mounted (`Startup.cs:284`). The route requires policy `UI` for results submission (line 375). The Go port can either re-implement profiler or drop it entirely; the dependency on `IConfigFileProvider.ProfilerPosition` and `Theme` (`Startup.cs:265-279`) is the only contract it surfaces.
* **OpenAPI in debug only.** `app.UseSwagger` is gated by `BuildInfo.IsDebug` (line 364). Release builds have no `/docs/.../openapi.json` endpoint, no Swagger UI. The OpenAPI spec is generated by `AddSwaggerGen` from controller attributes (line 116-216) — the Go port can either generate OpenAPI from struct tags or commit a hand-maintained spec; either way, the existing C# spec is the source of truth for the V3 contract.

## Test files (the port must keep passing equivalents)

There are essentially **no** dedicated test fixtures for the Sonarr.Http surface. A grep of `src/NzbDrone.*Test*/` for `Sonarr.Http` types yields:

* `src/NzbDrone.Integration.Test/` — the integration test project hits the live HTTP server via real HTTP calls; it implicitly exercises `RestController`, the error pipeline, the validation envelope, the auth schemes, and most controllers. The fixtures are organised per-resource (e.g. `SeriesFixture`, `ConfigFixture`).
* `src/NzbDrone.Api.Test/` (legacy) and `src/Sonarr.Api.V3.Test/` — controller-level tests, but they typically mock the deeper services and skip the HTTP framing.
* No fixture exercises `SchemaBuilder.ToSchema` end-to-end against a known provider DI graph; no fixture covers `IfModifiedMiddleware` 304 path; no fixture covers `BufferingMiddleware`'s two-path allowlist; no fixture covers `UrlBaseMiddleware`'s 307 path; no fixture covers `CacheableSpecification`'s many branches; no fixture covers `ErrorModel`'s `Description` field being dropped on `ApiException`.

The Go port's test corpus must therefore be substantially richer than the C# baseline. Minimum bar (table-driven tests):

1. Route precedence — every route in `analysis/rest/inventory.md` reaches its expected controller; no two routes collide (port `DuplicateEndpointDetector`'s assertion).
2. Auth — every policy × every scheme × authenticated/unauthenticated × local/non-local + `DisabledForLocalAddresses` toggle.
3. Error envelope — every `ApiException` subclass produces the expected `{message, description, content}` (note `description` should be null when constructed from `ApiException`); `ValidationException` produces the FluentValidation array shape; `SQLiteException` constraint-failed becomes 409 only on PUT/POST.
4. Cache headers — every branch of `CacheableSpecification.IsCacheable`.
5. ClientSchema — round-trip a settings class with every `FieldType`, with privacy redaction, with select-options-from-enum, with select-options-from-`ISelectOptionsConverter`, and with `SelectOptionsProviderAction`.
6. URL base — `UsePathBase` strips the prefix, `UrlBaseMiddleware` 307s when the prefix is missing, all frontend mappers prepend the prefix correctly.
7. CORS — `ApiCorsPolicy` returns the right headers on preflight; `AllowGet` policy on `/content/...` rejects non-GET.
8. Version header — present on `/api/...`, absent elsewhere.
9. `StartingUpMiddleware` — 503 with text vs JSON body depending on `IsApiRequest`.
10. SignalR broadcast — `RestControllerWithSignalR<T,M>` emits the right `SignalRMessage` envelope on every `ModelEvent<TModel>`.

## Open questions

* **SH-Q1**  *Sonarr.Http* — `InvalidApiKeyException` (`src/Sonarr.Http/Exceptions/InvalidApiKeyException.cs:5`) is defined but never thrown in the tree. Is it dead code, or is there an out-of-tree consumer (plugin? legacy v2?) that still uses it? Status: unresolved. Blocks Phase 0.5 sign-off (delete or document).

* **SH-Q2**  *Sonarr.Http* — `SchemaBuilder._mappings` (line 19) is a process-lifetime cache keyed by `Type`. Hot-reloaded provider settings types would not refresh; in current C# this is impossible (no hot reload). The Go port should decide whether to keep the cache and accept the same constraint, or invalidate on a config event. Status: unresolved. Blocks Phase 1 (ClientSchema port).

* **SH-Q3**  *Sonarr.Http* — `LoggingMiddleware` (`Middleware/LoggingMiddleware.cs:22`) takes `SonarrErrorPipeline` in its constructor and stores it (`_errorHandler`, line 19) but never uses it. Dead field, or intended for a future "log before error pipeline" path? Status: unresolved. Blocks Phase 0.5 sign-off (delete or document).

* **SH-Q4**  *Sonarr.Http* — `IfModifiedMiddleware` (`Middleware/IfModifiedMiddleware.cs:23`) returns 304 on **any** `If-Modified-Since` value for cacheable resources, without comparing the actual modification time. Currently safe because of the `?h={md5}` cache-buster, but if a future code path serves a cacheable URL whose content can change without the URL changing, this is a stale-cache footgun. Should the Go port keep this behavior or implement actual mtime comparison? Status: unresolved. Blocks Phase 1 (HTTP middleware port).

* **SH-Q5**  *Sonarr.Http* — `StartingUpMiddleware` (`Middleware/StartingUpMiddleware.cs:27`) emits `{ "errorMessage": "..." }` on the API path — but the rest of the codebase uses `ErrorModel { message, description, content }`. Should the Go port unify these, or keep the inconsistency for client compatibility? Status: unresolved. Blocks Phase 1 (HTTP middleware port).

* **SH-Q6**  *Sonarr.Http* — `Bootstrap.cs:209` sets `Kestrel.Limits.MaxRequestBodySize = null` (no limit). Pure-Go equivalent is `http.MaxBytesReader` *not* applied. Should the Go port keep "unlimited" (matches C#) or set a configurable cap (e.g. for backup uploads)? Status: unresolved. Blocks Phase 1 (HTTP server port).

* **SH-Q7**  *Sonarr.Http* — `SchemaBuilder.GetValueConverter` (`ClientSchema/SchemaBuilder.cs:291-326`) accepts `IEnumerable<int>` / `IEnumerable<string>` as either a `JsonElement` array or a comma-separated string. The comma-separated form is presumably legacy v2-era. Is anyone still depending on it? If not, the Go port can drop it. Status: unresolved. Blocks Phase 1 (ClientSchema port).

* **SH-Q8**  *Sonarr.Http* — `InitializeJsonController` (`Frontend/InitializeJsonController.cs:19-20`) caches `_apiKey` and `_urlBase` in static fields filled by the constructor. Changing either at runtime requires a restart for the SPA to pick it up. The auth-card's `AUTH_METHOD` issue (SEC-Q1) is the same shape. The Go port should eliminate this pattern entirely. Status: unresolved. Blocks Phase 1.

* **SH-Q9**  *Sonarr.Http* — `initialize.json` hardcodes `apiRoot = {urlBase}/api/v3` (`Frontend/InitializeJsonController.cs:48`) even on v5 builds. Document that the SPA is still expected to talk to v3 routes, and that v5 endpoints are addressed directly. Or change to v5 once the SPA migrates. Status: unresolved. Blocks Phase 4 (frontend cutover).

* **SH-Q10**  *Sonarr.Http* — `SonarrErrorPipeline.HandleException` (`ErrorManagement/SonarrErrorPipeline.cs:52`) for `ValidationException` returns the bare `IList<ValidationFailure>`, **not** wrapped in `ErrorModel`. Every other branch returns `ErrorModel`. The shape difference is part of the V3 contract. Confirm the React UI relies on the bare-array shape (it does — `client/src/Settings/...` parses arrays from 400 responses). Status: resolved-ish; the inconsistency is intentional. **Document, don't fix.** No phase blocked.

* **SH-Q11**  *Sonarr.Http* — `SonarrErrorPipeline` validation path uses `STJson.ToJson(validationException.Errors)` then `response.WriteAsync(string)` (line 52). Every other path uses `STJson.SerializeAsync` to the response stream directly. Minor allocation hot-spot; nothing functional. Document and unify in the Go port. Status: unresolved. Blocks nothing.

* **SH-Q12**  *Sonarr.Http* — `RestControllerWithSignalR` namespace check at `REST/RestControllerWithSignalR.cs:79, 102` hardcodes `"V3"` / `"V5"` substrings. Future versions would silently not broadcast. Replace with a check on `VersionedApiControllerAttribute.Version != null`? Status: unresolved. Blocks Phase 1 (REST port).

* **SH-Q13**  *Sonarr.Http* — `ApiInfoController` (`ApiInfoController.cs:6`) lacks `[AllowAnonymous]`. With the default fallback policy (`Startup.cs:240-242`) this means `GET /api` requires the API key — but clients use `/api` to discover versions *before* knowing whether/how to auth. Confirm whether existing clients hit `/api` with an API key already (likely yes, because they're configured with one), or whether this is an actual bug. Status: unresolved. Blocks Phase 1 (HTTP port).

* **SH-Q14**  *Sonarr.Http* — `BackupFileMapper` (`Frontend/Mappers/BackupFileMapper.cs:20`) builds the served path via `resourceUrl.Replace("/backup/", "")` then `Path.Combine(BackupFolder, ...)`. The `BackupService.BackupFileRegex` provides some validation (the path must match a backup-filename pattern), but a regex match is not the same as path-traversal protection. Need to confirm the regex rejects `..` segments, or harden the Go port via `filepath.Clean` + prefix check. Status: unresolved. Blocks Phase 3 (security review).

* **SH-Q15**  *Sonarr.Http* — `STJson` settings include `WriteIndented = true` (`STJson.cs:31`) — every API response is pretty-printed. This adds non-trivial bytes to high-throughput endpoints (`/api/v3/queue`, `/api/v3/episode`). Should the Go port preserve this for client compatibility, or switch to compact and add a `?pretty=` flag? Status: unresolved. Blocks nothing critical, but a perf consideration.
