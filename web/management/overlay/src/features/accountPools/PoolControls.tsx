import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type { Pool, PoolClient, PoolDraft } from '@/services/api/accountPools';
import { poolBaseURL } from '@/services/api/accountPools';
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

export function PoolControls({
  draft,
  savedIds,
  disabled,
  apiBase,
  onChange,
}: {
  draft: PoolDraft;
  savedIds: string[];
  disabled: boolean;
  apiBase: string;
  onChange: (draft: PoolDraft) => void;
}) {
  const { t } = useTranslation();
  const notify = useNotificationStore((state) => state.showNotification);
  const updatePool = (index: number, update: Partial<Pool>) =>
    onChange({
      ...draft,
      settings: {
        ...draft.settings,
        pools: draft.settings.pools.map((pool, i) => (i === index ? { ...pool, ...update } : pool)),
      },
    });
  const copy = async (url: string) =>
    notify(t((await copyToClipboard(url)) ? 'account_pools.copied' : 'account_pools.copy_failed'));
  return (
    <>
      <Card title={t('account_pools.mode')}>
        <div className={styles.stack}>
          <ToggleSwitch
            label={t('account_pools.enable')}
            checked={draft.settings.enabled}
            disabled={disabled}
            onChange={(enabled) => onChange({ ...draft, settings: { ...draft.settings, enabled } })}
          />
          <p className="hint">{t('account_pools.restart_hint')}</p>
          <Select
            ariaLabel={t('account_pools.unscoped')}
            value={draft.settings.unscoped.mode}
            disabled={disabled}
            options={[
              { value: 'deny', label: t('account_pools.deny') },
              { value: 'default', label: t('account_pools.default') },
            ]}
            onChange={(mode) =>
              onChange({
                ...draft,
                settings: { ...draft.settings, unscoped: { mode, 'pool-id': '' } },
              })
            }
          />
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
        </div>
      </Card>
      <div className={styles.heading}>
        <h2>{t('account_pools.pools')}</h2>
        <Button
          variant="secondary"
          disabled={disabled}
          onClick={() =>
            onChange({
              ...draft,
              settings: {
                ...draft.settings,
                pools: [...draft.settings.pools, { id: '', name: '', enabled: true }],
              },
            })
          }
        >
          {t('account_pools.add_pool')}
        </Button>
      </div>
      {!draft.settings.pools.length && <p>{t('account_pools.empty_pools')}</p>}
      <div className={styles.poolGrid}>
        {draft.settings.pools.map((pool, index) => (
          <Card key={index}>
            <div className={styles.stack}>
              <Input
                label={t('account_pools.pool_id')}
                value={pool.id}
                maxLength={48}
                hint={t('account_pools.id_hint')}
                disabled={disabled || index < savedIds.length}
                onChange={(event) => updatePool(index, { id: event.target.value })}
              />
              <Input
                label={t('account_pools.pool_name')}
                value={pool.name}
                maxLength={100}
                disabled={disabled}
                onChange={(event) => updatePool(index, { name: event.target.value })}
              />
              <ToggleSwitch
                label={t('account_pools.pool_enabled')}
                checked={pool.enabled}
                disabled={disabled}
                onChange={(enabled) => updatePool(index, { enabled })}
              />
              <Select
                ariaLabel={t('account_pools.strategy')}
                value={pool.strategy || ''}
                disabled={disabled}
                onChange={(strategy) => updatePool(index, { strategy })}
                options={['', 'round-robin', 'weighted-round-robin', 'fill-first'].map((value) => ({
                  value,
                  label: t(`account_pools.strategy_${value || 'inherit'}`),
                }))}
              />
              {pool.id && (
                <div className={styles.urls}>
                  <strong>{t('account_pools.addresses')}</strong>
                  {['openai', 'anthropic', 'gemini'].map((protocol) => {
                    const url = poolBaseURL(apiBase, pool.id, protocol);
                    return (
                      <div key={protocol}>
                        <small>
                          {protocol === 'openai'
                            ? 'OpenAI'
                            : protocol === 'anthropic'
                              ? 'Anthropic'
                              : 'Gemini'}
                        </small>
                        <code>{url}</code>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`${t('account_pools.copy')} ${protocol}`}
                          onClick={() => void copy(url)}
                        >
                          {t('account_pools.copy')}
                        </Button>
                      </div>
                    );
                  })}
                </div>
              )}
              {index >= savedIds.length && (
                <Button
                  variant="ghost"
                  disabled={disabled}
                  onClick={() =>
                    onChange({
                      ...draft,
                      settings: {
                        ...draft.settings,
                        pools: draft.settings.pools.filter((_, i) => i !== index),
                      },
                    })
                  }
                >
                  {t('account_pools.remove_draft')}
                </Button>
              )}
            </div>
          </Card>
        ))}
      </div>
    </>
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
