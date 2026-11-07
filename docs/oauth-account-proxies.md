# OAuth account proxies

In the bundled management panel, open OAuth and select a provider. Paste a
`socks5://` or `socks5h://` URL with a port, then select **Create login with SOCKS**.
For example, `socks5h://username:password@proxy.example.com:1080`. Percent-encode
reserved characters in the username or password. CPA must be able to reach the
proxy; in Docker, `127.0.0.1` refers to the container itself.

The field is required for new built-in OAuth logins only. Empty values,
HTTP proxies, direct mode, invalid ports, and URLs with paths or query parameters
are rejected before starting a session. Validation checks the URL format; the
actual connection happens when the provider flow makes an outbound request.
The panel sends the address in a POST body, masks it in the input, and does not
save it in browser storage. Starting another account returns to an empty field.

Third-party plugins such as Kiro can start login or import credentials without
`proxy_url`, including through their own plugin pages. The common OAuth dialog
also makes SOCKS optional for plugins. Leaving it empty preserves the plugin and
global network settings; an explicitly supplied proxy is still validated and
saved as a session/account override. Plugin GET login routes remain supported.

For authorization-code flows such as Codex and Claude, CPA constructs the login
link locally. It does not fetch the link from the provider using any IP address.
The selected SOCKS route is attached to the OAuth session before that link is
generated. CPA uses it for token exchange and provider account discovery. Device
flows also use it for their initial device-code request and token polling.
Plugins receive the session proxy and the corresponding host HTTP client; a
third-party plugin that bypasses that client controls its own network traffic.

After successful login, CPA saves `proxy_url` in the credential before activation.
Account loading restores it after restart. Normal inference and token refresh use
the account proxy before the global proxy. Explicit SDK execution proxy overrides
still retain their existing higher priority for inference. Proxy connection or
authentication failures fail the request rather than retrying directly.

Targeted reauthentication inherits the existing account proxy unless an explicit
session proxy is supplied. SIWC binding also uses the selected Codex account's
saved proxy. Existing credentials with HTTP proxies, explicit direct mode, or no
proxy keep their previous routing policy; the new SOCKS requirement applies to
new built-in account sessions in the v8 management API. CLI logins and the deprecated v0
API are unchanged.

## Browser login and User-Agent

The browser that opens the authorization page makes its own requests. CPA cannot
set that browser's proxy or User-Agent. Configure the browser separately to use
the same exit IP when that is required. A rotating proxy address does not guarantee
that two connections use the same IP. A loopback callback address also does not
make the browser inherit CPA's network settings.

OAuth and inference are separate HTTP clients:

| Stage | Current User-Agent behavior |
| --- | --- |
| Browser authorization | Set by the browser. |
| Codex token exchange, refresh, and SIWC | No explicit User-Agent; the standard Go HTTP transport supplies its default. |
| Claude token exchange, refresh, profile, and roles | `axios/1.15.2`, the existing OAuth control-plane profile. |
| Model requests | Existing provider/credential User-Agent settings. |

An OAuth User-Agent differing from the inference User-Agent is not by itself a
bug: clients can use separate HTTP libraries for these endpoints. This change
does not copy an inference UA into OAuth requests without evidence that it is
appropriate for the token endpoint. A shared SOCKS proxy neither changes these
headers nor guarantees any provider's acceptance or risk assessment.
