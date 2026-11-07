# v8 Management API

The base path is `/v8/management`. Configuration endpoints follow the structure in
[config.example.yaml](../config.example.yaml); operational endpoints are grouped
by function. The existing `/v0/management` API remains supported. Its reference is
available in the [management API guide](https://help.router-for.me/management/api).

## Access

The v8 endpoints use the same management key and remote-access policy as v0.
Send `Authorization: Bearer <management-key>` or `X-Management-Key: <management-key>`.
OAuth callbacks validate a pending login state and do not require a management-key
header. Home mode disables local management endpoints for both versions.

## Configuration endpoints

All paths below are relative to `/v8/management`.

| Path | Methods | Description |
| --- | --- | --- |
| `/config` | GET, PUT, PATCH | Read, replace, or merge the complete configuration as JSON. |
| `/config.yaml` | GET, PUT | Read or replace the complete configuration as YAML. |
| `/config/<section>/<field>` | GET, PUT, PATCH, DELETE | Read or modify a section or field using its path in the v8 tree. |

GET renders the persisted configuration in the v8 layout without migrating the
file or adding omitted runtime defaults. Successful v8 configuration writes save
the latest layout; reads and rejected writes leave the file unchanged.
Historical v8 paths and request bodies remain accepted as aliases.

JSON writes accept the value directly, without a `{ "value": ... }` envelope.
PUT replaces its target. PATCH merges objects and replaces lists and scalars.
Scalar field updates retain existing comments; complete replacements use the submitted document.
DELETE removes a field. Paths identify mapping keys, not array indexes; replace
an entire list to change its entries. Optional per-key `null` overrides inherit
the corresponding group value. Legacy field names are rejected by v8 writes.

| Example path | Value or purpose |
| --- | --- |
| `/config/access/api-keys` | Client authentication keys, for example `["client-key"]`. |
| `/config/api-keys` | All upstream provider groups. |
| `/config/api-keys/codex` | Codex upstream groups. |
| `/config/client/codex/optimize-multi-agent-v2` | Boolean, default `false`; applies to Codex clients across OAuth and API-key routes. |
| `/config/oauth/providers/codex/header-defaults/user-agent` | Final Codex OAuth/file-backed User-Agent, `"auto"`, or `""` for native behavior. |
| `/config/upstream/claude/header-defaults/user-agent` | Final shared Claude User-Agent, `"auto"`, or `""` for native behavior. |
| `/config/observability/logs/debug` | A boolean, for example `true`. |
| `/config/routing/retry/request-retry` | A number, for example `0`. |
| `/config/plugins/configs/<id>` | A plugin configuration object. |

For example, `PATCH /v8/management/config` with this body disables additional
request retries while preserving other settings:

```json
{
  "routing": {
    "retry": {
      "request-retry": 0
    }
  }
}
```

JSON reads omit TURN usernames and credentials under
`oauth.providers.codex.live-media-relay.ice-servers`; YAML reads include them.
The Home-owned revision fields
`credentials.concurrency.lifecycle-config-revision`,
`credentials.concurrency.observation-barrier-revision`, and `plugins.auth-revision`
cannot be changed through these endpoints.

### Native User-Agent settings

The management panel exposes both User-Agent defaults under **Configuration →
Advanced → Default headers**. To enable stable client version tracking without
plugins, send `PATCH /v8/management/config`:

```json
{
  "oauth": {"providers": {"codex": {"header-defaults": {"user-agent": "auto"}}}},
  "upstream": {"claude": {"header-defaults": {"user-agent": "auto"}}}
}
```

Explicit strings override native/model User-Agent rules. Credential-specific
`headers.User-Agent` takes precedence and also accepts `auto`. The Codex OAuth
default does not apply to API-key groups. Values containing control characters,
non-ASCII bytes, or more than 1024 bytes are rejected before saving. See
[User-Agent settings](user-agent-settings.md) for refresh behavior and deployment.

### Client User-Agent catalog

The following endpoints require the existing management authentication and are
unavailable in Home mode. They do not change active configuration:

| Method and path | Behavior |
| --- | --- |
| `GET /client-user-agents?provider=claude` | Returns `{ "items": [...], "limit": 100 }`; use `codex` for Codex. |
| `POST /client-user-agents?provider=claude` | Imports `{ "headers": "User-Agent: claude-cli/2.1.319\n..." }` and returns the sample. |
| `DELETE /client-user-agents/:id?provider=claude` | Deletes a sample; returns 204. |

Each sample has `id`, `provider`, `version`, whitelisted `headers`, `firstSeen`,
`lastSeen`, `count`, `observed`, and `imported`. Dates use RFC 3339 UTC. `count`
counts observed requests, not imports. Both source flags may be true. Lists are
ordered by last-recorded time, newest first. Invalid/mismatched client identities
return 422, invalid provider/body envelopes return 400, and persistence failures
return 500. Imports accept at most 32 KiB of raw headers in a 64 KiB JSON envelope.

Use the existing configuration save flow to apply a selected UA or supported
header group. See [client UA settings](user-agent-settings.md#pick-an-identity-from-your-clients)
for collection, whitelist, persistence, and scope details.

### Codex multi-agent configuration migration

`client.codex.optimize-multi-agent-v2` is the sole runtime setting. Loading older
YAML accepts `providers.codex.optimize-multi-agent-v2`,
`oauth.providers.codex.optimize-multi-agent-v2`, and the flat
`codex.optimize-multi-agent-v2` path. The client path always wins conflicts,
including explicit `false` or `null`. If it is absent, the historical OAuth path
wins over `providers`, which wins over the flat path.

These aliases now have client-wide semantics, not OAuth-only scope. Loading does
not rewrite legacy-only settings; normalization removes aliases conflicting with
the client path. A successful v8 write migrates the aliases while preserving their
comments. Historical API paths remain usable. Home configuration publishers
need matching client schema support before emitting the new path; older YAML
payloads remain readable.

## Operational endpoints

All paths below are relative to `/v8/management`. Request and response bodies
retain the corresponding business operation's fields.

| Path | Methods | Description |
| --- | --- | --- |
| `/config/upstream/<provider>` | GET, PUT, PATCH, DELETE | Manage shared provider settings. |
| `/server/latest-version` | GET | Get latest release information. |
| `/requests/api-call` | POST | Make an authenticated upstream call. |
| `/routing/cooldown/reset` | POST | Clear credential cooldown. |
| `/routing/model-definitions/<channel>` | GET | Get model definitions. |
| `/observability/logs` | GET, DELETE | Read or clear application logs. |
| `/observability/logs/errors` | GET | List error-log files. |
| `/observability/logs/errors/<name>` | GET | Download an error-log file. |
| `/observability/logs/requests/<id>` | GET | Get a request log. |
| `/observability/usage/api-keys` | GET | Get API-key usage. |
| `/observability/usage/queue` | GET | Get queued usage events. |
| `/credentials` | GET, POST, DELETE | List, upload, or delete credential files. |
| `/credentials/models` | GET | Get credential models. |
| `/credentials/download` | GET | Download a credential file. |
| `/credentials/status` | PATCH | Change credential status. |
| `/credentials/fields` | PATCH | Change credential fields. |
| `/credentials/refresh` | POST | Refresh credentials. |
| `/credentials/verify` | POST | Run an operator-triggered, account-pinned inference verification using an isolated session and minimal 1-token prompt. |
| `/oauth/import?provider=vertex` | POST | Import a Vertex service account using a multipart `file` upload. |
| `/oauth/auth-url` | POST; GET for plugins or targeted recovery | Start built-in or plugin OAuth with a required account SOCKS proxy for new built-in sessions; supports `name`/`auth_index` recovery and optional `verify`. |
| `/oauth/siwc/bind` | POST | Start SIWC binding to an existing file-backed Codex OAuth credential. |
| `/oauth/siwc/status` | GET | Read local SIWC credential status (`off`, `valid`, or `invalid`). |
| `/oauth/status?state=<state>` | GET | Get login status. |
| `/oauth/session?state=<state>` | DELETE | Cancel a login session. |
| `/oauth/callback` | GET, POST | Submit an OAuth callback. |
| `/plugins` | GET | List installed plugins. |
| `/plugins/<id>` | DELETE | Delete a plugin. |
| `/plugins/store` | GET | List the plugin store. |
| `/plugins/store/<id>/install` | POST | Install or update a plugin. |
| `/plugins/<id>/quota` | GET, POST, DELETE | Read, fetch, or reset plugin quota. |

## OAuth

The login endpoint is shared by all providers. Set the `provider` JSON field to
`claude`, `codex`, `antigravity`, `kimi`, `kimi-ai`, `xai`, `devin`,
`meta`, or a registered plugin provider ID. For example:

```http
POST /v8/management/oauth/auth-url
Authorization: Bearer <management-key>
Content-Type: application/json

{"provider":"codex","is_webui":true,"proxy_url":"socks5h://user:password@proxy.example.com:1080"}
```

New built-in account sessions require a valid SOCKS5/SOCKS5H `proxy_url` in the POST body;
query-string proxies, missing proxies, HTTP proxies, and direct mode are rejected.
Plugin login/import requests may omit `proxy_url` or pass an empty value. They
retain their existing plugin/global network settings. If a proxy is supplied,
it is validated as before and applies to that session. Plugin GET logins remain
supported. Unknown provider IDs still return `404 provider_not_found`.
The session proxy is used for provider requests and saved with the credential before
activation. This does not configure the user's browser proxy. See
[OAuth account proxies](oauth-account-proxies.md) for routing and User-Agent scope.

Provider-specific login parameters can also be passed in the POST body. To target recovery
at a specific existing credential, pass `name` (or `auth_index`) via query parameter
or POST JSON body. Targeted recovery inherits the saved account proxy when no
session proxy is provided; authenticating a different account will be rejected rather than
overwriting or clearing the target credential's recovery state. By default, post-login
inference verification is not executed automatically to avoid unexpected token
consumption; operators may explicitly opt in by passing `verify: true` (and optional
`verify_model`) when starting the targeted login, or trigger verification explicitly
after adoption via `POST /v8/management/credentials/verify` (`{"name":"<file>","model":"<optional-model>"}`).
The login response includes the authorization URL and a session `state` (plus `target_auth_id`
when targeted). Use that state for status queries and cancellation.

Callbacks accept `provider`, `state`, `code`, and `error` as GET query parameters
or POST JSON fields. POST also accepts `redirect_url` containing the complete
callback URL. If `provider` is omitted, it is inferred from the pending state;
an explicit provider must match that state. Poll until the status is `ok` or
`error`; `wait` means the login is still pending. Callback acceptance alone does
not mean credential exchange and persistence have completed.

Import uses `POST /v8/management/oauth/import?provider=vertex`. The `provider`
query parameter is required; Vertex is currently the supported import provider.

### Sign in with ChatGPT (SIWC) binding

SIWC is a second credential partition of an existing Codex credential, not a new
provider. This flow is available only in standalone CPA with local file-backed
Codex OAuth credentials. Home mode and non-file storage backends are not supported.

Start a binding with management authentication:

```http
POST /v8/management/oauth/siwc/bind
Authorization: Bearer <management-key>
Content-Type: application/json

{"name":"codex-example.json"}
```

`name` identifies an existing managed credential by its ID or file name; it is not
an arbitrary filesystem path. The credential must contain nonempty outer Codex
access and refresh tokens. A successful start returns:

```json
{
  "status": "ok",
  "url": "https://auth.openai.com/api/accounts/authorize?...",
  "state": "<random-state>",
  "flow": "siwc_bind",
  "expires_in": 300
}
```

Opening the returned URL starts authorization. Its redirect URI is fixed to
`http://127.0.0.1:1455/auth/callback`. This endpoint does not start a listener on
port 1455: for remote deployments, paste the complete URL from the browser address
bar, even if the loopback page cannot be reached, and submit it to the server:

```http
POST /v8/management/oauth/callback
Content-Type: application/json

{"provider":"codex","redirect_url":"http://127.0.0.1:1455/auth/callback?state=<state>&code=<code>&client_id=<issued-client-id>"}
```

The v8 callback also accepts `state`, `code`, and `client_id` directly as POST JSON
fields or GET query parameters. SIWC requires the **issued** `client_id` from the
callback; the authorization URL's `dynamic_agent_client` is not a substitute.
Error callbacks can supply `error` instead of `code` and `client_id`. Conflicting
parameters are rejected. The target, PKCE verifier, device ID, and redirect URI
are retained on the server and cannot be changed by a callback. Callback values
are handed directly to the SIWC session rather than written to legacy callback
files. Neither callbacks nor status responses return tokens.

Poll `GET /oauth/status?state=<state>` until `ok` or `error`. `wait` means binding
has not completed, including while tokens are being exchanged. Success means the
new `siwc` partition and `use_siwc=true` have been saved and published to the auth
manager. Other credential fields are retained. Rebinding explicitly replaces the
SIWC partition and reuses a valid existing device ID.

`DELETE /oauth/session?state=<state>` cancels a pending binding. Cancellation that
wins the commit gate prevents saving a late token response; cancellation after
commit returns `cancelled:false`. Duplicate callbacks and a second pending binding
for the same credential return `409`. Missing targets return `404`; ineligible
targets and incomplete callbacks return `400`. Deleting or replacing the target
through the v8 credential API invalidates its pending binding. An old server
returns `404` for the start endpoint; clients must not fall back to ordinary Codex
login. Sessions are process-local, expire after five minutes, and do not survive
restart; deployments with multiple independent instances require session affinity.

Query local SIWC status for a Codex credential with `GET /v8/management/oauth/siwc/status?name=<name>` (or `?auth_index=<auth_index>`). The endpoint reads local credentials and runtime rejection state without triggering refresh or upstream verification requests, and returns `{"status": "off" | "valid" | "invalid"}`.

For Codex entries, `GET /credentials` includes these non-secret summary fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `siwc_bind_supported` | boolean | This credential is eligible for the binding flow. Missing means unsupported. |
| `use_siwc` | boolean | Requests are configured to use the nested SIWC credential. |
| `siwc_status` | string | Local SIWC status: `off`, `valid`, or `invalid`. |
| `siwc_bound` | boolean | SIWC access token, refresh token, and issued client ID are present; not a live validity check. |
| `siwc_expires_at` | optional RFC3339 string | Stored SIWC expiration, when available. |
| `access_token_expires_at` | optional RFC3339 string | Active access token expiration deadline, when available. |
| `next_refresh_after` | optional RFC3339 string | Next scheduled automatic refresh check, when in the future. |
| `requires_sign_in` | boolean | Automatic renewal failed with a terminal grant rejection and independent sign-in is required. |
| `renewal_state` | string | Renewal lifecycle state: `sign_in_required`, `expired_sign_in_required`, `retry_scheduled`, `awaiting_first_renewal`, or `renewal_verified`. |
| `recovery_stage` | string | Post-login runtime adoption and verification stage: `sign_in_required`, `quota_blocked`, `loaded`, `inference_verified`, or `renewal_verified`. |
| `adopted` | boolean | Whether the current credential version is loaded in the running auth manager. |
| `inference_verified` | boolean | Whether the current credential version has completed an inference response. |
| `renewal_verified` | boolean | Whether the current credential version has completed an automatic refresh cycle. |
| `refresh_error` | optional object | Sanitized last refresh failure (`code`, `message`, `http_status`) with no tokens, codes, or URLs. |
| `next_action` | optional string | Required operator recovery action (`sign_in` when `requires_sign_in` is true). |
| `recovery_hint` | optional string | Operator guidance explaining instance-scoped OAuth recovery when `requires_sign_in` is true. |
| `renewal_event` | optional object | Deduplicated structured event (`renewal_sign_in_required`) for terminal renewal failures. |

The v0 callback and credential-list contracts are unchanged. SIWC activation,
unbinding, and standalone SIWC credential creation are not separate management
operations in this release.

## v0 compatibility

The `/v0/management` endpoints keep their original paths and payloads, including
provider-specific OAuth login URLs, callbacks, status queries, and cancellation.
New built-in v8 account sessions require a SOCKS proxy in the POST body. The deprecated
v0 login routes retain their original behavior.

Legacy flat configuration endpoints such as `/debug`, `/request-retry`, and
`/codex-api-key` exist under `/v0/management` only.
`/v0/management/api-keys` continues to manage client authentication keys; the v8
equivalent is `/v8/management/config/access/api-keys`.

Legacy-only configuration files keep their layout until a successful v8
configuration write. When both layouts specify a field, the new field takes
precedence and its legacy equivalent is removed. V0 setters keep legacy-only
files in their original layout; existing v8 files are saved in the latest v8
layout. `PUT /v0/management/config.yaml` still replaces the complete file and
accepts legacy, new, or mixed layouts, normalizing v8 documents on save.

Plugin OAuth uses the shared v8 login endpoint. Other plugin-defined HTTP
extensions retain their declared `/v0/management` routes.
