import { useTranslation } from 'react-i18next';
import type { PoolRuntime } from '@/services/api/accountPools';
import styles from './AccountPoolsPage.module.scss';

export function PoolRuntimeBanner({
  runtime,
  failed,
  savedEnabled,
}: {
  runtime: PoolRuntime | null;
  failed: boolean;
  savedEnabled?: boolean;
}) {
  const { t } = useTranslation();
  const state = failed
    ? 'unknown'
    : !runtime
      ? 'loading'
      : savedEnabled !== undefined && savedEnabled !== runtime.configured_enabled
        ? 'pending'
        : ['disabled', 'active', 'restart_required', 'home_unsupported'].includes(runtime.state)
          ? runtime.state
          : 'unknown';
  const blocked = ['restart_required', 'home_unsupported', 'unknown'].includes(state);
  return (
    <div className={styles.info} role={blocked ? 'alert' : 'status'}>
      <strong>{t('account_pools.runtime_title')}</strong>
      <div>{t(`account_pools.runtime_${state}`)}</div>
    </div>
  );
}
