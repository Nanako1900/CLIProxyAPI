import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import {
  poolMemberRows,
  type PoolSnapshot,
  type PoolMember,
  type ConfigPoolMember,
} from '@/services/api/accountPools';
import styles from './AccountPoolsPage.module.scss';

export function PoolMembers({
  snapshot,
  filter,
  disabled,
  dirty,
  onFilter,
  onAssign,
}: {
  snapshot: PoolSnapshot;
  filter: string;
  disabled: boolean;
  dirty: boolean;
  onFilter: (value: string) => void;
  onAssign: (member: PoolMember | ConfigPoolMember, poolId: string) => void;
}) {
  const { t } = useTranslation();
  const [search, setSearch] = useState('');
  const [provider, setProvider] = useState('*');
  const [limit, setLimit] = useState(40);
  const rows = poolMemberRows(snapshot);
  const providers = [
    ...new Set(rows.map((member) => ('family' in member ? member.family : member.provider))),
  ].sort();
  const filtered = rows.filter((member) => {
    const poolId = 'family' in member ? member.poolId : member.pool_id;
    const family = 'family' in member ? member.family : member.provider;
    return (
      (filter === '*' || poolId === filter) &&
      (provider === '*' || family === provider) &&
      `${member.name} ${family} ${'family' in member ? member.authIndex : member.id}`
        .toLowerCase()
        .includes(search.toLowerCase())
    );
  });
  return (
    <div className={styles.stack}>
      <div className={styles.toolbar}>
        <div>
          <h2>{t('account_pools.members')}</h2>
          <p>{t('account_pools.members_hint')}</p>
        </div>
        <span className={styles.badge}>
          {t('account_pools.member_count', { count: filtered.length })}
        </span>
      </div>
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
        <label className={styles.field}>
          <span>{t('account_pools.filter')}</span>
          <Select
            ariaLabel={t('account_pools.filter')}
            value={filter}
            onChange={(value) => {
              onFilter(value);
              setLimit(40);
            }}
            options={[
              { value: '*', label: t('account_pools.all_members') },
              { value: '', label: t('account_pools.unassigned') },
              ...snapshot.draft.settings.pools.map((pool) => ({
                value: pool.id,
                label: pool.name,
              })),
            ]}
          />
        </label>
        <label className={styles.field}>
          <span>{t('account_pools.provider')}</span>
          <Select
            ariaLabel={t('account_pools.provider')}
            value={provider}
            onChange={(value) => {
              setProvider(value);
              setLimit(40);
            }}
            options={[
              { value: '*', label: t('account_pools.all_providers') },
              ...providers.map((value) => ({ value, label: value })),
            ]}
          />
        </label>
      </div>
      <div className={styles.memberList}>
        {filtered.slice(0, limit).map((member) => {
          const configMember = 'family' in member;
          const poolId = configMember ? member.poolId : member.pool_id;
          const runtime = configMember
            ? snapshot.members.find((item) => item.auth_index === member.authIndex)
            : member;
          const pending = configMember && (!runtime || runtime.pool_id !== poolId);
          const source = configMember ? 'config' : member.source;
          const key = configMember ? `${member.family}:${member.group}:${member.key}` : member.id;
          const pool = snapshot.draft.settings.pools.find((item) => item.id === poolId);
          return (
            <div className={styles.member} key={key}>
              <div className={styles.memberIdentity}>
                <strong>{member.name}</strong>
                {!configMember && member.name !== member.id && <small>{member.id}</small>}
                <small>
                  {configMember ? member.family : member.provider} ·{' '}
                  {t(`account_pools.source_${source}`)}
                  {runtime?.disabled && ` · ${t('account_pools.disabled_member')}`}
                </small>
                {pending && (
                  <small role="status" className={styles.pending}>
                    {t('account_pools.pending', {
                      pool: runtime?.pool_id || t('account_pools.unassigned'),
                    })}
                  </small>
                )}
              </div>
              <span className={styles.memberPool}>
                {pool?.name || poolId || t('account_pools.unassigned')}
              </span>
              <Button
                variant="secondary"
                size="sm"
                disabled={disabled || dirty || source === 'plugin'}
                title={source === 'plugin' ? t('account_pools.source_managed') : undefined}
                aria-label={`${t('account_pools.assign')} ${member.name}${configMember ? '' : ` (${member.id})`}`}
                onClick={() => onAssign(member, poolId)}
              >
                {t('account_pools.assign')}
              </Button>
            </div>
          );
        })}
        {!filtered.length && <div className={styles.empty}>{t('account_pools.empty_members')}</div>}
      </div>
      {filtered.length > limit && (
        <Button variant="secondary" onClick={() => setLimit(limit + 40)}>
          {t('account_pools.more')}
        </Button>
      )}
    </div>
  );
}
