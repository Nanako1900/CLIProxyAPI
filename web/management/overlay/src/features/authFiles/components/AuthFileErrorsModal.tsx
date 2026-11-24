import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal } from '@/components/ui/Modal';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { IconCopy, IconRefreshCw, IconTrash2 } from '@/components/ui/icons';
import type { AuthFileItem } from '@/types';
import { formatDateTimeValue } from '@/utils/format';
import { deriveAuthFileIdentity } from '@/features/authFiles/identity';
import { logsApi, responseDataToText } from '@/services/api/logs';
import {
  authFileErrorsApi,
  isSafeExternalHttpUrl,
  type AuthFileErrorRecord,
  type AuthFileErrorsResponse,
} from '@/services/api/authFileErrors';
import styles from './AuthFileErrorsModal.module.scss';

export type AuthFileErrorsModalProps = {
  file: AuthFileItem | null;
  disableControls?: boolean;
  onClose: () => void;
  onCopyText: (text: string) => void;
  onHistoryCleared?: () => void;
};

type RawLogState = {
  loading: boolean;
  content?: string;
  error?: string;
};

const formatErrorMessage = (error: unknown, fallback: string): string => {
  if (error instanceof Error && error.message.trim()) {
    return error.message.trim();
  }
  return fallback;
};

export function AuthFileErrorsModal(props: AuthFileErrorsModalProps) {
  const { file, disableControls = false, onClose, onCopyText, onHistoryCleared } = props;
  const { t, i18n } = useTranslation();

  const [loading, setLoading] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [detail, setDetail] = useState<AuthFileErrorsResponse | null>(null);
  const [rawLogs, setRawLogs] = useState<Record<string, RawLogState>>({});

  const fileName = file?.name ?? '';
  const authIndex = file?.authIndex;

  const loadErrors = useCallback(
    async (signal?: AbortSignal) => {
      if (!fileName) return;
      setLoading(true);
      setLoadError(null);
      try {
        const res = await authFileErrorsApi.fetch({ name: fileName, authIndex }, { signal });
        if (!signal?.aborted) {
          setDetail(res);
        }
      } catch (err: unknown) {
        if (signal?.aborted) return;
        setLoadError(formatErrorMessage(err, t('auth_files.error_modal_load_failed')));
      } finally {
        if (!signal?.aborted) {
          setLoading(false);
        }
      }
    },
    [authIndex, fileName, t]
  );

  useEffect(() => {
    setDetail(null);
    setLoadError(null);
    setRawLogs({});
    if (!fileName) return;

    const controller = new AbortController();
    void loadErrors(controller.signal);
    return () => controller.abort();
  }, [fileName, loadErrors]);

  const handleClearHistory = async () => {
    if (!fileName || clearing || disableControls) return;
    setClearing(true);
    try {
      await authFileErrorsApi.clear({ name: fileName, authIndex });
      setRawLogs({});
      await loadErrors();
      onHistoryCleared?.();
    } catch (err: unknown) {
      setLoadError(formatErrorMessage(err, t('auth_files.error_modal_clear_failed')));
    } finally {
      setClearing(false);
    }
  };

  const handleToggleRawLog = async (record: AuthFileErrorRecord) => {
    const requestId = record.requestId;
    if (!requestId) return;
    const existing = rawLogs[record.id];
    if (existing?.content !== undefined) {
      setRawLogs((prev) => {
        const next = { ...prev };
        delete next[record.id];
        return next;
      });
      return;
    }
    if (existing?.loading) return;

    setRawLogs((prev) => ({ ...prev, [record.id]: { loading: true } }));
    try {
      const response = await logsApi.downloadRequestLogById(requestId);
      const text = await responseDataToText(response.data);
      setRawLogs((prev) => ({
        ...prev,
        [record.id]: { loading: false, content: text || t('auth_files.error_modal_raw_log_empty') },
      }));
    } catch (err: unknown) {
      setRawLogs((prev) => ({
        ...prev,
        [record.id]: {
          loading: false,
          error: formatErrorMessage(err, t('auth_files.error_modal_raw_log_failed')),
        },
      }));
    }
  };

  const identity = file ? deriveAuthFileIdentity(file) : null;
  const displayName = identity?.primary || fileName;

  const currentStatusMessage = detail?.statusMessage || file?.statusMessage || '';
  const currentLastError = detail?.lastError ?? file?.lastError;
  const currentRefreshError = detail?.refreshError ?? file?.refreshError;
  const requiresSignIn = detail?.requiresSignIn ?? file?.requiresSignIn === true;
  const recoveryHint = detail?.recoveryHint ?? file?.recoveryHint;
  const tosAppealUrl = detail?.tosAppealUrl ?? file?.tosAppealUrl;
  const tosAppealUrlText = detail?.tosAppealUrlText ?? file?.tosAppealUrlText;
  const modelErrors = detail?.modelErrors ?? [];
  const recentErrors = detail?.recentErrors ?? [];

  const hasCurrentSummary = Boolean(
    currentStatusMessage ||
      currentLastError ||
      currentRefreshError ||
      requiresSignIn ||
      (tosAppealUrl && isSafeExternalHttpUrl(tosAppealUrl))
  );

  const phaseLabel = (phase: string): string => {
    switch (phase.toLowerCase()) {
      case 'stream':
        return t('auth_files.error_phase_stream');
      case 'prepare':
        return t('auth_files.error_phase_prepare');
      case 'refresh':
        return t('auth_files.error_phase_refresh');
      case 'count_tokens':
        return t('auth_files.error_phase_count_tokens');
      default:
        return t('auth_files.error_phase_execute');
    }
  };

  return (
    <Modal
      open={Boolean(file)}
      onClose={onClose}
      width={760}
      title={`${t('auth_files.error_modal_title')} - ${displayName}`}
      footer={
        <div className={styles.footer}>
          <div className={styles.footerLeft}>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => void loadErrors()}
              disabled={loading || disableControls}
            >
              {loading ? <LoadingSpinner size={14} /> : <IconRefreshCw size={14} />}
              {t('common.refresh')}
            </Button>
            {recentErrors.length > 0 && (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => void handleClearHistory()}
                disabled={clearing || loading || disableControls}
              >
                {clearing ? <LoadingSpinner size={14} /> : <IconTrash2 size={14} />}
                {t('auth_files.error_modal_clear')}
              </Button>
            )}
          </div>
          <Button variant="secondary" onClick={onClose}>
            {t('common.close')}
          </Button>
        </div>
      }
    >
      <div className={styles.body}>
        {file && (
          <div className={styles.identityMeta}>
            <span className={styles.filePath}>{file.name}</span>
            {detail?.nextRetryAfter && (
              <span className={styles.metaBadge}>
                {t('auth_files.error_modal_retry_after', {
                  time: formatDateTimeValue(detail.nextRetryAfter, i18n.language),
                })}
              </span>
            )}
          </div>
        )}

        {loadError && (
          <div className={styles.loadError} role="alert">
            {loadError}
          </div>
        )}

        {hasCurrentSummary && (
          <section className={styles.section}>
            <h4 className={styles.sectionTitle}>{t('auth_files.error_modal_current_section')}</h4>
            <div className={styles.currentCard}>
              {currentStatusMessage && (
                <div className={styles.summaryRow}>
                  <span className={styles.summaryLabel}>{t('auth_files.error_modal_status')}</span>
                  <span className={styles.summaryValue}>{currentStatusMessage}</span>
                </div>
              )}

              {currentRefreshError && (
                <div className={styles.errorBlock}>
                  <div className={styles.errorBlockHead}>
                    <div className={styles.badges}>
                      <span className={styles.phaseBadge}>
                        {t('auth_files.error_phase_refresh')}
                      </span>
                      {currentRefreshError.httpStatus && (
                        <span className={styles.statusBadge}>
                          HTTP {currentRefreshError.httpStatus}
                        </span>
                      )}
                      {currentRefreshError.code && (
                        <span className={styles.codeBadge}>{currentRefreshError.code}</span>
                      )}
                    </div>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => onCopyText(currentRefreshError.message)}
                      title={t('common.copy')}
                    >
                      <IconCopy size={13} />
                    </Button>
                  </div>
                  <pre className={styles.errorPre}>{currentRefreshError.message}</pre>
                </div>
              )}

              {currentLastError &&
                currentLastError.message !== currentRefreshError?.message && (
                  <div className={styles.errorBlock}>
                    <div className={styles.errorBlockHead}>
                      <div className={styles.badges}>
                        {currentLastError.phase && (
                          <span className={styles.phaseBadge}>
                            {phaseLabel(currentLastError.phase)}
                          </span>
                        )}
                        {currentLastError.httpStatus && (
                          <span className={styles.statusBadge}>
                            HTTP {currentLastError.httpStatus}
                          </span>
                        )}
                        {currentLastError.code && (
                          <span className={styles.codeBadge}>{currentLastError.code}</span>
                        )}
                        {currentLastError.model && (
                          <span className={styles.modelBadge}>{currentLastError.model}</span>
                        )}
                      </div>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => onCopyText(currentLastError.message)}
                        title={t('common.copy')}
                      >
                        <IconCopy size={13} />
                      </Button>
                    </div>
                    <pre className={styles.errorPre}>{currentLastError.message}</pre>
                  </div>
                )}

              {requiresSignIn && recoveryHint && (
                <p className={styles.recoveryHint}>{recoveryHint}</p>
              )}

              {tosAppealUrl && isSafeExternalHttpUrl(tosAppealUrl) && (
                <a
                  href={tosAppealUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.appealLink}
                >
                  {tosAppealUrlText || tosAppealUrl}
                </a>
              )}
            </div>
          </section>
        )}

        {modelErrors.length > 0 && (
          <section className={styles.section}>
            <h4 className={styles.sectionTitle}>
              {t('auth_files.error_modal_models_section', { count: modelErrors.length })}
            </h4>
            <div className={styles.list}>
              {modelErrors.map((item) => (
                <div key={item.model} className={styles.recordCard}>
                  <div className={styles.recordHead}>
                    <div className={styles.badges}>
                      <span className={styles.modelBadge}>{item.model}</span>
                      {item.httpStatus && (
                        <span className={styles.statusBadge}>HTTP {item.httpStatus}</span>
                      )}
                      {item.quotaReason && (
                        <span className={styles.codeBadge}>{item.quotaReason}</span>
                      )}
                      {item.code && item.code !== item.quotaReason && (
                        <span className={styles.codeBadge}>{item.code}</span>
                      )}
                    </div>
                    {item.nextRetryAfter && (
                      <span className={styles.timestamp}>
                        {t('auth_files.error_modal_retry_after', {
                          time: formatDateTimeValue(item.nextRetryAfter, i18n.language),
                        })}
                      </span>
                    )}
                  </div>
                  {(item.message || item.statusMessage) && (
                    <pre className={styles.errorPre}>{item.message || item.statusMessage}</pre>
                  )}
                </div>
              ))}
            </div>
          </section>
        )}

        <section className={styles.section}>
          <h4 className={styles.sectionTitle}>
            {t('auth_files.error_modal_history_section', { count: recentErrors.length })}
          </h4>
          {loading && !detail ? (
            <div className={styles.loadingState}>
              <LoadingSpinner size={16} />
              <span>{t('auth_files.error_modal_loading')}</span>
            </div>
          ) : recentErrors.length === 0 ? (
            <EmptyState
              title={t('auth_files.error_modal_empty_title')}
              description={t('auth_files.error_modal_empty_desc')}
            />
          ) : (
            <div className={styles.list}>
              {recentErrors.map((record) => {
                const rawState = rawLogs[record.id];
                return (
                  <article key={record.id} className={styles.recordCard}>
                    <div className={styles.recordHead}>
                      <div className={styles.badges}>
                        <span className={styles.phaseBadge}>{phaseLabel(record.phase)}</span>
                        {record.httpStatus && (
                          <span className={styles.statusBadge}>HTTP {record.httpStatus}</span>
                        )}
                        {record.code && <span className={styles.codeBadge}>{record.code}</span>}
                        {record.model && <span className={styles.modelBadge}>{record.model}</span>}
                      </div>
                      <div className={styles.recordActions}>
                        {record.timestamp && (
                          <span className={styles.timestamp}>
                            {formatDateTimeValue(record.timestamp, i18n.language)}
                          </span>
                        )}
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => onCopyText(record.message)}
                          title={t('common.copy')}
                        >
                          <IconCopy size={13} />
                        </Button>
                      </div>
                    </div>

                    <pre className={styles.errorPre}>{record.message}</pre>

                    {(record.requestId || record.cooldownUntil) && (
                      <div className={styles.recordFoot}>
                        {record.requestId && (
                          <span className={styles.requestId}>
                            ID: <code>{record.requestId}</code>
                          </span>
                        )}
                        {record.cooldownUntil && (
                          <span className={styles.timestamp}>
                            {t('auth_files.error_modal_cooldown_until', {
                              time: formatDateTimeValue(record.cooldownUntil, i18n.language),
                            })}
                          </span>
                        )}
                        {record.hasRequestLog && record.requestId && (
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => void handleToggleRawLog(record)}
                            disabled={rawState?.loading}
                          >
                            {rawState?.loading
                              ? t('common.loading')
                              : rawState?.content !== undefined
                                ? t('auth_files.error_modal_hide_raw_log')
                                : t('auth_files.error_modal_view_raw_log')}
                          </Button>
                        )}
                      </div>
                    )}

                    {rawState?.error && (
                      <div className={styles.loadError} role="alert">
                        {rawState.error}
                      </div>
                    )}
                    {rawState?.content !== undefined && (
                      <pre className={styles.rawLogPre}>{rawState.content}</pre>
                    )}
                  </article>
                );
              })}
            </div>
          )}
        </section>
      </div>
    </Modal>
  );
}
