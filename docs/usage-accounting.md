# Account token values

Standalone CPA can persist each observed upstream execution, attribute it to an
upstream account, and estimate its output-token value in USD. The separate
`total_usd` field includes both input and output. These are API base-price
estimates, not subscription spending, remaining quota, or an upstream invoice.

## Enable collection

```yaml
observability:
  usage:
    accounting:
      enabled: true
      # Default: <auth-dir>/usage.db
      path: /persistent-data/usage.db
      detail-retention-days: 30
      pricing:
        source-url: https://basellm.github.io/llm-metadata/api/newapi/ratio_config-v1-base.json
        refresh-interval: 24h
```

This switch is independent of `usage-statistics-enabled`, request logging, and
the transient usage queue. Collection starts when enabled; old requests cannot
be recovered from the queue or reconstructed from missing response usage.
Only observed upstream attempts are counted. Requests rejected before an
upstream execution do not create an account usage event.

Configure the existing management key, then open `/usage.html` on the CPA server.
The embedded dashboard shows accounts, output and total values, coverage gaps,
daily model totals, and retained execution details. Enter the management key on
the page; it stays in page memory and is sent to this server only. The ordinary
management remote-access policy applies to all data requests. The HTML shell
contains no account data and needs no separate frontend deployment.

Settings reload with the server configuration. Changing the database path
requires a restart. Disabling collection keeps existing history readable. Use a
local persistent volume with one process per ledger file; the ledger is separate
from the general-purpose KV store and is not replicated by credential storage
backends. In Home mode, local collection is disabled and the existing Home usage
forwarding protocol is unchanged; query accounting in the Home control plane.

## Account identity

`account_uid` is a persistent, opaque ledger ID for an upstream account. It is
different from the client API key and from `auth_index`, which identifies a
credential. Identity resolution uses, in order:

1. An explicit `usage_account_id` credential metadata/attribute.
2. The provider's `account_id`, `user_id`, or `sub`, scoped by organization and
   project when available.
3. An upstream API key and base URL, hashed before recording.
4. The credential ID and email, hashed before recording.

The provider always scopes the identity. Equal emails alone do not merge
accounts. OAuth token refresh does not change an available principal identity;
fallback credential IDs can change on rename, and API-key rotation can change a
fallback identity. For those cases, configure stable bindings **before** the
replacement first serves requests:

```yaml
observability:
  usage:
    accounting:
      enabled: true
      account-bindings:
        "old-auth-index": my-account
        "replacement-auth-index": my-account
```

Keys can be an `auth_index` or auth ID; the index binding takes precedence.
Bindings override automatic identity, within the same provider. They apply to
new events and do not merge or rewrite already-recorded accounts. CPA does not
modify credential files to assign an identity. Labels use the credential label
or filename and may contain account names; protect ledger backups accordingly.

## Prices and token semantics

The built-in catalog is a **2026-11-09** snapshot of the linked BaseLLM NewAPI v1
configuration. It works without a network request. Enabled collection refreshes
prices every 24 hours by default (minimum interval: one minute); use the dashboard
or refresh endpoint for an immediate refresh. Invalid data, unsupported catalogs,
and fetch failures retain the previous snapshot and appear in status. HTTP(S)
sources must provide `data.billing_expr` expressions; legacy ratio-only catalogs
are not accepted.

Each event captures a content-hashed price version when committed. Later price
updates affect new events only. Decimal arithmetic is exact; persisted USD
values are decimal strings rounded to 18 fractional places per event and summed
without binary floating-point arithmetic. The dashboard rounds amounts for
display; API responses retain the stored precision.

Pricing selects the response model, then the final upstream model, then the
requested model. An exact `model-aliases` mapping can resolve provider-specific
names to catalog keys. No fuzzy model guessing or currency conversion occurs.
Changes to aliases affect new events only.

Canonical token buckets avoid counting cache or reasoning tokens twice.
Reasoning is included in output value. Cache reads and writes use distinct rates
when present in the expression; 1-hour and 12-hour writes use their corresponding
buckets. Input-length tiers, request timestamps/time zones, and allowlisted final
request parameters such as `enable_thinking` remain available when calculating
the output component.

This release prices text token usage. Known audio and generated-image usage is
recorded as unpriced. Unknown models, unsupported expressions, missing required
parameters, and inconsistent token breakdowns also remain unpriced, with a
reason. Missing usage is distinct from an explicit zero-token response. Failed
streams with observed usage contribute a `partial` estimate; their unobserved
remainder cannot be valued. Unpriced events add no estimated dollars and are
counted separately as coverage gaps. Discounts, additional fees, taxes, and
provider-reported charges not represented by the imported expression are not
included. Fixed fees in custom expressions contribute to total value, not output
token value.

## Durability and retention

The request-scoped recorder commits a sanitized event, account identity,
permanent execution-ID deduplication marker, and pending projection marker in one
bbolt transaction **before** notifying the asynchronous usage plugin bus. A
worker then atomically values the event and updates account/daily totals. Pending
events replay after restart. The dashboard's pending count explains short delays
between a completed execution and its projected totals.

An execution ID identifies one upstream attempt. Retries are separate attempts;
repeated delivery of the same execution is counted once. Additional-model usage
has its own event ID and a `parent_execution_id`: its usage/value is added without
counting another parent execution. `trace_id` is only a correlation field.

Request details are pruned hourly after the retention period (30 days by default;
zero also selects the default). Account/daily totals, price snapshots, and dedup
markers remain for lifetime statistics. They continue to grow, and deleting
details does not shrink the allocated bbolt file. Back up the ledger while the
server is stopped or with a consistent bbolt snapshot; deleting it resets all
local history.

No client API keys, OAuth tokens, token fingerprints, request bodies, response
bodies, or response headers are stored in the ledger. Storage failures are logged
and reported in health status while proxy requests continue. A failed event
commit cannot be reconstructed automatically. A process killed before publishing
upstream usage cannot record that usage. Check `last_error`, `price_error`, and
`pending_events` when assessing coverage.

## Management API and SDK

All paths below require the existing management authentication and are relative
to `/v8/management/observability/usage`:

| Method and path | Result |
| --- | --- |
| `GET /status` | Collection availability, ledger start time, pending count, price version/source/date, and errors. |
| `GET /accounts` | Paginated accounts and totals. |
| `GET /accounts/:id` | Account totals, daily/model breakdowns. |
| `GET /accounts/:id/requests` | Paginated retained events, newest first. |
| `POST /pricing/refresh` | Refresh the catalog and return status. |

Account queries accept `from` and `to` as inclusive UTC `YYYY-MM-DD` dates, an
exact pricing `model`, and a `provider` filter on the account list. Account sorting
is descending `output_usd` (default), `total_usd`, `output_tokens`, or `last_seen`.
Lists accept `offset` (default 0, maximum 1,000,000) and `limit` (default 50,
maximum 200). Account detail returns all matching daily/model rows; pagination
applies to account and request lists. Dates and model filters use retained daily
totals, so aggregate queries continue to work after request details expire.
Unknown accounts return 404; an unopened ledger returns 503. Responses containing
account data are marked `Cache-Control: no-store`.

Embedders can attach a `sdk/cliproxy/usage.Recorder` with `usage.WithRecorder` to
each execution context. The built-in HTTP server attaches its own runtime;
accounting is not registered as a process-global plugin. Custom SDK publishers
should supply stable execution IDs, account identity, and canonical token
breakdowns. Set `Detail.Reported` for explicit zero-token usage and use
`Generate: usage.GenerateFlag(false)` to exclude diagnostic/token-count events.
