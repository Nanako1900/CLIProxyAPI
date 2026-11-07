export type OAuthProxyValidation = 'required' | 'invalid';

/** Validate the proxy before a login attempt can send any management request. */
export function validateOAuthSocksProxy(value: string): OAuthProxyValidation | undefined {
  const proxyUrl = value.trim();
  if (!proxyUrl) return 'required';
  if (
    /\s/.test(proxyUrl) ||
    [...proxyUrl].some(
      (character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127
    )
  )
    return 'invalid';
  try {
    const url = new URL(proxyUrl);
    if (
      !['socks5:', 'socks5h:'].includes(url.protocol) ||
      !url.hostname ||
      !/^\d+$/.test(url.port) ||
      Number(url.port) < 1 ||
      Number(url.port) > 65535 ||
      (url.pathname !== '' && url.pathname !== '/') ||
      url.search ||
      url.hash
    )
      return 'invalid';
    decodeURIComponent(url.username);
    decodeURIComponent(url.password);
  } catch {
    return 'invalid';
  }
  return undefined;
}
