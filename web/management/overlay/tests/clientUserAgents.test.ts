import { describe, expect, test } from 'bun:test';
import {
  clientProfileDraft,
  extractIdentityHeaders,
  geminiUserAgentLabels,
  userAgentDraft,
} from '../src/features/config/clientUserAgents';
import type { ClientUserAgent } from '../src/services/api/clientUserAgents';

describe('client UA settings', () => {
  test('strips secrets and body before import, preserving duplicate headers for validation', () => {
    expect(
      extractIdentityHeaders(
        'POST /v1/messages HTTP/1.1\r\nAuthorization: Bearer secret\r\nCookie: session=secret\r\nUser-Agent: claude-cli/2.1.319\r\nx-stainless-os: Linux\r\nuser-agent: claude-cli/2.1.318\r\n\r\nUser-Agent: codex/1.2.3',
        'claude'
      )
    ).toBe('User-Agent: claude-cli/2.1.319\nx-stainless-os: Linux\nuser-agent: claude-cli/2.1.318');
    expect(extractIdentityHeaders('POST / HTTP/1.0\n\nUser-Agent: codex/1.2.3', 'codex')).toBe('');
    expect(
      extractIdentityHeaders(
        '\nUser-Agent: codex/1.2.3\nX-Codex-Beta-Features: multi_agent\nX-Api-Key: secret',
        'codex'
      )
    ).toBe('User-Agent: codex/1.2.3\nX-Codex-Beta-Features: multi_agent');
  });

  const sample: ClientUserAgent = {
    id: 'sample',
    provider: 'claude',
    version: '2.1.319',
    firstSeen: '',
    lastSeen: '',
    count: 1,
    observed: true,
    imported: false,
    headers: {
      'User-Agent': 'claude-cli/2.1.319',
      'X-Stainless-Os': 'Linux',
      'X-Stainless-Timeout': '600',
      Authorization: 'secret',
      'X-Codex-Beta-Features': 'multi_agent',
    },
  };

  test('applies only supported fields supplied by the chosen provider', () => {
    expect(clientProfileDraft(sample)).toEqual({
      claudeHeaderUserAgent: 'claude-cli/2.1.319',
      claudeHeaderOs: 'Linux',
      claudeHeaderTimeout: '600',
    });
    expect(
      clientProfileDraft({
        ...sample,
        provider: 'codex',
        headers: { ...sample.headers, 'User-Agent': 'codex/1.2.3' },
      })
    ).toEqual({ codexHeaderUserAgent: 'codex/1.2.3', codexHeaderBetaFeatures: 'multi_agent' });
  });

  test('UA-only, auto and native selections leave all other configuration untouched', () => {
    expect(userAgentDraft('claude', 'auto')).toEqual({ claudeHeaderUserAgent: 'auto' });
    expect(userAgentDraft('codex', '')).toEqual({ codexHeaderUserAgent: '' });
    expect(userAgentDraft('codex', 'codex/1.2.3')).toEqual({ codexHeaderUserAgent: 'codex/1.2.3' });
  });

  test('identifies Go SDK sample versions separately from legacy Gemini CLI sample versions', () => {
    const cli: ClientUserAgent = {
      ...sample,
      provider: 'gemini',
      version: '0.70.0',
      headers: { 'User-Agent': 'GeminiCLI/0.70.0/gemini-flash' },
    };
    const sdkUserAgent =
      'google-genai-sdk/1.79.1 gl-go/go1.28-20261102-RC01 cl/1012564305 +298f53e483 X:fieldtrack,boringcrypto,nogolistexportnewformat';
    const sdk: ClientUserAgent = {
      ...cli,
      clientKind: 'google-genai-go',
      version: '1.79.1',
      headers: { 'User-Agent': sdkUserAgent },
    };
    expect(geminiUserAgentLabels(cli)).toEqual({ client: 'gemini_cli', version: 'client_version' });
    expect(geminiUserAgentLabels(sdk)).toEqual({ client: 'gemini_go_sdk', version: 'sdk_version' });
    expect(clientProfileDraft(sdk)).toEqual({ geminiHeaderUserAgent: sdkUserAgent });
    expect(extractIdentityHeaders(`User-Agent: ${sdkUserAgent}`, 'gemini')).toBe(
      `User-Agent: ${sdkUserAgent}`
    );
  });

  test('Gemini imports and applies only User-Agent', () => {
    const userAgent = 'GeminiCLI/0.34.0/gemini-2.5-pro (darwin; arm64; terminal)';
    expect(
      extractIdentityHeaders(
        `POST /v1beta/models/gemini HTTP/1.1\r\nAuthorization: Bearer secret\r\nCookie: a=secret\r\nUser-Agent: ${userAgent}\r\nx-goog-api-client: private\r\nX-Stainless-Os: Linux\r\nX-Codex-Beta-Features: private\r\n\r\nUser-Agent: fake/1.2.3`,
        'gemini'
      )
    ).toBe(`User-Agent: ${userAgent}`);
    expect(
      clientProfileDraft({
        ...sample,
        provider: 'gemini',
        headers: { ...sample.headers, 'User-Agent': userAgent, 'X-Goog-Api-Client': 'private' },
      })
    ).toEqual({ geminiHeaderUserAgent: userAgent });
    expect(userAgentDraft('gemini', 'auto')).toEqual({ geminiHeaderUserAgent: 'auto' });
    expect(userAgentDraft('gemini', '')).toEqual({ geminiHeaderUserAgent: '' });
  });
});
