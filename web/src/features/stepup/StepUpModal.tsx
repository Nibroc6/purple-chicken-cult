// The global step-up modal (D27; plan §6.4): opens whenever the API answers
// 401 step_up_required (api/client.ts onStepUp) or a feature calls
// requestStepUp(), and verifies with one of the person's own passkeys.
// Owner: P0-W01 → P1-W07.
import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { onStepUp } from '../../api/client';
import { stepUpBegin, stepUpFinish } from '../auth/api';
import { authErrorKey } from '../auth/vm';
import { cancelCeremony, getAssertion } from '../auth/webauthn';
import { requestStepUp, settleStepUp, useStepUpOpen } from './store';
import './stepup.css';

export function StepUpModal() {
  const { t } = useTranslation(['stepup', 'auth', 'errors']);
  const open = useStepUpOpen();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | undefined>();
  const titleId = useId();
  const bodyId = useId();

  useEffect(
    () =>
      onStepUp(() => {
        void requestStepUp();
      }),
    [],
  );

  if (!open) return null;

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
    } catch (e) {
      setBusy(false);
      setErr(authErrorKey(e));
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
        onKeyDown={(e) => {
          if (e.key === 'Escape') cancel();
        }}
      >
        <h2 id={titleId}>{t('stepup:title')}</h2>
        <p id={bodyId}>{t('stepup:body')}</p>
        {err ? (
          <p className="stepup-error" role="alert">
            {t(err as 'errors:unknown')}
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
