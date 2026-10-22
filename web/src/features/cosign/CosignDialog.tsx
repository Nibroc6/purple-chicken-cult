// The global co-sign dialog (ARCHITECTURE §3 Phase 1, §17.4): opens when a
// feature calls requestCosign(), fetches the intent, shows the exact record and
// signs it with one of the person's own passkeys. Owner: P1-W01.
import { errorText } from '../../api/errors';
import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { authError, type ErrorMsg } from '../auth/vm';
import { cancelCeremony, getAssertion } from '../auth/webauthn';
import { beginCosign, type IntentJSON } from './api';
import { AdvancedRecords } from './RecordView';
import { settleCosign, usePendingCosign, type CosignRequest } from './store';
import { toRecordVMs, type RecordVM } from './vm';
import './cosign.css';

type State =
  | { phase: 'loading' }
  | { phase: 'ready'; intent: IntentJSON; records: RecordVM[] }
  | { phase: 'signing'; intent: IntentJSON; records: RecordVM[] }
  | { phase: 'failed'; error?: ErrorMsg; intent?: IntentJSON; records?: RecordVM[] };

export function CosignDialog() {
  const req = usePendingCosign();
  if (!req) return null;
  return <Dialog key={`${req.purpose}:${req.target ?? ''}`} req={req} />;
}

function Dialog({ req }: { req: CosignRequest }) {
  const { t } = useTranslation(['cosign', 'auth', 'errors']);
  const [state, setState] = useState<State>({ phase: 'loading' });
  const titleId = useId();
  const bodyId = useId();

  useEffect(() => {
    let live = true;
    beginCosign(req.purpose, req.target)
      .then((intent) => {
        if (live) setState({ phase: 'ready', intent, records: toRecordVMs(intent.record) });
      })
      .catch((e: unknown) => {
        if (live) setState({ phase: 'failed', error: authError(e) ?? { key: 'errors:unknown', params: {} } });
      });
    return () => {
      live = false;
    };
  }, [req]);

  const cancel = () => {
    if (state.phase === 'signing') cancelCeremony();
    settleCosign(null);
  };

  const sign = async (intent: IntentJSON, records: RecordVM[]) => {
    setState({ phase: 'signing', intent, records });
    try {
      const assertion = await getAssertion(intent.options);
      settleCosign({ intent: intent.intent, assertion });
    } catch (e) {
      setState({ phase: 'failed', error: authError(e), intent, records });
    }
  };

  const intent = 'intent' in state ? state.intent : undefined;
  const records = 'records' in state ? state.records : undefined;

  return (
    <div className="cosign-backdrop">
      <div
        className="cosign-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={bodyId}
        data-testid="cosign-dialog"
        onKeyDown={(e) => {
          if (e.key === 'Escape') cancel();
        }}
      >
        <div className="cosign-body">
          <h2 id={titleId}>{t('cosign:dialog.title')}</h2>
          {req.summary ? <p className="cosign-summary">{req.summary}</p> : null}
          <p id={bodyId}>{t('cosign:dialog.body')}</p>
          {state.phase === 'loading' ? <p aria-live="polite">{t('cosign:dialog.loading')}</p> : null}
          {records ? <AdvancedRecords records={records} /> : null}
          {state.phase === 'failed' && state.error ? (
            <p className="cosign-error" role="alert">
              {errorText(t, state.error)}
            </p>
          ) : null}
        </div>
        <div className="cosign-actions">
          {intent && records ? (
            <button
              autoFocus
              type="button"
              className="primary"
              data-testid="cosign-sign"
              disabled={state.phase === 'signing'}
              onClick={() => {
                void sign(intent, records);
              }}
            >
              {state.phase === 'signing' ? t('cosign:dialog.working') : t('cosign:dialog.sign')}
            </button>
          ) : null}
          <button type="button" data-testid="cosign-cancel" onClick={cancel}>
            {t('cosign:dialog.cancel')}
          </button>
        </div>
      </div>
    </div>
  );
}
