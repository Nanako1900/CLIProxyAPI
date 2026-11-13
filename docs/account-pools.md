# Account pools

Account pools restrict credential selection to an authenticated URL scope.
The URL prefix for pool `premium` is `/pools/premium`. An OpenAI client's base
URL is `https://proxy.example/pools/premium/v1`; an Anthropic client's base URL
is `https://proxy.example/pools/premium`. Gemini uses the same prefix followed
by `/v1beta`. These are inbound prefixes, independent of upstream `base-url`
and model-name `prefix`.

## Web management panel

Deploy the backend and source-built management panel from the same revision.
The repository Dockerfile bundles both. Open **Account pools** in the left
navigation (`management.html#/account-pools`). If the entry is absent, rebuild
the image or rebuild the panel with `web/management/build.sh`; an independently
downloaded upstream panel does not contain this extension. An explicit
`MANAGEMENT_STATIC_PATH` overrides the bundled panel and must also be updated.

1. Create pools, choose names and scheduling strategies, and save. Saved IDs
   are immutable in the UI; retire a pool by disabling it.
2. Grant existing access keys a shared pool list, or add dedicated client keys
   with individual grants. External authenticated identities have their own
   section. Empty grants deny access; `*` explicitly includes future pools.
   Keys stay masked and drafts are not written to browser storage.
3. Under **Account membership**, search/filter accounts and choose **Assign
   pool**. OAuth membership uses its versioned management endpoint. API-key
   membership changes only its provider configuration list, preserving other
   key settings and group policies. An explicit unassignment overrides an
   inherited group pool. Plugin-owned accounts cannot be moved here.
4. Enable isolation only after preparing pools, grants and membership, then
   restart CPA. Changes to the isolation switch require a restart in either
   direction. Copy the protocol-specific base URL from the desired pool card.
5. Refresh and compare the configured API-key pool with live membership. A
   mismatch is displayed explicitly: drain traffic and reapply the assignment,
   or restart after draining. Saving the source alone does not prove that a
   busy credential has moved at runtime.

Settings and memberships use ETags and the originating authenticated connection.
A concurrent change returns a conflict instead of overwriting a newer version;
refresh, review and reapply. Unsaved settings disable membership edits, and
navigation/refresh prompts before discarding a draft. Failed or unconfirmed
writes require readback before retrying. The page does not automatically retry
mutations. It displays supported protocols and restart requirements; it does not
implement batch membership transactions or pool accounting reports.

The **Runtime status** banner reports the running process separately from saved
settings. A persistent **Restart required** warning means inference is blocked
until the CPA process/container restarts. In Coolify, open the CPA service and
select **Restart**. With Docker, use `docker restart <cpa-container>`; with Compose,
use `docker compose restart <cpa-service>`. Restarting an old image does not install
code fixes: merge the fix and rebuild/redeploy the image first. Refresh the page
after restart. The management-only `GET /v8/management/account-pools/status`
endpoint exposes the startup/configured isolation flags and Home incompatibility
without credentials, with `Cache-Control: no-store`.

### Diagnosing failed requests

Read the response error code as well as the access-log HTTP status:

| Response | Meaning and next step |
| --- | --- |
| `400 pool_required` | Use the pool base URL, for example `/pools/kiro/v1` for an OpenAI client or `/pools/kiro` for an Anthropic client. |
| `503 pool_restart_required` | Restart CPA after changing the isolation switch. Reloading YAML alone is insufficient. |
| `503 pool_disabled` | Enable the named pool. |
| `503 pool_no_eligible_accounts` | Check live membership, model support, account availability, and cooldown; an empty pool cannot borrow another pool's accounts. |
| `501 pool_adapter_unsupported` | Update to the inactive-plugin-host fix, then check for a real plugin scheduler or unsupported private executor/adapter. |
| `501 pool_continuation_unsupported` | Send full history instead of server-side continuation handles. |

The service creates a plugin host even without a scheduling plugin. Releases
before the inactive-host fix could list models successfully but reject actual
inference with `501 pool_adapter_unsupported`. Regression tests now exercise the
production builder, streaming/non-streaming Chat Completions and Anthropic
`/v1/messages?beta=true`, token counts, scheduler activation, and pool isolation.
The `beta` query parameter does not change route support. A successful model list
does not prove that execution will succeed. `/api/pricing` is not a CPA inference
endpoint.

## Configuration

Merge this example into a v8 configuration. Replace all example secrets.

```yaml
routing:
  account-pools:
    enabled: true
    unscoped: {mode: deny}
    pools:
      - {id: basic, name: Basic, enabled: true}
      - {id: premium, name: Premium, enabled: true, strategy: weighted-round-robin}
access:
  clients:
    - id: basic-client
      api-key: replace-basic-client-secret
      allowed-pools: [basic]
    - id: operator-client
      api-key: replace-operator-client-secret
      allowed-pools: [basic, premium]
api-keys:
  codex:
    - name: premium-codex
      base-url: https://upstream.example/v1
      pool-id: premium
      keys:
        - {api-key: replace-upstream-secret, weight: 2}
```

- Each credential has zero or one pool. Unassigned credentials cannot serve
  scoped requests. A missing grant never means unrestricted access.
- Pool IDs are immutable operational identifiers matching
  `^[a-z][a-z0-9-]{0,47}$`. Do not reuse a removed ID for a different customer.
  Pool definitions require a name and an explicit boolean `enabled`.
- A pool inherits `routing.strategy` and the global session-affinity settings.
  It can override the strategy with `round-robin`, `weighted-round-robin`, or
  `fill-first`. Each pool has independent rotation and affinity state.
- Existing `access.api-keys` require `access.legacy-key-pools: [basic]`.
  `["*"]` explicitly grants all defined pools; it cannot be mixed with named
  grants. Structured client keys must be unique, including against legacy keys.
- Custom access-provider identities can be granted access with
  `access.provider-clients: [{provider: trusted-sso, id: caller, allowed-pools: [basic]}]`.
  The provider and principal must come from the configured authenticator.
  Request headers, JSON fields, and query parameters cannot establish membership
  or change the pool selected by the URL.
- Upstream API-key families accept `pool-id` at group or credential level.
  For OpenAI-compatible groups, a missing per-key value inherits the group;
  an explicit empty value makes the credential unassigned.
- OAuth files persist membership as top-level `"pool_id": "premium"`.
  Importing a new credential without this field leaves it unassigned.
  Refresh/preparation retains the existing membership.
- Omitted `unscoped` or `{mode: deny}` rejects legacy inference URLs. To migrate
  selected clients, use `{mode: default, pool-id: basic}`; those clients still
  need a grant for `basic`.

Changing `enabled` requires a restart. Live requests fail closed after a
mode mismatch; a running manager never switches from pool isolation back to
unrestricted global routing. Pool definitions, grants, and enabled states use
the existing validated configuration reload mechanism. Revocation affects new
admissions, including retries; a request already admitted may finish.

## Supported requests

| Protocol | Supported routes under the pool prefix |
| --- | --- |
| OpenAI | `GET /v1/models`, model detail, `POST /v1/chat/completions`, `/v1/completions` |
| Responses | `POST /v1/responses`, `/v1/responses/compact`, corresponding `/backend-api/codex` aliases |
| Anthropic | `POST /v1/messages`, `/v1/messages/count_tokens`; Anthropic-compatible model listing |
| Gemini | Model listing/detail, `generateContent`, `streamGenerateContent`, `countTokens` |
| Images/audio | `POST /v1/images/generations`, `/v1/images/edits`, `/v1/audio/speech`, `/v1/tts` |

Requests must carry full conversation history. Server-side continuation handles
(`previous_response_id`, `conversation`/`conversation_id`,
`thread.previous_message_id`/`thread_id`, `previous_interaction_id`) return
`501 pool_continuation_unsupported` until a durable pool ownership ledger is
implemented. This prevents reuse of another pool's upstream history after an
account changes membership.

Streaming and credential retries stay in the selected pool. An exhausted or empty
pool never falls back to another pool. Model catalogs and `auto` resolution are
filtered to credentials in the requested pool. Upstream cooldown and model
support checks continue to apply.

Home mode cannot currently execute pooled requests (`503 pool_protocol_unsupported`).
Downstream Responses WebSockets, realtime/live/tunnel, video resources, native
Interactions, direct search, Amp routes, relay adapters, plugin executors, and
plugin schedulers are not enabled for pooled execution. They fail closed with
`501 pool_adapter_unsupported` (an unscoped request can first fail the missing-pool
check). Management and OAuth callback routes retain their existing authorization.
The built-in Grok-specific model catalog is bypassed for scoped requests.

Do not enable account pools on an instance requiring those adapters. Their
cross-service and resource-ownership contracts are specified in
[the full design](account-pools-design.md), along with the remaining batch API,
audit/revision persistence, and accounting dashboard work.

## Membership management

Use the existing `/v8/management/config` API or configuration file to manage pool
definitions and grants. Configuration requests retain the existing v8 ETag
contract. Config-backed account membership is changed through its API-key group
configuration; OAuth membership has a separate endpoint:

1. `GET /v8/management/credentials/pool?id=<credential-id>` returns
   `{"id":"...","pool_id":"basic"}` and an `ETag`.
2. `PATCH /v8/management/credentials/pool` with that `If-Match` header and
   `{"id":"...","pool_id":"premium"}` moves the credential.
3. Use `"pool_id": ""` to unassign it. Omission or `null` is rejected.

The endpoint exposes no credentials or tokens. It uses the existing management
authentication. Missing `If-Match` returns 428; stale state returns 412.
`GET /v8/management/account-pools/credentials` supplies the panel with a
secret-free live inventory, source classification and per-account ETags.
It returns `Cache-Control: no-store` and includes disabled/unassigned accounts.
An active upstream operation returns 409 `credential_busy`. Stop new traffic to
the source pool, let requests finish, read a fresh ETag, and retry. Config-source
and plugin-owned credentials must be changed at their source.

A move fences new admissions while storage commits. The new membership is only
published after persistence succeeds. A storage error leaves the credential
quarantined against new admissions because a store may have partially committed;
repair storage and retry the assignment, or reload verified durable state.
Streams retain admission until the executor closes its output, including after
the downstream request is canceled. Re-registration cannot bypass an active
admission. Credential storage must be durable in a deployed server; an embedded
SDK manager with no store has only in-memory state.

Config-backed membership updates rejected as busy must be reapplied after drain;
do not assume the configuration watcher retries a failed runtime mutation.
Avoid editing/replacing OAuth files during active traffic; use the membership API.
Keep a single writer per auth store. This implementation does not provide
distributed membership transactions between independent CPA processes sharing
the same store.

## Deployment and validation

1. Back up configuration and credential storage; inventory the required adapters.
2. Define pools and grants, then assign accounts. Verify every required model is
   present in its intended pool. Do not duplicate the same upstream credential
   under multiple identities to circumvent membership.
3. Restart with isolation enabled. Test each allowed key/pool pair and a denied
   pair, an empty pool, a foreign pinned credential, and an unscoped request.
4. Verify streaming cancellation, upstream 429/5xx retries, pool disable, grant
   revocation, and a concurrent membership move. Monitor failures before moving
   production traffic to the new prefixes.
5. To roll back, drain traffic and restart with the backed-up configuration and
   matching credential state. Disabling isolation restores legacy global routing;
   protect the legacy listener before restarting.

The SDK usage `Record.PoolID` is captured from the admitted request. Existing
accounting dashboards do not yet offer pool reports; do not use mutable current
membership to attribute historical usage.

Regression tests cover strict configuration and grant handling, credential
synthesis, all three manager execution modes, retries, final admission,
stream/move fencing, failed persistence quarantine, refresh preservation, HTTP
scope propagation, scoped catalogs, secret-free ETags, and replay namespaces.
Full production acceptance for the wider system remains the gate in the design
document; passing local tests does not establish Home interoperability or
distributed-store safety.
