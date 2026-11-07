# Native management panel extension

This directory builds the official MIT-licensed management frontend from pinned
commit `953fb2444350c6c36052dda46cac0abce6b23ea6` of
[Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center).
The source archive SHA-256 is checked before any build steps run.

`native-settings.patch` adds the picker to Advanced → Default headers and updates
all seven locales. It also corrects an upstream HTML-escaping assertion and tests
it in English and Chinese, keeping Linux test order from breaking the build.
`oauth-socks.patch` adds mandatory account SOCKS setup before creating any OAuth
login, sends it in a POST body, and explains the separate browser proxy setting.
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
