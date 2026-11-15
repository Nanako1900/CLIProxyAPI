import { apiClient } from './client';
import { isRecord } from '@/utils/helpers';

export type Pool = { id: string; name: string; enabled: boolean; strategy?: string };
export type PoolRuntime = {
  state: 'disabled' | 'active' | 'restart_required' | 'home_unsupported';
  configured_enabled: boolean;
  startup_enabled: boolean;
  restart_required: boolean;
  home_enabled: boolean;
};
export type PoolClient = {
  id: string;
  'api-key'?: string;
  provider?: string;
  enabled?: boolean;
  'allowed-pools': string[];
};
export type PoolSettings = {
  enabled: boolean;
  unscoped: { mode: string; 'pool-id'?: string };
  pools: Pool[];
};
export type PoolDraft = {
  settings: PoolSettings;
  clients: PoolClient[];
  providerClients: PoolClient[];
  legacyGrants: string[];
};
export type PoolMember = {
  id: string;
  auth_index: string;
  name: string;
  provider: string;
  pool_id: string;
  source: 'credential' | 'config' | 'plugin';
  disabled: boolean;
  etag: string;
};
export type ConfigPoolMember = {
  family: string;
  group: number;
  key: number | null;
  name: string;
  authIndex: string;
  poolId: string;
};
export type PoolSnapshot = {
  config: Record<string, unknown>;
  etag: string;
  draft: PoolDraft;
  members: PoolMember[];
  configMembers: ConfigPoolMember[];
};

const object = (value: unknown): Record<string, unknown> => (isRecord(value) ? value : {});
const list = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);
const text = (value: unknown): string => (typeof value === 'string' ? value : '');
const clients = (value: unknown): PoolClient[] =>
  list(value).map((entry) => {
    const client = structuredClone(object(entry));
    return {
      ...client,
      id: text(client.id),
      'allowed-pools': list(client['allowed-pools']).map(text),
    };
  });

export function readPoolDraft(config: Record<string, unknown>): PoolDraft {
  const routing = object(object(config.routing)['account-pools']);
  const access = object(config.access);
  return {
    settings: {
      enabled: routing.enabled === true,
      unscoped: { mode: 'deny', ...object(routing.unscoped) },
      pools: list(routing.pools).map((entry) => {
        const pool = structuredClone(object(entry));
        return {
          ...pool,
          id: text(pool.id),
          name: text(pool.name),
          enabled: pool.enabled === true,
        };
      }),
    },
    clients: clients(access.clients),
    providerClients: clients(access['provider-clients']),
    legacyGrants: structuredClone(list(access['legacy-key-pools'])) as string[],
  };
}

export function poolConfigPatch(draft: PoolDraft) {
  return {
    routing: { 'account-pools': draft.settings },
    access: {
      clients: draft.clients,
      'provider-clients': draft.providerClients,
      'legacy-key-pools': draft.legacyGrants,
    },
  };
}

export function samePoolDraft(left: PoolDraft, right: PoolDraft): boolean {
  const ordered = (draft: PoolDraft) =>
    JSON.stringify(draft, (_key, value: unknown) =>
      isRecord(value)
        ? Object.fromEntries(
            Object.keys(value)
              .sort()
              .map((key) => [key, value[key]])
          )
        : value
    );
  return ordered(left) === ordered(right);
}

// Remove only named references. Wildcard grants retain their existing meaning.
export function removePoolFromDraft(draft: PoolDraft, id: string): PoolDraft {
  const next = structuredClone(draft);
  next.settings.pools = next.settings.pools.filter((pool) => pool.id !== id);
  if (next.settings.unscoped['pool-id'] === id) next.settings.unscoped = { mode: 'deny' };
  next.legacyGrants = next.legacyGrants.filter((grant) => grant !== id);
  for (const client of [...next.clients, ...next.providerClients]) {
    client['allowed-pools'] = client['allowed-pools'].filter((grant) => grant !== id);
  }
  return next;
}

export function poolRemovalBlockers(snapshot: PoolSnapshot, id: string): string[] {
  const pool = snapshot.draft.settings.pools.find((pool) => pool.id === id);
  if (!pool) return ['pool_state_unavailable'];
  const reasons: string[] = [];
  if (pool.enabled) reasons.push('pool_must_be_disabled');
  if (
    snapshot.members.some((member) => member.pool_id === id) ||
    snapshot.configMembers.some((member) => member.poolId === id)
  )
    reasons.push('pool_not_empty');
  return reasons;
}

export function poolMemberRows(snapshot: PoolSnapshot): (PoolMember | ConfigPoolMember)[] {
  return [
    ...snapshot.members.filter((member) => member.source !== 'config'),
    ...snapshot.configMembers,
  ];
}

export function verifyPoolMembership(
  snapshot: PoolSnapshot,
  member: PoolMember | ConfigPoolMember,
  poolId: string
): 'active' | 'pending' {
  if ('family' in member) {
    const saved = snapshot.configMembers.find(
      (item) =>
        item.family === member.family && item.group === member.group && item.key === member.key
    );
    if (!saved || saved.poolId !== poolId) throw membershipNotConfirmed();
    const runtime = snapshot.members.find((item) => item.auth_index === saved.authIndex);
    return runtime?.pool_id === poolId ? 'active' : 'pending';
  }
  if (!snapshot.members.some((item) => item.id === member.id && item.pool_id === poolId)) {
    throw membershipNotConfirmed();
  }
  return 'active';
}

function membershipNotConfirmed() {
  return Object.assign(new Error('membership_not_confirmed'), {
    apiCode: 'membership_not_confirmed',
  });
}

export function configPoolMembers(config: Record<string, unknown>): ConfigPoolMember[] {
  const members: ConfigPoolMember[] = [];
  for (const [family, groups] of Object.entries(object(config['api-keys']))) {
    list(groups).forEach((value, group) => {
      const item = object(value);
      const keys = list(item.keys);
      const add = (key: number | null, credential: Record<string, unknown>) => {
        members.push({
          family,
          group,
          key,
          name: `${text(item.name) || family} / ${key === null ? '—' : key + 1}`,
          authIndex: text(credential.auth_index),
          poolId: text(credential['pool-id'] ?? item['pool-id']),
        });
      };
      if (keys.length) keys.forEach((key, index) => add(index, object(key)));
      else if (family === 'openai-compatibility') add(null, item);
    });
  }
  return members;
}

// Replace only the selected provider list from the ETag-protected snapshot.
export function configMembershipPatch(
  config: Record<string, unknown>,
  member: ConfigPoolMember,
  poolId: string
) {
  const groups = structuredClone(list(object(config['api-keys'])[member.family]));
  const group = object(groups[member.group]);
  const target = member.key === null ? group : object(list(group.keys)[member.key]);
  target['pool-id'] = poolId;
  return { 'api-keys': { [member.family]: groups } };
}

export function poolBaseURL(apiBase: string, poolId: string, protocol: string): string {
  const base = apiBase.replace(/\/+$/, '').replace(/\/v[08]\/management$/, '');
  return `${base}/pools/${encodeURIComponent(poolId)}${protocol === 'openai' ? '/v1' : protocol === 'gemini' ? '/v1beta' : ''}`;
}

export function validatePoolDraft(draft: PoolDraft, legacyKeys: unknown): string | null {
  const ids = new Set<string>();
  for (const pool of draft.settings.pools) {
    if (
      !/^[a-z][a-z0-9-]{0,47}$/.test(pool.id) ||
      ids.has(pool.id) ||
      !pool.name.trim() ||
      Array.from(pool.name).length > 100
    )
      return 'invalid_pool';
    ids.add(pool.id);
  }
  if (
    draft.settings.unscoped.mode === 'default' &&
    !draft.settings.pools.some(
      (pool) => pool.id === draft.settings.unscoped['pool-id'] && pool.enabled
    )
  )
    return 'invalid_default';
  const validGrants = (grants: string[]) =>
    new Set(grants).size === grants.length &&
    ((grants.length === 1 && grants[0] === '*') || grants.every((id) => ids.has(id)));
  if (!validGrants(draft.legacyGrants)) return 'invalid_grants';
  const clientIds = new Set<string>();
  const keys = new Set(list(legacyKeys).map(text));
  for (const client of draft.clients) {
    if (
      !client.id.trim() ||
      clientIds.has(client.id) ||
      !client['api-key']?.trim() ||
      keys.has(client['api-key'])
    )
      return 'invalid_client';
    clientIds.add(client.id);
    keys.add(client['api-key']);
    if (!validGrants(client['allowed-pools'])) return 'invalid_grants';
  }
  const providerIds = new Set<string>();
  for (const client of draft.providerClients) {
    const id = JSON.stringify([client.provider, client.id]);
    if (!client.provider?.trim() || !client.id.trim() || providerIds.has(id))
      return 'invalid_client';
    providerIds.add(id);
    if (!validGrants(client['allowed-pools'])) return 'invalid_grants';
  }
  return null;
}

export function accountPoolsConnection() {
  const revision = apiClient.getConnectionRevision();
  const client = apiClient.captureConnection();
  const assertCurrent = () => {
    if (revision !== apiClient.getConnectionRevision()) throw new Error('connection_changed');
  };
  return {
    async runtime(): Promise<PoolRuntime> {
      assertCurrent();
      const runtime = await client.get<PoolRuntime>('/account-pools/status');
      assertCurrent();
      return runtime;
    },
    async load(): Promise<PoolSnapshot> {
      assertCurrent();
      const [response, inventory] = await Promise.all([
        client.getRaw('/config'),
        client.get<{ items: PoolMember[] }>('/account-pools/credentials'),
      ]);
      assertCurrent();
      const etag = response.headers.etag;
      if (typeof etag !== 'string' || !etag) throw new Error('missing_etag');
      const config = object(response.data);
      return {
        config,
        etag,
        draft: readPoolDraft(config),
        members: inventory.items,
        configMembers: configPoolMembers(config),
      };
    },
    async saveConfig(etag: string, patch: unknown) {
      assertCurrent();
      if (!etag) throw new Error('missing_etag');
      await client.patch('/config', patch, { headers: { 'If-Match': etag } });
      assertCurrent();
    },
    async assign(member: PoolMember, poolId: string) {
      assertCurrent();
      if (!member.etag) throw new Error('missing_etag');
      await client.patch(
        '/credentials/pool',
        { id: member.id, pool_id: poolId },
        {
          headers: { 'If-Match': member.etag },
        }
      );
      assertCurrent();
      const saved = await client.get<{ id: string; pool_id: string }>('/credentials/pool', {
        params: { id: member.id },
      });
      assertCurrent();
      if (saved.id !== member.id || saved.pool_id !== poolId) throw membershipNotConfirmed();
    },
  };
}
