import { apiClient } from './client';
import { normalizeApiBase } from '@/utils/connection';
import type { PluginListEntry } from '@/types';
import type { KiroImportAPI } from '@/features/oauth/kiroImportRun';

export function supportsKiroJSONImport(plugin: PluginListEntry): boolean {
  const repository = plugin.metadata?.githubRepository.replace(/\/$/, '').toLowerCase();
  return (
    plugin.effectiveEnabled &&
    plugin.supportsOAuth &&
    (plugin.id === 'kiro-ha' || repository === 'https://github.com/nguyenha935/kiro-cpa-plugin')
  );
}

/** Pin all calls, including cleanup, to the server/key that opened the import. */
export function captureKiroImportAPI(apiBase: string): KiroImportAPI {
  const client = apiClient.captureConnection();
  // This is an existing plugin-declared extension, not a legacy CPA API fallback.
  const pluginBase = `${normalizeApiBase(apiBase)}/v0/management/plugins/kiro`;
  return {
    check: async () => {
      const result = await client.get<{ provider?: string; auth?: string }>(`${pluginBase}/status`);
      return result.provider === 'kiro' && result.auth === 'multi-method';
    },
    start: async () => {
      const result = await client.post<{ state?: string }>('/oauth/auth-url', { provider: 'kiro' });
      if (!result.state || !/^[a-zA-Z0-9_-]{1,128}$/.test(result.state))
        throw new Error('Invalid Kiro login session');
      return result.state;
    },
    connect: async (state, credential) => {
      const result = await client.post<{ status?: string }>(
        `${pluginBase}/connect`,
        {
          state,
          method: 'refresh_token',
          refresh_auth_method: credential.authMethod,
          refresh_token: credential.refreshToken,
          client_id: credential.clientId,
          client_secret: credential.clientSecret,
          region: credential.region,
          ...(credential.startUrl ? { start_url: credential.startUrl } : {}),
        },
        { timeout: 120000 }
      );
      if (result.status !== 'connected') throw new Error('Kiro credential was not connected');
    },
    status: async (state) => {
      const result = await client.get<{ status?: string }>('/oauth/status', { params: { state } });
      if (result.status !== 'ok' && result.status !== 'wait' && result.status !== 'error')
        throw new Error('Invalid Kiro login status');
      return result.status;
    },
    cancel: (state) => client.delete('/oauth/session', { params: { state } }),
  };
}
