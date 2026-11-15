import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Select } from '@/components/ui/Select';
import { Modal } from '@/components/ui/Modal';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';
import {
  accountPoolsConnection,
  configMembershipPatch,
  poolConfigPatch,
  validatePoolDraft,
  samePoolDraft,
  removePoolFromDraft,
  poolRemovalBlockers,
  poolMemberRows,
  verifyPoolMembership,
  type PoolDraft,
  type PoolSnapshot,
  type PoolMember,
  type PoolRuntime,
  type ConfigPoolMember,
} from '@/services/api/accountPools';
import { ClientControls, PoolControls, PoolSettings } from './PoolControls';
import { PoolMembers } from './PoolMembers';
import { PoolRuntimeBanner } from './PoolRuntimeBanner';
import styles from './AccountPoolsPage.module.scss';

const tabs = ['pools', 'members', 'access', 'settings'] as const;
type Tab = (typeof tabs)[number];

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
  const [needsRefresh, setNeedsRefresh] = useState(false);
  const [review, setReview] = useState(false);
  const [runtime, setRuntime] = useState<PoolRuntime | null>(null);
  const [runtimeFailed, setRuntimeFailed] = useState(false);
  const [tab, setTab] = useState<Tab>('pools');
  const [filter, setFilter] = useState('*');
  const [deleting, setDeleting] = useState<string | null>(null);
  const [assignment, setAssignment] = useState<{
    member: PoolMember | ConfigPoolMember;
    poolId: string;
  } | null>(null);
  const generation = useRef(0);
  const alive = useRef(false);
  const mutation = useRef(false);
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const notify = useNotificationStore((state) => state.showNotification);
  const confirm = useNotificationStore((state) => state.showConfirmation);
  const dirty = Boolean(snapshot && draft && !samePoolDraft(snapshot.draft, draft));
  const disabled = saving || loading || !connected;
  const writeDisabled = disabled || needsRefresh;

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
    const details = failure as { status?: number; apiCode?: string };
    const codes = [
      'pool_must_be_disabled',
      'pool_not_empty',
      'pool_state_unavailable',
      'membership_not_confirmed',
    ];
    const key =
      details.status === 412
        ? 'conflict'
        : details.apiCode === 'credential_busy'
          ? 'busy'
          : details.apiCode === 'membership_source_managed'
            ? 'source_managed'
            : codes.includes(details.apiCode || '')
              ? details.apiCode!
              : details.status === 404
                ? 'unsupported'
                : 'failed';
    // Validation errors may contain configured secrets; only display known messages.
    setError(key);
  }, []);

  const load = useCallback(async (): Promise<PoolSnapshot | undefined> => {
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
      setNeedsRefresh(false);
      return next;
    } catch (failure) {
      if (alive.current && request === generation.current) {
        reportError(failure);
        setNeedsRefresh(true);
      }
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
    const reload = () => {
      if (alive.current) {
        setNotice('');
        void load();
      }
    };
    if (dirty)
      confirm({
        title: t('account_pools.discard'),
        message: t('account_pools.discard_hint'),
        onConfirm: reload,
      });
    else reload();
  };
  const beginMutation = () => {
    if (writeDisabled || mutation.current) return false;
    mutation.current = true;
    setSaving(true);
    setError('');
    setNotice('');
    return true;
  };
  const endMutation = () => {
    mutation.current = false;
    if (alive.current) setSaving(false);
  };
  const mutationFailed = (failure: unknown) => {
    if (alive.current) {
      reportError(failure);
      setNeedsRefresh(true);
    }
  };
  const save = async () => {
    if (!snapshot || !draft || !beginMutation()) return;
    const modeChanged = snapshot.draft.settings.enabled !== draft.settings.enabled;
    try {
      await connection.saveConfig(snapshot.etag, poolConfigPatch(draft));
      if (!alive.current) return;
      useConfigStore.getState().clearCache();
      setReview(false);
      const next = await load();
      if (!next) return;
      if (!samePoolDraft(next.draft, draft)) {
        mutationFailed({ apiCode: 'membership_not_confirmed' });
        return;
      }
      setNotice(t(modeChanged ? 'account_pools.restart_required' : 'account_pools.saved'));
    } catch (failure) {
      mutationFailed(failure);
      if (alive.current) setReview(false);
    } finally {
      endMutation();
    }
  };
  const assign = async () => {
    if (!assignment || !snapshot || !beginMutation()) return;
    try {
      if ('family' in assignment.member) {
        await connection.saveConfig(
          snapshot.etag,
          configMembershipPatch(snapshot.config, assignment.member, assignment.poolId)
        );
      } else {
        await connection.assign(assignment.member, assignment.poolId);
      }
      if (!alive.current) return;
      useConfigStore.getState().clearCache();
      setAssignment(null);
      const next = await load();
      if (!next) return;
      const state = verifyPoolMembership(next, assignment.member, assignment.poolId);
      setNotice(
        t(state === 'pending' ? 'account_pools.config_saved' : 'account_pools.member_saved')
      );
    } catch (failure) {
      mutationFailed(failure);
      if (alive.current) setAssignment(null);
    } finally {
      endMutation();
    }
  };
  const remove = async () => {
    if (
      !snapshot ||
      !deleting ||
      dirty ||
      poolRemovalBlockers(snapshot, deleting).length ||
      !beginMutation()
    )
      return;
    try {
      await connection.saveConfig(
        snapshot.etag,
        poolConfigPatch(removePoolFromDraft(snapshot.draft, deleting))
      );
      if (!alive.current) return;
      useConfigStore.getState().clearCache();
      setDeleting(null);
      const next = await load();
      if (!next) return;
      if (next.draft.settings.pools.some((pool) => pool.id === deleting)) {
        mutationFailed({ apiCode: 'membership_not_confirmed' });
        return;
      }
      if (filter === deleting) setFilter('*');
      setNotice(t('account_pools.deleted'));
    } catch (failure) {
      mutationFailed(failure);
      if (alive.current) setDeleting(null);
    } finally {
      endMutation();
    }
  };
  const pools = snapshot?.draft.settings.pools || [];
  const rows = snapshot ? poolMemberRows(snapshot) : [];
  const counts: Record<string, number> = {};
  for (const row of rows) {
    const id = 'family' in row ? row.poolId : row.pool_id;
    counts[id] = (counts[id] || 0) + 1;
  }
  const options = [
    { value: '', label: t('account_pools.unassigned') },
    ...pools.filter((pool) => pool.enabled).map((pool) => ({ value: pool.id, label: pool.name })),
  ];
  const deleteBlockers =
    snapshot && deleting
      ? [...(dirty ? ['save_first'] : []), ...poolRemovalBlockers(snapshot, deleting)]
      : [];
  const showMembers = (id: string) => {
    setFilter(id);
    setTab('members');
  };

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
      {needsRefresh && snapshot && (
        <p className={styles.info}>{t('account_pools.reload_before_retry')}</p>
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
          <div className={styles.summary}>
            <span>
              <b>{pools.length}</b> {t('account_pools.pools')}
            </span>
            <span>
              <b>{rows.length}</b> {t('account_pools.accounts_unit')}
            </span>
            <button type="button" onClick={() => showMembers('')}>
              <b>{counts[''] || 0}</b> {t('account_pools.unassigned')}
            </button>
          </div>
          <div className={styles.tabs} role="tablist" aria-label={t('account_pools.title')}>
            {tabs.map((name, index) => (
              <button
                type="button"
                key={name}
                ref={(node) => {
                  tabRefs.current[index] = node;
                }}
                id={`pool-tab-${name}`}
                role="tab"
                aria-selected={tab === name}
                aria-controls={`pool-panel-${name}`}
                tabIndex={tab === name ? 0 : -1}
                onClick={() => setTab(name)}
                onKeyDown={(event) => {
                  let next: number;
                  if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
                  else if (event.key === 'ArrowLeft')
                    next = (index - 1 + tabs.length) % tabs.length;
                  else if (event.key === 'Home') next = 0;
                  else if (event.key === 'End') next = tabs.length - 1;
                  else return;
                  event.preventDefault();
                  setTab(tabs[next]);
                  tabRefs.current[next]?.focus();
                }}
              >
                {t(`account_pools.tab_${name}`)}
              </button>
            ))}
          </div>
          <section
            className={styles.stack}
            id={`pool-panel-${tab}`}
            role="tabpanel"
            aria-labelledby={`pool-tab-${tab}`}
          >
            {tab === 'pools' && (
              <PoolControls
                draft={draft}
                savedIds={pools.map((pool) => pool.id)}
                disabled={writeDisabled}
                apiBase={apiBase}
                counts={counts}
                onChange={setDraft}
                onDelete={setDeleting}
                onMembers={showMembers}
              />
            )}
            {tab === 'members' && (
              <PoolMembers
                snapshot={snapshot}
                filter={filter}
                onFilter={setFilter}
                disabled={writeDisabled}
                dirty={dirty}
                onAssign={(member, poolId) => setAssignment({ member, poolId })}
              />
            )}
            {tab === 'access' && (
              <ClientControls draft={draft} disabled={writeDisabled} onChange={setDraft} />
            )}
            {tab === 'settings' && (
              <PoolSettings draft={draft} disabled={writeDisabled} onChange={setDraft} />
            )}
          </section>
          {dirty && (
            <div className={styles.saveBar}>
              <span>{t('account_pools.unsaved')}</span>
              <div className={styles.actions}>
                <Button variant="ghost" disabled={disabled} onClick={refresh}>
                  {t('account_pools.discard')}
                </Button>
                <Button
                  disabled={writeDisabled}
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
            </div>
          )}
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
            <Button loading={saving} disabled={writeDisabled} onClick={() => void save()}>
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
              disabled={
                writeDisabled ||
                !assignment ||
                !options.some((item) => item.value === assignment.poolId)
              }
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
            {!('family' in assignment.member) && <code>{assignment.member.id}</code>}
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
      <Modal
        open={Boolean(deleting)}
        title={t('account_pools.delete_pool')}
        onClose={() => setDeleting(null)}
        closeDisabled={saving}
        footer={
          <>
            <Button variant="secondary" disabled={saving} onClick={() => setDeleting(null)}>
              {t('account_pools.cancel')}
            </Button>
            <Button
              variant="danger"
              loading={saving}
              disabled={writeDisabled || deleteBlockers.length > 0}
              onClick={() => void remove()}
            >
              {t('account_pools.delete_pool')}
            </Button>
          </>
        }
      >
        <div className={styles.stack}>
          <strong>
            {pools.find((pool) => pool.id === deleting)?.name} <code>{deleting}</code>
          </strong>
          <p>{t('account_pools.delete_hint')}</p>
          {deleteBlockers.length ? (
            <div role="alert" className={styles.info}>
              {deleteBlockers.map((reason) => (
                <p key={reason}>{t(`account_pools.${reason}`)}</p>
              ))}
            </div>
          ) : (
            <p className={styles.info}>{t('account_pools.delete_grants_hint')}</p>
          )}
          {deleteBlockers.includes('pool_not_empty') && (
            <Button
              variant="secondary"
              disabled={saving}
              onClick={() => {
                if (deleting) showMembers(deleting);
                setDeleting(null);
              }}
            >
              {t('account_pools.view_members')}
            </Button>
          )}
        </div>
      </Modal>
    </div>
  );
}
