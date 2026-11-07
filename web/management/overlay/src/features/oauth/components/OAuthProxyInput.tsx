import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/Input';
import type { OAuthProxyValidation } from '../socksProxy';

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
  return (
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
      error={error ? t(`auth_login.socks_${error}`) : undefined}
    />
  );
}
