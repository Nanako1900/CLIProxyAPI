# Quota shadow management

Open **Observe → Quota shadow** (`#/quota-shadow`) in the bundled CPA management
panel. The panel configures `routing.quota-shadow` and displays process-local
telemetry from the recorder. Deploy both the backend and the bundled management
HTML from this change; an older backend will not provide the status endpoint.

## Operator workflow

1. Connect using the normal management key. Enable recording and review the
   server-side file path, rotation size, and soft-limit percentage.
2. Choose **Review and save**, then **Save settings**. Settings are saved through
   `PATCH /v8/management/config` with the ETag from the preceding GET. Conflicting
   edits return 412 and retain the draft; reload and reapply it explicitly.
3. Refresh runtime status after config hot reload. No restart is required for
   shadow settings. `idle` means enabled without processed traffic, not that
   the output file has already been verified writable.
4. Send ordinary inference traffic, including `/pools/<id>/...` requests when
   pools are configured. Inspect actual versus shadow choices and actual outcomes.
   Filter recent events by pool or type; optionally enable five-second refresh.
5. Inspect dropped records and file errors before trusting the sample. Disabling
   recording retains the most recent session in memory until restart or re-enable.

The soft limit only affects shadow ranking. Shadow mode never executes the
hypothetical choice, retries it, or changes the actual routing policy. Actual
success rates cannot establish the counterfactual success rate of shadow choices.
Home dispatch is excluded. Existing account-pool routing startup requirements
still apply; enabling shadow mode does not reconfigure pool routes.

## Configuration and storage

```yaml
routing:
  quota-shadow:
    enabled: true
    file: ""                  # <server log directory>/quota-shadow.jsonl
    max-file-mb: 256           # MiB, 0 uses 256; integer range 0..65536
    soft-limit-percent: 90     # 0 uses 90; finite range 0..100
```

Relative paths resolve against the server working directory. Paths are limited
to 4096 bytes without NUL or line breaks. Use a dedicated regular file in a
trusted, writable directory. The recorder rejects an existing symlink, directory
or device as its output. This is not a filesystem sandbox: the management key
already grants configuration privileges, and parent directories must be trusted.
New directories use 0700 and new files 0600, subject to the OS umask; permissions
of existing paths are not changed. Separate CPA processes must use separate files.

One backup (`<file>.1`) is retained. Rotation is checked after opening an existing
file and before appending. Rotation failures discard the current record and report
an error, rather than allowing unbounded growth. A single record larger than the
limit can occupy a fresh file; this is a rotation threshold, not a hard disk quota.
Records are asynchronously appended, without per-record fsync or crash durability.

The queue holds 1024 jobs; full queues drop new records without blocking inference.
Disabling or changing paths drains the previous writer asynchronously. Successors
wait for it to close, including A → B → A changes and disable/re-enable cycles.
Slow storage can therefore delay new recording and cause visible queue losses.
Shutdown drains accepted jobs. No I/O timeout is added.

## Runtime API and interpretation

`GET /v8/management/observability/quota-shadow` uses the existing management
authentication and availability checks, with `Cache-Control: no-store`.
It returns 503 if no auth manager is available. There is no v0 endpoint.

The response includes `state`, `since`, effective `file`, `max_file_mb`,
`soft_limit_percent`, write/loss counters, selection/outcome counters, queue
occupancy, `last_error`, and up to 200 recent safe projections. It never reads
the configured log file or exposes request bodies, headers, session hashes,
quota signals, or raw error messages. Auth identifiers, models and pool IDs are
visible only to management users. Counters are per process and recorder lifetime,
not a durable audit ledger. Restart, re-enable and output-path changes reset them;
changing only size or soft limit preserves them. Historical JSONL is not imported.

States are `disabled`, `home`, `stopped`, `draining`, `idle`, `recording` and `error`.
The last stable error code is `open_failed`, `write_failed`, `rotate_failed` or
`render_failed`; a subsequent successful write clears it. Cumulative error counts
remain. `draining` indicates a successor waiting for a prior writer. Disabled
sessions may still drain accepted work. Home normally hides management routes.

Agreement is `agreements / comparable`, excluding no-candidate selections and
cases where the real choice is outside the shadow candidate set. Candidates obey
pool scope and pinned-account constraints. The counters count events, so retries
can produce multiple selections/outcomes for one request. They are eventually
updated by the recorder worker and are not an atomic transaction with file I/O.
Recent events can include failed disk writes. Pool/type filters affect only the
recent table; session totals remain unfiltered. If refresh fails, the page marks
the retained snapshot as stale. Changing connections clears drafts and results,
and old responses cannot overwrite the new connection, including ABA switches.

## Review fixes and regression coverage

- Treat an installed plugin host with no active scheduler as native routing.
  Production services always install a host; the old non-nil check rejected all
  pooled selections with 501 even when no scheduling plugin was enabled. Active
  schedulers still fail closed, and scoped requests never invoke the host after
  the initial availability check. HTTP tests now install a real empty host.
- Serialize recorder lifetimes so rapid configuration changes cannot create two
  writers to the same file within a manager.
- Check pre-existing file size before the first append; surface rotation, open,
  write and rendering failures with bounded telemetry.
- Carry trusted request pool scope into selection and observation records;
  namespace hashed sessions by scope and principal. Honor pinned-account filters.
- Replace raw selection errors and arbitrary execution error codes in JSONL with
  stable codes, preventing upstream error strings from leaking credentials.
- Use a transitive quantized ordering for shadow scores. Pairwise epsilon
  comparisons previously allowed input-order-dependent results near ties.
- Validate path, size and threshold at config load and management save boundaries.

Automated coverage includes these failures, bounded history, snapshot copy
isolation, Home exclusion, actual-routing invariance, configuration persistence,
ETag conflicts, management authentication, connection changes, filters,
agreement denominators and all seven locale contracts.

For manual verification, use an isolated server with dummy credentials and a
local mock upstream. Check enable/save, traffic from two pools, filtering,
threshold updates, concurrent config conflicts, invalid/unwritable output,
disable/re-enable, navigation with unsaved changes, and a narrow viewport.
Run `go test ./...`, race tests for affected packages, the required server build,
and `web/management/build.sh` from an empty source directory before release.
