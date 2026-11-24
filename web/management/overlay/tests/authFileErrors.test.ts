import { afterEach, describe, expect, mock, spyOn, test } from 'bun:test';
import { apiClient } from '../src/services/api/client';
import {
  authFileErrorsApi,
  buildAuthFileErrorLookupParams,
  isSafeExternalHttpUrl,
  normalizeAuthFileErrorsResponse,
  resolveAuthFileCardErrorPreview,
} from '../src/services/api/authFileErrors';
import en from '../src/i18n/locales/en.json';
import zhCN from '../src/i18n/locales/zh-CN.json';
import zhTW from '../src/i18n/locales/zh-TW.json';
import ko from '../src/i18n/locales/ko.json';
import ru from '../src/i18n/locales/ru.json';
import tr from '../src/i18n/locales/tr.json';
import vi from '../src/i18n/locales/vi.json';

afterEach(() => mock.restore());

describe('authFileErrors service', () => {
  test('normalizes snake_case backend response including model and recent errors', () => {
    const normalized = normalizeAuthFileErrorsResponse({
      observed_at: '2026-11-24T12:00:00Z',
      auth_id: 'claude-oauth.json',
      auth_index: 'idx-1',
      name: 'claude-oauth.json',
      provider: 'claude',
      email: 'dev@example.com',
      status: 'error',
      status_message: 'quota exceeded',
      unavailable: true,
      requires_sign_in: true,
      recovery_hint: 'Re-authenticate',
      tos_appeal_url: 'https://example.com/appeal',
      last_error: {
        code: 'rate_limit_error',
        http_status: 429,
        message: 'Too many requests',
        retryable: true,
      },
      refresh_error: {
        code: 'invalid_grant',
        http_status: 401,
        message: 'refresh token expired',
      },
      model_errors: [
        {
          model: 'claude-sonnet-4',
          status: 'error',
          unavailable: true,
          http_status: 429,
          quota_reason: 'quota',
          message: 'quota reached',
          next_retry_after: '2026-11-24T13:00:00Z',
        },
      ],
      recent_errors: [
        {
          id: 'err-1',
          timestamp: '2026-11-24T11:59:00Z',
          request_id: 'req-abc',
          phase: 'stream',
          model: 'claude-sonnet-4',
          http_status: 429,
          code: 'rate_limit_error',
          message: 'upstream 429',
          has_request_log: true,
        },
      ],
      recent_error_count: 1,
    });

    expect(normalized.authId).toBe('claude-oauth.json');
    expect(normalized.authIndex).toBe('idx-1');
    expect(normalized.requiresSignIn).toBe(true);
    expect(normalized.lastError?.httpStatus).toBe(429);
    expect(normalized.refreshError?.code).toBe('invalid_grant');
    expect(normalized.modelErrors).toHaveLength(1);
    expect(normalized.modelErrors[0].quotaReason).toBe('quota');
    expect(normalized.recentErrors).toHaveLength(1);
    expect(normalized.recentErrors[0].hasRequestLog).toBe(true);
    expect(normalized.recentErrorCount).toBe(1);
  });

  test('calls v8 credentials/errors GET and DELETE with name and auth_index', async () => {
    const getSpy = spyOn(apiClient, 'get').mockResolvedValue({
      auth_id: 'acct.json',
      name: 'acct.json',
      status: 'active',
      recent_errors: [],
      recent_error_count: 0,
    });
    const delSpy = spyOn(apiClient, 'delete').mockResolvedValue({ status: 'ok' });

    const res = await authFileErrorsApi.fetch({ name: 'acct.json', authIndex: 'idx-9' }, { limit: 20 });
    expect(res.name).toBe('acct.json');
    expect(getSpy).toHaveBeenCalledWith('/credentials/errors', {
      params: { name: 'acct.json', auth_index: 'idx-9', limit: 20 },
      signal: undefined,
    });

    await authFileErrorsApi.clear({ name: 'acct.json', authIndex: 'idx-9' });
    expect(delSpy).toHaveBeenCalledWith('/credentials/errors', {
      params: { name: 'acct.json', auth_index: 'idx-9' },
    });
  });

  test('resolves clickable card error preview across active warnings, refresh errors, and historical failures', () => {
    const warningCard = resolveAuthFileCardErrorPreview(
      { name: 'a.json', status: 'error', statusMessage: 'unauthorized', unavailable: true },
      'Account error'
    );
    expect(warningCard.showBanner).toBe(true);
    expect(warningCard.clickable).toBe(true);
    expect(warningCard.text).toBe('unauthorized');

    const refreshErrCard = resolveAuthFileCardErrorPreview(
      {
        name: 'b.json',
        status: 'active',
        refreshError: { code: 'invalid_grant', httpStatus: 401, message: 'token revoked' },
      },
      'Account error'
    );
    expect(refreshErrCard.showBanner).toBe(true);
    expect(refreshErrCard.text).toBe('token revoked');
    expect(refreshErrCard.httpStatus).toBe(401);

    const recoveredWithHistory = resolveAuthFileCardErrorPreview(
      {
        name: 'c.json',
        status: 'active',
        unavailable: false,
        failureCount: 0,
        recentErrorCount: 3,
        lastError: { message: 'previous 429' },
      },
      'Account error'
    );
    expect(recoveredWithHistory.showBanner).toBe(false);
    expect(recoveredWithHistory.clickable).toBe(true);
    expect(recoveredWithHistory.recentCount).toBe(3);

    const healthy = resolveAuthFileCardErrorPreview(
      { name: 'd.json', status: 'active', unavailable: false, failureCount: 0 },
      'Account error'
    );
    expect(healthy.showBanner).toBe(false);
    expect(healthy.clickable).toBe(false);
  });

  test('validates external appeal URLs and lookup params safely', () => {
    expect(isSafeExternalHttpUrl('https://example.com/appeal')).toBe(true);
    expect(isSafeExternalHttpUrl('javascript:alert(1)')).toBe(false);
    expect(buildAuthFileErrorLookupParams({ name: ' kiro.json ', authIndex: ' idx-2 ' })).toEqual({
      name: 'kiro.json',
      auth_index: 'idx-2',
    });
  });

  test('provides auth file error translations in all seven locales', () => {
    const requiredKeys = [
      'error_modal_title',
      'error_click_to_inspect',
      'error_status_fallback',
      'error_modal_current_section',
      'error_modal_models_section',
      'error_modal_history_section',
      'error_modal_empty_title',
      'error_modal_clear',
    ];
    for (const locale of [en, zhCN, zhTW, ko, ru, tr, vi]) {
      const af = locale.auth_files as Record<string, string>;
      for (const key of requiredKeys) {
        expect(typeof af[key]).toBe('string');
        expect(af[key].trim().length).toBeGreaterThan(0);
      }
    }
  });
});
