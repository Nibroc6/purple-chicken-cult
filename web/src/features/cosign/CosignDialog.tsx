// The global co-sign dialog (ARCHITECTURE §3 Phase 1, §17.4): opens when a
// feature calls requestCosign(), fetches the intent, shows the record the
// person is about to sign and signs it with one of their own passkeys.
// Owner: P1-W01.
import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { authErrorKey } from '../auth/vm';
import { cancelCeremony, getAssertion } from '../auth/webauthn';
import { formatDateTime } from '../../lib/time';
import { cosignBegin, type CosignIntentJSON } from './api';
import { settleCosign, useCosignRequest, type CosignRequest } from './store';
import { toRecordVM } from './vm';
import './cosign.css';

export function CosignDialog() {
  const req = useCosignRequest();
  if (!req) return null;
  // A new request (purpose or target) remounts the dialog with fresh state.
  return <Dialog key={`${req.purpose}:${req.target ?? ''}`} req={req} />;
}

function Dialog({ req }: { req: CosignRequest }) {
  const { t, i18n } = useTranslation(['cosign', 'auth', 'errors']);
  const [intent, setIntent] = useState<CosignIntentJSON | undefined>();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const titleId = useId();
  const bodyId = useId();

  useEffect(() => {
    let live = true;
    cosignBegin(req.purpose, req.target).then(
      (iv) => {
        if (live) setIntent(iv);
      },
      (e: unknown) => {
        if (live) setErr(authErrorKey(e));
      },
    );
    return () => {
      live = false;
    };
  }, [req.purpose, req.target]);

  const cancel = () => {
    if (busy) cancelCeremony();
    settleCosign(null);
  };

  const sign = async () => {
    if (!intent) return;
    setBusy(true);
    setErr(undefined);
    try {
      const assertion = await getAssertion(intent.options);
      settleCosign({ intent: intent.intent, assertion });
    } catch (e) {
      setBusy(false);
      setErr(authErrorKey(e));
    }
  };

  const rec = intent ? toRecordVM(intent) : undefined;
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
        <h2 id={titleId}>{t('cosign:title')}</h2>
        {/* i18n-dynamic: cosign:purpose. */}
        <p id={bodyId}>{t(`cosign:purpose.${req.purpose}` as 'cosign:purpose.block')}</p>
        <p className="cosign-why">{t('cosign:why')}</p>
        {rec ? (
          <details className="cosign-record" data-testid="cosign-record">
            <summary>
              {rec.issuedAt ? (
                <>
                  {t('cosign:record.summary')}{' '}
                  <time dateTime={rec.issuedAt.toISOString()}>{formatDateTime(rec.issuedAt, i18n.language)}</time>
                </>
              ) : (
                t('cosign:record.summary')
              )}
            </summary>
            <pre>
              <code>{rec.json}</code>
            </pre>
          </details>
        ) : err ? null : (
          <p aria-live="polite">{t('cosign:loading')}</p>
        )}
        {err ? (
          <p className="cosign-error" role="alert">
            {t(err as 'errors:unknown')}
          </p>
        ) : null}
        <div className="cosign-actions">
          <button
            autoFocus
            type="button"
            className="primary"
            data-testid="cosign-sign"
            disabled={busy || !intent}
            onClick={() => {
              void sign();
            }}
          >
            {busy ? t('cosign:working') : t('cosign:sign')}
          </button>
          <button type="button" data-testid="cosign-cancel" onClick={cancel}>
            {t('cosign:cancel')}
          </button>
        </div>
      </div>
    </div>
  );
}
