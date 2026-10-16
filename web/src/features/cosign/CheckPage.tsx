// Co-signing check: signs a throwaway record with one of the person's passkeys
// through the same dialog every graph change uses. The server offers the
// cosign_check purpose only in test deployments (end-to-end tests); elsewhere
// the page reports that the check is unavailable. Owner: P1-W01.
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { requestCosign } from './store';

type Outcome = 'idle' | 'signed' | 'cancelled';

export default function CheckPage() {
  const { t } = useTranslation('cosign');
  const [outcome, setOutcome] = useState<Outcome>('idle');
  return (
    <main className="cosign-check">
      <h1>{t('check.title')}</h1>
      <p>{t('check.body')}</p>
      <button
        type="button"
        className="primary"
        data-testid="cosign-check-run"
        onClick={() => {
          setOutcome('idle');
          void requestCosign({ purpose: 'cosign_check', summary: t('check.summary') }).then((r) => {
            setOutcome(r && r.intent && Object.keys(r.assertion).length > 0 ? 'signed' : 'cancelled');
          });
        }}
      >
        {t('check.run')}
      </button>
      {outcome === 'signed' ? (
        <p role="status" data-testid="cosign-check-signed">
          {t('check.signed')}
        </p>
      ) : null}
      {outcome === 'cancelled' ? (
        <p role="status" data-testid="cosign-check-cancelled">
          {t('check.cancelled')}
        </p>
      ) : null}
    </main>
  );
}
