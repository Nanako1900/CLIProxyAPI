import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Modal } from '@/components/ui/Modal';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';
import { ClientUserAgentPicker } from '@/features/config/components/blocks/ClientUserAgentPicker';
import type { VisualConfigValues } from '@/types/visualConfig';
import type { ClientProvider } from '@/services/api/clientUserAgents';
import {
  userAgentPanelConnection,
  userAgentChanges,
  validateUserAgentDraft,
  type UserAgentDraft,
  type UserAgentSnapshot,
} from '@/services/api/userAgentPanel';
import { UserAgentSettings } from './UserAgentSettings';
import { UserAgentRules } from './UserAgentRules';
import styles from './UserAgentsPage.module.scss';

export function UserAgentsPage() {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connected = useAuthStore((state) => state.connectionStatus === 'connected');
  return (
    <UserAgentWorkspace
      key={JSON.stringify([apiBase, managementKey, connected])}
      connected={connected}
    />
  );
}

function UserAgentWorkspace({ connected }: { connected: boolean }) {
  const { t, i18n } = useTranslation();
  const [connection] = useState(userAgentPanelConnection);
  const [provider, setProvider] = useState<ClientProvider>('claude');
  const [snapshot, setSnapshot] = useState<UserAgentSnapshot | null>(null);
  const [draft, setDraft] = useState<UserAgentDraft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [review, setReview] = useState(false);
  const [loadedAt, setLoadedAt] = useState('');
  const alive = useRef(false);
  const errorElement = useRef<HTMLDivElement>(null);
  const generation = useRef(0);
  const confirm = useNotificationStore((state) => state.showConfirmation);
  const changes = snapshot && draft ? userAgentChanges(snapshot.draft, draft) : [];
  const dirty = changes.length > 0;
  const disabled = !connected || loading || saving;
  const runtime = snapshot?.runtime;
  const status = runtime?.providers.find((item) => item.provider === provider);
  const overrides = runtime?.overrides.filter((item) => item.provider === provider) || [];
  const formatDate = (value?: string) =>
    value ? new Date(value).toLocaleString(i18n.language) : t('ua_panel.no_record');

  useEffect(() => {
    if (error) errorElement.current?.focus();
  }, [error]);

  useUnsavedChangesGuard({
    shouldBlock: dirty || saving,
    dialog: {
      title: t('ua_panel.discard'),
      message: t('ua_panel.discard_hint'),
      confirmText: t('ua_panel.discard'),
      cancelText: t('ua_panel.cancel'),
    },
  });
  useEffect(() => {
    if (!dirty && !saving) return;
    const handler = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirty, saving]);

  const reportError = useCallback((failure: unknown) => {
    const status = (failure as { status?: number }).status;
    setError(status === 412 ? 'conflict' : status === 404 ? 'unsupported' : 'failed');
  }, []);
  const load = useCallback(async () => {
    if (!connected) {
      setLoading(false);
      return;
    }
    const request = ++generation.current;
    setLoading(true);
    setError('');
    try {
      const next = await connection.load();
      if (!alive.current || request !== generation.current) return;
      setSnapshot(next);
      setDraft(structuredClone(next.draft));
      setLoadedAt(new Date().toISOString());
    } catch (failure) {
      if (alive.current && request === generation.current) reportError(failure);
    } finally {
      if (alive.current && request === generation.current) setLoading(false);
    }
  }, [connection, connected, reportError]);
  useEffect(() => {
    alive.current = true;
    void load();
    return () => {
      alive.current = false;
      generation.current += 1;
    };
  }, [load]);

  const refresh = () => {
    if (dirty)
      confirm({
        title: t('ua_panel.discard'),
        message: t('ua_panel.discard_hint'),
        onConfirm: () => {
          if (alive.current) void load();
        },
      });
    else void load();
  };
  const save = async () => {
    if (!snapshot || !draft || disabled) return;
    const invalid = validateUserAgentDraft(draft);
    if (invalid) {
      setError(invalid);
      setReview(false);
      return;
    }
    setSaving(true);
    setError('');
    setNotice('');
    try {
      await connection.save(snapshot, draft);
      if (!alive.current) return;
      useConfigStore.getState().clearCache();
      setReview(false);
      setNotice('saved');
      await load();
    } catch (failure) {
      if (alive.current) {
        reportError(failure);
        setReview(false);
      }
    } finally {
      if (alive.current) setSaving(false);
    }
  };
  const applySample = (patch: Partial<VisualConfigValues>) => {
    setNotice('');
    setDraft((previous) => {
      if (!previous) return previous;
      const next = structuredClone(previous);
      const mappings = [
        ['claudeHeaderUserAgent', 'userAgent'],
        ['claudeHeaderPackageVersion', 'packageVersion'],
        ['claudeHeaderRuntimeVersion', 'runtimeVersion'],
        ['claudeHeaderOs', 'os'],
        ['claudeHeaderArch', 'arch'],
        ['claudeHeaderTimeout', 'timeout'],
      ] as const;
      if (provider === 'claude') {
        for (const [source, target] of mappings)
          if (typeof patch[source] === 'string') next.claude[target] = patch[source];
      } else {
        if (typeof patch.codexHeaderUserAgent === 'string')
          next.codex.userAgent = patch.codexHeaderUserAgent;
        if (typeof patch.codexHeaderBetaFeatures === 'string')
          next.codex.betaFeatures = patch.codexHeaderBetaFeatures;
      }
      return next;
    });
  };
  const copyReport = async () => {
    if (!runtime) return;
    try {
      await navigator.clipboard.writeText(
        JSON.stringify(
          { capturedAt: loadedAt, type: 'provider-defaults-not-request-capture', ...runtime },
          null,
          2
        )
      );
      if (alive.current) setNotice('copied');
    } catch {
      if (alive.current) setNotice('copy_failed');
    }
  };
  const uaVersion = status?.resolvedUserAgent.match(/^claude-cli\/(\d+\.\d+\.\d+)/)?.[1];
  const versionSplit =
    provider === 'claude' &&
    uaVersion &&
    status?.billingVersion &&
    uaVersion !== status.billingVersion;

  return (
    <div className={styles.page}>
      <header className={styles.heading}>
        <div>
          <span className={styles.eyebrow}>{t('ua_panel.eyebrow')}</span>
          <h1>{t('ua_panel.title')}</h1>
          <p>{t('ua_panel.description')}</p>
        </div>
        <div className={styles.actions}>
          <Button variant="ghost" disabled={!runtime || disabled} onClick={() => void copyReport()}>
            {t('ua_panel.copy_report')}
          </Button>
          <Button variant="secondary" disabled={disabled} onClick={refresh}>
            {t('ua_panel.refresh')}
          </Button>
        </div>
      </header>
      <div className={styles.providerBar}>
        <div className={styles.segmented} role="group" aria-label={t('ua_panel.provider')}>
          {(['claude', 'codex'] as const).map((value) => (
            <Button
              key={value}
              className={styles.providerButton}
              variant={provider === value ? 'primary' : 'ghost'}
              aria-pressed={provider === value}
              onClick={() => setProvider(value)}
            >
              {value === 'claude' ? 'Claude Code' : 'Codex'}
            </Button>
          ))}
        </div>
        <span className={styles.muted}>
          {loadedAt
            ? t('ua_panel.loaded_at', { time: formatDate(loadedAt) })
            : t('ua_panel.loading')}
        </span>
      </div>
      {!connected && <p className={styles.note}>{t('ua_panel.disconnected')}</p>}
      {error && (
        <div role="alert" className="error-box" tabIndex={-1} ref={errorElement}>
          {t('ua_panel.' + error)}
        </div>
      )}
      {notice && (
        <p role="status" className={styles.note}>
          {t('ua_panel.' + notice)}
        </p>
      )}
      {runtime?.homeMode && <p className={styles.note}>{t('ua_panel.home_mode')}</p>}
      {snapshot && !runtime && <p className={styles.note}>{t('ua_panel.runtime_unavailable')}</p>}
      {status && (
        <>
          <div className={styles.stats}>
            {[
              ['mode', t('ua_panel.mode_' + status.mode)],
              ['cached_version', status.auto.cachedVersion],
              [
                'lookup_status',
                t('ua_panel.lookup_' + (status.auto.enabled ? status.auto.result : 'disabled')),
              ],
              [
                'overrides',
                runtime?.overrideInventoryAvailable
                  ? String(overrides.length)
                  : t('ua_panel.unknown'),
              ],
            ].map(([key, value]) => (
              <div className={styles.stat} key={key}>
                <small>{t('ua_panel.' + key)}</small>
                <strong>{value}</strong>
              </div>
            ))}
          </div>
          <div className={styles.overview}>
            <Card
              title={t('ua_panel.runtime_identity')}
              extra={<span className={styles.eyebrow}>{t('ua_panel.saved_state')}</span>}
            >
              <div className={styles.stack}>
                <div className={styles.identity}>{status.resolvedUserAgent}</div>
                <p className={styles.muted}>{t('ua_panel.baseline_hint')}</p>
                {versionSplit && (
                  <p className={`${styles.note} ${styles.warning}`}>
                    {t('ua_panel.version_split', { ua: uaVersion, billing: status.billingVersion })}
                  </p>
                )}
                <dl className={styles.facts}>
                  <div>
                    <dt>{t('ua_panel.builtin_ua')}</dt>
                    <dd>{status.defaultUserAgent}</dd>
                  </div>
                  {Object.entries(status.baselineHeaders).map(([name, value]) => (
                    <div key={name}>
                      <dt>{name}</dt>
                      <dd>
                        <code>{value || t('ua_panel.unset')}</code>
                      </dd>
                    </div>
                  ))}
                  {status.billingVersion && (
                    <div>
                      <dt>billing cc_version</dt>
                      <dd>
                        <code>{status.billingVersion}</code>
                      </dd>
                    </div>
                  )}
                </dl>
              </div>
            </Card>
            <Card title={t('ua_panel.auto_updates')}>
              <div className={styles.stack}>
                <dl className={styles.facts}>
                  <div>
                    <dt>{t('ua_panel.lookup_enabled')}</dt>
                    <dd>{t('ua_panel.' + (status.auto.enabled ? 'enabled' : 'disabled'))}</dd>
                  </div>
                  <div>
                    <dt>{t('ua_panel.last_attempt')}</dt>
                    <dd>{formatDate(status.auto.lastAttempt)}</dd>
                  </div>
                  <div>
                    <dt>{t('ua_panel.last_success')}</dt>
                    <dd>{formatDate(status.auto.lastSuccess)}</dd>
                  </div>
                  <div>
                    <dt>{t('ua_panel.last_result')}</dt>
                    <dd>{t('ua_panel.lookup_' + status.auto.result)}</dd>
                  </div>
                  <div>
                    <dt>{t('ua_panel.refresh_interval')}</dt>
                    <dd>
                      {t('ua_panel.hours', { count: status.auto.refreshIntervalSeconds / 3600 })}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('ua_panel.cache_source')}</dt>
                    <dd>
                      {t('ua_panel.' + (status.auto.lastSuccess ? 'npm_cache' : 'builtin_cache'))}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('ua_panel.catalog_storage')}</dt>
                    <dd>{t('ua_panel.storage_' + runtime?.catalogPersistence)}</dd>
                  </div>
                </dl>
                <a
                  className={styles.muted}
                  href={status.auto.registryURL}
                  target="_blank"
                  rel="noreferrer"
                >
                  {status.auto.registryURL}
                </a>
                <p className={styles.muted}>{t('ua_panel.updater_hint')}</p>
                <p className={styles.muted}>{t('ua_panel.refresh_hint')}</p>
              </div>
            </Card>
          </div>
        </>
      )}
      {draft && (
        <UserAgentSettings
          provider={provider}
          draft={draft}
          status={status}
          disabled={disabled}
          onChange={(next) => {
            setDraft(next);
            setNotice('');
          }}
        />
      )}
      {snapshot && (
        <Card title={t('ua_panel.samples')}>
          {runtime?.catalogAvailable === false ? (
            <p className={styles.muted}>{t('ua_panel.catalog_unavailable')}</p>
          ) : (
            <ClientUserAgentPicker
              key={provider}
              provider={provider}
              limit={runtime?.catalogLimit}
              disabled={disabled}
              onChange={applySample}
            />
          )}
          <p className={styles.muted}>{t('ua_panel.catalog_hint')}</p>
        </Card>
      )}
      {runtime && (
        <Card
          title={t('ua_panel.credential_overrides')}
          extra={<Link to="/auth-files">{t('ua_panel.open_credentials')}</Link>}
        >
          <div className={styles.stack}>
            <p className={styles.muted}>{t('ua_panel.overrides_hint')}</p>
            {!runtime.overrideInventoryAvailable ? (
              <p>{t('ua_panel.inventory_unavailable')}</p>
            ) : !overrides.length ? (
              <p>{t('ua_panel.no_overrides')}</p>
            ) : (
              overrides.map((item, index) => (
                <div className={styles.override} key={`${item.authIndex}-${index}`}>
                  <div>
                    <strong>{item.name || item.authIndex || t('ua_panel.credential')}</strong> ·{' '}
                    {t('ua_panel.source_' + item.source)}
                    {item.disabled && ` · ${t('ua_panel.disabled')}`}
                  </div>
                  <code>{item.userAgent}</code>
                  <small className={styles.muted}>
                    {t('ua_panel.credential_index')}: {item.authIndex || '—'}
                  </small>
                </div>
              ))
            )}
          </div>
        </Card>
      )}
      <UserAgentRules provider={provider} />
      {draft && (
        <div className={styles.saveBar}>
          <span>
            {dirty ? t('ua_panel.unsaved', { count: changes.length }) : t('ua_panel.no_changes')}
          </span>
          <div className={styles.actions}>
            <Button variant="ghost" disabled={!dirty || disabled} onClick={refresh}>
              {t('ua_panel.discard')}
            </Button>
            <Button
              disabled={!dirty || disabled}
              loading={saving}
              onClick={() => {
                const invalid = validateUserAgentDraft(draft);
                if (invalid) setError(invalid);
                else setReview(true);
              }}
            >
              {t('ua_panel.review_save')}
            </Button>
          </div>
        </div>
      )}
      <Modal
        open={review}
        title={t('ua_panel.review_save')}
        closeDisabled={saving}
        onClose={() => setReview(false)}
        footer={
          <>
            <Button variant="secondary" disabled={saving} onClick={() => setReview(false)}>
              {t('ua_panel.cancel')}
            </Button>
            <Button loading={saving} disabled={disabled} onClick={() => void save()}>
              {t('ua_panel.save')}
            </Button>
          </>
        }
      >
        <p className={styles.muted}>{t('ua_panel.save_hint')}</p>
        <div className={styles.changes}>
          {changes.map((change) => (
            <div key={`${change.provider}-${change.field}`} className={styles.change}>
              <strong>
                {change.provider} / {change.field}
              </strong>
              <small className={styles.muted}>{t('ua_panel.before')}</small>
              <pre>{JSON.stringify(change.before, null, 2)}</pre>
              <small className={styles.muted}>{t('ua_panel.after')}</small>
              <pre>{JSON.stringify(change.after, null, 2)}</pre>
            </div>
          ))}
        </div>
      </Modal>
    </div>
  );
}
