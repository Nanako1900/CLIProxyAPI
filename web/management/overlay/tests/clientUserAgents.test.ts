import { describe, expect, test } from 'bun:test';
import {
  clientProfileDraft,
  extractIdentityHeaders,
  userAgentDraft,
} from '../src/features/config/clientUserAgents';
import type { ClientUserAgent } from '../src/services/api/clientUserAgents';

describe('client UA settings', () => {
  test('strips secrets and body before import, preserving duplicate headers for validation', () => {
    expect(
      extractIdentityHeaders(
        'POST /v1/messages HTTP/1.1\r\nAuthorization: Bearer secret\r\nCookie: session=secret\r\nUser-Agent: claude-cli/2.1.319\r\nx-stainless-os: Linux\r\nuser-agent: claude-cli/2.1.318\r\n\r\nUser-Agent: codex/1.2.3'
      )
    ).toBe('User-Agent: claude-cli/2.1.319\nx-stainless-os: Linux\nuser-agent: claude-cli/2.1.318');
    expect(extractIdentityHeaders('POST / HTTP/1.0\n\nUser-Agent: codex/1.2.3')).toBe('');
    expect(
      extractIdentityHeaders(
        '\nUser-Agent: codex/1.2.3\nX-Codex-Beta-Features: multi_agent\nX-Api-Key: secret'
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
});
