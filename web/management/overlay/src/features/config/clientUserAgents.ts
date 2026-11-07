import type { ClientProvider, ClientUserAgent } from '@/services/api/clientUserAgents';
import type { VisualConfigValues } from '@/types/visualConfig';

const fields: Record<string, keyof VisualConfigValues> = {
  'User-Agent': 'claudeHeaderUserAgent',
  'X-Stainless-Package-Version': 'claudeHeaderPackageVersion',
  'X-Stainless-Runtime-Version': 'claudeHeaderRuntimeVersion',
  'X-Stainless-Os': 'claudeHeaderOs',
  'X-Stainless-Arch': 'claudeHeaderArch',
  'X-Stainless-Timeout': 'claudeHeaderTimeout',
};
const allowed = new Set(
  [...Object.keys(fields), 'X-Codex-Beta-Features'].map((key) => key.toLowerCase())
);

// Strip unrelated headers and the body locally before contacting the management API.
export function extractIdentityHeaders(raw: string): string {
  const result: string[] = [];
  let started = false;
  for (const line of raw.split(/\r?\n/)) {
    if (!line.trim()) {
      if (started) break;
      continue;
    }
    started = true;
    const separator = line.indexOf(':');
    if (separator < 0) continue;
    const name = line.slice(0, separator).trim();
    if (allowed.has(name.toLowerCase())) {
      result.push(`${name}: ${line.slice(separator + 1).trim()}`);
    }
  }
  return result.join('\n');
}

export function userAgentDraft(
  provider: ClientProvider,
  userAgent: string
): Partial<VisualConfigValues> {
  return provider === 'claude'
    ? { claudeHeaderUserAgent: userAgent }
    : { codexHeaderUserAgent: userAgent };
}

export function clientProfileDraft(entry: ClientUserAgent): Partial<VisualConfigValues> {
  const result: Record<string, string> = {};
  const mapping =
    entry.provider === 'claude'
      ? fields
      : {
          'User-Agent': 'codexHeaderUserAgent',
          'X-Codex-Beta-Features': 'codexHeaderBetaFeatures',
        };
  for (const [header, field] of Object.entries(mapping)) {
    const value = entry.headers[header];
    if (typeof value === 'string' && value.trim()) result[field] = value;
  }
  return result;
}
