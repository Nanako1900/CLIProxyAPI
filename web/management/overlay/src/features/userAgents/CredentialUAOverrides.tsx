import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Modal } from '@/components/ui/Modal';
import {
  buildCredentialOverrideClearPatch,
  buildCredentialOverrideSavePatch,
  CLAUDE_CREDENTIAL_OVERRIDE_HEADERS,
  CODEX_CREDENTIAL_OVERRIDE_HEADERS,
  parseUALabClaudeCredentialHeaders,
  parseUALabCodexCredentialHeaders,
  type AuthFileCredentialOption,
  type UserAgentOverrideEntry,
} from '@/services/api/userAgentPanel';
import styles from './UserAgentsPage.module.scss';

type Props = {
  provider: 'claude' | 'codex';
  overrides: UserAgentOverrideEntry[];
  credentials: AuthFileCredentialOption[];
  inventoryAvailable: boolean;
  disabled: boolean;
  onSaveOverride: (fileName: string, headers: Record<string, string>) => Promise<void>;
};

const fieldPlaceholders: Record<string, string> = {
  'User-Agent': '例如 claude-cli/2.1.331 (external, sdk-cli) 或 codex-tui/0.172.0 (...)',
  'X-Stainless-Package-Version': '例如 0.128.0（留空则沿用全局）',
  'X-Stainless-Runtime-Version': '例如 v26.12.1（留空则沿用全局）',
  'X-Stainless-OS': '例如 MacOS（留空则沿用全局）',
  'X-Stainless-Arch': '例如 arm64（留空则沿用全局）',
  'X-Stainless-Timeout': '例如 600（留空则沿用全局）',
  Originator: '例如 codex_tui（留空则沿用全局）',
  'X-Codex-Beta-Features': '例如 js_repl,apps（留空则沿用全局）',
  'Accept-Encoding': '例如 zstd,gzip（留空则沿用全局）',
};

export function CredentialUAOverrides({
  provider,
  overrides,
  credentials,
  inventoryAvailable,
  disabled,
  onSaveOverride,
}: Props) {
  const [modalOpen, setModalOpen] = useState(false);
  const [selectedFile, setSelectedFile] = useState('');
  const [formValues, setFormValues] = useState<Record<string, string>>({});
  const [editingPreviousHeaders, setEditingPreviousHeaders] = useState<
    Record<string, string> | undefined
  >(undefined);
  const [yamlInput, setYamlInput] = useState('');
  const [modalError, setModalError] = useState('');
  const [busy, setBusy] = useState(false);

  const managedKeys: readonly string[] =
    provider === 'claude' ? CLAUDE_CREDENTIAL_OVERRIDE_HEADERS : CODEX_CREDENTIAL_OVERRIDE_HEADERS;

  const providerCredentials = useMemo(
    () => credentials.filter((item) => item.provider === provider),
    [credentials, provider]
  );

  const credentialByIndex = useMemo(() => {
    const map = new Map<string, AuthFileCredentialOption>();
    for (const item of providerCredentials) {
      if (item.authIndex) map.set(item.authIndex, item);
    }
    return map;
  }, [providerCredentials]);

  const overrideByIndex = useMemo(() => {
    const map = new Map<string, UserAgentOverrideEntry>();
    for (const item of overrides) {
      if (item.authIndex) map.set(item.authIndex, item);
    }
    return map;
  }, [overrides]);

  const openEditor = (targetFileName?: string, existing?: UserAgentOverrideEntry) => {
    const initialFile = targetFileName || providerCredentials[0]?.name || '';
    const initialValues: Record<string, string> = {};
    const existingHeaders = existing?.headers ?? (existing?.userAgent ? { 'User-Agent': existing.userAgent } : undefined);
    for (const key of managedKeys) {
      initialValues[key] = existingHeaders?.[key] || '';
    }
    setSelectedFile(initialFile);
    setFormValues(initialValues);
    setEditingPreviousHeaders(existingHeaders);
    setYamlInput('');
    setModalError('');
    setModalOpen(true);
  };

  const handleSelectAccountChange = (nextFile: string) => {
    setSelectedFile(nextFile);
    setModalError('');
    const matchedCred = providerCredentials.find((item) => item.name === nextFile);
    const existing = matchedCred?.authIndex ? overrideByIndex.get(matchedCred.authIndex) : undefined;
    const existingHeaders = existing?.headers ?? (existing?.userAgent ? { 'User-Agent': existing.userAgent } : undefined);
    const nextValues: Record<string, string> = {};
    for (const key of managedKeys) {
      nextValues[key] = existingHeaders?.[key] || '';
    }
    setFormValues(nextValues);
    setEditingPreviousHeaders(existingHeaders);
  };

  const handleParseYaml = () => {
    setModalError('');
    const result =
      provider === 'claude'
        ? parseUALabClaudeCredentialHeaders(yamlInput)
        : parseUALabCodexCredentialHeaders(yamlInput);
    if (result.error !== undefined || !result.headers) {
      setModalError(result.error || '解析 UALab YAML 失败。');
      return;
    }
    const nextValues: Record<string, string> = {};
    for (const key of managedKeys) {
      nextValues[key] = result.headers[key] || '';
    }
    setFormValues(nextValues);
  };

  const handleSave = async () => {
    if (!selectedFile) {
      setModalError('请先选择要设置独立 UA 的目标账号。');
      return;
    }
    const built = buildCredentialOverrideSavePatch(provider, formValues, editingPreviousHeaders);
    if (built.error !== undefined || !built.headers) {
      setModalError(built.error || '请检查输入字段。');
      return;
    }
    setBusy(true);
    setModalError('');
    try {
      await onSaveOverride(selectedFile, built.headers);
      setModalOpen(false);
    } catch {
      setModalError('保存单账号 UA 覆写失败，请检查服务连接或凭据状态后重试。');
    } finally {
      setBusy(false);
    }
  };

  const handleClear = async (fileName: string, existingHeaders?: Record<string, string>) => {
    setBusy(true);
    try {
      const clearPatch = buildCredentialOverrideClearPatch(provider, existingHeaders);
      await onSaveOverride(fileName, clearPatch);
    } finally {
      setBusy(false);
    }
  };

  const previewHeaders = useMemo(() => {
    const out: Record<string, string> = {};
    for (const key of managedKeys) {
      const val = (formValues[key] || '').trim();
      if (val) out[key] = val;
    }
    return out;
  }, [formValues, managedKeys]);

  return (
    <>
      <Card
        title="单账号独立 UA 覆写（保护老号）"
        extra={
          <div className={styles.actions}>
            <Button
              variant="primary"
              disabled={disabled || busy || providerCredentials.length === 0}
              onClick={() => openEditor()}
            >
              为账号设置独立 UA
            </Button>
            <Link to="/auth-files">管理认证文件</Link>
          </div>
        }
      >
        <div className={styles.stack}>
          <p className={styles.note}>
            未配置独立覆写的现有老号将继续 <strong>100% 沿用全局配置，零变动</strong>；仅对下方显式配置的账号生效。
            {provider === 'claude'
              ? ' Claude 单账号覆写会自动同步请求头、请求体 billing cc_version 以及额度/重置接口。'
              : ' Codex 单账号覆写会自动同步 HTTP 推理、WebSocket、OAuth2 Token 刷新以及额度/重置接口。'}
          </p>
          {!inventoryAvailable ? (
            <p className={styles.muted}>凭据清单当前不可用。</p>
          ) : !overrides.length ? (
            <p className={styles.muted}>
              当前暂无单账号独立覆写，所有 {provider === 'claude' ? 'Claude' : 'Codex'} 账号均沿用全局配置。
            </p>
          ) : (
            overrides.map((item, index) => {
              const matchedCred = item.authIndex ? credentialByIndex.get(item.authIndex) : undefined;
              const fileName = matchedCred?.name;
              const extraEntries = Object.entries(item.headers || {}).filter(
                ([key]) => key.toLowerCase() !== 'user-agent'
              );
              return (
                <div className={styles.override} key={`${item.authIndex}-${index}`}>
                  <div className={styles.overrideHeader}>
                    <div>
                      <strong>{item.name || item.authIndex || 'Credential'}</strong> ·{' '}
                      {item.source === 'config' ? '配置密钥' : '文件凭据'}
                      {item.disabled && ' · 已禁用'}
                    </div>
                    {item.source === 'credential' && fileName && (
                      <div className={styles.actions}>
                        <Button
                          variant="secondary"
                          disabled={disabled || busy}
                          onClick={() => openEditor(fileName, item)}
                        >
                          编辑
                        </Button>
                        <Button
                          variant="ghost"
                          disabled={disabled || busy}
                          onClick={() => void handleClear(fileName, item.headers)}
                        >
                          恢复继承全局
                        </Button>
                      </div>
                    )}
                  </div>
                  <code>User-Agent: {item.userAgent}</code>
                  {extraEntries.length > 0 && (
                    <div className={styles.overrideExtras}>
                      {extraEntries.map(([key, value]) => (
                        <code key={key}>
                          {key}: {value}
                        </code>
                      ))}
                    </div>
                  )}
                  <small className={styles.muted}>
                    凭据索引: {item.authIndex || '—'}
                    {fileName ? ` · 文件: ${fileName}` : ''}
                  </small>
                </div>
              );
            })
          )}
        </div>
      </Card>

      <Modal
        open={modalOpen}
        title={`为单个 ${provider === 'claude' ? 'Claude' : 'Codex'} 账号设置独立 UA`}
        closeDisabled={busy}
        onClose={() => setModalOpen(false)}
        footer={
          <>
            <Button variant="ghost" disabled={busy} onClick={() => setModalOpen(false)}>
              取消
            </Button>
            <Button loading={busy} disabled={!selectedFile} onClick={() => void handleSave()}>
              保存该账号覆写
            </Button>
          </>
        }
      >
        <div className={styles.stack}>
          <p className={styles.muted}>
            此设置仅写入目标账号的 <code>headers</code> 元数据，<strong>不会修改全局配置或其他现有老号</strong>。
          </p>
          {modalError && (
            <div role="alert" className="error-box">
              {modalError}
            </div>
          )}
          <label className={styles.field}>
            <span>目标账号</span>
            <select
              className={styles.select}
              value={selectedFile}
              disabled={busy}
              onChange={(event) => handleSelectAccountChange(event.target.value)}
            >
              {providerCredentials.map((cred) => {
                const hasOverride = Boolean(cred.authIndex && overrideByIndex.has(cred.authIndex));
                return (
                  <option key={cred.name} value={cred.name}>
                    {cred.label} ({cred.name})
                    {hasOverride ? ' [已有独立覆写]' : ''}
                    {cred.disabled ? ' [已禁用]' : ''}
                  </option>
                );
              })}
            </select>
          </label>
          <div className={styles.field}>
            <div className={styles.overrideHeader}>
              <span>快捷方式：粘贴本机 UALab 导出的 YAML（可选）</span>
              <Button
                variant="secondary"
                disabled={busy || !yamlInput.trim()}
                onClick={handleParseYaml}
              >
                解析到下方表单
              </Button>
            </div>
            <textarea
              className={styles.textarea}
              rows={5}
              value={yamlInput}
              disabled={busy}
              placeholder="# Generated by UALab ..."
              onChange={(event) => setYamlInput(event.target.value)}
            />
          </div>
          {managedKeys.map((key) => (
            <label className={styles.field} key={key}>
              <span>
                {key}
                {key === 'User-Agent' ? ' *' : ''}
              </span>
              <input
                className={styles.input}
                type="text"
                value={formValues[key] || ''}
                disabled={busy}
                placeholder={fieldPlaceholders[key] || ''}
                onChange={(event) => {
                  const val = event.target.value;
                  setFormValues((prev) => ({ ...prev, [key]: val }));
                }}
              />
            </label>
          ))}
          {Object.keys(previewHeaders).length > 0 && (
            <div className={styles.change}>
              <strong>将写入该账号的 headers 预览</strong>
              <pre>{JSON.stringify(previewHeaders, null, 2)}</pre>
            </div>
          )}
        </div>
      </Modal>
    </>
  );
}
