import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type { ClientProvider } from '@/services/api/clientUserAgents';
import {
  formatUALabCodexYaml,
  formatUALabGeminiYaml,
  parseUALabCodexYaml,
  parseUALabGeminiYaml,
  UALAB_GENERATED_MARKER,
  type UserAgentDraft,
  type UserAgentProviderStatus,
  type ClaudeIdentityDraft,
} from '@/services/api/userAgentPanel';
import styles from './UserAgentsPage.module.scss';

type Props = {
  provider: ClientProvider;
  draft: UserAgentDraft;
  status?: UserAgentProviderStatus;
  disabled: boolean;
  onChange: (draft: UserAgentDraft) => void;
};

function GeminiUALabSettings({
  draft,
  disabled,
  onChange,
}: {
  draft: UserAgentDraft;
  disabled: boolean;
  onChange: (draft: UserAgentDraft) => void;
}) {
  const [rawYaml, setRawYaml] = useState(() => formatUALabGeminiYaml(draft.gemini));
  const [parseError, setParseError] = useState('');

  useEffect(() => {
    setRawYaml((current) => {
      if (current.trim()) return current;
      return formatUALabGeminiYaml(draft.gemini);
    });
  }, [draft.gemini]);

  const applyPastedYaml = (value: string) => {
    setRawYaml(value);
    if (!value.trim()) {
      setParseError('');
      onChange({
        ...draft,
        gemini: {
          userAgent: '',
          apiClient: '',
          acceptEncoding: '',
          forceHttp1: false,
          headerOrder: [],
          extraHeaders: {},
        },
      });
      return;
    }
    const result = parseUALabGeminiYaml(value);
    if (!result.draft) {
      setParseError(result.error ?? '');
      return;
    }
    setParseError('');
    onChange({ ...draft, gemini: result.draft });
  };

  return (
    <Card
      title="Gemini — 粘贴 UALab 本地配置"
      extra={<span className={styles.eyebrow}>{UALAB_GENERATED_MARKER}</span>}
    >
      <div className={styles.stack}>
        <p className={styles.muted}>
          Gemini 已移除 auto 与内置默认 UA 逻辑。请在本地运行{' '}
          <code>./bin/ualab export --tag agy --format cpa-yaml</code> 后，将生成的完整 YAML 直接粘贴到下方：
        </p>
        <label className={styles.stack}>
          <span>{UALAB_GENERATED_MARKER} YAML</span>
          <textarea
            className="input"
            rows={14}
            spellCheck={false}
            disabled={disabled}
            value={rawYaml}
            placeholder={`${UALAB_GENERATED_MARKER} from local capture (tag: agy)\nupstream:\n  gemini:\n    header-defaults:\n      user-agent: "google-genai-sdk/..."\n      api-client: "google-genai-sdk/..."\n      accept-encoding: "gzip"\n      force-http1: true\n      header-order:\n        - Host\n        - User-Agent\n        - Content-Length\n        - Content-Type\n        - X-Goog-Api-Client\n        - X-Goog-Api-Key\n        - Accept-Encoding`}
            onChange={(event) => applyPastedYaml(event.target.value)}
          />
        </label>
        {parseError && (
          <div role="alert" className="error-box">
            {parseError}
          </div>
        )}
        {draft.gemini.userAgent && (
          <dl className={styles.facts}>
            <div>
              <dt>User-Agent</dt>
              <dd>
                <code>{draft.gemini.userAgent}</code>
              </dd>
            </div>
            <div>
              <dt>X-Goog-Api-Client</dt>
              <dd>
                <code>{draft.gemini.apiClient || '—'}</code>
              </dd>
            </div>
            <div>
              <dt>Accept-Encoding</dt>
              <dd>
                <code>{draft.gemini.acceptEncoding || '—'}</code>
              </dd>
            </div>
            <div>
              <dt>Force HTTP/1.1</dt>
              <dd>
                <code>{String(draft.gemini.forceHttp1)}</code>
              </dd>
            </div>
            <div>
              <dt>Header Order</dt>
              <dd>
                <code>{draft.gemini.headerOrder.join(' -> ') || '—'}</code>
              </dd>
            </div>
          </dl>
        )}
      </div>
    </Card>
  );
}

function CodexUALabSettings({
  draft,
  disabled,
  onChange,
}: {
  draft: UserAgentDraft;
  disabled: boolean;
  onChange: (draft: UserAgentDraft) => void;
}) {
  const [rawYaml, setRawYaml] = useState(() => formatUALabCodexYaml(draft.codex));
  const [parseError, setParseError] = useState('');

  useEffect(() => {
    setRawYaml((current) => {
      if (current.trim()) return current;
      return formatUALabCodexYaml(draft.codex);
    });
  }, [draft.codex]);

  const applyPastedYaml = (value: string) => {
    setRawYaml(value);
    if (!value.trim()) {
      setParseError('');
      onChange({
        ...draft,
        codex: {
          userAgent: '',
          originator: '',
          betaFeatures: '',
          acceptEncoding: '',
          forceHttp1: false,
          headerOrder: [],
          extraHeaders: {},
        },
      });
      return;
    }
    const result = parseUALabCodexYaml(value);
    if (!result.draft) {
      setParseError(result.error ?? '');
      return;
    }
    setParseError('');
    onChange({ ...draft, codex: result.draft });
  };

  return (
    <Card
      title="Codex — 粘贴 UALab 本地配置"
      extra={<span className={styles.eyebrow}>{UALAB_GENERATED_MARKER}</span>}
    >
      <div className={styles.stack}>
        <p className={styles.muted}>
          Codex 已移除 auto 与内置默认 UA 逻辑。请在本地运行{' '}
          <code>./bin/ualab export --tag codex-cli --format cpa-yaml</code> 后，将生成的完整 YAML
          直接粘贴到下方：
        </p>
        <label className={styles.stack}>
          <span>{UALAB_GENERATED_MARKER} YAML</span>
          <textarea
            className="input"
            rows={16}
            spellCheck={false}
            disabled={disabled}
            value={rawYaml}
            placeholder={`${UALAB_GENERATED_MARKER} from local capture (tag: codex-cli)\noauth:\n  providers:\n    codex:\n      header-defaults:\n        user-agent: "codex-tui/..."\n        originator: "codex-tui"\n        beta-features: "remote_compaction_v2"\n        accept-encoding: "zstd,gzip"\n        force-http1: true\n        header-order:\n          - x-codex-beta-features\n          - x-codex-window-id\n          - x-codex-turn-metadata\n          - x-client-request-id\n          - session-id\n          - thread-id\n          - accept\n          - content-type\n          - authorization\n          - originator\n          - user-agent\n          - accept-encoding\n          - host\n          - content-length`}
            onChange={(event) => applyPastedYaml(event.target.value)}
          />
        </label>
        {parseError && (
          <div role="alert" className="error-box">
            {parseError}
          </div>
        )}
        {draft.codex.userAgent && (
          <dl className={styles.facts}>
            <div>
              <dt>User-Agent</dt>
              <dd>
                <code>{draft.codex.userAgent}</code>
              </dd>
            </div>
            <div>
              <dt>Originator</dt>
              <dd>
                <code>{draft.codex.originator || '—'}</code>
              </dd>
            </div>
            <div>
              <dt>X-Codex-Beta-Features</dt>
              <dd>
                <code>{draft.codex.betaFeatures || '—'}</code>
              </dd>
            </div>
            <div>
              <dt>Accept-Encoding</dt>
              <dd>
                <code>{draft.codex.acceptEncoding || '—'}</code>
              </dd>
            </div>
            <div>
              <dt>Force HTTP/1.1</dt>
              <dd>
                <code>{String(draft.codex.forceHttp1)}</code>
              </dd>
            </div>
            <div>
              <dt>Header Order</dt>
              <dd>
                <code>{draft.codex.headerOrder.join(' -> ') || '—'}</code>
              </dd>
            </div>
          </dl>
        )}
      </div>
    </Card>
  );
}

export function UserAgentSettings({ provider, draft, status, disabled, onChange }: Props) {
  const { t } = useTranslation();
  const uaInputId = useId();
  if (provider === 'gemini') {
    return <GeminiUALabSettings draft={draft} disabled={disabled} onChange={onChange} />;
  }
  if (provider === 'codex') {
    return <CodexUALabSettings draft={draft} disabled={disabled} onChange={onChange} />;
  }
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
          id={uaInputId}
          value={current.userAgent}
          disabled={disabled}
          spellCheck={false}
          placeholder={t('ua_panel.default_placeholder')}
          hint={t('ua_panel.ua_hint')}
          onChange={(event) => setUA(event.target.value)}
        />
        {mode === 'auto' && <p className={styles.note}>{t('ua_panel.auto_scope')}</p>}
        <p className={styles.muted}>{t('ua_panel.scope_' + provider)}</p>
        {provider === 'claude' && (
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
        )}
      </div>
    </Card>
  );
}
