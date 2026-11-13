import { afterEach, expect, mock, spyOn, test } from 'bun:test';
import { AxiosError, type AxiosAdapter } from 'axios';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n';
import { apiClient } from '../src/services/api/client';
import {
  identityList,
  readUserAgentDraft,
  userAgentChanges,
  userAgentConfigPatch,
  userAgentPanelConnection,
  validateUserAgentDraft,
} from '../src/services/api/userAgentPanel';
import { UserAgentSettings } from '../src/features/userAgents/UserAgentSettings';
import { UserAgentRules } from '../src/features/userAgents/UserAgentRules';
import en from '../src/i18n/locales/en.json';
import zh from '../src/i18n/locales/zh-CN.json';
import tw from '../src/i18n/locales/zh-TW.json';
import ru from '../src/i18n/locales/ru.json';
import vi from '../src/i18n/locales/vi.json';
import ko from '../src/i18n/locales/ko.json';
import tr from '../src/i18n/locales/tr.json';

const config = {
  upstream: {
    claude: {
      'header-defaults': {
        'user-agent': 'auto',
        'future-field': 'keep',
        'inject-diagnostics': false,
        'native-entrypoints': ['custom'],
      },
    },
  },
  oauth: { providers: { codex: { 'header-defaults': { 'user-agent': 'codex_cli_rs/1.2.3' } } } },
  'api-keys': {
    claude: [{ 'api-key': 'private-key', headers: { 'User-Agent': 'credential-override' } }],
  },
  access: { 'api-keys': ['client-secret'] },
};

afterEach(() => mock.restore());

test('draft contains only identity settings and patches only edited v8 leaves', () => {
  const before = readUserAgentDraft(config);
  const after = structuredClone(before);
  expect(before.claude.injectDiagnostics).toBe(false);
  expect(before.claude.clientRequestId).toBe('on');
  expect(before.claude.nativeEntrypoints).toBe('custom');
  expect(JSON.stringify(before)).not.toContain('private-key');
  expect(JSON.stringify(before)).not.toContain('client-secret');
  after.claude.userAgent = '';
  after.claude.betaAppend = ' first, second\nfirst \n';
  after.codex.betaFeatures = 'enabled';
  expect(userAgentConfigPatch(before, after)).toEqual({
    upstream: {
      claude: {
        'header-defaults': { 'user-agent': '', 'anthropic-beta-append': ['first', 'second'] },
      },
    },
    oauth: { providers: { codex: { 'header-defaults': { 'beta-features': 'enabled' } } } },
  });
  expect(userAgentChanges(before, after)).toHaveLength(3);
  expect(userAgentConfigPatch(before, before)).toEqual({});
  expect(config.upstream.claude['header-defaults']['future-field']).toBe('keep');
  expect(config.upstream.claude['header-defaults']['user-agent']).toBe('auto');
  expect(identityList(' first, second\nfirst ')).toEqual(['first', 'second']);
});

test('validates UA and HTTP values and accepts an empty or IANA timezone', () => {
  const draft = readUserAgentDraft({});
  expect(validateUserAgentDraft(draft)).toBeNull();
  draft.claude.userAgent = 'bad\r\nInjected: yes';
  expect(validateUserAgentDraft(draft)).toBe('invalid_ua');
  draft.claude.userAgent = 'auto';
  draft.claude.runtimeVersion = 'v26.0.0\n';
  expect(validateUserAgentDraft(draft)).toBe('invalid_header');
  draft.claude.runtimeVersion = 'v26.0.0';
  draft.claude.timezone = 'Not/AZone';
  expect(validateUserAgentDraft(draft)).toBe('invalid_timezone');
  draft.claude.timezone = 'Asia/Singapore';
  expect(validateUserAgentDraft(draft)).toBeNull();
});

function transport(adapter: AxiosAdapter) {
  apiClient.setConfig({ apiBase: 'https://original.invalid/cpa', managementKey: 'dummy-key' });
  const client = apiClient.captureConnection();
  spyOn(apiClient, 'captureConnection').mockReturnValue(client);
  const get = client.get.bind(client);
  const raw = client.getRaw.bind(client);
  const patch = client.patch.bind(client);
  spyOn(client, 'get').mockImplementation((url, options) => get(url, { ...options, adapter }));
  spyOn(client, 'getRaw').mockImplementation((url, options) => raw(url, { ...options, adapter }));
  spyOn(client, 'patch').mockImplementation((url, body, options) =>
    patch(url, body, { ...options, adapter })
  );
  return userAgentPanelConnection();
}

test('uses captured v8 connection and If-Match, without retrying conflicts', async () => {
  let writes = 0;
  const api = transport(async (request) => {
    expect(request.baseURL).toBe('https://original.invalid/cpa/v8/management');
    expect(request.headers.Authorization).toBe('Bearer dummy-key');
    if (request.method === 'patch') {
      writes++;
      expect(request.headers['If-Match']).toBe('"revision"');
      expect(JSON.parse(request.data)).toEqual({
        upstream: { claude: { 'header-defaults': { 'user-agent': '' } } },
      });
      throw new AxiosError('conflict', 'ERR_BAD_RESPONSE', request, undefined, {
        data: { error: 'conflict' },
        status: 412,
        statusText: 'Precondition Failed',
        headers: {},
        config: request,
      });
    }
    return {
      data: request.url === '/config' ? config : { providers: [], overrides: [] },
      status: 200,
      statusText: 'OK',
      headers: { etag: '"revision"' },
      config: request,
    };
  });
  const snapshot = await api.load();
  const draft = structuredClone(snapshot.draft);
  draft.claude.userAgent = '';
  await expect(api.save(snapshot, draft)).rejects.toMatchObject({ status: 412 });
  expect(writes).toBe(1);
  expect(draft.claude.userAgent).toBe('');
  apiClient.setConfig({ apiBase: 'https://other.invalid', managementKey: 'other-key' });
  await expect(api.save(snapshot, draft)).rejects.toThrow('connection_changed');
  expect(writes).toBe(1);
});

test('missing ETag prevents writes; old status endpoints degrade without v0 fallback', async () => {
  const urls: string[] = [];
  let etag = '';
  const api = transport(async (request) => {
    urls.push(request.url || '');
    if (request.url === '/client-user-agents/status')
      throw new AxiosError('missing', 'ERR_BAD_RESPONSE', request, undefined, {
        data: { error: 'missing' },
        status: 404,
        statusText: 'Not Found',
        headers: {},
        config: request,
      });
    return { data: config, status: 200, statusText: 'OK', headers: { etag }, config: request };
  });
  await expect(api.load()).rejects.toThrow('missing_etag');
  etag = '"revision"';
  const snapshot = await api.load();
  expect(snapshot.runtime).toBeNull();
  await expect(api.save({ ...snapshot, etag: '' }, snapshot.draft)).rejects.toThrow('missing_etag');
  expect(urls.every((url) => url === '/config' || url === '/client-user-agents/status')).toBe(true);
});

test('rejects stale reads even when the connection switches away and back', async () => {
  let finish!: () => void;
  const ready = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const api = transport(async (request) => {
    await ready;
    return {
      data: request.url === '/config' ? config : { providers: [] },
      status: 200,
      statusText: 'OK',
      headers: { etag: '"revision"' },
      config: request,
    };
  });
  const loading = api.load();
  apiClient.setConfig({ apiBase: 'https://other.invalid', managementKey: 'other-key' });
  apiClient.setConfig({ apiBase: 'https://original.invalid/cpa', managementKey: 'dummy-key' });
  finish();
  await expect(loading).rejects.toThrow('connection_changed');
});

test('all seven locales cover the panel, with disabled controls and complete rule labels', async () => {
  const languages = { en, 'zh-CN': zh, 'zh-TW': tw, ru, vi, ko, tr };
  const previous = i18n.language;
  try {
    for (const [language, resource] of Object.entries(languages)) {
      expect(Object.keys(resource.ua_panel).sort()).toEqual(Object.keys(en.ua_panel).sort());
      await i18n.changeLanguage(language);
      const markup = renderToStaticMarkup(
        createElement(UserAgentSettings, {
          provider: 'claude',
          draft: readUserAgentDraft({}),
          disabled: true,
          onChange: () => {},
        })
      );
      expect(markup).toContain('disabled');
      expect(markup).toContain('User-Agent');
      expect(markup).not.toContain('ua_panel.');
      const rules = renderToStaticMarkup(createElement(UserAgentRules, { provider: 'claude' }));
      expect(rules).toContain('X-Anthropic-Additional-Protection');
      expect(rules).toContain('metadata.user_id');
      expect(rules).not.toContain('ua_panel.');
    }
  } finally {
    await i18n.changeLanguage(previous);
  }
});
