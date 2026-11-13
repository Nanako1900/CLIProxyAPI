import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import type { ClientProvider } from '@/services/api/clientUserAgents';
import styles from './UserAgentsPage.module.scss';

const claudeRules = [
  ['native', 'User-Agent / x-app / anthropic-beta / metadata.user_id'],
  ['authentication', 'Authorization / x-api-key'],
  ['protocol', 'Content-Type / anthropic-version'],
  ['client', 'x-app / anthropic-dangerous-direct-browser-access'],
  ['sdk', 'X-Stainless-Lang / X-Stainless-Runtime / X-Stainless-Retry-Count / X-Stainless-Async'],
  ['betas', 'anthropic-beta'],
  ['session', 'X-Claude-Code-Session-Id / x-client-request-id'],
  ['agents', 'X-Claude-Code-Agent-Id / X-Claude-Code-Parent-Agent-Id'],
  ['remote', 'X-Claude-Remote-Container-Id / X-Claude-Remote-Session-Id / X-Client-App'],
  ['protection', 'X-Anthropic-Additional-Protection'],
  [
    'gateway',
    'X-Claude-Code-Request-Class / X-Claude-Code-Agent-Type / X-Claude-Code-Prev-Tool-Durations / X-Claude-Code-Compaction / X-Claude-Code-Context-Compacted',
  ],
  ['negotiation', 'Accept / Accept-Encoding / Connection'],
  ['body', 'metadata.user_id / x-anthropic-billing-header / diagnostics'],
  ['transport', 'TLS / HTTP/1.1 / header order'],
] as const;
const codexRules = [
  ['codex_auth', 'Authorization / ChatGPT-Account-ID'],
  ['codex_origin', 'User-Agent / Originator'],
  ['codex_ws', 'X-Codex-Beta-Features / WebSocket'],
  ['codex_session', 'Session / conversation / request headers'],
] as const;

export function UserAgentRules({ provider }: { provider: ClientProvider }) {
  const { t } = useTranslation();
  return (
    <Card title={t('ua_panel.rules')}>
      <div className={styles.stack}>
        <ol className={styles.priority}>
          {['credential', 'provider', 'native'].map((key) => (
            <li key={key}>
              <strong>{t('ua_panel.priority_' + key)}</strong>
              <p>{t('ua_panel.priority_' + key + '_hint')}</p>
            </li>
          ))}
        </ol>
        <p className={styles.note}>{t('ua_panel.rules_hint')}</p>
        <div className={styles.ruleList}>
          {(provider === 'claude' ? claudeRules : codexRules).map(([key, headers]) => (
            <details key={key} className={styles.rule}>
              <summary>
                <strong>{t('ua_panel.rule_' + key + '_title')}</strong>
                <code>{headers}</code>
              </summary>
              <p>{t('ua_panel.rule_' + key)}</p>
            </details>
          ))}
        </div>
        {provider === 'claude' && <p className={styles.muted}>{t('ua_panel.oauth_control')}</p>}
      </div>
    </Card>
  );
}
