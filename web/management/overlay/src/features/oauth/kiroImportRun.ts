import type { KiroCredential } from './kiroCredentials';

export interface KiroImportAPI {
  check: () => Promise<boolean>;
  start: () => Promise<string>;
  connect: (state: string, credential: KiroCredential) => Promise<void>;
  status: (state: string) => Promise<'ok' | 'wait' | 'error'>;
  cancel: (state: string) => Promise<unknown>;
}

export type KiroImportStatus = 'pending' | 'importing' | 'success' | 'failed' | 'uncertain';
export type KiroImportFailure = 'unavailable' | 'start' | 'connect' | 'save' | 'uncertain';
export interface KiroImportResult {
  index: number;
  status: KiroImportStatus;
  failure?: KiroImportFailure;
}

function waitForPoll(signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) {
      resolve();
      return;
    }
    const finish = () => {
      clearTimeout(timer);
      signal.removeEventListener('abort', finish);
      resolve();
    };
    const timer = setTimeout(finish, 1000);
    signal.addEventListener('abort', finish, { once: true });
  });
}

/** Each entry owns a fresh session; a failed entry is never retried automatically. */
export function createKiroImportRun(
  api: KiroImportAPI,
  onResult: (result: KiroImportResult) => void,
  wait: (signal: AbortSignal) => Promise<void> = waitForPoll
) {
  const controller = new AbortController();
  let started = false;
  let state = '';
  const active = () => !controller.signal.aborted;
  const cleanup = async () => {
    const pending = state;
    state = '';
    if (pending) await api.cancel(pending).catch(() => {});
  };
  return {
    cancel: () => {
      controller.abort();
      void cleanup();
    },
    run: async (credentials: readonly KiroCredential[]) => {
      if (started || !active()) return;
      started = true;
      let available = false;
      try {
        available = await api.check();
      } catch {
        /* Report a credential-free error below. */
      }
      if (!active()) return;
      if (!available) {
        for (const entry of credentials)
          onResult({ index: entry.index, status: 'failed', failure: 'unavailable' });
        return;
      }
      for (const credential of credentials) {
        if (!active()) break;
        onResult({ index: credential.index, status: 'importing' });
        let phase: KiroImportFailure = 'start';
        try {
          state = await api.start();
          if (!active()) break;
          phase = 'connect';
          await api.connect(state, credential);
          if (!active()) break;
          phase = 'uncertain';
          let result: 'ok' | 'wait' | 'error' = 'wait';
          for (let attempt = 0; attempt < 120 && active(); attempt++) {
            result = await api.status(state);
            if (result !== 'wait') break;
            await wait(controller.signal);
          }
          if (!active()) break;
          if (result === 'ok') {
            state = '';
            onResult({ index: credential.index, status: 'success' });
          } else {
            onResult({
              index: credential.index,
              status: result === 'error' ? 'failed' : 'uncertain',
              failure: result === 'error' ? 'save' : 'uncertain',
            });
          }
        } catch {
          if (active())
            onResult({
              index: credential.index,
              status: phase === 'uncertain' ? 'uncertain' : 'failed',
              failure: phase,
            });
        } finally {
          await cleanup();
        }
      }
    },
  };
}
