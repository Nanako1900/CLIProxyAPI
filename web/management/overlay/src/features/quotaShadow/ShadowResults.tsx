import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Select } from '@/components/ui/Select';
import {
  filterShadowEvents,
  shadowAgreementPercent,
  type ShadowStatus,
} from '@/services/api/quotaShadow';
import styles from './QuotaShadowPage.module.scss';

const reasons = [
  'no_candidates',
  'only_candidate',
  'soft_limit',
  'perish_long',
  'perish_short',
  'short_idle',
  'long_remaining',
  'id',
];

export function ShadowResults({
  status,
  poolId,
  type,
  onPool,
  onType,
}: {
  status: ShadowStatus;
  poolId: string;
  type: string;
  onPool: (value: string) => void;
  onType: (value: string) => void;
}) {
  const { t } = useTranslation();
  const rate = shadowAgreementPercent(status);
  const pools = Array.from(
    new Set(status.recent.map((event) => event.pool_id).filter(Boolean))
  ).sort();
  if (poolId !== '*' && poolId && !pools.includes(poolId)) pools.push(poolId);
  const rows = filterShadowEvents(status.recent, poolId, type);
  const counts = {
    comparable: status.comparable,
    agreement: rate === null ? '—' : `${rate.toFixed(1)}%`,
    written: status.written,
    lost: status.dropped + status.write_errors + status.render_errors,
  };
  return (
    <>
      <div className={styles.metrics}>
        {Object.entries(counts).map(([key, value]) => (
          <Card key={key} title={t(`quota_shadow.${key}`)}>
            <strong className={styles.metric}>{value}</strong>
          </Card>
        ))}
      </div>
      <Card title={t('quota_shadow.statistics')}>
        <p className="hint">{t('quota_shadow.stats_hint')}</p>
        <dl className={styles.facts}>
          <dt>{t('quota_shadow.since')}</dt>
          <dd>{status.since ? new Date(status.since).toLocaleString() : '—'}</dd>
          <dt>{t('quota_shadow.comparisons')}</dt>
          <dd>
            {t('quota_shadow.comparison_counts', {
              agree: status.agreements,
              disagree: status.disagreements,
              structural: status.structural,
              empty: status.no_candidates,
            })}
          </dd>
          <dt>{t('quota_shadow.outcomes')}</dt>
          <dd>
            {t('quota_shadow.outcome_counts', {
              success: status.successes,
              failed: status.failures,
              errors: status.selection_errors,
            })}
          </dd>
          <dt>{t('quota_shadow.queue')}</dt>
          <dd>
            {status.queue_pending} / {status.queue_capacity}
          </dd>
          <dt>{t('quota_shadow.losses')}</dt>
          <dd>
            {t('quota_shadow.loss_counts', {
              dropped: status.dropped,
              io: status.write_errors,
              render: status.render_errors,
            })}
          </dd>
          <dt>{t('quota_shadow.effective')}</dt>
          <dd>
            {status.file || '—'}
            {status.file && ` · ${status.max_file_mb} MiB · ${status.soft_limit_percent}%`}
          </dd>
        </dl>
      </Card>
      <Card title={t('quota_shadow.recent')}>
        <div className={styles.filters}>
          <Select
            ariaLabel={t('quota_shadow.pool_filter')}
            value={poolId}
            onChange={onPool}
            options={[
              { value: '*', label: t('quota_shadow.all_pools') },
              { value: '', label: t('quota_shadow.unscoped') },
              ...pools.map((pool) => ({ value: pool, label: pool })),
            ]}
          />
          <Select
            ariaLabel={t('quota_shadow.type_filter')}
            value={type}
            onChange={onType}
            options={[
              { value: '*', label: t('quota_shadow.all_events') },
              ...['selection', 'observation', 'selection_error'].map((value) => ({
                value,
                label: t(`quota_shadow.${value}`),
              })),
            ]}
          />
        </div>
        <p className="hint">{t('quota_shadow.recent_hint', { limit: status.recent_limit })}</p>
        <div
          className={styles.tableScroll}
          tabIndex={0}
          role="region"
          aria-label={t('quota_shadow.recent')}
        >
          <table className={styles.table}>
            <thead>
              <tr>
                {['time', 'event', 'pool', 'model', 'actual', 'shadow', 'result'].map((key) => (
                  <th key={key} scope="col">
                    {t(`quota_shadow.${key}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => (
                <tr key={`${row.ts_ms}:${index}`}>
                  <td>{new Date(row.ts_ms).toLocaleTimeString()}</td>
                  <td>{t(`quota_shadow.${row.type}`)}</td>
                  <td>{row.pool_id || t('quota_shadow.unscoped')}</td>
                  <td>{row.model || '—'}</td>
                  <td>{row.actual || '—'}</td>
                  <td>{row.shadow || '—'}</td>
                  <td>
                    {row.type === 'selection' ? (
                      <>
                        <span>
                          {t(
                            !row.shadow
                              ? 'quota_shadow.reason_no_candidates'
                              : !row.actual_in_pool
                                ? 'quota_shadow.structural'
                                : row.agree
                                  ? 'quota_shadow.same'
                                  : 'quota_shadow.different'
                          )}
                        </span>
                        <small>
                          {t(
                            `quota_shadow.reason_${reasons.includes(row.reason) ? row.reason : 'unknown'}`
                          )}{' '}
                          · {t('quota_shadow.candidates', { count: row.candidates })}
                        </small>
                      </>
                    ) : (
                      <>
                        {t(
                          row.type === 'selection_error'
                            ? 'quota_shadow.selection_error'
                            : row.success
                              ? 'quota_shadow.success'
                              : 'quota_shadow.failed'
                        )}
                        {row.status ? ` (${row.status})` : ''}
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!rows.length && <p>{t('quota_shadow.no_events')}</p>}
        </div>
      </Card>
    </>
  );
}
