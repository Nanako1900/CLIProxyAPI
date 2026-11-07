import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/Input';
import type { OAuthProxyValidation } from '../socksProxy';

export function OAuthProxyInput({
  value,
  error,
  onChange,
}: {
  value: string;
  error?: OAuthProxyValidation;
  onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <Input
      label={t('auth_login.socks_label')}
      type="password"
      value={value}
      onChange={(event) => onChange(event.target.value)}
      placeholder="socks5://user:password@host:1080"
      autoComplete="off"
      spellCheck={false}
      required
      hint={t('auth_login.socks_hint')}
      error={error ? t(`auth_login.socks_${error}`) : undefined}
    />
  );
}
