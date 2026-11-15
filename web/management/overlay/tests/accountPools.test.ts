import { afterEach, describe, expect, mock, spyOn, test } from 'bun:test';
import type { AxiosAdapter } from 'axios';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n';
import { apiClient } from '../src/services/api/client';
import {
  accountPoolsConnection,
  configPoolMembers,
  configMembershipPatch,
  poolBaseURL,
  poolConfigPatch,
  readPoolDraft,
  validatePoolDraft,
  samePoolDraft,
  removePoolFromDraft,
  poolRemovalBlockers,
  verifyPoolMembership,
  type PoolSnapshot,
  type PoolMember,
} from '../src/services/api/accountPools';
import {
  ClientControls,
  GrantPicker,
  PoolControls,
  PoolEditor,
} from '../src/features/accountPools/PoolControls';
import { PoolMembers } from '../src/features/accountPools/PoolMembers';
import { PoolRuntimeBanner } from '../src/features/accountPools/PoolRuntimeBanner';
import en from '../src/i18n/locales/en.json';
import zh from '../src/i18n/locales/zh-CN.json';
import tw from '../src/i18n/locales/zh-TW.json';
import ru from '../src/i18n/locales/ru.json';
import vi from '../src/i18n/locales/vi.json';
import ko from '../src/i18n/locales/ko.json';
import tr from '../src/i18n/locales/tr.json';

const config = {
  routing: {
    'account-pools': {
      enabled: true,
      pools: [
        { id: 'basic', name: 'Basic', enabled: true },
        { id: 'premium', name: 'Premium', enabled: true },
      ],
    },
    strategy: 'round-robin',
  },
  access: {
    'api-keys': ['legacy-secret'],
    clients: [{ id: 'client', 'api-key': 'client-secret', 'allowed-pools': ['basic'] }],
  },
  'api-keys': {
    codex: [
      {
        name: 'Codex group',
        'pool-id': 'premium',
        headers: { Custom: 'keep' },
        keys: [
          { 'api-key': 'upstream-secret', auth_index: 'one', weight: 7 },
          { 'api-key': 'second-secret', auth_index: 'two', 'pool-id': '' },
        ],
      },
    ],
    'openai-compatibility': [
      { name: 'Keyless', keys: [], 'pool-id': 'basic', auth_index: 'three' },
    ],
  },
  plugins: { untouched: 'private' },
};

afterEach(() => mock.restore());

test('runtime status uses the originating authenticated connection and rejects stale responses', async () => {
  let finish!: () => void;
  const ready = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const api = transport(async (request) => {
    expect(request.url).toBe('/account-pools/status');
    expect(request.headers.Authorization).toBe('Bearer test-management-key');
    await ready;
    return {
      data: { state: 'restart_required' },
      status: 200,
      statusText: 'OK',
      headers: {},
      config: request,
    };
  });
  const pending = api.runtime();
  await Promise.resolve();
  apiClient.setConfig({ apiBase: 'https://other.invalid', managementKey: 'different' });
  finish();
  await expect(pending).rejects.toThrow('connection_changed');
});

test.each(['en', 'zh-CN'])(
  'runtime banner distinguishes saved configuration from active isolation (%s)',
  async (language) => {
    const previous = i18n.language;
    try {
      await i18n.changeLanguage(language);
      const runtime = {
        state: 'restart_required' as const,
        configured_enabled: true,
        startup_enabled: false,
        restart_required: true,
        home_enabled: false,
      };
      const render = (savedEnabled: boolean, failed = false) =>
        renderToStaticMarkup(createElement(PoolRuntimeBanner, { runtime, savedEnabled, failed }));
      expect(render(true)).toContain('pool_restart_required');
      expect(render(true)).toContain('role="alert"');
      expect(render(true)).toContain('Coolify');
      expect(render(false)).toContain(i18n.t('account_pools.runtime_pending'));
      expect(render(true, true)).toContain(i18n.t('account_pools.runtime_unknown'));
    } finally {
      await i18n.changeLanguage(previous);
    }
  }
);

describe('account pool configuration', () => {
  test('defaults to deny, preserves grants, and never includes unrelated config in settings patch', () => {
    const draft = readPoolDraft(config);
    expect(draft.settings.unscoped).toEqual({ mode: 'deny' });
    draft.clients[0]['allowed-pools'] = ['premium'];
    const patch = poolConfigPatch(draft);
    expect(patch.access.clients[0]['api-key']).toBe('client-secret');
    expect(JSON.stringify(patch)).not.toContain('upstream-secret');
    expect(JSON.stringify(patch)).not.toContain('private');
    expect(config.access.clients[0]['allowed-pools']).toEqual(['basic']);
    expect(readPoolDraft({}).settings).toEqual({
      enabled: false,
      unscoped: { mode: 'deny' },
      pools: [],
    });
  });
  test('assignment preserves per-key policy and siblings, including explicit unassignment and keyless groups', () => {
    const members = configPoolMembers(config);
    expect(members.map((member) => member.poolId)).toEqual(['premium', '', 'basic']);
    const patch = configMembershipPatch(config, members[0], 'basic');
    expect(patch['api-keys'].codex).toEqual([
      {
        ...config['api-keys'].codex[0],
        keys: [
          { ...config['api-keys'].codex[0].keys[0], 'pool-id': 'basic' },
          config['api-keys'].codex[0].keys[1],
        ],
      },
    ]);
    expect(configPoolMembers(config)[0].poolId).toBe('premium');
    expect(
      configPoolMembers({
        'api-keys': configMembershipPatch(config, members[0], '')['api-keys'],
      })[0].poolId
    ).toBe('');
    expect(
      configMembershipPatch(config, members[2], '')['api-keys']['openai-compatibility'][0][
        'pool-id'
      ]
    ).toBe('');
  });
  test('validates grants, duplicates and enabled default pool before saving', () => {
    const draft = readPoolDraft(config);
    expect(validatePoolDraft(draft, config.access['api-keys'])).toBeNull();
    draft.clients[0]['api-key'] = 'legacy-secret';
    expect(validatePoolDraft(draft, config.access['api-keys'])).toBe('invalid_client');
    draft.clients[0]['api-key'] = 'unique';
    draft.legacyGrants = ['*', 'basic'];
    expect(validatePoolDraft(draft, [])).toBe('invalid_grants');
    draft.legacyGrants = ['*'];
    draft.settings.unscoped = { mode: 'default', 'pool-id': 'missing' };
    expect(validatePoolDraft(draft, [])).toBe('invalid_default');
    draft.settings.unscoped = { mode: 'deny' };
    draft.settings.pools[1].id = 'basic';
    expect(validatePoolDraft(draft, [])).toBe('invalid_pool');
  });
  test('builds protocol URLs without discarding reverse-proxy paths', () => {
    expect(poolBaseURL('https://proxy.test/cpa/', 'premium', 'openai')).toBe(
      'https://proxy.test/cpa/pools/premium/v1'
    );
    expect(poolBaseURL('https://proxy.test/cpa/v8/management', 'basic', 'anthropic')).toBe(
      'https://proxy.test/cpa/pools/basic'
    );
    expect(poolBaseURL('https://proxy.test', 'basic', 'gemini')).toBe(
      'https://proxy.test/pools/basic/v1beta'
    );
  });
});

function transport(adapter: AxiosAdapter) {
  apiClient.setConfig({
    apiBase: 'https://original.invalid/cpa',
    managementKey: 'test-management-key',
  });
  const captured = apiClient.captureConnection();
  spyOn(apiClient, 'captureConnection').mockReturnValue(captured);
  const get = captured.get.bind(captured);
  const raw = captured.getRaw.bind(captured);
  const patch = captured.patch.bind(captured);
  spyOn(captured, 'get').mockImplementation((url, options) => get(url, { ...options, adapter }));
  spyOn(captured, 'getRaw').mockImplementation((url, options) => raw(url, { ...options, adapter }));
  spyOn(captured, 'patch').mockImplementation((url, body, options) =>
    patch(url, body, { ...options, adapter })
  );
  return accountPoolsConnection();
}

test('transport pins the connection, sends ETags and never retries stale writes', async () => {
  const requests: string[] = [];
  const api = transport(async (request) => {
    expect(request.baseURL).toBe('https://original.invalid/cpa/v8/management');
    expect(request.headers.Authorization).toBe('Bearer test-management-key');
    requests.push(`${request.method} ${request.url}`);
    if (request.method === 'patch') {
      expect(request.headers['If-Match']).toBe('"revision"');
      throw Object.assign(new Error('conflict'), { status: 412 });
    }
    return {
      data:
        request.url === '/config'
          ? config
          : request.url === '/credentials/pool'
            ? { id: 'account', pool_id: 'premium' }
            : { items: [] },
      status: 200,
      statusText: 'OK',
      headers: { etag: '"revision"' },
      config: request,
    };
  });
  const snapshot = await api.load();
  expect(snapshot.etag).toBe('"revision"');
  await expect(api.saveConfig(snapshot.etag, poolConfigPatch(snapshot.draft))).rejects.toThrow();
  expect(requests.filter((request) => request.startsWith('patch'))).toHaveLength(1);
  apiClient.setConfig({ apiBase: 'https://other.invalid', managementKey: 'different' });
  await expect(api.saveConfig(snapshot.etag, {})).rejects.toThrow('connection_changed');
  expect(requests).toHaveLength(3);
});

test('missing ETags fail closed and membership sends only identity and pool', async () => {
  const bodies: unknown[] = [];
  const api = transport(async (request) => {
    if (request.method === 'patch') {
      bodies.push(JSON.parse(request.data));
      expect(request.headers['If-Match']).toBe('"member-version"');
    }
    return {
      data:
        request.url === '/config'
          ? config
          : request.url === '/credentials/pool'
            ? { id: 'account', pool_id: 'premium' }
            : { items: [] },
      status: 200,
      statusText: 'OK',
      headers: {},
      config: request,
    };
  });
  await expect(api.load()).rejects.toThrow('missing_etag');
  await expect(api.saveConfig('', {})).rejects.toThrow('missing_etag');
  await api.assign(
    {
      id: 'account',
      etag: '"member-version"',
      name: 'Account',
      auth_index: 'one',
      source: 'credential',
      provider: 'codex',
      pool_id: 'basic',
      disabled: false,
    },
    'premium'
  );
  expect(bodies).toEqual([{ id: 'account', pool_id: 'premium' }]);
});

test('connection changes during a read discard its result, including ABA switches', async () => {
  let finish!: () => void;
  const ready = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const api = transport(async (request) => {
    await ready;
    return {
      data:
        request.url === '/config'
          ? config
          : request.url === '/credentials/pool'
            ? { id: 'account', pool_id: 'premium' }
            : { items: [] },
      status: 200,
      statusText: 'OK',
      headers: { etag: '"revision"' },
      config: request,
    };
  });
  const loading = api.load();
  apiClient.setConfig({ apiBase: 'https://other.invalid', managementKey: 'different' });
  apiClient.setConfig({
    apiBase: 'https://original.invalid/cpa',
    managementKey: 'test-management-key',
  });
  finish();
  await expect(loading).rejects.toThrow('connection_changed');
});

test.each(['zh-CN', 'en'])(
  'pool controls expose disabled state, masked keys and stable saved IDs (%s)',
  async (language) => {
    const previous = i18n.language;
    try {
      await i18n.changeLanguage(language);
      const draft = readPoolDraft(config);
      const markup = renderToStaticMarkup(
        createElement(PoolControls, {
          draft,
          savedIds: ['basic', 'premium'],
          disabled: true,
          apiBase: 'https://proxy.test',
          onChange() {},
        })
      );
      expect(markup).toContain('disabled=""');
      expect(markup).toContain('https://proxy.test/pools/premium/v1');
      expect(markup).not.toContain('upstream-secret');
      const clients = renderToStaticMarkup(
        createElement(ClientControls, { draft, disabled: true, onChange() {} })
      );
      expect(clients).toContain('type="password"');
      const grants = renderToStaticMarkup(
        createElement(GrantPicker, {
          value: [],
          pools: draft.settings.pools,
          disabled: true,
          onChange() {},
        })
      );
      expect(grants).toContain('<legend>');
      expect(grants).not.toContain('checked=""');
    } finally {
      await i18n.changeLanguage(previous);
    }
  }
);

test('every supported locale contains the pool UI keys', () => {
  for (const locale of [zh, tw, ru, vi, ko, tr]) {
    expect(Object.keys(locale.account_pools).sort()).toEqual(Object.keys(en.account_pools).sort());
  }
});

test('identities with omitted or null grants remain denied and render safely', () => {
  const draft = readPoolDraft({
    access: {
      clients: [{ id: 'denied', 'api-key': 'fixture' }],
      'provider-clients': [{ id: 'denied', provider: 'sso', 'allowed-pools': null }],
    },
  });
  expect(draft.clients[0]['allowed-pools']).toEqual([]);
  expect(draft.providerClients[0]['allowed-pools']).toEqual([]);
  expect(() =>
    renderToStaticMarkup(createElement(ClientControls, { draft, disabled: false, onChange() {} }))
  ).not.toThrow();
});

test('pool overview separates editing and retains saved identity after a removal', () => {
  const draft = removePoolFromDraft(readPoolDraft(config), 'basic');
  const props = {
    draft,
    savedIds: ['basic', 'premium'],
    disabled: false,
    apiBase: 'https://proxy.test',
    onChange() {},
  };
  const markup = renderToStaticMarkup(createElement(PoolControls, props));
  expect(markup).toContain('https://proxy.test/pools/premium/v1');
  expect(markup).toContain(i18n.t('account_pools.delete_pool'));
  expect(markup).not.toContain('<input');
  const editor = renderToStaticMarkup(
    createElement(PoolEditor, {
      pool: draft.settings.pools[0],
      lockedId: true,
      disabled: false,
      onChange() {},
    })
  );
  expect(editor.match(/<input[^>]*value="premium"[^>]*>/)?.[0]).toContain('disabled');
  const newEditor = renderToStaticMarkup(
    createElement(PoolEditor, {
      pool: { id: 'new', name: 'New', enabled: true },
      lockedId: false,
      disabled: false,
      onChange() {},
    })
  );
  expect(newEditor.match(/<input[^>]*value="new"[^>]*>/)?.[0]).not.toContain('disabled');
});

const member: PoolMember = {
  id: 'account',
  auth_index: 'one',
  name: 'Account',
  source: 'credential',
  provider: 'kiro4cpa',
  pool_id: 'basic',
  etag: '"revision"',
  disabled: false,
};
const snapshot = (): PoolSnapshot => ({
  config,
  etag: '"revision"',
  draft: readPoolDraft(config),
  members: [],
  configMembers: [],
});

test('deletion removes only named grants and resets a removed default without mutating the draft', () => {
  const draft = readPoolDraft(config);
  draft.settings.unscoped = { mode: 'default', 'pool-id': 'basic' };
  draft.legacyGrants = ['basic', 'premium'];
  draft.providerClients = [
    { id: 'sso', provider: 'example', 'allowed-pools': ['basic'] },
    { id: 'admin', provider: 'example', 'allowed-pools': ['*'] },
  ];
  const removed = removePoolFromDraft(draft, 'basic');
  expect(removed.settings.pools.map((pool) => pool.id)).toEqual(['premium']);
  expect(removed.settings.unscoped).toEqual({ mode: 'deny' });
  expect(removed.clients[0]['allowed-pools']).toEqual([]);
  expect(removed.clients[0]['api-key']).toBe('client-secret');
  expect(removed.legacyGrants).toEqual(['premium']);
  expect(removed.providerClients.map((client) => client['allowed-pools'])).toEqual([[], ['*']]);
  expect(validatePoolDraft(removed, config.access['api-keys'])).toBeNull();
  expect(draft.clients[0]['allowed-pools']).toEqual(['basic']);
  expect(draft.settings.pools).toHaveLength(2);
});

test('deletion blocks enabled pools, disabled members and config/runtime disagreement', () => {
  const current = snapshot();
  expect(poolRemovalBlockers(current, 'basic')).toEqual(['pool_must_be_disabled']);
  current.draft.settings.pools[0].enabled = false;
  current.members = [{ ...member, disabled: true }];
  expect(poolRemovalBlockers(current, 'basic')).toEqual(['pool_not_empty']);
  current.members = [];
  current.configMembers = [
    { family: 'codex', group: 0, key: 0, poolId: 'basic', authIndex: 'one', name: 'Configured' },
  ];
  expect(poolRemovalBlockers(current, 'basic')).toEqual(['pool_not_empty']);
  current.configMembers = [];
  expect(poolRemovalBlockers(current, 'basic')).toEqual([]);
  expect(poolRemovalBlockers(current, 'missing')).toEqual(['pool_state_unavailable']);
});

test('membership readback distinguishes confirmed, pending and mismatched state', () => {
  const current = snapshot();
  current.members = [{ ...member, pool_id: 'premium' }];
  expect(verifyPoolMembership(current, member, 'premium')).toBe('active');
  expect(() => verifyPoolMembership(current, member, 'basic')).toThrow('membership_not_confirmed');
  const configured = {
    family: 'codex',
    group: 0,
    key: 0,
    poolId: 'basic',
    authIndex: 'one',
    name: 'Configured',
  };
  current.configMembers = [configured];
  expect(verifyPoolMembership(current, configured, 'basic')).toBe('pending');
  current.members[0].pool_id = 'basic';
  expect(verifyPoolMembership(current, configured, 'basic')).toBe('active');
  expect(() => verifyPoolMembership(current, configured, 'premium')).toThrow(
    'membership_not_confirmed'
  );
});

test.each(['mismatch', 'read_failure', 'identity_mismatch'])(
  'a successful PATCH is not success when readback fails: %s',
  async (mode) => {
    const requests: string[] = [];
    const api = transport(async (request) => {
      requests.push(request.method!);
      if (request.method === 'get') {
        expect(request.params).toEqual({ id: member.id });
        if (mode === 'read_failure') throw new Error('read failed');
      }
      return {
        data: {
          id: mode === 'identity_mismatch' ? 'other' : member.id,
          pool_id: mode === 'mismatch' ? '' : 'premium',
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config: request,
      };
    });
    await expect(api.assign(member, 'premium')).rejects.toThrow();
    expect(requests).toEqual(['patch', 'get']);
  }
);

test('only explicit enablement is displayed and readback equality ignores JSON property order', () => {
  const draft = readPoolDraft({
    routing: { 'account-pools': { pools: [{ id: 'basic', name: 'Basic' }] } },
  });
  expect(draft.settings.pools[0].enabled).toBe(false);
  const reordered = structuredClone(draft);
  reordered.settings.pools[0] = { enabled: false, name: 'Basic', id: 'basic' };
  expect(samePoolDraft(draft, reordered)).toBe(true);
  reordered.settings.pools[0].enabled = true;
  expect(samePoolDraft(draft, reordered)).toBe(false);
});

test('accounts with the same plugin label expose distinct file identities', () => {
  const current = snapshot();
  current.members = [
    { ...member, id: 'kiro-one.json', name: 'Kiro' },
    { ...member, id: 'kiro-two.json', name: 'Kiro' },
  ];
  const markup = renderToStaticMarkup(
    createElement(PoolMembers, {
      snapshot: current,
      filter: '*',
      disabled: false,
      dirty: false,
      onFilter() {},
      onAssign() {},
    })
  );
  expect(markup).toContain('kiro-one.json');
  expect(markup).toContain('kiro-two.json');
  expect(markup).toContain('Kiro (kiro-one.json)');
});
