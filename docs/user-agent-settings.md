# Codex and Claude Code User-Agent settings

Use the existing management panel's **Configuration → Advanced → Default headers**
fields. No plugin installation or frontend replacement is required. Save the
configuration to apply changes to subsequent upstream requests without restarting
CPA. Existing WebSocket connections retain their handshake until reconnected.

| Value | Behavior |
| --- | --- |
| Full User-Agent string | Use that exact identity after native, cloaking, and model header rules. Leading/trailing spaces are trimmed. |
| `auto` | Update the client version from the official npm package's stable `latest` release. |
| Empty | Keep CPA's existing native identity behavior. |

Credential `headers.User-Agent` takes precedence over the provider default. Header
names are case insensitive. Existing `$Header-Name` credential mappings continue
to work. Clearing a provider default does not clear a credential override.

## Scope

| Setting | Applies to |
| --- | --- |
| `oauth.providers.codex.header-defaults.user-agent` | Codex OAuth/file-backed credentials, including HTTP, streaming, compact, image, and WebSocket requests. |
| `upstream.claude.header-defaults.user-agent` | Claude OAuth and API-key credentials, including streaming and upstream token counting. |
| `api-keys.codex[].headers.User-Agent` or a key's `headers.User-Agent` | That Codex API-key group or credential. The OAuth default does not apply here. |
| `api-keys.claude[].headers.User-Agent` or a key's `headers.User-Agent` | That Claude API-key group or credential, overriding the shared default. |

Existing credential-file `header:User-Agent` attributes are also respected.
Provider defaults and configured API-key headers accept `auto`; automatic lookups
are enabled by those configuration fields, not by credential-file attributes alone.

Example v8 configuration:

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
```

Merge these fields into the existing configuration rather than replacing the
complete file. The [v8 management API](management-api-v8.md#native-user-agent-settings)
supports the same fields.

## Automatic updates

Auto mode queries `https://registry.npmjs.org/@openai/codex/latest` and/or
`https://registry.npmjs.org/@anthropic-ai/claude-code/latest` for enabled providers.
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

For the `Nanako1900/CLIProxyAPI` deployment, the PR targets `coolify`. Merging into
the branch watched by Coolify triggers deployment when its automatic deployment
is enabled; creating the PR alone does not prove a production deployment occurred.
After deployment, enter `auto` or a full UA in the two fields and save. Codex
API-key groups use their own custom `User-Agent` header as described above.

To stop tracking releases, replace `auto` with a fixed UA or clear it and save.
To roll back the implementation, redeploy the prior image and clear any `auto`
values first, because older CPA builds may send that text as a literal header.
