import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { Select } from '@/components/ui/Select';
import { apiClient } from '@/services/api/client';
import {
  clientUserAgentsConnection,
  type ClientProvider,
  type ClientUserAgent,
} from '@/services/api/clientUserAgents';
import { useAuthStore } from '@/stores/useAuthStore';
import type { VisualConfigValues } from '@/types/visualConfig';
import { clientProfileDraft, extractIdentityHeaders, userAgentDraft } from '../../clientUserAgents';
import styles from './ClientUserAgentPicker.module.scss';

type Props = {
  provider: ClientProvider;
  disabled?: boolean;
  onChange: (patch: Partial<VisualConfigValues>) => void;
};

export function ClientUserAgentPicker({ provider, disabled, onChange }: Props) {
  const { t, i18n } = useTranslation();
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const authenticated = useAuthStore((state) => state.isAuthenticated);
  const generation = useRef(0);
  const [reload, setReload] = useState(0);
  const [catalog, setCatalog] = useState<{ items: ClientUserAgent[]; revision: number }>({
    items: [],
    revision: -1,
  });
  const [selected, setSelected] = useState('');
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [applied, setApplied] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [raw, setRaw] = useState('');
  const ns = 'client_user_agents.';
  const currentRevision = apiClient.getConnectionRevision();
  const current = authenticated && catalog.revision === currentRevision;
  const entry = current ? catalog.items.find((item) => item.id === selected) : undefined;
  const blocked = disabled || !current || loading || busy;

  useEffect(() => {
    const request = ++generation.current;
    const revision = apiClient.getConnectionRevision();
    const connection = clientUserAgentsConnection();
    let active = true;
    const valid = () =>
      active && request === generation.current && revision === apiClient.getConnectionRevision();
    queueMicrotask(() => {
      if (!valid()) return;
      setLoading(true);
      setBusy(false);
      setError('');
      setApplied(false);
      setSelected('');
      setImportOpen(false);
      setRaw('');
      setCatalog({ items: [], revision: -1 });
    });
    if (authenticated) {
      connection
        .list(provider)
        .then((result) => {
          if (valid()) setCatalog({ items: result.items, revision });
        })
        .catch((cause: unknown) => {
          if (valid())
            setError(cause instanceof Error ? cause.message : t('client_user_agents.load_error'));
        })
        .finally(() => {
          if (valid()) setLoading(false);
        });
    }
    return () => {
      active = false;
      generation.current = request + 1;
    };
  }, [apiBase, managementKey, authenticated, provider, reload, t]);

  const apply = (wholeProfile: boolean) => {
    if (blocked || catalog.revision !== apiClient.getConnectionRevision()) return;
    if (selected === 'auto' || selected === 'native') {
      onChange(userAgentDraft(provider, selected === 'auto' ? 'auto' : ''));
    } else if (entry) {
      onChange(
        wholeProfile
          ? clientProfileDraft(entry)
          : userAgentDraft(provider, entry.headers['User-Agent'])
      );
    } else return;
    setApplied(true);
  };

  const importHeaders = async () => {
    if (blocked) return;
    const revision = apiClient.getConnectionRevision();
    const request = generation.current;
    const headers = extractIdentityHeaders(raw);
    if (!headers || new TextEncoder().encode(raw).length > 32768) {
      setError(t(ns + 'invalid_import'));
      return;
    }
    setBusy(true);
    setError('');
    const connection = clientUserAgentsConnection();
    const valid = () =>
      request === generation.current && revision === apiClient.getConnectionRevision();
    try {
      const imported = await connection.import(provider, headers);
      if (!valid()) return;
      const result = await connection.list(provider);
      if (!valid()) return;
      setCatalog({ items: result.items, revision });
      setSelected(imported.id);
      setImportOpen(false);
      setRaw('');
      setApplied(false);
    } catch (cause: unknown) {
      if (valid()) setError(cause instanceof Error ? cause.message : t(ns + 'import_error'));
    } finally {
      if (valid()) setBusy(false);
    }
  };

  const remove = async () => {
    if (!entry || blocked) return;
    const revision = apiClient.getConnectionRevision();
    const request = generation.current;
    setBusy(true);
    setError('');
    try {
      await clientUserAgentsConnection().remove(provider, entry.id);
      if (request !== generation.current || revision !== apiClient.getConnectionRevision()) return;
      setCatalog((previous) => ({
        ...previous,
        items: previous.items.filter((item) => item.id !== entry.id),
      }));
      setSelected('');
    } catch (cause: unknown) {
      if (request === generation.current && revision === apiClient.getConnectionRevision()) {
        setError(cause instanceof Error ? cause.message : t(ns + 'delete_error'));
      }
    } finally {
      if (request === generation.current && revision === apiClient.getConnectionRevision())
        setBusy(false);
    }
  };

  const source = (item: ClientUserAgent) =>
    [item.observed && t(ns + 'observed'), item.imported && t(ns + 'imported')]
      .filter(Boolean)
      .join(' / ');
  const options = [
    { value: 'auto', label: t(ns + 'auto') },
    { value: 'native', label: t(ns + 'native') },
    ...(current ? catalog.items : []).map((item) => ({
      value: item.id,
      label: `${source(item)} · ${item.headers['User-Agent']}${item.headers['X-Stainless-Package-Version'] ? ' · SDK ' + item.headers['X-Stainless-Package-Version'] : ''}`,
    })),
  ];

  return (
    <section
      className={styles.picker}
      aria-label={t(ns + 'title', { provider: provider === 'claude' ? 'Claude Code' : 'Codex' })}
    >
      <div className={styles.heading}>
        <strong>
          {t(ns + 'title', { provider: provider === 'claude' ? 'Claude Code' : 'Codex' })}
        </strong>
        <div className={styles.actions}>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={disabled || busy || loading}
            onClick={() => setReload((value) => value + 1)}
          >
            {t(ns + 'refresh')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="secondary"
            disabled={blocked}
            onClick={() => {
              setError('');
              setImportOpen(true);
            }}
          >
            {t(ns + 'import')}
          </Button>
        </div>
      </div>
      <p className={styles.hint}>{t(ns + 'description')}</p>
      <p className={styles.hint}>{t(ns + provider + '_scope')}</p>
      <Select
        value={selected}
        options={options}
        disabled={blocked}
        ariaLabel={t(ns + 'select', { provider })}
        placeholder={t(ns + (loading ? 'loading' : 'placeholder'))}
        onChange={(value) => {
          setSelected(value);
          setApplied(false);
        }}
      />
      {current && !catalog.items.length && <p className={styles.hint}>{t(ns + 'empty')}</p>}
      {entry && (
        <div className={styles.preview}>
          <p>
            {source(entry)} · {t(ns + 'last_seen')}{' '}
            {new Date(entry.lastSeen).toLocaleString(i18n.language)} ·{' '}
            {t(ns + 'count', { count: entry.count })}
          </p>
          <pre>
            {Object.entries(entry.headers)
              .map(([key, value]) => `${key}: ${value}`)
              .join('\n')}
          </pre>
        </div>
      )}
      {selected === 'auto' && <p className={styles.hint}>{t(ns + 'auto_hint')}</p>}
      <div className={styles.actions}>
        <Button
          type="button"
          size="sm"
          disabled={blocked || !selected}
          onClick={() => apply(false)}
        >
          {t(ns + 'apply_ua')}
        </Button>
        {entry && Object.keys(entry.headers).length > 1 && (
          <Button
            type="button"
            size="sm"
            variant="secondary"
            disabled={blocked}
            onClick={() => apply(true)}
          >
            {t(ns + 'apply_profile')}
          </Button>
        )}
        {entry && (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={blocked}
            onClick={() => void remove()}
          >
            {t(ns + 'remove')}
          </Button>
        )}
      </div>
      {applied && (
        <p role="status" className={styles.hint}>
          {t(ns + 'applied')}
        </p>
      )}
      {error && !importOpen && (
        <p role="alert" className={styles.error}>
          {error}
        </p>
      )}
      <Modal
        open={importOpen && current}
        title={t(ns + 'import')}
        onClose={() => {
          setImportOpen(false);
          setRaw('');
          setError('');
        }}
        closeDisabled={busy}
        footer={
          <Button
            type="button"
            disabled={busy || !raw.trim()}
            loading={busy}
            onClick={() => void importHeaders()}
          >
            {t(ns + 'import_preview')}
          </Button>
        }
      >
        <p>{t(ns + 'import_hint')}</p>
        <textarea
          className={`input ${styles.importText}`}
          aria-label={t(ns + 'headers')}
          value={raw}
          onChange={(event) => setRaw(event.target.value)}
          maxLength={32768}
          disabled={busy}
          rows={9}
          spellCheck={false}
          placeholder="User-Agent: ..."
        />
        {error && (
          <p role="alert" className={styles.error}>
            {error}
          </p>
        )}
      </Modal>
    </section>
  );
}
