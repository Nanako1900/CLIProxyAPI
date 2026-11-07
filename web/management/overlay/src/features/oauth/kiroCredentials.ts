export const KIRO_IMPORT_MAX_BYTES = 1024 * 1024;
export const KIRO_IMPORT_MAX_ACCOUNTS = 50;

export interface KiroImportDefaults {
  region: string;
  authMethod: 'builder-id' | 'idc';
  startUrl: string;
}

export interface KiroCredential extends KiroImportDefaults {
  index: number;
  refreshToken: string;
  clientId: string;
  clientSecret: string;
}

export type KiroImportIssueCode =
  | 'json'
  | 'shape'
  | 'limit'
  | 'field'
  | 'refresh'
  | 'registration'
  | 'region'
  | 'start_url'
  | 'method'
  | 'duplicate';

export class KiroImportIssue extends Error {
  constructor(
    public readonly code: KiroImportIssueCode,
    public readonly index = 0,
    public readonly field = ''
  ) {
    // JSON parser errors may contain credential fragments; expose only fixed codes.
    super(code);
  }
}

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

/** Parse locally and forward only the fields understood by refresh-token login. */
export function parseKiroCredentials(raw: string, defaults: KiroImportDefaults): KiroCredential[] {
  if (new TextEncoder().encode(raw).length > KIRO_IMPORT_MAX_BYTES)
    throw new KiroImportIssue('limit');
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw.trim().replace(/^\uFEFF/, ''));
  } catch {
    throw new KiroImportIssue('json');
  }
  const entries: unknown[] = Array.isArray(parsed) ? parsed : [parsed];
  if (!entries.length) throw new KiroImportIssue('shape');
  if (entries.length > KIRO_IMPORT_MAX_ACCOUNTS) throw new KiroImportIssue('limit');
  const seen = new Set<string>();
  return entries.map((value, offset) => {
    const index = offset + 1;
    if (!isObject(value)) throw new KiroImportIssue('shape', index);
    const read = (camel: string, snake = camel): string => {
      const values = [...new Set([camel, snake])].map((key) => value[key]);
      if (values.some((item) => item !== undefined && item !== null && typeof item !== 'string'))
        throw new KiroImportIssue('field', index, camel);
      const strings = values
        .filter((item): item is string => typeof item === 'string')
        .map((item) => item.trim())
        .filter(Boolean);
      if (new Set(strings).size > 1) throw new KiroImportIssue('field', index, camel);
      return strings[0] ?? '';
    };
    const refreshToken = read('refreshToken', 'refresh_token');
    const clientId = read('clientId', 'client_id');
    const clientSecret = read('clientSecret', 'client_secret');
    if (!refreshToken) throw new KiroImportIssue('refresh', index);
    if (
      Boolean(clientId) !== Boolean(clientSecret) ||
      (!clientId && !refreshToken.startsWith('aorAAAAAG'))
    )
      throw new KiroImportIssue('registration', index);
    const region = read('region') || defaults.region.trim();
    if (!/^[a-z]{2}(?:-[a-z]+)+-\d+$/.test(region)) throw new KiroImportIssue('region', index);
    const startUrl = read('startUrl', 'start_url') || defaults.startUrl.trim();
    const method = read('authMethod', 'auth_method') || (startUrl ? 'idc' : defaults.authMethod);
    if (method !== 'builder-id' && method !== 'idc') throw new KiroImportIssue('method', index);
    if (method === 'idc') {
      let url: URL;
      try {
        url = new URL(startUrl);
      } catch {
        throw new KiroImportIssue('start_url', index);
      }
      if (
        url.protocol !== 'https:' ||
        url.username ||
        url.password ||
        url.port ||
        url.search ||
        url.hash ||
        !url.hostname.endsWith('.awsapps.com') ||
        !/^\/start\/?$/.test(url.pathname)
      )
        throw new KiroImportIssue('start_url', index);
    }
    if (seen.has(refreshToken)) throw new KiroImportIssue('duplicate', index);
    seen.add(refreshToken);
    const credential: KiroCredential = {
      index,
      refreshToken,
      clientId,
      clientSecret,
      region,
      authMethod: method,
      startUrl: method === 'idc' ? startUrl : '',
    };
    if (new TextEncoder().encode(JSON.stringify(credential)).length > 60 * 1024)
      throw new KiroImportIssue('limit', index);
    return credential;
  });
}
