// The optional "add a second passkey" step (§10.3; plan §10 decision 34),
// shown right after enrollment and after joining (features/join reuses it).
// A second passkey on another device is what keeps a lost phone from becoming
// a lost account. Owner: P0-W01 → P1-W07.
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { withStepUp } from '../stepup/store';
import { addPasskeyBegin, addPasskeyFinish } from './api';
import { authErrorKey } from './vm';
import { createPasskey } from './webauthn';
import './auth.css';

export function SecondPasskeyPrompt({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation(['auth', 'errors']);
  const nameId = useId();
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [added, setAdded] = useState(false);
  const [err, setErr] = useState<string | undefined>();

  const add = async () => {
    setBusy(true);
    setErr(undefined);
    try {
      // Retry the whole flow after a step-up (one ceremony per session).
      await withStepUp(async () => {
        const options = await addPasskeyBegin();
        const credential = await createPasskey(options);
        await addPasskeyFinish(credential, name);
      });
      setAdded(true);
    } catch (e) {
      setErr(authErrorKey(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="auth-second" aria-labelledby={`${nameId}-title`}>
      <h2 id={`${nameId}-title`}>{t('second.title')}</h2>
      {added ? (
        <p role="status">{t('second.added')}</p>
      ) : (
        <>
          <p>{t('second.body')}</p>
          <div className="auth-field">
            <label htmlFor={nameId}>{t('passkeys.nameLabel')}</label>
            <input
              id={nameId}
              type="text"
              maxLength={50}
              value={name}
              placeholder={t('passkeys.namePlaceholder')}
              onChange={(e) => {
                setName(e.target.value);
              }}
            />
          </div>
        </>
      )}
      {err ? (
        <p className="auth-error" role="alert">
          {t(err as 'errors:unknown')}
        </p>
      ) : null}
      <div className="auth-actions">
        {added ? null : (
          <button
            type="button"
            className="primary"
            data-testid="second-add"
            disabled={busy}
            onClick={() => {
              void add();
            }}
          >
            {busy ? t('second.working') : t('second.add')}
          </button>
        )}
        <button type="button" data-testid="second-done" disabled={busy} onClick={onDone}>
          {added ? t('second.continue') : t('second.skip')}
        </button>
      </div>
    </section>
  );
}
