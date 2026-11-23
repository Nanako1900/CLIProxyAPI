import type { ClientProvider, ClientUserAgent } from '@/services/api/clientUserAgents';
import type { VisualConfigValues } from '@/types/visualConfig';

export type ClientIdentityPatch = Partial<VisualConfigValues> & { geminiHeaderUserAgent?: string };

export const clientProviderNames: Record<ClientProvider, string> = {
  claude: 'Claude Code',
  codex: 'Codex',
  gemini: 'Gemini',
};

export function geminiUserAgentLabels(entry: ClientUserAgent) {
  return entry.clientKind === 'google-genai-go'
    ? { client: 'gemini_go_sdk', version: 'sdk_version' }
    : { client: 'gemini_cli', version: 'client_version' };
}

const fields = {
  claude: {
    'User-Agent': 'claudeHeaderUserAgent',
    'X-Stainless-Package-Version': 'claudeHeaderPackageVersion',
    'X-Stainless-Runtime-Version': 'claudeHeaderRuntimeVersion',
    'X-Stainless-Os': 'claudeHeaderOs',
    'X-Stainless-Arch': 'claudeHeaderArch',
    'X-Stainless-Timeout': 'claudeHeaderTimeout',
  },
  codex: {
    'User-Agent': 'codexHeaderUserAgent',
    'X-Codex-Beta-Features': 'codexHeaderBetaFeatures',
  },
  gemini: { 'User-Agent': 'geminiHeaderUserAgent' },
} as const satisfies Record<ClientProvider, Record<string, keyof ClientIdentityPatch>>;

// Strip unrelated headers and the body locally before contacting the management API.
export function extractIdentityHeaders(raw: string, provider: ClientProvider): string {
  const allowed = new Set(Object.keys(fields[provider]).map((header) => header.toLowerCase()));
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
): ClientIdentityPatch {
  return { [fields[provider]['User-Agent']]: userAgent };
}

export function clientProfileDraft(entry: ClientUserAgent): ClientIdentityPatch {
  const result: Record<string, string> = {};
  for (const [header, field] of Object.entries(fields[entry.provider])) {
    const value = entry.headers[header];
    if (typeof value === 'string' && value.trim()) result[field] = value;
  }
  return result;
}
