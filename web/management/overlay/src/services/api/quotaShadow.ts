import { apiClient } from './client';
import { isRecord } from '@/utils/helpers';

export type ShadowSettings = {
  enabled: boolean;
  file: string;
  maxFileMB: number;
  softLimit: number;
};
export type ShadowEvent = {
  type: 'selection' | 'selection_error' | 'observation';
  ts_ms: number;
  pool_id: string;
  model: string;
  actual: string;
  shadow: string;
  reason: string;
  candidates: number;
  actual_in_pool: boolean;
  agree: boolean;
  success: boolean;
  status: number;
};
export type ShadowStatus = {
  state: 'disabled' | 'home' | 'stopped' | 'draining' | 'error' | 'idle' | 'recording';
  since: string;
  file: string;
  max_file_mb: number;
  soft_limit_percent: number;
  processed: number;
  written: number;
  write_errors: number;
  render_errors: number;
  dropped: number;
  queue_pending: number;
  queue_capacity: number;
  selections: number;
  comparable: number;
  agreements: number;
  disagreements: number;
  structural: number;
  no_candidates: number;
  selection_errors: number;
  observations: number;
  successes: number;
  failures: number;
  last_error: string;
  recent_limit: number;
  recent: ShadowEvent[];
};

export function readShadowSettings(value: unknown): ShadowSettings {
  const root = isRecord(value) ? value : {};
  const routing = isRecord(root.routing) ? root.routing : {};
  const source = isRecord(routing['quota-shadow']) ? routing['quota-shadow'] : {};
  return {
    enabled: source.enabled === true,
    file: typeof source.file === 'string' ? source.file : '',
    maxFileMB:
      typeof source['max-file-mb'] === 'number' && source['max-file-mb'] !== 0
        ? source['max-file-mb']
        : 256,
    softLimit:
      typeof source['soft-limit-percent'] === 'number' && source['soft-limit-percent'] !== 0
        ? source['soft-limit-percent']
        : 90,
  };
}

export function validShadowSettings(value: ShadowSettings): boolean {
  return (
    Number.isInteger(value.maxFileMB) &&
    value.maxFileMB >= 0 &&
    value.maxFileMB <= 65536 &&
    Number.isFinite(value.softLimit) &&
    value.softLimit >= 0 &&
    value.softLimit <= 100 &&
    new TextEncoder().encode(value.file).length <= 4096 &&
    !/[\0\r\n]/.test(value.file)
  );
}

export function shadowSettingsPatch(value: ShadowSettings) {
  if (!validShadowSettings(value)) throw new Error('invalid_settings');
  return {
    routing: {
      'quota-shadow': {
        enabled: value.enabled,
        file: value.file.trim(),
        'max-file-mb': value.maxFileMB,
        'soft-limit-percent': value.softLimit,
      },
    },
  };
}

export function shadowAgreementPercent(
  status: Pick<ShadowStatus, 'agreements' | 'comparable'>
): number | null {
  return status.comparable > 0 ? (status.agreements / status.comparable) * 100 : null;
}

export function filterShadowEvents(
  events: ShadowEvent[],
  poolId: string,
  type: string
): ShadowEvent[] {
  return events
    .filter(
      (event) =>
        (poolId === '*' || event.pool_id === poolId) && (type === '*' || event.type === type)
    )
    .slice()
    .reverse();
}

export function quotaShadowConnection() {
  const revision = apiClient.getConnectionRevision();
  const client = apiClient.captureConnection();
  const assertCurrent = () => {
    if (revision !== apiClient.getConnectionRevision()) throw new Error('connection_changed');
  };
  return {
    async settings() {
      assertCurrent();
      const response = await client.getRaw('/config');
      assertCurrent();
      const etag = response.headers.etag;
      if (typeof etag !== 'string' || !etag) throw new Error('missing_etag');
      return { settings: readShadowSettings(response.data), etag };
    },
    async status() {
      assertCurrent();
      const status = await client.get<ShadowStatus>('/observability/quota-shadow');
      assertCurrent();
      return status;
    },
    async save(settings: ShadowSettings, etag: string) {
      assertCurrent();
      if (!etag) throw new Error('missing_etag');
      await client.patch('/config', shadowSettingsPatch(settings), {
        headers: { 'If-Match': etag },
      });
      assertCurrent();
    },
  };
}
