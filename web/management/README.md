# Native management panel extension

This directory builds the official MIT-licensed management frontend from pinned
commit `953fb2444350c6c36052dda46cac0abce6b23ea6` of
[Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center).
The source archive SHA-256 is checked before any build steps run.

`native-settings.patch` adds the picker to Advanced → Default headers and updates
all seven locales. It also corrects an upstream HTML-escaping assertion and tests
it in English and Chinese, keeping Linux test order from breaking the build.
`oauth-socks.patch` adds mandatory account SOCKS setup before creating built-in OAuth
logins, keeps it optional for plugins, sends it in a POST body, and explains the separate browser proxy setting.
`kiro-json-import.patch` adds a credential import option for the Kiro multi-method
plugin, using its existing authenticated refresh-token login API.
`account-pools.patch` adds the Account pools navigation and route, with localized
pool definitions, client grants, account assignment and protocol base URLs.
`overlay/` contains the new React component, API adapter,
configuration mapping, styles, and regression tests. It uses the existing v8
management client, connection guards, controls, and configuration save flow. It
does not inject scripts into a downloaded release or edit generated HTML.

## Build and verify

Required: Node.js 24, Bun 1.4.3, bash, curl, git, and tar.

```sh
bash web/management/build.sh /tmp/cpa-management-source /tmp/cpa-panel/management.html
```

Use an empty source directory. The script installs frozen dependencies and runs
`bun run verify` (tests, lint, TypeScript, and the single-file Vite build). The
output directory receives `management.html` and the upstream `management-LICENSE.txt`.
The Dockerfile runs this same build and pins the panel with
`MANAGEMENT_BUNDLED_PATH`. The management-ui PR workflow also runs this script.

For an upstream refresh, update the revision and archive checksum in `build.sh`,
reapply/rebase the small patch, read the upstream repository guidance, and run the
script again. Keep generated HTML and node_modules out of this repository.

## Manual verification

Run a local CPA with temporary configuration, dummy keys, and a mock upstream.
Send versioned Claude Code and Codex request headers through the inference routes.
Verify collection, import preview, selection, the ordinary save confirmation,
upstream UA after save, restart persistence, and deletion. Verify that secrets and
bodies are absent from catalog responses and its YAML file. Also inspect the
layout at a narrow viewport and that changing connections clears old samples.

See [the user guide](../../docs/user-agent-settings.md) for usage and scope.
See [OAuth account proxies](../../docs/oauth-account-proxies.md) for login routing
and the separate browser, OAuth, and inference User-Agent behavior.
See [Account pools](../../docs/account-pools.md#web-management-panel) for the pool
setup sequence, restart requirement, live membership verification and limitations.

For account-pool UI verification, use an isolated CPA with dummy keys and a
temporary auth directory. Create a pool, save grants, move both an OAuth account
and a configured API key, and verify persisted and live membership. Change the
configuration concurrently to confirm a stale save is rejected and its draft
retained. Check connection changes, a narrow viewport, and explicit unassignment.
The tests cover source preservation, omitted grants, wildcard validation, ETags,
connection revisions (including ABA switches), missing ETags, and localized
disabled controls. Drafts and raw errors containing credentials are never stored
in browser persistence or rendered in error messages.

## Kiro credential JSON import

With the enabled [Kiro multi-method plugin](https://github.com/nguyenha935/kiro-cpa-plugin)
(`kiro-ha`), open **OAuth login → Other login methods → Kiro JSON import**.
Paste a single account object or an array of objects, preview the accounts, and
start the import. Each account gets its own OAuth session and result. The panel
only reports success after CPA confirms that the credential was saved.

```json
[
  {
    "accessToken": "optional-unused-example",
    "refreshToken": "example-refresh-token",
    "clientId": "example-client-id",
    "clientSecret": "example-client-secret"
  }
]
```

CamelCase and snake_case field names are supported. `accessToken` is not sent:
the plugin refreshes the account before CPA saves it. An entry can override
`region`, `authMethod`/`auth_method` (`builder-id` or `idc`), and
`startUrl`/`start_url`. Otherwise the selected account type and AWS region apply;
the initial defaults are AWS Builder ID and `us-east-1`. Use the account's actual
region. IAM Identity Center also requires its Start URL. Both client fields may
be omitted for supported Kiro desktop refresh tokens only.

Imports are limited to 50 accounts, 1 MiB of input, and 60 KiB per normalized
account. Duplicate refresh tokens and invalid entries are rejected during the
local preview. Failed accounts do not stop the rest of the batch. A lost save
response is shown as unconfirmed; inspect authentication files before retrying
that account. The importer never automatically retries a credential refresh.

This is separate from the plugin's Microsoft External IDP JSON format. No SOCKS
field is required, and plugin/global networking applies. Secrets are sent in
POST bodies, remain out of URLs, are never added to browser storage, and are
cleared from the form after preview and from the batch after completion/close.
The adapter pins calls and session cleanup to the original server and key.
Switching connections or leaving the page stops subsequent entries and cancels
the pending CPA session; already saved accounts remain saved.

The only non-v8 requests are to the plugin's existing, declared custom extension
routes `/v0/management/plugins/kiro/status` and `/v0/management/plugins/kiro/connect`.
No deprecated CPA management endpoint is changed or used as a fallback. Login
start, status/save, and cancellation use the v8 OAuth API.
