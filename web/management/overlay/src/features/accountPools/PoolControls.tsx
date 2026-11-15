import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { Modal } from '@/components/ui/Modal';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconCopy, IconPlus } from '@/components/ui/icons';
import type { Pool, PoolClient, PoolDraft } from '@/services/api/accountPools';
import { poolBaseURL, removePoolFromDraft } from '@/services/api/accountPools';
import { copyToClipboard } from '@/utils/clipboard';
import { useNotificationStore } from '@/stores';
import styles from './AccountPoolsPage.module.scss';

export function GrantPicker({
  value,
  pools,
  disabled,
  onChange,
}: {
  value: string[];
  pools: Pool[];
  disabled: boolean;
  onChange: (value: string[]) => void;
}) {
  const { t } = useTranslation();
  return (
    <fieldset className={styles.grants} disabled={disabled}>
      <legend>{t('account_pools.allowed')}</legend>
      {['*', ...new Set(pools.map((pool) => pool.id).filter(Boolean))].map((id) => (
        <label key={id}>
          <input
            type="checkbox"
            checked={value.includes(id)}
            onChange={(event) => {
              onChange(
                id === '*'
                  ? event.target.checked
                    ? ['*']
                    : []
                  : event.target.checked
                    ? [...value.filter((entry) => entry !== '*'), id]
                    : value.filter((entry) => entry !== id)
              );
            }}
          />
          {id === '*' ? t('account_pools.all_pools') : id}
        </label>
      ))}
      <small>{t('account_pools.grant_hint')}</small>
    </fieldset>
  );
}

export function PoolEditor({
  pool,
  lockedId,
  disabled,
  onChange,
}: {
  pool: Pool;
  lockedId: boolean;
  disabled: boolean;
  onChange: (pool: Pool) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className={styles.stack}>
      <Input
        label={t('account_pools.pool_name')}
        value={pool.name}
        maxLength={100}
        disabled={disabled}
        onChange={(event) => onChange({ ...pool, name: event.target.value })}
      />
      <Input
        label={t('account_pools.pool_id')}
        value={pool.id}
        maxLength={48}
        hint={t(lockedId ? 'account_pools.id_locked' : 'account_pools.id_hint')}
        disabled={disabled || lockedId}
        onChange={(event) => onChange({ ...pool, id: event.target.value })}
      />
      <ToggleSwitch
        label={t('account_pools.pool_enabled')}
        checked={pool.enabled}
        disabled={disabled}
        onChange={(enabled) => onChange({ ...pool, enabled })}
      />
      <label className={styles.field}>
        <span>{t('account_pools.strategy')}</span>
        <Select
          ariaLabel={t('account_pools.strategy')}
          value={pool.strategy || ''}
          disabled={disabled}
          onChange={(strategy) => onChange({ ...pool, strategy })}
          options={['', 'round-robin', 'weighted-round-robin', 'fill-first'].map((value) => ({
            value,
            label: t(`account_pools.strategy_${value || 'inherit'}`),
          }))}
        />
      </label>
    </div>
  );
}

export function PoolControls({
  draft,
  savedIds,
  disabled,
  apiBase,
  counts = {},
  onChange,
  onDelete,
  onMembers,
}: {
  draft: PoolDraft;
  savedIds: string[];
  disabled: boolean;
  apiBase: string;
  counts?: Record<string, number>;
  onChange: (draft: PoolDraft) => void;
  onDelete?: (id: string) => void;
  onMembers?: (id: string) => void;
}) {
  const { t } = useTranslation();
  const notify = useNotificationStore((state) => state.showNotification);
  const [protocol, setProtocol] = useState('openai');
  const [editor, setEditor] = useState<{ index: number | null; pool: Pool } | null>(null);
  const [invalid, setInvalid] = useState(false);
  const edit = (index: number | null) => {
    setInvalid(false);
    setEditor({
      index,
      pool:
        index === null ? { id: '', name: '', enabled: true } : { ...draft.settings.pools[index] },
    });
  };
  const apply = () => {
    if (!editor || disabled) return;
    const pool = { ...editor.pool, name: editor.pool.name.trim() };
    if (
      !/^[a-z][a-z0-9-]{0,47}$/.test(pool.id) ||
      !pool.name ||
      Array.from(pool.name).length > 100 ||
      draft.settings.pools.some((item, index) => index !== editor.index && item.id === pool.id)
    ) {
      setInvalid(true);
      return;
    }
    onChange({
      ...draft,
      settings: {
        ...draft.settings,
        pools:
          editor.index === null
            ? [...draft.settings.pools, pool]
            : draft.settings.pools.map((item, index) => (index === editor.index ? pool : item)),
      },
    });
    setEditor(null);
  };
  const copy = async (url: string) =>
    notify(t((await copyToClipboard(url)) ? 'account_pools.copied' : 'account_pools.copy_failed'));
  return (
    <>
      <div className={styles.toolbar}>
        <div>
          <h2>{t('account_pools.pools')}</h2>
          <p>{t('account_pools.overview_hint')}</p>
        </div>
        <div className={styles.actions}>
          <Select
            ariaLabel={t('account_pools.protocol')}
            fullWidth={false}
            className={styles.protocol}
            value={protocol}
            onChange={setProtocol}
            options={['openai', 'anthropic', 'gemini'].map((value) => ({
              value,
              label: value === 'openai' ? 'OpenAI' : value === 'anthropic' ? 'Anthropic' : 'Gemini',
            }))}
          />
          <Button disabled={disabled} onClick={() => edit(null)}>
            <IconPlus size={16} /> {t('account_pools.add_pool')}
          </Button>
        </div>
      </div>
      {!draft.settings.pools.length && (
        <div className={styles.empty}>
          <strong>{t('account_pools.empty_pools')}</strong>
          <p>{t('account_pools.empty_pools_hint')}</p>
        </div>
      )}
      <div className={styles.poolGrid}>
        {draft.settings.pools.map((pool, index) => {
          const saved = savedIds.includes(pool.id);
          const url = poolBaseURL(apiBase, pool.id, protocol);
          return (
            <article className={styles.poolCard} key={pool.id}>
              <div className={styles.poolTitle}>
                <div>
                  <h3>{pool.name}</h3>
                  <code>{pool.id}</code>
                </div>
                <span className={`${styles.badge} ${pool.enabled ? styles.enabled : ''}`}>
                  {t(
                    !saved
                      ? 'account_pools.draft_badge'
                      : pool.enabled
                        ? 'account_pools.enabled_badge'
                        : 'account_pools.disabled_badge'
                  )}
                </span>
              </div>
              <div className={styles.poolMeta}>
                <span>
                  <b>{counts[pool.id] || 0}</b> {t('account_pools.accounts_unit')}
                </span>
                <span>{t(`account_pools.strategy_${pool.strategy || 'inherit'}`)}</span>
              </div>
              <div className={styles.endpoint}>
                <span>{t('account_pools.base_url')}</span>
                <div>
                  <code>{url}</code>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`${t('account_pools.copy')} ${pool.name}`}
                    onClick={() => void copy(url)}
                  >
                    <IconCopy size={16} />
                  </Button>
                </div>
              </div>
              <footer className={styles.cardActions}>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={disabled}
                  onClick={() => edit(index)}
                >
                  {t('account_pools.edit')}
                </Button>
                {saved && (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={disabled}
                    onClick={() => onMembers?.(pool.id)}
                  >
                    {t('account_pools.view_members')}
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  className={styles.deleteAction}
                  disabled={disabled}
                  aria-label={`${t('account_pools.delete_pool')} ${pool.name}`}
                  onClick={() =>
                    saved ? onDelete?.(pool.id) : onChange(removePoolFromDraft(draft, pool.id))
                  }
                >
                  {t(saved ? 'account_pools.delete' : 'account_pools.remove_draft')}
                </Button>
              </footer>
            </article>
          );
        })}
      </div>
      <Modal
        open={Boolean(editor)}
        title={t(editor?.index === null ? 'account_pools.add_pool' : 'account_pools.edit_pool')}
        onClose={() => setEditor(null)}
        closeDisabled={disabled}
        footer={
          <>
            <Button variant="secondary" disabled={disabled} onClick={() => setEditor(null)}>
              {t('account_pools.cancel')}
            </Button>
            <Button disabled={disabled} type="submit" form="pool-editor-form">
              {t('account_pools.apply_draft')}
            </Button>
          </>
        }
      >
        {editor && (
          <form
            id="pool-editor-form"
            onSubmit={(event) => {
              event.preventDefault();
              apply();
            }}
          >
            <PoolEditor
              pool={editor.pool}
              lockedId={
                editor.index !== null && savedIds.includes(draft.settings.pools[editor.index].id)
              }
              disabled={disabled}
              onChange={(pool) => setEditor({ ...editor, pool })}
            />
            {invalid && (
              <p role="alert" className="error-box">
                {t('account_pools.invalid_pool')}
              </p>
            )}
          </form>
        )}
      </Modal>
    </>
  );
}

export function PoolSettings({
  draft,
  disabled,
  onChange,
}: {
  draft: PoolDraft;
  disabled: boolean;
  onChange: (draft: PoolDraft) => void;
}) {
  const { t } = useTranslation();
  return (
    <Card title={t('account_pools.mode')}>
      <div className={styles.stack}>
        <ToggleSwitch
          label={t('account_pools.enable')}
          checked={draft.settings.enabled}
          disabled={disabled}
          onChange={(enabled) => onChange({ ...draft, settings: { ...draft.settings, enabled } })}
        />
        <p className="hint">{t('account_pools.restart_hint')}</p>
        <label className={styles.field}>
          <span>{t('account_pools.unscoped')}</span>
          <Select
            ariaLabel={t('account_pools.unscoped')}
            value={draft.settings.unscoped.mode}
            disabled={disabled}
            options={['deny', 'default'].map((value) => ({
              value,
              label: t(`account_pools.${value}`),
            }))}
            onChange={(mode) =>
              onChange({
                ...draft,
                settings: { ...draft.settings, unscoped: { mode, 'pool-id': '' } },
              })
            }
          />
        </label>
        {draft.settings.unscoped.mode === 'default' && (
          <Select
            ariaLabel={t('account_pools.default_pool')}
            disabled={disabled}
            value={draft.settings.unscoped['pool-id'] || ''}
            options={[
              { value: '', label: t('account_pools.select_pool') },
              ...draft.settings.pools
                .filter((pool) => pool.enabled)
                .map((pool) => ({ value: pool.id, label: `${pool.name} (${pool.id})` })),
            ]}
            onChange={(id) =>
              onChange({
                ...draft,
                settings: { ...draft.settings, unscoped: { mode: 'default', 'pool-id': id } },
              })
            }
          />
        )}
        <details className={styles.details}>
          <summary>{t('account_pools.compatibility')}</summary>
          <p>{t('account_pools.scope_hint')}</p>
        </details>
      </div>
    </Card>
  );
}

export function ClientControls({
  draft,
  disabled,
  onChange,
}: {
  draft: PoolDraft;
  disabled: boolean;
  onChange: (draft: PoolDraft) => void;
}) {
  const { t } = useTranslation();
  const section = (field: 'clients' | 'providerClients') => {
    const clients = draft[field];
    const change = (index: number, update: Partial<PoolClient>) =>
      onChange({
        ...draft,
        [field]: clients.map((client, i) => (i === index ? { ...client, ...update } : client)),
      });
    return (
      <Card
        title={t(`account_pools.${field}`)}
        extra={
          <Button
            variant="secondary"
            disabled={disabled}
            onClick={() =>
              onChange({
                ...draft,
                [field]: [
                  ...clients,
                  {
                    id: '',
                    enabled: true,
                    'allowed-pools': [],
                    ...(field === 'clients' ? { 'api-key': '' } : { provider: '' }),
                  },
                ],
              })
            }
          >
            {t('account_pools.add_client')}
          </Button>
        }
      >
        <div className={styles.stack}>
          {clients.map((client, index) => (
            <div className={styles.client} key={index}>
              <Input
                label={t('account_pools.client_id')}
                value={client.id}
                disabled={disabled}
                onChange={(event) => change(index, { id: event.target.value })}
              />
              {field === 'clients' ? (
                <Input
                  label={t('account_pools.client_key')}
                  type="password"
                  autoComplete="new-password"
                  value={client['api-key'] || ''}
                  disabled={disabled}
                  onChange={(event) => change(index, { 'api-key': event.target.value })}
                />
              ) : (
                <Input
                  label={t('account_pools.provider')}
                  value={client.provider || ''}
                  disabled={disabled}
                  onChange={(event) => change(index, { provider: event.target.value })}
                />
              )}
              <ToggleSwitch
                label={t('account_pools.client_enabled')}
                checked={client.enabled !== false}
                disabled={disabled}
                onChange={(enabled) => change(index, { enabled })}
              />
              <GrantPicker
                value={client['allowed-pools']}
                pools={draft.settings.pools}
                disabled={disabled}
                onChange={(grants) => change(index, { 'allowed-pools': grants })}
              />
              <Button
                variant="ghost"
                disabled={disabled}
                onClick={() =>
                  onChange({ ...draft, [field]: clients.filter((_, i) => i !== index) })
                }
              >
                {t('account_pools.remove_client')}
              </Button>
            </div>
          ))}
          {!clients.length && <p className="hint">{t('account_pools.empty_clients')}</p>}
        </div>
      </Card>
    );
  };
  return (
    <>
      <h2>{t('account_pools.access')}</h2>
      <Card title={t('account_pools.legacy')}>
        <GrantPicker
          value={draft.legacyGrants}
          pools={draft.settings.pools}
          disabled={disabled}
          onChange={(legacyGrants) => onChange({ ...draft, legacyGrants })}
        />
      </Card>
      {section('clients')}
      <details>
        <summary>{t('account_pools.providerClients')}</summary>
        {section('providerClients')}
      </details>
    </>
  );
}
