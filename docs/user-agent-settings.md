# Claude Code, Codex and Gemini User-Agent settings

Open **UA control panel** in the management sidebar (`#/user-agents`). Claude Code,
Codex and Gemini each have a view for configuration, runtime defaults, client samples
and identity rules. **Configuration → Advanced → Default headers** now links to
this dedicated panel. The Docker image includes it. After you save, CPA reloads
the configuration in the background. New requests use the updated settings once
that reload completes. Existing WebSocket connections retain their handshake
until reconnected.

| Value | Behavior |
| --- | --- |
| Full User-Agent string | Use that exact identity after native, cloaking, and model header rules. Leading/trailing spaces are trimmed. For a Claude Code UA, comment fields the client sends that the setting does not name, such as `workload/cron`, `request-phase/background`, or `agent-sdk/<version>`, are appended after the configured identity. |
| `auto` | Update the client version from the official npm package's stable `latest` release. |
| Empty | Keep CPA's existing native identity behavior. |

## Pick an identity from your clients

1. Connect your client to CPA and send a request using a valid CPA access key.
   For Gemini CLI in API key mode, set `GOOGLE_GEMINI_BASE_URL` to the CPA base URL
   (for example, `http://localhost:8317`) and `GEMINI_API_KEY` to a CPA access key.
   Open **UA control panel**, select the provider, and click **Refresh status** or
   **Refresh list** to load its samples. If an earlier build missed a request, send a
   new one through the updated CPA instance and refresh the list again.
2. Filter by source or search UA, SDK, Node and platform values. Select a sample.
   The preview shows every supported header, sample ID, client or SDK version,
   source, first/last recorded times and observed request count. Headers can be
   copied. **Use this UA** fills only the UA field.
   **Use UA and captured fields** also fills the supported fields in that sample.
   Missing fields, timezone, and device stabilization settings stay unchanged.
3. Use **Review and save → Save changes** to review old and new values for the edited
   providers. Only edited fields are patched, with an ETag guard against concurrent
   configuration changes. A rejected save keeps the draft. Collection,
   importing, and selecting a sample never automatically change active settings.

Alternatively, click **Import request headers**, paste a header block or raw HTTP
request (up to 32 KiB), and preview it. The frontend removes unrelated headers and
the request body before sending it. The backend independently validates and
filters the import. Copying a cURL command or HAR JSON is not supported; copy the
raw request headers instead.

```http
User-Agent: claude-cli/2.1.319 (external, cli)
X-Stainless-Package-Version: 0.74.0
X-Stainless-Runtime-Version: v24.3.0
X-Stainless-Os: Linux
X-Stainless-Arch: arm64
X-Stainless-Timeout: 600
```

This is an illustrative sample, not a certified version bundle. Use the exact
values from your own client. For Codex, `User-Agent` and optional
`X-Codex-Beta-Features` are supported. For Claude, the six headers shown above are
supported. The list recognizes versioned `claude-cli`, `codex-tui`, `codex_cli_rs`,
`codex_exec`, `codex_vscode`, `codex`, and `Codex Desktop` identities.

Gemini samples store only the incoming `User-Agent`. A terminal request can
send `GeminiCLI-tui/0.70.0/gemini-2.5-flash (darwin; arm64; terminal)`. Older
`GeminiCLI/` identities, CLI command, ACP and A2A variants are also recognized.
In VS Code, the CLI can send `CloudCodeVSCode/` with `proxy_client=geminicli`.
If Google's auth library appends `google-api-nodejs-client/<version>`, the sample
keeps that suffix. The catalog displays the CLI version from the start of the UA.

In Gemini API key mode, Antigravity CLI (`agy` 1.5.15) sent a User-Agent starting
with `google-genai-sdk/1.79.1 gl-go/`. The Gemini picker displays this sample as
**Google Gen AI Go SDK** with SDK version `1.79.1`. Other Go applications can send
the same header. To route `agy` through CPA,
set `modelProvider` to `gemini` in `~/.gemini/antigravity-cli/settings.json`,
set `GOOGLE_GEMINI_BASE_URL` to the CPA base URL, and use a CPA access key for
`GEMINI_API_KEY`. See the [Antigravity authentication guide](https://antigravity.google/docs/install).

CPA observes the incoming headers after client authentication on the `/v1` and `/v1beta`
model APIs, `/backend-api/codex`, `/v1/realtime`, `/openai/v1/videos` and `/muse-code`,
including supported account-pool routes and response WebSocket handshakes. The client
identity comes from the inbound request. On refresh, the Gemini picker shows how many
authenticated requests the collector inspected across all providers since startup and
how many had a missing, invalid or unrecognized identity. Refresh before and after
sending one request in a quiet period. If both counts increase, the request reached
the collector with an unrecognized identity. If the inspected count stays the same,
check the client's base URL and CPA access key and refresh the same server instance.
The UA catalog never stores credentials, cookies, IPs, or request bodies. It
deduplicates identical supported header groups and keeps the latest 100 samples
per provider. **Delete sample** removes the catalog entry, not a saved setting;
another matching request can add it again.

The catalog is saved to `.client-user-agents.yaml` in the auth directory selected
at startup. Keep that directory on persistent writable storage for Docker. The
request path only updates memory; a background worker coalesces disk writes and
shutdown flushes pending changes. Abrupt termination can lose the latest pending
observations. With an empty auth directory it is memory-only. The standalone
catalog and panel are disabled in CLIProxyAPIHome-managed mode.

The picker also offers **Auto** and **Default**. Choosing a recorded sample pins
that exact UA; it does not switch to a newer sample automatically. Updating your
client and sending another request makes its new identity available in the list.

Credential `headers.User-Agent` takes precedence over the provider default. For a
plain Claude API key, a fixed Claude Code UA or `auto` keeps request-specific fields
such as `workload/cron`. An explicit `$Header-Name` mapping uses the complete value
of that header. A missing or invalid mapping falls back to the provider default or
the native UA. Header names are case insensitive. Clearing a provider default does
not clear a credential override.

## Inspect runtime state and identity rules

The panel distinguishes saved runtime state from the editable draft. It displays
the built-in UA, resolved provider default, SDK/Node/platform baseline, Claude
billing version, cached release version, last lookup attempt/success/result,
lookup interval and catalog persistence mode. Refresh reads this state; it does
not force a registry query. Lookup timestamps and release caches are process-local.
The copyable report contains this runtime metadata, not configuration secrets.

Loaded credential UA overrides are listed read-only with source, display label,
auth index and disabled state. They remain editable at their credential or API-key
configuration source. No tokens, account identifiers, raw credential metadata or
credential file paths are included in the status response.

Claude controls include SDK and Node versions, OS, architecture, the declared SDK
timeout header, timezone, device stabilization, diagnostics, request-ID policy,
additional native entrypoints, and Beta removal/addition. Codex also exposes its
WebSocket Beta-feature fallback. The expandable reference explains authentication,
CLI/SDK markers, Betas, session/request/agent identifiers, remote-environment and
additional-protection headers, native request hints, body identity and transport.

These values describe provider defaults and construction rules, not an actual
outbound request. Credential selection, caller headers and request shape can
change the final result. In particular, automatic UA updates do not synchronize
SDK/Node, billing or the TLS template; the panel flags a differing Claude UA and
billing version. OAuth login/refresh uses a separate control-plane identity.

## Scope

| Setting | Applies to |
| --- | --- |
| `oauth.providers.codex.header-defaults.user-agent` | Codex OAuth/file-backed credentials, including HTTP, streaming, compact, image, and WebSocket requests. |
| `upstream.claude.header-defaults.user-agent` | Claude OAuth and API-key credentials, including streaming and upstream token counting. |
| `upstream.gemini.header-defaults.user-agent` | Native Gemini, Interactions, native Vertex and Gemini CLI OAuth inference, including streaming and upstream token counting. |
| `api-keys.codex[].headers.User-Agent` or a key's `headers.User-Agent` | That Codex API-key group or credential. The OAuth default does not apply here. |
| `api-keys.claude[].headers.User-Agent` or a key's `headers.User-Agent` | That Claude API-key group or credential, overriding the shared default. |
| `headers.User-Agent` on a Gemini, Interactions or Vertex API-key group or key, or a Gemini CLI credential | That credential's UA on the Gemini inference routes above. |

Existing credential-file `header:User-Agent` attributes are also respected.
Provider defaults and configured API-key headers accept `auto`; automatic lookups
are enabled by those configuration fields, not by credential-file attributes alone.

### Gemini modes

**Default** leaves native Gemini, Interactions and Vertex requests to Go's HTTP
transport. It sends `Go-http-client/1.1` over HTTP/1.1 and `Go-http-client/2.0`
over HTTP/2. The optional Gemini CLI OAuth plugin supplies its own default.
Vertex OpenAI compatibility, AI Studio WebSocket relay and Antigravity use their
respective channel identities.

**Fixed** sends the configured UA on each covered request. **Auto** builds
`GeminiCLI/<version>/<model> (<os>; <arch>; terminal)`. The model is the upstream
model for that request, or `unknown` when unavailable. For a raw SDK Interactions
request, CPA can read the model from the first 64 KiB of a replayable body.
The OS and architecture come from the running proxy, so a Linux amd64 container
reports `linux; x64`. The initial version is `0.34.0` until a newer stable release
is cached.
The setting controls the `User-Agent` header. The channel supplies any separate
`X-Goog-Api-Client` header.

Use a single spelling of `User-Agent` per credential. Gemini API-key configuration
rejects conflicting values under different spellings. For existing credential
files, the canonical `User-Agent` spelling takes priority.

### Example v8 configuration

```yaml
config-version: 8
oauth:
  providers:
    codex:
      header-defaults:
        user-agent: auto
upstream:
  claude:
    header-defaults:
      user-agent: auto
  gemini:
    header-defaults:
      user-agent: auto
```

Merge these fields into the existing configuration rather than replacing the
complete file. The [v8 management API](management-api-v8.md#native-user-agent-settings)
supports the same fields.

## Automatic updates

For each provider in auto mode, CPA queries the npm `latest` endpoint for
`@openai/codex`, `@anthropic-ai/claude-code` or `@google/gemini-cli`.
Lookups happen on startup, when relevant auto/proxy settings change, and every
three hours. Unrelated settings saves do not cause another lookup. The configured
global proxy is used; provider credentials, cookies, and inference payloads are
never included. With auto disabled, this updater makes no network requests.

Only stable numeric `major.minor.patch` versions are accepted. Auto never
downgrades a recognized client version, preserves recognized platform/entrypoint
text, and updates repeated Codex version suffixes. An unrecognized native UA uses
CPA's built-in client identity. This changes only the User-Agent header; SDK,
Node.js, OS/architecture headers, authentication, and request payloads retain their
existing rules. It does not measure or certify a complete new client fingerprint.

Inference requests read an in-memory version cache and never wait for npm. Failed
lookups retain the cached version; after a restart the built-in baseline is used
until a lookup succeeds. A fixed manual UA needs no network lookup. Automatic
lookups are disabled in CLIProxyAPIHome-managed mode.

## Linux, Docker, and Coolify

Deploy the CPA build containing this change through the existing Dockerfile or
Linux build process. There are no plugin binaries, extra ports, or architecture
specific installation steps. Keep `config.yaml` on persistent writable storage
so settings saved in the panel survive container replacement.

Docker builds the panel from a pinned upstream source revision plus the native
extension under `web/management/`. `MANAGEMENT_BUNDLED_PATH` points to the bundled
HTML and prevents the panel updater from replacing it with an upstream panel
without the picker. Updating this panel requires deploying a new image. An
explicit `MANAGEMENT_STATIC_PATH` takes precedence and retains its existing
update behavior; remove that override to use the bundled picker.

For a Linux binary installation, install Node.js 24, Bun 1.4.3, curl, git, and tar,
then build and install the panel alongside CPA:

```sh
bash web/management/build.sh /tmp/cpa-management-source /opt/cpa/management.html
export MANAGEMENT_BUNDLED_PATH=/opt/cpa/management.html
./cli-proxy-api --config /opt/cpa/config.yaml
```

The source build directory must be empty. See
[`web/management/README.md`](../web/management/README.md) for maintenance details.

For the `Nanako1900/CLIProxyAPI` deployment, the PR targets `coolify`. Merging into
the branch watched by Coolify triggers deployment when its automatic deployment
is enabled; creating the PR alone does not prove a production deployment occurred.
After deployment, open the provider's tab, enter `auto` or a full UA, and save. Codex
API-key groups use their own custom `User-Agent` header as described above.

To stop tracking releases, replace `auto` with a fixed UA or clear it and save.
To roll back the implementation, redeploy the prior image and clear any `auto`
values first, because older CPA builds may send that text as a literal header.
