import { afterEach, expect, mock, spyOn, test } from 'bun:test';
import type { AxiosAdapter } from 'axios';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n';
import { apiClient } from '../src/services/api/client';
import {
  filterShadowEvents,
  quotaShadowConnection,
  readShadowSettings,
  shadowAgreementPercent,
  shadowSettingsPatch,
  validShadowSettings,
  type ShadowEvent,
  type ShadowStatus,
} from '../src/services/api/quotaShadow';
import { ShadowResults } from '../src/features/quotaShadow/ShadowResults';
import en from '../src/i18n/locales/en.json';
import zh from '../src/i18n/locales/zh-CN.json';
import tw from '../src/i18n/locales/zh-TW.json';
import ru from '../src/i18n/locales/ru.json';
import vi from '../src/i18n/locales/vi.json';
import ko from '../src/i18n/locales/ko.json';
import tr from '../src/i18n/locales/tr.json';

afterEach(() => mock.restore());

test('settings normalize defaults and patch only the shadow fields', () => {
  expect(readShadowSettings({})).toEqual({
    enabled: false,
    file: '',
    maxFileMB: 256,
    softLimit: 90,
  });
  const config = {
    routing: {
      strategy: 'round-robin',
      'quota-shadow': { enabled: true, 'max-file-mb': 0, 'soft-limit-percent': 0 },
    },
    access: { 'api-keys': ['private'] },
  };
  const settings = readShadowSettings(config);
  settings.file = ' logs/shadow.jsonl ';
  expect(shadowSettingsPatch(settings)).toEqual({
    routing: {
      'quota-shadow': {
        enabled: true,
        file: 'logs/shadow.jsonl',
        'max-file-mb': 256,
        'soft-limit-percent': 90,
      },
    },
  });
  expect(config.routing.strategy).toBe('round-robin');
  for (const invalid of [
    { maxFileMB: -1 },
    { maxFileMB: 65537 },
    { maxFileMB: 0.5 },
    { maxFileMB: NaN },
    { softLimit: Infinity },
    { softLimit: -1 },
    { softLimit: 101 },
    { file: 'bad\npath' },
    { file: 'bad\0path' },
    { file: '文'.repeat(1366) },
  ]) {
    expect(validShadowSettings({ ...settings, ...invalid })).toBe(false);
    expect(() => shadowSettingsPatch({ ...settings, ...invalid })).toThrow('invalid_settings');
  }
  expect(validShadowSettings({ ...settings, maxFileMB: 0, softLimit: 0 })).toBe(true);
});

function transport(adapter: AxiosAdapter) {
  apiClient.setConfig({ apiBase: 'https://shadow.invalid/cpa', managementKey: 'test-key' });
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
  return quotaShadowConnection();
}

test('transport pins the authenticated connection and never retries conflicts', async () => {
  const calls: string[] = [];
  const api = transport(async (request) => {
    expect(request.baseURL).toBe('https://shadow.invalid/cpa/v8/management');
    expect(request.headers.Authorization).toBe('Bearer test-key');
    calls.push(`${request.method} ${request.url}`);
    if (request.method === 'patch') {
      expect(request.headers['If-Match']).toBe('"revision"');
      expect(Object.keys(JSON.parse(request.data))).toEqual(['routing']);
      throw Object.assign(new Error('conflict'), { status: 412 });
    }
    return {
      data: {},
      status: 200,
      statusText: 'OK',
      headers: { etag: '"revision"' },
      config: request,
    };
  });
  const snapshot = await api.settings();
  await api.status();
  await expect(api.save(snapshot.settings, snapshot.etag)).rejects.toThrow();
  expect(calls).toEqual(['get /config', 'get /observability/quota-shadow', 'patch /config']);
  apiClient.setConfig({ apiBase: 'https://other.invalid', managementKey: 'other' });
  await expect(api.status()).rejects.toThrow('connection_changed');
  await expect(api.save(snapshot.settings, snapshot.etag)).rejects.toThrow('connection_changed');
  expect(calls).toHaveLength(3);
});

test('missing ETags prevent unsafe settings saves', async () => {
  const api = transport(async (request) => ({
    data: {},
    status: 200,
    statusText: 'OK',
    headers: {},
    config: request,
  }));
  await expect(api.settings()).rejects.toThrow('missing_etag');
  await expect(api.save(readShadowSettings({}), '')).rejects.toThrow('missing_etag');
});

test.each(['settings', 'status'] as const)(
  'late %s responses are discarded after an ABA connection switch',
  async (method) => {
    let finish!: () => void;
    const ready = new Promise<void>((resolve) => {
      finish = resolve;
    });
    const api = transport(async (request) => {
      await ready;
      return {
        data: {},
        status: 200,
        statusText: 'OK',
        headers: { etag: '"revision"' },
        config: request,
      };
    });
    const pending = api[method]();
    apiClient.setConfig({ apiBase: 'https://other.invalid', managementKey: 'other' });
    apiClient.setConfig({ apiBase: 'https://shadow.invalid/cpa', managementKey: 'test-key' });
    finish();
    await expect(pending).rejects.toThrow('connection_changed');
  }
);

const event: ShadowEvent = {
  type: 'selection',
  ts_ms: 1000,
  pool_id: 'basic',
  model: 'demo',
  actual: 'account-a',
  shadow: 'account-b',
  reason: 'soft_limit',
  candidates: 2,
  actual_in_pool: true,
  agree: false,
  success: false,
  status: 0,
};
const status: ShadowStatus = {
  state: 'recording',
  since: '',
  file: 'shadow.jsonl',
  max_file_mb: 256,
  soft_limit_percent: 90,
  processed: 5,
  written: 4,
  write_errors: 1,
  render_errors: 0,
  dropped: 2,
  queue_pending: 0,
  queue_capacity: 1024,
  selections: 4,
  comparable: 2,
  agreements: 1,
  disagreements: 1,
  structural: 1,
  no_candidates: 1,
  selection_errors: 0,
  observations: 1,
  successes: 1,
  failures: 0,
  last_error: '',
  recent_limit: 200,
  recent: [
    event,
    { ...event, ts_ms: 2000, pool_id: 'premium', actual: 'foreign-account', actual_in_pool: false },
    { ...event, ts_ms: 3000, type: 'observation', shadow: '', success: true },
  ],
};

test('agreement excludes structural differences and pool filters do not mutate session data', () => {
  expect(shadowAgreementPercent(status)).toBe(50);
  expect(shadowAgreementPercent({ agreements: 0, comparable: 0 })).toBeNull();
  expect(filterShadowEvents(status.recent, 'basic', '*').map((item) => item.ts_ms)).toEqual([
    3000, 1000,
  ]);
  expect(filterShadowEvents(status.recent, '*', 'observation')).toHaveLength(1);
  expect(filterShadowEvents(status.recent, '', '*')).toHaveLength(0);
  expect(status.recent[0].ts_ms).toBe(1000);
});

test.each(['en', 'zh-CN'])(
  'results render meaningful comparisons and pool-filtered events (%s)',
  async (language) => {
    const previous = i18n.language;
    try {
      await i18n.changeLanguage(language);
      const markup = renderToStaticMarkup(
        createElement(ShadowResults, {
          status,
          poolId: 'basic',
          type: '*',
          onPool() {},
          onType() {},
        })
      );
      expect(markup).toContain('50.0%');
      expect(markup).toContain('account-a');
      expect(markup).not.toContain('foreign-account');
      expect(markup).toContain('aria-label=');
      expect(markup).not.toContain('quota_shadow.');
    } finally {
      await i18n.changeLanguage(previous);
    }
  }
);

test('all supported locales have complete shadow labels and interpolation variables', () => {
  const variables = (value: string) => value.match(/\{\{\w+\}\}/g)?.sort() ?? [];
  for (const locale of [zh, tw, ru, vi, ko, tr]) {
    expect(Object.keys(locale.quota_shadow).sort()).toEqual(Object.keys(en.quota_shadow).sort());
    for (const key of Object.keys(en.quota_shadow) as (keyof typeof en.quota_shadow)[]) {
      expect(variables(locale.quota_shadow[key])).toEqual(variables(en.quota_shadow[key]));
    }
  }
});
