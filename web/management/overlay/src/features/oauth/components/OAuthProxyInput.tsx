import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { oauthApi } from '@/services/api/oauth';
import { validateOAuthSocksProxy, type OAuthProxyValidation } from '../socksProxy';

function ProxyIPCheck({ value, disabled }: { value: string; disabled: boolean }) {
  const { t } = useTranslation();
  const request = useRef<AbortController | null>(null);
  const [status, setStatus] = useState<'idle' | 'checking' | 'failed' | { ip: string }>('idle');
  useEffect(() => () => request.current?.abort(), []);

  const check = async () => {
    if (disabled || request.current) return;
    const controller = new AbortController();
    request.current = controller;
    setStatus('checking');
    try {
      const { ip } = await oauthApi.checkProxyIP(value, controller.signal);
      if (!controller.signal.aborted) setStatus(ip ? { ip } : 'failed');
    } catch {
      if (!controller.signal.aborted) setStatus('failed');
    } finally {
      request.current = null;
    }
  };

  return (
    <div className="form-group">
      <Button
        type="button"
        variant="secondary"
        size="sm"
        disabled={disabled}
        loading={status === 'checking'}
        onClick={() => void check()}
      >
        {t(status === 'checking' ? 'auth_login.socks_ip_checking' : 'auth_login.socks_ip_check')}
      </Button>
      <div role="status" aria-live="polite">
        {typeof status === 'object' && (
          <span className="hint">{t('auth_login.socks_ip_result', { ip: status.ip })}</span>
        )}
        {status === 'failed' && (
          <span className="error-box">{t('auth_login.socks_ip_failed')}</span>
        )}
      </div>
    </div>
  );
}

export function OAuthProxyInput({
  value,
  error,
  onChange,
  required = true,
}: {
  value: string;
  error?: OAuthProxyValidation;
  onChange: (value: string) => void;
  required?: boolean;
}) {
  const { t } = useTranslation();
  const validation = validateOAuthSocksProxy(value);
  const inputError = error ?? (validation === 'invalid' ? validation : undefined);
  return (
    <>
      <Input
        label={t(required ? 'auth_login.socks_label' : 'auth_login.socks_optional_label')}
        type="password"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder="socks5://user:password@host:1080"
        autoComplete="off"
        spellCheck={false}
        required={required}
        hint={t(required ? 'auth_login.socks_hint' : 'auth_login.socks_optional_hint')}
        error={inputError ? t(`auth_login.socks_${inputError}`) : undefined}
      />
      <ProxyIPCheck key={value} value={value} disabled={Boolean(validation)} />
    </>
  );
}
