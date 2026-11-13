import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type { ClientProvider } from '@/services/api/clientUserAgents';
import type {
  UserAgentDraft,
  UserAgentProviderStatus,
  ClaudeIdentityDraft,
} from '@/services/api/userAgentPanel';
import styles from './UserAgentsPage.module.scss';

type Props = {
  provider: ClientProvider;
  draft: UserAgentDraft;
  status?: UserAgentProviderStatus;
  disabled: boolean;
  onChange: (draft: UserAgentDraft) => void;
};

export function UserAgentSettings({ provider, draft, status, disabled, onChange }: Props) {
  const { t } = useTranslation();
  const current = draft[provider];
  const mode = !current.userAgent.trim()
    ? 'default'
    : current.userAgent.trim().toLowerCase() === 'auto'
      ? 'auto'
      : 'fixed';
  const setUA = (userAgent: string) =>
    onChange({ ...draft, [provider]: { ...current, userAgent } });
  const setClaude = <K extends keyof ClaudeIdentityDraft>(key: K, value: ClaudeIdentityDraft[K]) =>
    onChange({ ...draft, claude: { ...draft.claude, [key]: value } });
  const fields = [
    ['packageVersion', 'X-Stainless-Package-Version'],
    ['runtimeVersion', 'X-Stainless-Runtime-Version'],
    ['os', 'X-Stainless-OS'],
    ['arch', 'X-Stainless-Arch'],
    ['timeout', 'X-Stainless-Timeout'],
    ['timezone', ''],
  ] as const;
  return (
    <Card
      title={t('ua_panel.settings')}
      extra={<span className={styles.eyebrow}>{t('ua_panel.draft')}</span>}
    >
      <div className={styles.stack}>
        <div className={styles.segmented} role="group" aria-label={t('ua_panel.mode')}>
          {(['default', 'auto', 'fixed'] as const).map((value) => (
            <Button
              key={value}
              variant={mode === value ? 'primary' : 'ghost'}
              disabled={disabled}
              aria-pressed={mode === value}
              onClick={() =>
                setUA(
                  value === 'default'
                    ? ''
                    : value === 'auto'
                      ? 'auto'
                      : status?.resolvedUserAgent || status?.defaultUserAgent || current.userAgent
                )
              }
            >
              {t('ua_panel.mode_' + value)}
            </Button>
          ))}
        </div>
        <Input
          label="User-Agent"
          value={current.userAgent}
          disabled={disabled}
          spellCheck={false}
          placeholder={t('ua_panel.default_placeholder')}
          hint={t('ua_panel.ua_hint')}
          onChange={(event) => setUA(event.target.value)}
        />
        {mode === 'auto' && <p className={styles.note}>{t('ua_panel.auto_scope')}</p>}
        <p className={styles.muted}>{t('ua_panel.scope_' + provider)}</p>
        {provider === 'claude' ? (
          <>
            <div className={styles.formGrid}>
              {fields.map(([key, header]) => (
                <Input
                  key={key}
                  label={t('ua_panel.field_' + key)}
                  value={draft.claude[key]}
                  disabled={disabled}
                  spellCheck={false}
                  placeholder={header ? status?.baselineHeaders[header] : 'Asia/Singapore'}
                  hint={t('ua_panel.hint_' + key)}
                  onChange={(event) => setClaude(key, event.target.value)}
                />
              ))}
            </div>
            <div className={styles.switchRow}>
              <div>
                <strong>{t('ua_panel.stabilize')}</strong>
                <p>{t('ua_panel.stabilize_hint')}</p>
              </div>
              <ToggleSwitch
                checked={draft.claude.stabilizeDeviceProfile}
                disabled={disabled}
                ariaLabel={t('ua_panel.stabilize')}
                onChange={(value) => setClaude('stabilizeDeviceProfile', value)}
              />
            </div>
            <div className={styles.switchRow}>
              <div>
                <strong>{t('ua_panel.diagnostics')}</strong>
                <p>{t('ua_panel.diagnostics_hint')}</p>
              </div>
              <ToggleSwitch
                checked={draft.claude.injectDiagnostics}
                disabled={disabled}
                ariaLabel={t('ua_panel.diagnostics')}
                onChange={(value) => setClaude('injectDiagnostics', value)}
              />
            </div>
            <label className={styles.stack}>
              <span>{t('ua_panel.request_id')}</span>
              <Select
                value={draft.claude.clientRequestId}
                disabled={disabled}
                ariaLabel={t('ua_panel.request_id')}
                options={['on', 'passthrough', 'off'].map((value) => ({
                  value,
                  label: t('ua_panel.request_id_' + value),
                }))}
                onChange={(value) => setClaude('clientRequestId', value)}
              />
              <small className={styles.muted}>{t('ua_panel.request_id_hint')}</small>
            </label>
            <div className={styles.formGrid}>
              {(['nativeEntrypoints', 'betaStrip', 'betaAppend'] as const).map((field) => (
                <label className={styles.stack} key={field}>
                  <span>{t('ua_panel.field_' + field)}</span>
                  <textarea
                    className="input"
                    value={draft.claude[field]}
                    rows={3}
                    spellCheck={false}
                    disabled={disabled}
                    onChange={(event) => setClaude(field, event.target.value)}
                  />
                  <small className={styles.muted}>{t('ua_panel.hint_' + field)}</small>
                </label>
              ))}
            </div>
          </>
        ) : (
          <Input
            label="X-Codex-Beta-Features"
            value={draft.codex.betaFeatures}
            disabled={disabled}
            hint={t('ua_panel.codex_beta_hint')}
            onChange={(event) =>
              onChange({ ...draft, codex: { ...draft.codex, betaFeatures: event.target.value } })
            }
          />
        )}
      </div>
    </Card>
  );
}
