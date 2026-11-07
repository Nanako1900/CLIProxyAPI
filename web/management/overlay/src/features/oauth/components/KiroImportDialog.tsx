import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { Select } from '@/components/ui/Select';
import { useAuthStore } from '@/stores/useAuthStore';
import { captureKiroImportAPI } from '@/services/api/kiroImport';
import { notifyAuthFilesChanged } from '@/features/authFiles/authFilesEvents';
import {
  KiroImportIssue,
  parseKiroCredentials,
  type KiroCredential,
  type KiroImportDefaults,
} from '../kiroCredentials';
import { createKiroImportRun, type KiroImportResult } from '../kiroImportRun';
import { DialogTitle } from './OAuthFlowDialog';
import dialogStyles from './OAuthFlowDialog.module.scss';
import styles from './KiroImportDialog.module.scss';

interface ImportRow extends KiroImportResult {
  region: string;
  authMethod: KiroImportDefaults['authMethod'];
}

interface KiroImportDialogProps {
  open: boolean;
  icon: string;
  onClose: () => void;
  onCredentialAdded: () => void;
  onViewAuthFiles: () => void;
}

export function KiroImportDialog({
  open,
  icon,
  onClose,
  onCredentialAdded,
  onViewAuthFiles,
}: KiroImportDialogProps) {
  const { t } = useTranslation();
  const inputId = useId();
  const [raw, setRaw] = useState('');
  const [defaults, setDefaults] = useState<KiroImportDefaults>({
    region: 'us-east-1',
    authMethod: 'builder-id',
    startUrl: '',
  });
  const [credentials, setCredentials] = useState<KiroCredential[]>([]);
  const [rows, setRows] = useState<ImportRow[]>([]);
  const [error, setError] = useState<KiroImportIssue>();
  const [busy, setBusy] = useState(false);
  const runRef = useRef<ReturnType<typeof createKiroImportRun> | null>(null);
  const alive = useRef(true);
  const closeRef = useRef(onClose);
  useEffect(() => {
    closeRef.current = onClose;
  }, [onClose]);

  useEffect(() => {
    alive.current = true;
    const unsubscribe = useAuthStore.subscribe((current, previous) => {
      if (
        current.apiBase !== previous.apiBase ||
        current.managementKey !== previous.managementKey ||
        current.isAuthenticated !== previous.isAuthenticated
      ) {
        alive.current = false;
        runRef.current?.cancel();
        setRaw('');
        setCredentials([]);
        setRows([]);
        closeRef.current();
      }
    });
    return () => {
      alive.current = false;
      runRef.current?.cancel();
      unsubscribe();
    };
  }, []);

  const reset = () => {
    setRaw('');
    setCredentials([]);
    setRows([]);
    setError(undefined);
  };
  const close = () => {
    reset();
    onClose();
  };
  const preview = () => {
    try {
      const parsed = parseKiroCredentials(raw, defaults);
      setCredentials(parsed);
      setRows(
        parsed.map(({ index, region, authMethod }) => ({
          index,
          region,
          authMethod,
          status: 'pending',
        }))
      );
      setRaw('');
      setError(undefined);
    } catch (issue) {
      setError(issue instanceof KiroImportIssue ? issue : new KiroImportIssue('json'));
    }
  };
  const start = async () => {
    if (runRef.current || !credentials.length || !alive.current) return;
    setBusy(true);
    const run = createKiroImportRun(
      captureKiroImportAPI(useAuthStore.getState().apiBase),
      (result) => {
        if (!alive.current) return;
        setRows((current) =>
          current.map((row) => (row.index === result.index ? { ...row, ...result } : row))
        );
        if (result.status === 'success') {
          notifyAuthFilesChanged();
          onCredentialAdded();
        }
      }
    );
    runRef.current = run;
    try {
      await run.run(credentials);
    } finally {
      runRef.current = null;
      if (alive.current) {
        setCredentials([]);
        setBusy(false);
      }
    }
  };
  const succeeded = rows.filter((row) => row.status === 'success').length;
  const finished = rows.length > 0 && !credentials.length && !busy;
  const errorMessage = error ? t(`kiro_import.error_${error.code}`, { field: error.field }) : '';

  return (
    <Modal
      open={open}
      onClose={close}
      closeDisabled={busy}
      width={650}
      className={dialogStyles.dialog}
      title={
        <DialogTitle
          title={t('kiro_import.title')}
          caption={t('auth_login.method_plugin')}
          icon={icon}
        />
      }
    >
      <div className={dialogStyles.stack}>
        <p className={dialogStyles.hint}>{t('kiro_import.description')}</p>
        {rows.length === 0 ? (
          <form
            className={styles.form}
            onSubmit={(event) => {
              event.preventDefault();
              preview();
            }}
          >
            <div className="form-group">
              <label htmlFor={inputId}>{t('kiro_import.json_label')}</label>
              <textarea
                id={inputId}
                className={`input ${styles.json}`}
                value={raw}
                onChange={(event) => {
                  setRaw(event.target.value);
                  setError(undefined);
                }}
                autoComplete="off"
                autoCorrect="off"
                autoCapitalize="off"
                spellCheck={false}
                required
                maxLength={1024 * 1024}
                rows={7}
                placeholder={'[{"refreshToken":"…","clientId":"…","clientSecret":"…"}]'}
                aria-invalid={Boolean(error)}
                aria-describedby={error ? `${inputId}-error` : undefined}
              />
            </div>
            <div className={styles.defaults}>
              <div className="form-group">
                <label htmlFor={`${inputId}-type`}>{t('kiro_import.account_type')}</label>
                <Select
                  id={`${inputId}-type`}
                  value={defaults.authMethod}
                  onChange={(value) =>
                    setDefaults((current) => ({
                      ...current,
                      authMethod: value === 'idc' ? 'idc' : 'builder-id',
                      startUrl: value === 'idc' ? current.startUrl : '',
                    }))
                  }
                  options={[
                    { value: 'builder-id', label: 'AWS Builder ID' },
                    { value: 'idc', label: 'IAM Identity Center' },
                  ]}
                />
              </div>
              <Input
                label="AWS Region"
                value={defaults.region}
                required
                spellCheck={false}
                onChange={(event) =>
                  setDefaults((current) => ({ ...current, region: event.target.value }))
                }
              />
            </div>
            {defaults.authMethod === 'idc' && (
              <Input
                label="Start URL"
                value={defaults.startUrl}
                required
                placeholder="https://company.awsapps.com/start"
                type="url"
                onChange={(event) =>
                  setDefaults((current) => ({ ...current, startUrl: event.target.value }))
                }
              />
            )}
            <p className={dialogStyles.hint}>{t('kiro_import.defaults_hint')}</p>
            {error && (
              <div id={`${inputId}-error`} className={dialogStyles.alert} role="alert">
                {error.index
                  ? t('kiro_import.entry_error', { index: error.index, message: errorMessage })
                  : errorMessage}
              </div>
            )}
            <div>
              <button type="submit" className={dialogStyles.primary} disabled={!raw.trim()}>
                {t('kiro_import.preview')}
              </button>
            </div>
          </form>
        ) : (
          <>
            <p className={dialogStyles.hint} role="status" aria-live="polite">
              {finished
                ? t('kiro_import.finished', { success: succeeded, total: rows.length })
                : busy
                  ? t('kiro_import.progress', { success: succeeded, total: rows.length })
                  : t('kiro_import.detected', { count: rows.length })}
            </p>
            <div className={styles.results}>
              <table>
                <thead>
                  <tr>
                    <th>{t('kiro_import.account')}</th>
                    <th>AWS Region</th>
                    <th>{t('kiro_import.status')}</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => (
                    <tr key={row.index}>
                      <td>
                        {t('kiro_import.account_number', { index: row.index })}
                        <small>
                          {row.authMethod === 'idc' ? 'IAM Identity Center' : 'AWS Builder ID'}
                        </small>
                      </td>
                      <td>{row.region}</td>
                      <td data-status={row.status}>
                        {t(`kiro_import.status_${row.status}`)}
                        {row.failure && <small>{t(`kiro_import.failure_${row.failure}`)}</small>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className={styles.actions}>
              {!finished && (
                <button
                  type="button"
                  className={dialogStyles.primary}
                  disabled={busy}
                  onClick={() => void start()}
                >
                  {t(busy ? 'kiro_import.importing' : 'kiro_import.import')}
                </button>
              )}
              {!busy && (
                <button type="button" className={dialogStyles.secondary} onClick={reset}>
                  {t(finished ? 'kiro_import.another' : 'kiro_import.edit')}
                </button>
              )}
              {finished && (
                <button type="button" className={dialogStyles.primary} onClick={onViewAuthFiles}>
                  {t('auth_login.view_auth_files')}
                </button>
              )}
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}
