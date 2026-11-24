import { apiClient } from './client';
import { isRecord } from '@/utils/helpers';
import type { AuthFileErrorSummary, AuthFileItem, AuthFileRefreshError } from '@/types/authFile';
import {
  getAuthFileStatusMessage,
  hasAuthFileStatusWarning,
} from '@/features/authFiles/constants';

export interface AuthFileErrorLookup {
  name: string;
  authIndex?: string | number | null;
}

export interface AuthFileModelErrorItem {
  model: string;
  status: string;
  unavailable: boolean;
  statusMessage?: string;
  code?: string;
  httpStatus?: number;
  message?: string;
  nextRetryAfter?: string;
  quotaReason?: string;
  updatedAt?: string;
}

export interface AuthFileErrorRecord {
  id: string;
  timestamp: string;
  requestId?: string;
  phase: string;
  provider?: string;
  model?: string;
  routeModel?: string;
  httpStatus?: number;
  code?: string;
  message: string;
  retryable?: boolean;
  credentialScope?: boolean;
  cooldownUntil?: string;
  hasRequestLog?: boolean;
}

export interface AuthFileErrorsResponse {
  observedAt?: string;
  authId: string;
  authIndex?: string;
  name: string;
  provider?: string;
  email?: string;
  status: string;
  statusMessage?: string;
  disabled: boolean;
  unavailable: boolean;
  nextRetryAfter?: string;
  requiresSignIn: boolean;
  recoveryHint?: string;
  tosAppealUrl?: string;
  tosAppealUrlText?: string;
  success: number;
  failed: number;
  lastError?: AuthFileErrorSummary;
  refreshError?: AuthFileRefreshError;
  modelErrors: AuthFileModelErrorItem[];
  recentErrors: AuthFileErrorRecord[];
  recentErrorCount: number;
}

export interface AuthFileCardErrorPreview {
  text: string;
  httpStatus?: number;
  recentCount: number;
  clickable: boolean;
  showBanner: boolean;
}

const readStr = (obj: Record<string, unknown>, ...keys: string[]): string => {
  for (const key of keys) {
    const val = obj[key];
    if (typeof val === 'string' && val.trim()) {
      return val.trim();
    }
  }
  return '';
};

const readNum = (obj: Record<string, unknown>, ...keys: string[]): number | undefined => {
  for (const key of keys) {
    const val = obj[key];
    if (typeof val === 'number' && Number.isFinite(val)) {
      return val;
    }
    if (typeof val === 'string' && val.trim()) {
      const parsed = Number(val.trim());
      if (Number.isFinite(parsed)) {
        return parsed;
      }
    }
  }
  return undefined;
};

const readBool = (obj: Record<string, unknown>, ...keys: string[]): boolean => {
  for (const key of keys) {
    const val = obj[key];
    if (typeof val === 'boolean') return val;
    if (typeof val === 'string') {
      const normalized = val.trim().toLowerCase();
      if (normalized === 'true' || normalized === '1') return true;
      if (normalized === 'false' || normalized === '0') return false;
    }
  }
  return false;
};

export const normalizeAuthFileErrorSummary = (value: unknown): AuthFileErrorSummary | undefined => {
  if (!isRecord(value)) return undefined;
  const message = readStr(value, 'message');
  const code = readStr(value, 'code');
  const httpStatus = readNum(value, 'http_status', 'httpStatus');
  if (!message && !code && !httpStatus) return undefined;
  return {
    message: message || code || (httpStatus ? `HTTP ${httpStatus}` : ''),
    ...(code ? { code } : {}),
    ...(httpStatus && httpStatus > 0 ? { httpStatus } : {}),
    ...(readStr(value, 'model') ? { model: readStr(value, 'model') } : {}),
    ...(readStr(value, 'phase') ? { phase: readStr(value, 'phase') } : {}),
    ...(readStr(value, 'timestamp') ? { timestamp: readStr(value, 'timestamp') } : {}),
    ...(readBool(value, 'retryable') ? { retryable: true } : {}),
  };
};

export const normalizeAuthFileRefreshError = (value: unknown): AuthFileRefreshError | undefined => {
  if (!isRecord(value)) return undefined;
  const message = readStr(value, 'message');
  const code = readStr(value, 'code');
  const httpStatus = readNum(value, 'http_status', 'httpStatus');
  if (!message && !code && !httpStatus) return undefined;
  return {
    message: message || code || (httpStatus ? `HTTP ${httpStatus}` : ''),
    ...(code ? { code } : {}),
    ...(httpStatus && httpStatus > 0 ? { httpStatus } : {}),
  };
};

const normalizeModelError = (value: unknown): AuthFileModelErrorItem | null => {
  if (!isRecord(value)) return null;
  const model = readStr(value, 'model');
  if (!model) return null;
  const httpStatus = readNum(value, 'http_status', 'httpStatus');
  return {
    model,
    status: readStr(value, 'status') || 'error',
    unavailable: readBool(value, 'unavailable'),
    ...(readStr(value, 'status_message', 'statusMessage')
      ? { statusMessage: readStr(value, 'status_message', 'statusMessage') }
      : {}),
    ...(readStr(value, 'code') ? { code: readStr(value, 'code') } : {}),
    ...(httpStatus && httpStatus > 0 ? { httpStatus } : {}),
    ...(readStr(value, 'message') ? { message: readStr(value, 'message') } : {}),
    ...(readStr(value, 'next_retry_after', 'nextRetryAfter')
      ? { nextRetryAfter: readStr(value, 'next_retry_after', 'nextRetryAfter') }
      : {}),
    ...(readStr(value, 'quota_reason', 'quotaReason')
      ? { quotaReason: readStr(value, 'quota_reason', 'quotaReason') }
      : {}),
    ...(readStr(value, 'updated_at', 'updatedAt')
      ? { updatedAt: readStr(value, 'updated_at', 'updatedAt') }
      : {}),
  };
};

const normalizeErrorRecord = (value: unknown, index: number): AuthFileErrorRecord | null => {
  if (!isRecord(value)) return null;
  const message = readStr(value, 'message');
  const code = readStr(value, 'code');
  const httpStatus = readNum(value, 'http_status', 'httpStatus');
  if (!message && !code && !httpStatus) return null;
  return {
    id: readStr(value, 'id') || `err-${index}`,
    timestamp: readStr(value, 'timestamp'),
    phase: readStr(value, 'phase') || 'execute',
    message: message || code || (httpStatus ? `HTTP ${httpStatus}` : ''),
    ...(readStr(value, 'request_id', 'requestId')
      ? { requestId: readStr(value, 'request_id', 'requestId') }
      : {}),
    ...(readStr(value, 'provider') ? { provider: readStr(value, 'provider') } : {}),
    ...(readStr(value, 'model') ? { model: readStr(value, 'model') } : {}),
    ...(readStr(value, 'route_model', 'routeModel')
      ? { routeModel: readStr(value, 'route_model', 'routeModel') }
      : {}),
    ...(httpStatus && httpStatus > 0 ? { httpStatus } : {}),
    ...(code ? { code } : {}),
    ...(readBool(value, 'retryable') ? { retryable: true } : {}),
    ...(readBool(value, 'credential_scope', 'credentialScope') ? { credentialScope: true } : {}),
    ...(readStr(value, 'cooldown_until', 'cooldownUntil')
      ? { cooldownUntil: readStr(value, 'cooldown_until', 'cooldownUntil') }
      : {}),
    ...(readBool(value, 'has_request_log', 'hasRequestLog') ? { hasRequestLog: true } : {}),
  };
};

export const normalizeAuthFileErrorsResponse = (data: unknown): AuthFileErrorsResponse => {
  const obj = isRecord(data) ? data : {};
  const rawModelErrors = Array.isArray(obj.model_errors)
    ? obj.model_errors
    : Array.isArray(obj.modelErrors)
      ? obj.modelErrors
      : [];
  const rawRecentErrors = Array.isArray(obj.recent_errors)
    ? obj.recent_errors
    : Array.isArray(obj.recentErrors)
      ? obj.recentErrors
      : [];

  const modelErrors = rawModelErrors
    .map(normalizeModelError)
    .filter((item): item is AuthFileModelErrorItem => item !== null);
  const recentErrors = rawRecentErrors
    .map(normalizeErrorRecord)
    .filter((item): item is AuthFileErrorRecord => item !== null);

  return {
    observedAt: readStr(obj, 'observed_at', 'observedAt') || undefined,
    authId: readStr(obj, 'auth_id', 'authId'),
    authIndex: readStr(obj, 'auth_index', 'authIndex') || undefined,
    name: readStr(obj, 'name'),
    provider: readStr(obj, 'provider') || undefined,
    email: readStr(obj, 'email') || undefined,
    status: readStr(obj, 'status') || 'active',
    statusMessage: readStr(obj, 'status_message', 'statusMessage') || undefined,
    disabled: readBool(obj, 'disabled'),
    unavailable: readBool(obj, 'unavailable'),
    nextRetryAfter: readStr(obj, 'next_retry_after', 'nextRetryAfter') || undefined,
    requiresSignIn: readBool(obj, 'requires_sign_in', 'requiresSignIn'),
    recoveryHint: readStr(obj, 'recovery_hint', 'recoveryHint') || undefined,
    tosAppealUrl: readStr(obj, 'tos_appeal_url', 'tosAppealUrl') || undefined,
    tosAppealUrlText: readStr(obj, 'tos_appeal_url_text', 'tosAppealUrlText') || undefined,
    success: readNum(obj, 'success') ?? 0,
    failed: readNum(obj, 'failed') ?? 0,
    lastError: normalizeAuthFileErrorSummary(obj.last_error ?? obj.lastError),
    refreshError: normalizeAuthFileRefreshError(obj.refresh_error ?? obj.refreshError),
    modelErrors,
    recentErrors,
    recentErrorCount: readNum(obj, 'recent_error_count', 'recentErrorCount') ?? recentErrors.length,
  };
};

export const buildAuthFileErrorLookupParams = (
  lookup: AuthFileErrorLookup,
  limit?: number
): Record<string, string | number> => {
  const params: Record<string, string | number> = {};
  const name = lookup.name.trim();
  if (name) {
    params.name = name;
  }
  const authIndex =
    typeof lookup.authIndex === 'string'
      ? lookup.authIndex.trim()
      : typeof lookup.authIndex === 'number' && Number.isFinite(lookup.authIndex)
        ? String(lookup.authIndex)
        : '';
  if (authIndex) {
    params.auth_index = authIndex;
  }
  if (typeof limit === 'number' && Number.isInteger(limit) && limit > 0) {
    params.limit = limit;
  }
  return params;
};

export const isSafeExternalHttpUrl = (raw?: string): boolean => {
  if (!raw) return false;
  try {
    const parsed = new URL(raw);
    return parsed.protocol === 'https:' || parsed.protocol === 'http:';
  } catch {
    return false;
  }
};

export const resolveAuthFileCardErrorPreview = (
  file: AuthFileItem,
  fallbackErrorLabel: string
): AuthFileCardErrorPreview => {
  const statusMsg = getAuthFileStatusMessage(file);
  const hasWarning = hasAuthFileStatusWarning(file);
  const refreshMsg = file.refreshError?.message?.trim() ?? '';
  const lastErrMsg = file.lastError?.message?.trim() ?? '';
  const normalizedStatus = typeof file.status === 'string' ? file.status.trim().toLowerCase() : '';
  const isErrorState =
    file.disabled !== true &&
    normalizedStatus !== 'disabled' &&
    (file.unavailable === true ||
      normalizedStatus === 'error' ||
      file.requiresSignIn === true ||
      hasWarning ||
      Boolean(refreshMsg));

  const recentCount =
    typeof file.recentErrorCount === 'number' && Number.isFinite(file.recentErrorCount)
      ? Math.max(0, Math.floor(file.recentErrorCount))
      : 0;
  const failureCount = file.failureCount ?? 0;

  const text =
    (hasWarning && statusMsg) ||
    refreshMsg ||
    (isErrorState ? lastErrMsg || fallbackErrorLabel : '');

  const httpStatus = file.refreshError?.httpStatus ?? file.lastError?.httpStatus;
  const showBanner = Boolean(text);
  const clickable = showBanner || recentCount > 0 || failureCount > 0 || Boolean(lastErrMsg);

  return {
    text,
    httpStatus,
    recentCount,
    clickable,
    showBanner,
  };
};

export const authFileErrorsApi = {
  async fetch(
    lookup: AuthFileErrorLookup,
    options: { limit?: number; signal?: AbortSignal } = {}
  ): Promise<AuthFileErrorsResponse> {
    const data = await apiClient.get('/credentials/errors', {
      params: buildAuthFileErrorLookupParams(lookup, options.limit),
      signal: options.signal,
    });
    return normalizeAuthFileErrorsResponse(data);
  },

  async clear(lookup: AuthFileErrorLookup): Promise<void> {
    await apiClient.delete('/credentials/errors', {
      params: buildAuthFileErrorLookupParams(lookup),
    });
  },
};
