import { describe, expect, spyOn, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import type { AxiosAdapter } from 'axios';
import type { PluginListEntry } from '@/types';
import { KiroImportIssue, parseKiroCredentials } from '@/features/oauth/kiroCredentials';
import {
  createKiroImportRun,
  type KiroImportAPI,
  type KiroImportResult,
} from '@/features/oauth/kiroImportRun';
import { captureKiroImportAPI } from '@/services/api/kiroImport';
import { apiClient } from '@/services/api/client';
import { buildPluginOAuthProviderCards } from '@/features/oauth/providers';
import { KiroImportDialog } from '@/features/oauth/components/KiroImportDialog';
import en from '@/i18n/locales/en.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';
import ru from '@/i18n/locales/ru.json';
import vi from '@/i18n/locales/vi.json';
import ko from '@/i18n/locales/ko.json';
import tr from '@/i18n/locales/tr.json';

const defaults = { region: 'us-east-1', authMethod: 'builder-id' as const, startUrl: '' };
const fixture = {
  accessToken: 'unused-fixture-access',
  refreshToken: 'fixture-refresh',
  clientId: 'fixture-client',
  clientSecret: 'fixture-secret',
};
const parse = (value: unknown) => parseKiroCredentials(JSON.stringify(value), defaults);
const entries = parse([fixture, { ...fixture, refreshToken: 'fixture-refresh-2' }]);
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

describe('Kiro credential JSON', () => {
  test('accepts the pasted four-field array and a single object without forwarding access tokens', () => {
    expect(parse([fixture])).toEqual(parse(fixture));
    expect(parse(fixture)[0]).toEqual({
      index: 1,
      refreshToken: fixture.refreshToken,
      clientId: fixture.clientId,
      clientSecret: fixture.clientSecret,
      ...defaults,
    });
    expect(JSON.stringify(parse(fixture))).not.toContain(fixture.accessToken);
    expect(parseKiroCredentials('\uFEFF' + JSON.stringify(fixture), defaults)).toHaveLength(1);
  });

  test('accepts snake_case, per-account regions and IAM Identity Center settings', () => {
    const [entry] = parse({
      refresh_token: 'fixture-token',
      client_id: 'fixture-id',
      client_secret: 'fixture-secret',
      region: 'eu-west-1',
      auth_method: 'idc',
      start_url: 'https://company.awsapps.com/start',
    });
    expect(entry.region).toBe('eu-west-1');
    expect(entry.authMethod).toBe('idc');
    expect(entry.startUrl).toBe('https://company.awsapps.com/start');
  });

  test('allows desktop tokens without registration but rejects incomplete AWS registration', () => {
    expect(parse({ refreshToken: 'aorAAAAAG-fixture-only' })[0].clientId).toBe('');
    for (const value of [
      { refreshToken: 'aws-fixture' },
      { ...fixture, clientId: '' },
      { ...fixture, clientSecret: null },
    ]) {
      expect(() => parse(value)).toThrow(KiroImportIssue);
    }
  });

  test('rejects malformed entries, duplicates and conflicting aliases before any requests', () => {
    for (const value of [
      null,
      'text',
      1,
      [],
      [fixture, null],
      { ...fixture, refreshToken: '' },
      { ...fixture, clientId: 9 },
      { ...fixture, refresh_token: 'conflicting-fixture' },
      [fixture, fixture],
      { ...fixture, authMethod: 'external_idp' },
      { ...fixture, region: 'https://other.invalid' },
      { ...fixture, authMethod: 'idc', startUrl: 'https://other.invalid/start' },
      { ...fixture, authMethod: 'idc', startUrl: 'https://company.awsapps.com/start?secret=x' },
    ]) {
      expect(() => parse(value)).toThrow(KiroImportIssue);
    }
    expect(parse({ ...fixture, refresh_token: fixture.refreshToken })).toHaveLength(1);
  });

  test('caps batches and reports only safe codes with the failing entry index', () => {
    for (const value of [
      Array.from({ length: 51 }, (_, i) => ({ ...fixture, refreshToken: `r-${i}` })),
      { ...fixture, clientSecret: 'x'.repeat(62 * 1024) },
    ])
      expect(() => parse(value)).toThrow();
    expect(() => parseKiroCredentials('x'.repeat(1024 * 1024 + 1), defaults)).toThrow();
    try {
      parseKiroCredentials('{"refreshToken":"private-content",oops}', defaults);
    } catch (error) {
      expect(String(error)).not.toContain('private-content');
    }
    try {
      parse([fixture, {}]);
    } catch (error) {
      expect(error).toMatchObject({ code: 'refresh', index: 2 });
    }
  });
});

function mockAPI(overrides: Partial<KiroImportAPI> = {}) {
  const calls: string[] = [];
  let nextState = 0;
  const api: KiroImportAPI = {
    check: async () => {
      calls.push('check');
      return true;
    },
    start: async () => {
      const state = `fixture-${++nextState}`;
      calls.push(`start:${state}`);
      return state;
    },
    connect: async (state) => {
      calls.push(`connect:${state}`);
    },
    status: async (state) => {
      calls.push(`status:${state}`);
      return 'ok';
    },
    cancel: async (state) => {
      calls.push(`cancel:${state}`);
    },
    ...overrides,
  };
  return { api, calls };
}

describe('Kiro import session lifecycle', () => {
  test('saves sequentially using separate sessions and prevents duplicate starts', async () => {
    const { api, calls } = mockAPI();
    const results: KiroImportResult[] = [];
    const run = createKiroImportRun(api, (result) => results.push(result));
    await Promise.all([run.run(entries), run.run(entries)]);
    expect(calls).toEqual([
      'check',
      'start:fixture-1',
      'connect:fixture-1',
      'status:fixture-1',
      'start:fixture-2',
      'connect:fixture-2',
      'status:fixture-2',
    ]);
    expect(results.map((row) => row.status)).toEqual([
      'importing',
      'success',
      'importing',
      'success',
    ]);
  });

  test('keeps partial success, cancels failed sessions, and never exposes provider secrets', async () => {
    const { api, calls } = mockAPI({
      connect: async (state) => {
        if (state === 'fixture-1') throw new Error('secret-provider-error');
      },
    });
    const results: KiroImportResult[] = [];
    await createKiroImportRun(api, (result) => results.push(result)).run(entries);
    expect(calls).toContain('cancel:fixture-1');
    expect(results).toContainEqual({ index: 1, status: 'failed', failure: 'connect' });
    expect(results).toContainEqual({ index: 2, status: 'success' });
    expect(JSON.stringify(results)).not.toContain('secret-provider-error');
  });

  test('does not send credentials to an unavailable or incompatible plugin', async () => {
    const { api, calls } = mockAPI({ check: async () => false });
    const results: KiroImportResult[] = [];
    await createKiroImportRun(api, (result) => results.push(result)).run(entries);
    expect(calls).toEqual([]);
    expect(results).toHaveLength(2);
    expect(results.every((row) => row.failure === 'unavailable')).toBe(true);
  });

  test('requires save confirmation, polls pending results, and reports uncertain saves without retries', async () => {
    let polls = 0;
    const { api, calls } = mockAPI({
      status: async () => {
        if (++polls === 1) return 'wait';
        throw new Error('lost-response');
      },
    });
    const results: KiroImportResult[] = [];
    await createKiroImportRun(
      api,
      (result) => results.push(result),
      async () => {}
    ).run(entries.slice(0, 1));
    expect(results.at(-1)).toEqual({ index: 1, status: 'uncertain', failure: 'uncertain' });
    expect(calls.filter((call) => call.startsWith('connect:'))).toHaveLength(1);
    expect(calls).toContain('cancel:fixture-1');
  });

  test('cleans up a late start after closing or switching connections, without sending credentials', async () => {
    const started = deferred<void>();
    const response = deferred<string>();
    const { api, calls } = mockAPI({
      start: () => {
        started.resolve();
        return response.promise;
      },
    });
    const results: KiroImportResult[] = [];
    const run = createKiroImportRun(api, (result) => results.push(result));
    const task = run.run(entries);
    await started.promise;
    run.cancel();
    response.resolve('late-fixture-state');
    await task;
    expect(calls).toEqual(['check', 'cancel:late-fixture-state']);
    expect(results).toEqual([{ index: 1, status: 'importing' }]);
  });

  test('stops the batch and discards late results when cancelled during connect', async () => {
    const connected = deferred<void>();
    const response = deferred<void>();
    const { api, calls } = mockAPI({
      connect: () => {
        connected.resolve();
        return response.promise;
      },
    });
    const results: KiroImportResult[] = [];
    const run = createKiroImportRun(api, (result) => results.push(result));
    const task = run.run(entries);
    await connected.promise;
    run.cancel();
    response.resolve();
    await task;
    expect(calls).toEqual(['check', 'start:fixture-1', 'cancel:fixture-1']);
    expect(results.some((row) => row.status === 'success')).toBe(false);
  });
});

test('Kiro import transport uses the original connection, POST bodies and declared plugin routes', async () => {
  apiClient.setConfig({ apiBase: 'https://original.invalid/cpa', managementKey: 'fixture-key' });
  const captured = apiClient.captureConnection();
  const capture = spyOn(apiClient, 'captureConnection').mockReturnValue(captured);
  const requests: Array<{ url: string; body: unknown }> = [];
  const adapter: AxiosAdapter = async (config) => {
    expect(config.baseURL).toBe('https://original.invalid/cpa/v8/management');
    expect(config.headers.Authorization).toBe('Bearer fixture-key');
    const url = config.url ?? '';
    requests.push({ url, body: config.data ? JSON.parse(config.data) : undefined });
    const data = url.endsWith('/kiro/status')
      ? { provider: 'kiro', auth: 'multi-method' }
      : url.endsWith('/auth-url')
        ? { state: 'fixture-state' }
        : url.endsWith('/connect')
          ? { status: 'connected' }
          : { status: 'ok' };
    return { data, status: 200, statusText: 'OK', headers: {}, config };
  };
  const originalGet = captured.get.bind(captured);
  const originalPost = captured.post.bind(captured);
  const originalDelete = captured.delete.bind(captured);
  spyOn(captured, 'get').mockImplementation((url, config) =>
    originalGet(url, { ...config, adapter })
  );
  spyOn(captured, 'post').mockImplementation((url, body, config) =>
    originalPost(url, body, { ...config, adapter })
  );
  spyOn(captured, 'delete').mockImplementation((url, config) =>
    originalDelete(url, { ...config, adapter })
  );
  try {
    const api = captureKiroImportAPI('https://original.invalid/cpa/v8/management');
    apiClient.setConfig({ apiBase: 'https://new.invalid', managementKey: 'new-fixture' });
    expect(await api.check()).toBe(true);
    const state = await api.start();
    await api.connect(state, entries[0]);
    expect(await api.status(state)).toBe('ok');
    await api.cancel(state);
    expect(requests.map((request) => request.url)).toEqual([
      'https://original.invalid/cpa/v0/management/plugins/kiro/status',
      '/oauth/auth-url',
      'https://original.invalid/cpa/v0/management/plugins/kiro/connect',
      '/oauth/status',
      '/oauth/session',
    ]);
    expect(requests[1].body).toEqual({ provider: 'kiro' });
    expect(requests[2].body).toEqual({
      state: 'fixture-state',
      method: 'refresh_token',
      refresh_auth_method: 'builder-id',
      refresh_token: fixture.refreshToken,
      client_id: fixture.clientId,
      client_secret: fixture.clientSecret,
      region: 'us-east-1',
    });
    expect(JSON.stringify(requests)).not.toContain(fixture.accessToken);
    expect(requests.every((request) => !request.url.includes(fixture.refreshToken))).toBe(true);
  } finally {
    capture.mockRestore();
    apiClient.setConfig({ apiBase: '', managementKey: '' });
  }
});

test('only the compatible enabled plugin receives the import option', () => {
  const plugin: PluginListEntry = {
    id: 'kiro-ha',
    path: '',
    configured: true,
    registered: true,
    enabled: true,
    effectiveEnabled: true,
    supportsOAuth: true,
    oauthProvider: 'kiro',
    supportsQuota: false,
    logo: '',
    configFields: [],
    menus: [],
    metadata: null,
  };
  expect(buildPluginOAuthProviderCards([plugin], '')[0]?.kiroJSONImport).toBe(true);
  expect(
    buildPluginOAuthProviderCards([{ ...plugin, id: 'another-kiro' }], '')[0]?.kiroJSONImport
  ).toBeUndefined();
  expect(buildPluginOAuthProviderCards([{ ...plugin, effectiveEnabled: false }], '')).toEqual([]);
});

for (const [lng, translation] of Object.entries({
  en,
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  ru,
  vi,
  ko,
  tr,
})) {
  test(`Kiro import form has localized labels and no required proxy (${lng})`, async () => {
    const i18n = createInstance();
    await i18n.init({ lng, resources: { [lng]: { translation } } });
    const markup = renderToStaticMarkup(
      createElement(
        I18nextProvider,
        { i18n },
        createElement(KiroImportDialog, {
          open: true,
          icon: '',
          onClose: () => {},
          onCredentialAdded: () => {},
          onViewAuthFiles: () => {},
        })
      )
    );
    expect(markup).toContain('<textarea');
    expect(markup).toContain('autoComplete="off"');
    expect(markup).not.toContain('proxy_url');
    expect(markup).not.toContain('kiro_import.');
    expect(markup).not.toContain(fixture.refreshToken);
    expect(Object.keys(translation.kiro_import).sort()).toEqual(Object.keys(en.kiro_import).sort());
  });
}
