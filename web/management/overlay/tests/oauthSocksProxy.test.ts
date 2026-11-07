import { describe, expect, spyOn, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { apiClient } from '@/services/api/client';
import { oauthApi } from '@/services/api/oauth';
import {
  requiresOAuthSocksProxy,
  validateOAuthSocksProxy,
  validateOAuthLoginProxy,
} from '@/features/oauth/socksProxy';
import { OAuthFlowDialog } from '@/features/oauth/components/OAuthFlowDialog';
import type { ProviderFlowState } from '@/features/oauth/hooks/useOAuthFlows';
import type { i18n } from 'i18next';
import en from '@/i18n/locales/en.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';
import ru from '@/i18n/locales/ru.json';
import ko from '@/i18n/locales/ko.json';
import vi from '@/i18n/locales/vi.json';
import tr from '@/i18n/locales/tr.json';

function renderDialog(i18n: i18n, providerId: string, state: ProviderFlowState = {}) {
  return renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(OAuthFlowDialog, {
        open: true,
        providerId,
        heading: { title: providerId, caption: '', icon: '' },
        state,
        text: (suffix: string) => i18n.t(`auth_login.plugin_${suffix}`, { name: providerId }),
        supportsCallback: true,
        onStart: () => {},
        onProxyChange: () => {},
        onCancel: () => {},
        onCallbackChange: () => {},
        onCallbackSubmit: () => {},
        onViewAuthFiles: () => {},
        onClose: () => {},
      })
    )
  );
}

describe('OAuth account proxy policy', () => {
  test('accepts authenticated SOCKS, remote DNS, IPv6 and surrounding whitespace', () => {
    for (const value of [
      'socks5://localhost:1080',
      ' socks5h://user:p%40ss@proxy.example.test:65535/ ',
      'socks5://[::1]:1080',
    ])
      expect(validateOAuthSocksProxy(value)).toBeUndefined();
  });

  test('rejects missing proxies, bypasses, web proxies and invalid addresses before HTTP', () => {
    const post = spyOn(apiClient, 'post');
    try {
      for (const value of [
        '',
        '   ',
        'direct',
        'none',
        'http://host:1080',
        'https://host:1080',
        'socks4://host:1080',
        'socks5://host',
        'socks5://host:0',
        'socks5://host:65536',
        'socks5://host:1080/path',
        'socks5://host:1080?x=1',
        'socks5://host:1080#fragment',
        'socks5://host:\n1080',
        'socks5://user:%ZZ@host:1080',
      ]) {
        expect(validateOAuthSocksProxy(value)).toBeTruthy();
        expect(() => oauthApi.startAuth('codex', value)).toThrow();
      }
      expect(post).not.toHaveBeenCalled();
    } finally {
      post.mockRestore();
    }
  });

  test('requires SOCKS only for normalized built-in OAuth providers', () => {
    for (const provider of [
      'codex',
      ' CoDeX ',
      'claude',
      'anthropic',
      'antigravity',
      'kimi',
      'kimi-ai',
      'xai',
      'devin',
      'meta',
      'Muse',
    ]) {
      expect(requiresOAuthSocksProxy(provider)).toBe(true);
      expect(validateOAuthLoginProxy(provider, '')).toBe('required');
    }
    for (const provider of ['kiro', ' KIRO ', 'custom-plugin', 'codex-plugin']) {
      expect(requiresOAuthSocksProxy(provider)).toBe(false);
      expect(validateOAuthLoginProxy(provider, '')).toBeUndefined();
      expect(validateOAuthLoginProxy(provider, '   ')).toBeUndefined();
      expect(validateOAuthLoginProxy(provider, 'socks5://host:0')).toBe('invalid');
    }
  });

  test('starts plugin login without including an empty proxy override', async () => {
    const post = spyOn(apiClient, 'post').mockResolvedValue({
      url: 'https://example.test',
      state: 'plugin-state',
    });
    const signal = new AbortController().signal;
    try {
      await oauthApi.startAuth('kiro');
      expect(post).toHaveBeenLastCalledWith('/oauth/auth-url', { provider: 'kiro' }, undefined);
      await oauthApi.startAuth('custom-plugin', '   ', signal);
      expect(post).toHaveBeenLastCalledWith(
        '/oauth/auth-url',
        { provider: 'custom-plugin' },
        { signal }
      );
      expect(() => oauthApi.startAuth('kiro', 'socks5://host:0')).toThrow();
      expect(post).toHaveBeenCalledTimes(2);
    } finally {
      post.mockRestore();
    }
  });

  test('sends the account proxy only in the POST body for every OAuth provider', async () => {
    const post = spyOn(apiClient, 'post').mockResolvedValue({
      url: 'https://example.test',
      state: 'test',
    });
    const get = spyOn(apiClient, 'get');
    const signal = new AbortController().signal;
    const proxy = 'socks5h://user:fixture-password@proxy.example.test:1080';
    try {
      for (const provider of [
        'codex',
        'anthropic',
        'antigravity',
        'kimi',
        'kimi-ai',
        'xai',
        'devin',
        'meta',
        'custom-plugin',
      ]) {
        await oauthApi.startAuth(provider, ` ${proxy} `, signal);
        const [path, body, options] = post.mock.calls.at(-1)!;
        expect(path).toBe('/oauth/auth-url');
        expect(body).toMatchObject({
          provider: provider === 'anthropic' ? 'claude' : provider,
          proxy_url: proxy,
        });
        expect(options).toEqual({ signal });
      }
      expect(get).not.toHaveBeenCalled();
    } finally {
      post.mockRestore();
      get.mockRestore();
    }
  });

  for (const [lng, translation] of Object.entries({
    en,
    'zh-CN': zhCN,
    'zh-TW': zhTW,
    ru,
    ko,
    vi,
    tr,
  })) {
    test(`plugin proxy is optional and proxy-free success makes no binding claim (${lng})`, async () => {
      const i18n = createInstance();
      await i18n.init({ lng, resources: { [lng]: { translation } } });
      const markup = renderDialog(i18n, 'kiro');
      expect(markup).not.toMatch(/<input[^>]*required/);
      expect(markup).not.toMatch(/<button[^>]*type="submit"[^>]*disabled/);
      expect(markup).not.toContain('auth_login.');
      const success = renderDialog(i18n, 'kiro', { status: 'success', proxyBound: false });
      const bound = renderDialog(i18n, 'kiro', { status: 'success', proxyBound: true });
      const hint = renderToStaticMarkup(
        createElement('p', null, i18n.t('auth_login.socks_saved_hint'))
      ).slice(3, -4);
      expect(success).not.toContain(hint);
      expect(bound).toContain(hint);
    });

    test(`shows proxy setup before any authorization link (${lng})`, async () => {
      const i18n = createInstance();
      await i18n.init({ lng, resources: { [lng]: { translation } } });
      const markup = renderDialog(i18n, 'codex');
      expect(markup).toContain('type="password"');
      expect(markup).toContain('autoComplete="off"');
      expect(markup).toMatch(/<input[^>]*required/);
      expect(markup).toMatch(/<button[^>]*type="submit"[^>]*disabled/);
      expect(markup).not.toContain('auth.openai.com');
      expect(markup).not.toContain('auth_login.');
      for (const key of Object.keys(en.auth_login).filter((key) => key.startsWith('socks_'))) {
        expect((translation.auth_login as Record<string, string>)[key]?.trim()).toBeTruthy();
      }
    });
  }
});
