# Quota Shadow Mode

Quota shadow mode evaluates a quota-aware credential scheduling policy next to the
configured routing strategy and records both decisions. It never changes routing:
the credential chosen by the active strategy is always the one used.

Use it to answer, with your own traffic, whether a quota-aware policy would serve
more requests than round-robin, fill-first, or weighted round-robin before enabling
such a policy.

## Enabling

```yaml
routing:
  quota-shadow:
    enabled: true
    file: ""                 # default: <log directory>/quota-shadow.jsonl
    max-file-mb: 256         # rotate to <file>.1 past this size
    soft-limit-percent: 90   # usage at which the shadow policy avoids a credential
```

The setting is hot-reloaded. Records are written by a background goroutine; when its
queue is full, records are dropped and counted in the `dropped` field of later records.
Rotation keeps a single previous file and overwrites an older `<file>.1`.

When disabled, the selection path only performs one atomic load. When enabled, each
pick additionally scans the credential list once under a read lock and may derive the
canonical session ID from the request; scoring and serialization run off the request path.

Selections made by CLIProxyAPIHome (Home mode) are not recorded.

## Data sources

The shadow policy reads the passive quota snapshot that every completed upstream
response already stores on the credential (`Quota.Signals`):

| Provider | Windows |
|---|---|
| Claude | `anthropic-ratelimit-unified-<window>-{utilization,reset,status}`: `5h`, `7d`, and model-specific windows such as `7d_oi` |
| Codex | `x-codex-[<limit>-]{primary,secondary}-{used-percent,window-minutes,reset-at,reset-after-seconds}` |
| Devin | `{daily,weekly}_quota_{remaining_percent,reset_at}` |

Windows without a scope (`5h`, `7d`, Codex `primary`/`secondary`, Devin `daily`/`weekly`)
apply to the whole credential and are scored. Scoped windows (model-specific Claude
windows, Codex additional limits) are recorded but not scored, because the models they
cover are not known. Codex windows are classified as short or long by `window-minutes`,
not by slot name.

A window whose reset time has passed is treated as fully replenished. A window the
provider reports as `rejected` is treated as exhausted. The credential-wide Codex
`x-codex-limit-reached` flag marks both credential-wide windows `limit_reached`, which
soft-limits the credential without changing the recorded usage. Reset timestamps are
accepted as Unix seconds, Unix milliseconds, or RFC3339.

## Shadow policy

For every candidate in the highest available priority tier (the same filter the real
selection applies), the policy derives:

- `soft_limited`: some credential-wide window is at or above `soft-limit-percent`, or rejected.
- `perish_long` / `perish_short`: share of the longest/shortest window that would expire
  unused even if the credential were consumed at full pace (one full window per window
  length) from now until the reset: `max(0, remaining - time_until_reset / window_length)`.
- `short_active`: the short window is running with recorded usage.
- `long_remaining`: remaining share of the long window.

Candidates are ranked lexicographically, and `shadow_reason` names the first key that
separated the winner from the runner-up:

1. `soft_limit`: not soft-limited first.
2. `perish_long`: more weekly quota that would otherwise expire.
3. `perish_short`: more short-window quota that would otherwise expire.
4. `short_idle`: no running short window first, which keeps windows cycling and spreads load.
5. `long_remaining`: more remaining weekly quota.
6. `id`: credential ID, for determinism.

Floating-point keys closer than 0.01 are treated as equal.

## Record schema

One JSON object per line. Times are Unix milliseconds.

### `selection`

Written after every successful credential pick, including failover attempts.

| Field | Meaning |
|---|---|
| `ts_ms` | Selection time |
| `model`, `providers` | Route model and candidate providers |
| `session` | Short SHA-256 digest of the canonical session ID, when present |
| `attempt` | Credentials already tried for this request (0 for the first pick) |
| `actual`, `actual_provider` | Credential chosen by the active strategy |
| `shadow`, `shadow_reason` | Credential the shadow policy would choose, and why |
| `agree` | `actual == shadow` |
| `actual_in_pool` | Whether the real pick was among the scored candidates. Session affinity can bind across priority tiers and plugin schedulers apply their own filters, so `agree=false` with `actual_in_pool=false` is a structural difference, not a policy one |
| `candidate_count`, `candidates_truncated` | Candidate count; at most 128 are listed |
| `candidates[]` | `id`, `provider`, `priority`, `observed_at_ms`, `soft_limited`, `perish_long`, `perish_short`, `short_active`, `long_remaining`, `windows[]` |
| `dropped` | Records dropped so far because the queue was full |

`windows[]` entries contain `name`, optional `scope` and `label`, `used` (fraction),
`reset_at_ms`, `duration_s`, and `status` when known.

### `selection_error`

Written when no credential could be picked: `ts_ms`, `model`, `providers`, `session`,
`attempt`, `status`, `retry_after_ms`, `error` (truncated). Use these to measure outages.

### `observation`

Written after every execution result: `auth`, `provider`, `model`, optional
`route_model`, `success`, `status`, `error_code`, `credential_scope`, `retry_after_ms`.
When the result refreshed the credential's quota snapshot, `observed_at_ms`, the parsed
`windows`, and the raw `signals` are included. Only the credential-level snapshot is
recorded. Devin quota is refreshed by status polling rather than by responses, so Devin
observations rarely carry windows; its windows still appear in `selection` candidates.

Consecutive observations of one credential show whether a window's `reset_at_ms` stays
fixed (fixed window started on first use or anchored) or moves forward with usage
(rolling window). That determines whether staggering window starts can help at all.

## Analyzing the log

`cmd/quota-shadow-report` turns a shadow log into a plain-text report. Collect about a
week of traffic first, so that weekly windows reset at least once inside the log.

```bash
go run ./cmd/quota-shadow-report -file logs/quota-shadow.jsonl > quota-report.txt
```

Without file arguments it reads `-file`, preceded by its rotation `<file>.1` when present.
File arguments are read in the order given. Malformed lines (for example a line cut off
by a crash) are counted and skipped.

| Flag | Default | Meaning |
|---|---|---|
| `-file` | `quota-shadow.jsonl` | Log to read when no file arguments are given |
| `-no-rotated` | `false` | Do not read `<file>.1` first |
| `-reset-tolerance` | `5m` | `reset_at_ms` changes up to this size count as the same reset; at least 1m, and widened automatically when the provider rounds reset times |
| `-timeline` | `true` | Print every observed window per credential in section 5 |
| `-strategy` | `round-robin` | Strategy that produced the log (`round-robin` or `fill-first`), used to calibrate the replay |
| `-soft-limit-percent` | `90` | Soft limit used by the replayed shadow policy |
| `-min-cost-samples` | `20` | Consecutive same-window observations needed to estimate a credential's cost per request |

The report has six sections:

1. **Agreement** between `actual` and `shadow`, for picks inside the scored pool, split by
   `shadow_reason` and by first attempt versus failover. Picks outside the pool
   (`actual_in_pool=false`) are counted separately because their disagreement is structural.
2. **Soft-limited real picks**: how often the real pick was soft-limited in the shadow view,
   and how often another candidate was not.
3. **Outages** from `selection_error`: an episode runs from the first failed pick to the
   next successful pick on the same route (providers and model). Reports the count, total
   and maximum gap minutes, status codes, and the `retry_after_ms` distribution. Idle
   clients stretch episodes, so gaps are upper bounds.
4. **Window semantics** per credential window and per window type, from consecutive
   observations. Within one window, `reset_at_ms` either stays put (fixed) or moves with
   elapsed time (rolling); the drift column is reset movement per unit of observed time.
   Observations without a reset are skipped, and so are stale ones that report an earlier
   reset than already seen (responses completing out of order). For fixed windows,
   transitions after an idle gap show whether the next window started on its first use
   (allowing the first response to complete up to 15 minutes after the start) or stayed
   anchored to the previous reset. The table also lists the nominal length, the largest
   observed reset lead, and the spacing between resets.
5. **Utilization and perished quota**: per credential window, the last used share and,
   for every window whose reset passed inside the log, the peak used share and the unused
   remainder (perished). Usage outside this proxy is invisible, so perished is an upper
   bound. Not computed for rolling windows.
6. **Replay** of round-robin, fill-first, and the shadow policy over the recorded request
   arrivals, using each credential's mean observed cost per request. It runs only when
   every credential-wide window type on a route is fixed with verified drift, has a known
   length, and has enough observations with a measurable usage increase to estimate
   costs. A rejected request is one that ended without a credential. Calibration compares
   the simulation of `-strategy` with the log: rejected requests must match within 2% of
   demand and the per-credential share of first picks within 0.10 (total variation),
   otherwise the table is marked FAILED. When neither side ran out of credentials the
   calibration is UNINFORMATIVE and only perished quota can be compared. When the replay
   cannot run, the report lists why and which extra data would be needed. Even a
   calibrated replay ignores prompt-cache loss when sessions move between credentials,
   session affinity, and request size.

Reading the result: a quota-aware strategy can only help when windows are fixed
(section 4), some credentials let weekly quota perish while others are exhausted
(sections 3 and 5), and the calibrated replay shows fewer or shorter outages for the
shadow policy than for the current strategy (section 6). If all credentials use their
weekly windows fully, the pool is capacity-bound and no ordering adds requests.

## Privacy

Records contain credential IDs (usually auth file names) and quota header values, but no
request content, tokens, or raw session identifiers. The file is created with mode 0600.
