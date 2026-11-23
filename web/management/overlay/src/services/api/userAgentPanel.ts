import { apiClient } from './client';
import type { ClientProvider } from './clientUserAgents';
import { isRecord } from '@/utils/helpers';

export type ClaudeIdentityDraft = {
  userAgent: string;
  packageVersion: string;
  runtimeVersion: string;
  os: string;
  arch: string;
  timeout: string;
  timezone: string;
  stabilizeDeviceProfile: boolean;
  injectDiagnostics: boolean;
  clientRequestId: string;
  nativeEntrypoints: string;
  betaStrip: string;
  betaAppend: string;
};
export type UserAgentDraft = {
  claude: ClaudeIdentityDraft;
  codex: { userAgent: string; betaFeatures: string };
  gemini: { userAgent: string };
};
export type UserAgentProviderStatus = {
  provider: ClientProvider;
  mode: 'default' | 'auto' | 'fixed';
  configuredUserAgent: string;
  defaultUserAgent: string;
  resolvedUserAgent: string;
  baselineHeaders: Record<string, string>;
  billingVersion?: string;
  auto: {
    enabled: boolean;
    cachedVersion: string;
    registryURL: string;
    refreshIntervalSeconds: number;
    result: 'never' | 'checking' | 'ok' | 'failed' | 'cancelled';
    lastAttempt?: string;
    lastSuccess?: string;
  };
};
export type UserAgentRuntime = {
  providers: UserAgentProviderStatus[];
  overrides: {
    provider: ClientProvider;
    authIndex: string;
    name: string;
    source: 'config' | 'credential';
    userAgent: string;
    disabled: boolean;
  }[];
  overrideInventoryAvailable: boolean;
  homeMode: boolean;
  catalogAvailable: boolean;
  catalogPersistence: 'disk' | 'memory' | 'unavailable';
  catalogLimit: number;
};
export type UserAgentSnapshot = {
  draft: UserAgentDraft;
  etag: string;
  runtime: UserAgentRuntime | null;
};

const object = (value: unknown): Record<string, unknown> => (isRecord(value) ? value : {});
const text = (value: unknown): string => (typeof value === 'string' ? value : '');
const lines = (value: unknown): string =>
  Array.isArray(value) ? value.filter((item) => typeof item === 'string').join('\n') : '';
export const identityList = (value: string): string[] => [
  ...new Set(
    value
      .split(/[\n,]/)
      .map((item) => item.trim())
      .filter(Boolean)
  ),
];

const claudeFields = {
  userAgent: 'user-agent',
  packageVersion: 'package-version',
  runtimeVersion: 'runtime-version',
  os: 'os',
  arch: 'arch',
  timeout: 'timeout',
  timezone: 'timezone',
  stabilizeDeviceProfile: 'stabilize-device-profile',
  injectDiagnostics: 'inject-diagnostics',
  clientRequestId: 'client-request-id',
  nativeEntrypoints: 'native-entrypoints',
  betaStrip: 'anthropic-beta-strip',
  betaAppend: 'anthropic-beta-append',
} as const;
const listFields = new Set(['nativeEntrypoints', 'betaStrip', 'betaAppend']);
const providerFields = {
  claude: claudeFields,
  codex: { userAgent: 'user-agent', betaFeatures: 'beta-features' },
  gemini: { userAgent: 'user-agent' },
} as const;

export function readUserAgentDraft(config: Record<string, unknown>): UserAgentDraft {
  const upstream = object(config.upstream);
  const claude = object(object(upstream.claude)['header-defaults']);
  const codex = object(object(object(object(config.oauth).providers).codex)['header-defaults']);
  const gemini = object(object(upstream.gemini)['header-defaults']);
  return {
    claude: {
      userAgent: text(claude['user-agent']),
      packageVersion: text(claude['package-version']),
      runtimeVersion: text(claude['runtime-version']),
      os: text(claude.os),
      arch: text(claude.arch),
      timeout: text(claude.timeout),
      timezone: text(claude.timezone),
      stabilizeDeviceProfile: claude['stabilize-device-profile'] === true,
      injectDiagnostics: claude['inject-diagnostics'] !== false,
      clientRequestId: text(claude['client-request-id']) || 'on',
      nativeEntrypoints: lines(claude['native-entrypoints']),
      betaStrip: lines(claude['anthropic-beta-strip']),
      betaAppend: lines(claude['anthropic-beta-append']),
    },
    codex: { userAgent: text(codex['user-agent']), betaFeatures: text(codex['beta-features']) },
    gemini: { userAgent: text(gemini['user-agent']) },
  };
}

export function userAgentChanges(before: UserAgentDraft, after: UserAgentDraft) {
  const changes: { provider: ClientProvider; field: string; before: unknown; after: unknown }[] =
    [];
  for (const provider of ['claude', 'codex', 'gemini'] as const) {
    for (const [key, field] of Object.entries(providerFields[provider])) {
      const normalize = (draft: UserAgentDraft) => {
        const value = (draft[provider] as unknown as Record<string, string | boolean>)[key];
        return typeof value === 'string'
          ? listFields.has(key)
            ? identityList(value)
            : value.trim()
          : value;
      };
      const previous = normalize(before);
      const next = normalize(after);
      if (JSON.stringify(previous) !== JSON.stringify(next))
        changes.push({ provider, field, before: previous, after: next });
    }
  }
  return changes;
}

// Patch only edited leaves so unrelated and future header defaults survive a save.
export function userAgentConfigPatch(before: UserAgentDraft, after: UserAgentDraft) {
  const headers: Record<ClientProvider, Record<string, unknown>> = {
    claude: {},
    codex: {},
    gemini: {},
  };
  for (const change of userAgentChanges(before, after))
    headers[change.provider][change.field] = change.after;
  const upstream = Object.fromEntries(
    (['claude', 'gemini'] as const)
      .filter((provider) => Object.keys(headers[provider]).length)
      .map((provider) => [provider, { 'header-defaults': headers[provider] }])
  );
  return {
    ...(Object.keys(upstream).length
      ? { upstream }
      : {}),
    ...(Object.keys(headers.codex).length
      ? { oauth: { providers: { codex: { 'header-defaults': headers.codex } } } }
      : {}),
  };
}

export function isValidUserAgent(value: string): boolean {
  return value.length <= 1024 && !/[^\x20-\x7e]/.test(value);
}

export function validateUserAgentDraft(draft: UserAgentDraft): string | null {
  for (const { userAgent } of Object.values(draft)) {
    if (!isValidUserAgent(userAgent)) return 'invalid_ua';
  }
  for (const value of [
    draft.claude.packageVersion,
    draft.claude.runtimeVersion,
    draft.claude.os,
    draft.claude.arch,
    draft.claude.timeout,
    draft.codex.betaFeatures,
  ]) {
    if (Array.from(value).some((char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127))
      return 'invalid_header';
  }
  if (draft.claude.timezone.trim()) {
    try {
      new Intl.DateTimeFormat('en', { timeZone: draft.claude.timezone.trim() });
    } catch {
      return 'invalid_timezone';
    }
  }
  return null;
}

export function userAgentPanelConnection() {
  const revision = apiClient.getConnectionRevision();
  const client = apiClient.captureConnection();
  const assertCurrent = () => {
    if (revision !== apiClient.getConnectionRevision()) throw new Error('connection_changed');
  };
  return {
    async load(): Promise<UserAgentSnapshot> {
      assertCurrent();
      const [response, runtime] = await Promise.all([
        client.getRaw('/config'),
        client.get<UserAgentRuntime>('/client-user-agents/status').catch(() => null),
      ]);
      assertCurrent();
      const etag = response.headers.etag;
      if (typeof etag !== 'string' || !etag) throw new Error('missing_etag');
      return { draft: readUserAgentDraft(object(response.data)), etag, runtime };
    },
    async save(snapshot: UserAgentSnapshot, draft: UserAgentDraft) {
      assertCurrent();
      if (!snapshot.etag) throw new Error('missing_etag');
      await client.patch('/config', userAgentConfigPatch(snapshot.draft, draft), {
        headers: { 'If-Match': snapshot.etag },
      });
      assertCurrent();
    },
  };
}
