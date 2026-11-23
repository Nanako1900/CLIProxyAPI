import { apiClient } from './client';

export type ClientProvider = 'claude' | 'codex' | 'gemini';
export type ClientUserAgent = {
  id: string;
  provider: ClientProvider;
  clientKind?: 'google-genai-go';
  version: string;
  headers: Record<string, string>;
  firstSeen: string;
  lastSeen: string;
  count: number;
  observed: boolean;
  imported: boolean;
};

export type ClientUserAgentCaptureStats = {
  inspected: number;
  unrecognized: number;
};

export type ClientUserAgentCatalog = {
  items: ClientUserAgent[];
  captureStats?: ClientUserAgentCaptureStats;
};

// Pin each operation to its originating authenticated connection.
export function clientUserAgentsConnection() {
  const client = apiClient.captureConnection();
  return {
    list: (provider: ClientProvider) =>
      client.get<ClientUserAgentCatalog>('/client-user-agents', { params: { provider } }),
    import: (provider: ClientProvider, headers: string) =>
      client.post<ClientUserAgent>('/client-user-agents', { headers }, { params: { provider } }),
    remove: (provider: ClientProvider, id: string) =>
      client.delete(`/client-user-agents/${encodeURIComponent(id)}`, { params: { provider } }),
  };
}
