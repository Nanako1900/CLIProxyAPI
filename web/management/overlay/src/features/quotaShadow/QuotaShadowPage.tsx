import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { Modal } from '@/components/ui/Modal';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';
import {
  quotaShadowConnection,
  validShadowSettings,
  type ShadowSettings,
  type ShadowStatus,
} from '@/services/api/quotaShadow';
import { ShadowResults } from './ShadowResults';
import styles from './QuotaShadowPage.module.scss';

export function QuotaShadowPage() {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connected = useAuthStore((state) => state.connectionStatus === 'connected');
  return (
    <ShadowWorkspace
      key={JSON.stringify([apiBase, managementKey, connected])}
      connected={connected}
    />
  );
}

function ShadowWorkspace({ connected }: { connected: boolean }) {
  const { t } = useTranslation();
  const [connection] = useState(quotaShadowConnection);
  const [baseline, setBaseline] = useState<{ settings: ShadowSettings; etag: string } | null>(null);
  const [draft, setDraft] = useState<ShadowSettings | null>(null);
  const [status, setStatus] = useState<ShadowStatus | null>(null);
  const [error, setError] = useState('');
  const [statusError, setStatusError] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [review, setReview] = useState(false);
  const [auto, setAuto] = useState(false);
  const [poolId, setPool] = useState('*');
  const [type, setType] = useState('*');
  const alive = useRef(false);
  const statusPending = useRef(false);
  const request = useRef(0);
  const confirm = useNotificationStore((state) => state.showConfirmation);
  const notify = useNotificationStore((state) => state.showNotification);
  const dirty = Boolean(
    baseline && draft && JSON.stringify(baseline.settings) !== JSON.stringify(draft)
  );
  const disabled = !connected || loading || saving;
  useUnsavedChangesGuard({
    shouldBlock: dirty || saving,
    dialog: {
      title: t('quota_shadow.discard'),
      message: t('quota_shadow.discard_hint'),
      confirmText: t('quota_shadow.discard'),
      cancelText: t('quota_shadow.cancel'),
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

  const refreshStatus = useCallback(async () => {
    if (!connected || statusPending.current) return;
    statusPending.current = true;
    try {
      const next = await connection.status();
      if (alive.current) {
        setStatus(next);
        setStatusError(false);
      }
    } catch {
      if (alive.current) setStatusError(true);
    } finally {
      statusPending.current = false;
    }
  }, [connected, connection]);
  const load = useCallback(async () => {
    if (!connected) {
      setLoading(false);
      return;
    }
    const current = ++request.current;
    setLoading(true);
    setError('');
    try {
      const next = await connection.settings();
      if (alive.current && current === request.current) {
        setBaseline(next);
        setDraft(next.settings);
      }
    } catch {
      if (alive.current && current === request.current) setError('load_failed');
    } finally {
      if (alive.current && current === request.current) setLoading(false);
    }
  }, [connected, connection]);
  useEffect(() => {
    alive.current = true;
    void load();
    void refreshStatus();
    return () => {
      alive.current = false;
      request.current += 1;
    };
  }, [load, refreshStatus]);
  useEffect(() => {
    if (!auto || !connected) return;
    const timer = window.setInterval(() => {
      if (document.visibilityState !== 'hidden') void refreshStatus();
    }, 5000);
    return () => window.clearInterval(timer);
  }, [auto, connected, refreshStatus]);

  const reloadSettings = () => {
    if (dirty)
      confirm({
        title: t('quota_shadow.discard'),
        message: t('quota_shadow.discard_hint'),
        onConfirm: () => {
          if (alive.current) void load();
        },
      });
    else void load();
  };
  const save = async () => {
    if (!baseline || !draft || disabled || !validShadowSettings(draft)) return;
    setSaving(true);
    setError('');
    try {
      await connection.save(draft, baseline.etag);
      if (!alive.current) return;
      useConfigStore.getState().clearCache();
      setReview(false);
      notify(t('quota_shadow.saved'), 'success');
      await load();
      await refreshStatus();
    } catch (failure) {
      if (alive.current) {
        setError((failure as { status?: number }).status === 412 ? 'conflict' : 'save_failed');
        setReview(false);
      }
    } finally {
      if (alive.current) setSaving(false);
    }
  };
  const update = (value: Partial<ShadowSettings>) => {
    if (draft) setDraft({ ...draft, ...value });
  };

  return (
    <div className={styles.page}>
      <header className={styles.heading}>
        <div>
          <h1>{t('quota_shadow.title')}</h1>
          <p>{t('quota_shadow.description')}</p>
        </div>
      </header>
      <p className={styles.info}>{t('quota_shadow.passive_hint')}</p>
      {!connected && <div role="alert">{t('quota_shadow.disconnected')}</div>}
      {error && (
        <div role="alert" className="error-box">
          {t(`quota_shadow.${error}`)}
        </div>
      )}
      <Card
        title={t('quota_shadow.settings')}
        extra={
          <Button variant="secondary" disabled={disabled} onClick={reloadSettings}>
            {t('quota_shadow.reload_settings')}
          </Button>
        }
      >
        {loading && <p role="status">{t('quota_shadow.loading')}</p>}
        {draft && (
          <div className={styles.form}>
            <ToggleSwitch
              label={t('quota_shadow.enabled')}
              checked={draft.enabled}
              disabled={disabled}
              onChange={(enabled) => update({ enabled })}
            />
            <Input
              label={t('quota_shadow.soft_limit')}
              type="number"
              min={0}
              max={100}
              step="any"
              value={Number.isFinite(draft.softLimit) ? draft.softLimit : ''}
              disabled={disabled}
              hint={t('quota_shadow.soft_hint')}
              onChange={(event) => update({ softLimit: event.target.valueAsNumber })}
            />
            <Input
              label={t('quota_shadow.file')}
              value={draft.file}
              disabled={disabled}
              hint={t('quota_shadow.file_hint')}
              onChange={(event) => update({ file: event.target.value })}
            />
            <Input
              label={t('quota_shadow.max_file')}
              type="number"
              min={0}
              max={65536}
              step={1}
              value={Number.isFinite(draft.maxFileMB) ? draft.maxFileMB : ''}
              disabled={disabled}
              hint={t('quota_shadow.size_hint')}
              onChange={(event) => update({ maxFileMB: event.target.valueAsNumber })}
            />
            {!validShadowSettings(draft) && (
              <p role="alert" className="error-box">
                {t('quota_shadow.invalid')}
              </p>
            )}
            <p className="hint">{t('quota_shadow.lifecycle')}</p>
            <Button
              disabled={disabled || !dirty || !validShadowSettings(draft)}
              onClick={() => setReview(true)}
            >
              {t('quota_shadow.review_save')}
            </Button>
          </div>
        )}
      </Card>
      <div className={styles.heading}>
        <h2>{t('quota_shadow.live')}</h2>
        <div className={styles.actions}>
          <ToggleSwitch
            label={t('quota_shadow.auto_refresh')}
            checked={auto}
            disabled={!connected}
            onChange={setAuto}
          />
          <Button variant="secondary" disabled={!connected} onClick={() => void refreshStatus()}>
            {t('quota_shadow.refresh')}
          </Button>
        </div>
      </div>
      {statusError && (
        <div role="alert" className="error-box">
          {t('quota_shadow.status_failed')}
        </div>
      )}
      {status && (
        <>
          <p role="status" className={styles.info}>
            {t(`quota_shadow.state_${status.state}`)}
            {status.last_error &&
              ` · ${t(`quota_shadow.error_${['open_failed', 'write_failed', 'rotate_failed', 'render_failed'].includes(status.last_error) ? status.last_error : 'unknown'}`)}`}
          </p>
          <ShadowResults
            status={status}
            poolId={poolId}
            type={type}
            onPool={setPool}
            onType={setType}
          />
        </>
      )}
      <Modal
        open={review}
        title={t('quota_shadow.review_save')}
        closeDisabled={saving}
        onClose={() => setReview(false)}
        footer={
          <>
            <Button variant="secondary" disabled={saving} onClick={() => setReview(false)}>
              {t('quota_shadow.cancel')}
            </Button>
            <Button loading={saving} onClick={() => void save()}>
              {t('quota_shadow.save')}
            </Button>
          </>
        }
      >
        <p>{t('quota_shadow.review_hint')}</p>
        <p>{t('quota_shadow.lifecycle')}</p>
      </Modal>
    </div>
  );
}
