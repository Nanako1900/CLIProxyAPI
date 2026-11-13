import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { Modal } from '@/components/ui/Modal';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';
import {
  accountPoolsConnection,
  configMembershipPatch,
  poolConfigPatch,
  validatePoolDraft,
  type PoolDraft,
  type PoolSnapshot,
  type PoolMember,
  type PoolRuntime,
  type ConfigPoolMember,
} from '@/services/api/accountPools';
import { ClientControls, PoolControls } from './PoolControls';
import { PoolRuntimeBanner } from './PoolRuntimeBanner';
import styles from './AccountPoolsPage.module.scss';

export function AccountPoolsPage() {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connected = useAuthStore((state) => state.connectionStatus === 'connected');
  return (
    <PoolWorkspace
      key={JSON.stringify([apiBase, managementKey, connected])}
      apiBase={apiBase}
      connected={connected}
    />
  );
}

function PoolWorkspace({ apiBase, connected }: { apiBase: string; connected: boolean }) {
  const { t } = useTranslation();
  const [connection] = useState(accountPoolsConnection);
  const [snapshot, setSnapshot] = useState<PoolSnapshot | null>(null);
  const [draft, setDraft] = useState<PoolDraft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [review, setReview] = useState(false);
  const [runtime, setRuntime] = useState<PoolRuntime | null>(null);
  const [runtimeFailed, setRuntimeFailed] = useState(false);
  const [filter, setFilter] = useState('*');
  const [search, setSearch] = useState('');
  const [limit, setLimit] = useState(40);
  const [assignment, setAssignment] = useState<{
    member: PoolMember | ConfigPoolMember;
    poolId: string;
  } | null>(null);
  const generation = useRef(0);
  const alive = useRef(false);
  const notify = useNotificationStore((state) => state.showNotification);
  const confirm = useNotificationStore((state) => state.showConfirmation);
  const dirty = Boolean(
    snapshot && draft && JSON.stringify(snapshot.draft) !== JSON.stringify(draft)
  );
  const disabled = saving || loading || !connected;

  useUnsavedChangesGuard({
    shouldBlock: dirty || saving,
    dialog: {
      title: t('account_pools.discard'),
      message: t('account_pools.discard_hint'),
      confirmText: t('account_pools.discard'),
      cancelText: t('account_pools.cancel'),
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
    const details = failure as { status?: number; apiCode?: string; message?: string };
    const key =
      details.status === 412
        ? 'conflict'
        : details.apiCode === 'credential_busy'
          ? 'busy'
          : details.apiCode === 'membership_source_managed'
            ? 'source_managed'
            : details.status === 404
              ? 'unsupported'
              : 'failed';
    // Never render raw server errors: validation messages may contain a configured secret.
    setError(key);
  }, []);

  const load = useCallback(async () => {
    if (!connected) {
      setLoading(false);
      return;
    }
    const request = ++generation.current;
    setLoading(true);
    setError('');
    setRuntime(null);
    setRuntimeFailed(false);
    void connection
      .runtime()
      .then((next) => {
        if (alive.current && request === generation.current) setRuntime(next);
      })
      .catch(() => {
        if (alive.current && request === generation.current) setRuntimeFailed(true);
      });
    try {
      const next = await connection.load();
      if (!alive.current || request !== generation.current) return;
      setSnapshot(next);
      setDraft(structuredClone(next.draft));
    } catch (failure) {
      if (alive.current && request === generation.current) reportError(failure);
    } finally {
      if (alive.current && request === generation.current) setLoading(false);
    }
  }, [connected, connection, reportError]);
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
        title: t('account_pools.discard'),
        message: t('account_pools.discard_hint'),
        onConfirm: () => {
          if (alive.current) void load();
        },
      });
    else void load();
  };
  const save = async () => {
    if (!snapshot || !draft || disabled) return;
    setSaving(true);
    setError('');
    setNotice('');
    try {
      await connection.saveConfig(snapshot.etag, poolConfigPatch(draft));
      if (!alive.current) return;
      useConfigStore.getState().clearCache();
      setNotice(
        t(
          snapshot.draft.settings.enabled !== draft.settings.enabled
            ? 'account_pools.restart_required'
            : 'account_pools.saved'
        )
      );
      setReview(false);
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
  const assign = async () => {
    if (!assignment || !snapshot || disabled) return;
    setSaving(true);
    setError('');
    setNotice('');
    try {
      if ('family' in assignment.member) {
        await connection.saveConfig(
          snapshot.etag,
          configMembershipPatch(snapshot.config, assignment.member, assignment.poolId)
        );
        if (alive.current) setNotice(t('account_pools.config_saved'));
      } else {
        await connection.assign(assignment.member, assignment.poolId);
        if (alive.current) setNotice(t('account_pools.member_saved'));
      }
      if (!alive.current) return;
      useConfigStore.getState().clearCache();
      setAssignment(null);
      await load();
    } catch (failure) {
      if (alive.current) {
        reportError(failure);
        setAssignment(null);
      }
    } finally {
      if (alive.current) setSaving(false);
    }
  };
  const pools = snapshot?.draft.settings.pools || [];
  const rows: (PoolMember | ConfigPoolMember)[] = snapshot
    ? [
        ...snapshot.members.filter((member) => member.source !== 'config'),
        ...snapshot.configMembers,
      ]
    : [];
  const filtered = rows.filter((member) => {
    const pool = 'family' in member ? member.poolId : member.pool_id;
    return (
      (filter === '*' || pool === filter) &&
      `${member.name} ${'family' in member ? member.family : member.provider}`
        .toLowerCase()
        .includes(search.toLowerCase())
    );
  });
  const options = [
    { value: '', label: t('account_pools.unassigned') },
    ...pools.filter((pool) => pool.enabled).map((pool) => ({ value: pool.id, label: pool.name })),
  ];

  return (
    <div className={styles.page}>
      <header className={styles.heading}>
        <div>
          <h1>{t('account_pools.title')}</h1>
          <p>{t('account_pools.description')}</p>
        </div>
        <Button variant="secondary" disabled={disabled} onClick={refresh}>
          {t('account_pools.refresh')}
        </Button>
      </header>
      <p className={styles.info}>{t('account_pools.scope_hint')}</p>
      {connected && (
        <PoolRuntimeBanner
          runtime={runtime}
          failed={runtimeFailed}
          savedEnabled={snapshot?.draft.settings.enabled}
        />
      )}
      {error && (
        <div role="alert" className="error-box">
          {t(`account_pools.${error}`)}
        </div>
      )}
      {notice && (
        <div role="status" className={styles.info}>
          {notice}
        </div>
      )}
      {loading && <p role="status">{t('account_pools.loading')}</p>}
      {!connected && <p role="alert">{t('account_pools.disconnected')}</p>}
      {snapshot && draft && (
        <>
          <PoolControls
            draft={draft}
            savedIds={pools.map((pool) => pool.id)}
            disabled={disabled}
            apiBase={apiBase}
            onChange={setDraft}
          />
          <ClientControls draft={draft} disabled={disabled} onChange={setDraft} />
          <div className={styles.saveBar}>
            <span>{t(dirty ? 'account_pools.unsaved' : 'account_pools.no_changes')}</span>
            <Button
              disabled={disabled || !dirty}
              onClick={() => {
                const access = snapshot.config.access as { 'api-keys'?: unknown } | undefined;
                const invalid = validatePoolDraft(draft, access?.['api-keys']);
                if (invalid) {
                  notify(t(`account_pools.${invalid}`), 'error');
                  return;
                }
                setReview(true);
              }}
            >
              {t('account_pools.review_save')}
            </Button>
          </div>
          <Card title={t('account_pools.members')}>
            <div className={styles.stack}>
              <p className="hint">{t('account_pools.members_hint')}</p>
              {dirty && <p className={styles.info}>{t('account_pools.save_first')}</p>}
              <div className={styles.filters}>
                <Input
                  label={t('account_pools.search')}
                  value={search}
                  onChange={(event) => {
                    setSearch(event.target.value);
                    setLimit(40);
                  }}
                />
                <Select
                  ariaLabel={t('account_pools.filter')}
                  value={filter}
                  onChange={(value) => {
                    setFilter(value);
                    setLimit(40);
                  }}
                  options={[
                    { value: '*', label: t('account_pools.all_members') },
                    { value: '', label: t('account_pools.unassigned') },
                    ...pools.map((pool) => ({ value: pool.id, label: pool.name })),
                  ]}
                />
              </div>
              <p>{t('account_pools.member_count', { count: filtered.length })}</p>
              {filtered.slice(0, limit).map((member) => {
                const configMember = 'family' in member;
                const poolId = configMember ? member.poolId : member.pool_id;
                const runtime = configMember
                  ? snapshot.members.find((item) => item.auth_index === member.authIndex)
                  : member;
                const mismatch = configMember && (!runtime || runtime.pool_id !== poolId);
                const key = configMember
                  ? `${member.family}:${member.group}:${member.key}`
                  : member.id;
                return (
                  <div className={styles.member} key={key}>
                    <div>
                      <strong>{member.name}</strong>
                      <small>
                        {configMember ? member.family : member.provider}
                        {' · '}
                        {t(
                          configMember
                            ? 'account_pools.source_config'
                            : 'account_pools.source_credential'
                        )}
                      </small>
                      <small>
                        {t('account_pools.current_pool')}:{' '}
                        <code>{poolId || t('account_pools.unassigned')}</code>
                      </small>
                      {runtime?.disabled && <small>{t('account_pools.disabled_member')}</small>}
                      {mismatch && (
                        <small role="status" className={styles.pending}>
                          {t('account_pools.pending', {
                            pool: runtime?.pool_id || t('account_pools.unassigned'),
                          })}
                        </small>
                      )}
                    </div>
                    <Button
                      variant="secondary"
                      disabled={disabled || dirty || (!configMember && member.source === 'plugin')}
                      aria-label={`${t('account_pools.assign')} ${member.name}`}
                      onClick={() => setAssignment({ member, poolId })}
                    >
                      {t('account_pools.assign')}
                    </Button>
                  </div>
                );
              })}
              {!filtered.length && <p>{t('account_pools.empty_members')}</p>}
              {filtered.length > limit && (
                <Button variant="secondary" onClick={() => setLimit(limit + 40)}>
                  {t('account_pools.more')}
                </Button>
              )}
            </div>
          </Card>
        </>
      )}
      <Modal
        open={review}
        title={t('account_pools.review_save')}
        onClose={() => setReview(false)}
        closeDisabled={saving}
        footer={
          <>
            <Button variant="secondary" disabled={saving} onClick={() => setReview(false)}>
              {t('account_pools.cancel')}
            </Button>
            <Button loading={saving} onClick={() => void save()}>
              {t('account_pools.save')}
            </Button>
          </>
        }
      >
        <p>{t('account_pools.review_hint')}</p>
        <p>
          {t('account_pools.review_counts', {
            pools: draft?.settings.pools.length,
            clients: (draft?.clients.length || 0) + (draft?.providerClients.length || 0),
          })}
        </p>
        {snapshot?.draft.settings.enabled !== draft?.settings.enabled && (
          <p className={styles.info}>{t('account_pools.restart_hint')}</p>
        )}
      </Modal>
      <Modal
        open={Boolean(assignment)}
        title={t('account_pools.assign')}
        onClose={() => setAssignment(null)}
        closeDisabled={saving}
        footer={
          <>
            <Button variant="secondary" disabled={saving} onClick={() => setAssignment(null)}>
              {t('account_pools.cancel')}
            </Button>
            <Button
              loading={saving}
              disabled={!assignment || !options.some((item) => item.value === assignment.poolId)}
              onClick={() => void assign()}
            >
              {t('account_pools.save')}
            </Button>
          </>
        }
      >
        {assignment && (
          <div className={styles.stack}>
            <strong>{assignment.member.name}</strong>
            <Select
              ariaLabel={t('account_pools.select_pool')}
              value={assignment.poolId}
              options={options}
              disabled={saving}
              onChange={(poolId) => setAssignment({ ...assignment, poolId })}
            />
            <p className="hint">
              {t(
                'family' in assignment.member
                  ? 'account_pools.config_move_hint'
                  : 'account_pools.move_hint'
              )}
            </p>
          </div>
        )}
      </Modal>
    </div>
  );
}
