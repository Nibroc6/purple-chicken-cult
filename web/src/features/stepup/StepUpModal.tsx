// The global step-up modal (D27; plan §6.4): opens whenever the API answers
// 401 step_up_required (api/client.ts onStepUp) or a feature calls
// requestStepUp(), and verifies with one of the person's own passkeys. P1:
// when automation signals asked for the step-up (the session says step_up,
// or the event stream sends stepup) it says so in plain words — a human tap
// on a passkey is all it takes, nothing is blocked or reported to anyone —
// and afterwards reloads what the page was showing.
// Owner: P0-W01 → P1-W07.
import { errorText } from '../../api/errors';
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { onStepUp } from '../../api/client';
import { subscribe } from '../../api/sse';
import { fetchSession, sessionKey, useSession } from '../../app/session';
import { stepUpBegin, stepUpFinish } from '../auth/api';
import { authError, type ErrorMsg } from '../auth/vm';
import { cancelCeremony, getAssertion } from '../auth/webauthn';
import { requestStepUp, settleStepUp, useStepUpOpen, useStepUpReason } from './store';
import './stepup.css';

export function StepUpModal() {
  const { t } = useTranslation(['stepup', 'signals', 'auth', 'errors']);
  const qc = useQueryClient();
  const open = useStepUpOpen();
  const reason = useStepUpReason();
  const signedIn = useSession().data?.authenticated === true;
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<ErrorMsg | undefined>();
  const titleId = useId();
  const bodyId = useId();

  useEffect(
    () =>
      onStepUp(() => {
        void requestStepUp();
        // Was it the signals? The session says so (a fresh-check 401 does not set it).
        void fetchSession().then(
          (sv) => {
            qc.setQueryData(sessionKey, sv);
            if (sv.step_up) void requestStepUp('signals');
          },
          () => undefined,
        );
      }),
    [qc],
  );
  useEffect(() => {
    if (!signedIn) return undefined;
    return subscribe('stepup', () => {
      void requestStepUp('signals');
    });
  }, [signedIn]);

  if (!open) return null;
  const signals = reason === 'signals';

  const cancel = () => {
    if (busy) cancelCeremony();
    setBusy(false);
    setErr(undefined);
    settleStepUp(false);
  };

  const verify = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      const options = await stepUpBegin();
      const credential = await getAssertion(options);
      await stepUpFinish(credential);
      setBusy(false);
      setErr(undefined);
      settleStepUp(true);
      // Reads refused while the step-up was pending are fetched again.
      if (signals) await qc.invalidateQueries();
    } catch (e) {
      setBusy(false);
      setErr(authError(e));
    }
  };

  return (
    <div className="stepup-backdrop">
      <div
        className="stepup-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={bodyId}
        data-reason={signals ? 'signals' : 'fresh'}
        onKeyDown={(e) => {
          if (e.key === 'Escape') cancel();
        }}
      >
        <h2 id={titleId}>{signals ? t('signals:stepup.title') : t('stepup:title')}</h2>
        <p id={bodyId}>{signals ? t('signals:stepup.body') : t('stepup:body')}</p>
        {signals ? <p className="muted">{t('signals:stepup.privacy')}</p> : null}
        {err ? (
          <p className="stepup-error" role="alert">
            {errorText(t, err)}
          </p>
        ) : null}
        <div className="stepup-actions">
          <button
            autoFocus
            type="button"
            className="primary"
            data-testid="stepup-verify"
            disabled={busy}
            onClick={() => {
              void verify();
            }}
          >
            {busy ? t('stepup:working') : t('stepup:verify')}
          </button>
          <button type="button" data-testid="stepup-cancel" onClick={cancel}>
            {t('stepup:cancel')}
          </button>
        </div>
      </div>
    </div>
  );
}
