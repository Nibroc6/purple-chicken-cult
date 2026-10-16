// Terms of service (/terms; §17.3(6)). Owner: P0-W12. Static text from the
// catalog (no app data) plus the published server key fingerprints, so members
// can compare the keys their exports are signed with. The tdm-policy header of
// every response points at #tdm.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { formatDateTime } from '../../lib/time';
import { fetchKeys, groupFingerprint, keysPath } from './keys';
import './landing.css';

const sections = ['service', 'invites', 'conduct', 'moderation', 'tdm', 'data', 'changes'] as const;

export default function TermsPage() {
  const { t } = useTranslation('landing');
  return (
    <article className="page terms-page" data-testid="terms-page">
      <h1>{t('terms.title')}</h1>
      <p className="muted">{t('terms.intro')}</p>
      {sections.map((s) => (
        <section key={s} id={s} aria-labelledby={`terms-${s}`} data-testid={`terms-${s}`}>
          {/* i18n-dynamic: landing:terms.sections. */}
          <h2 id={`terms-${s}`}>{t(`terms.sections.${s}.title`)}</h2>
          <p>{t(`terms.sections.${s}.body`)}</p>
        </section>
      ))}
      <Keys />
      <p>
        <Link to="/">{t('terms.home')}</Link>
      </p>
    </article>
  );
}

function Keys() {
  const { t, i18n } = useTranslation('landing');
  const q = useQuery({ queryKey: ['landing', 'keys'], queryFn: fetchKeys, retry: false, staleTime: 300_000 });
  return (
    <section id="keys" aria-labelledby="terms-keys" data-testid="terms-keys">
      <h2 id="terms-keys">{t('keys.title')}</h2>
      <p>{t('keys.body')}</p>
      {q.isPending ? <p className="muted">{t('keys.loading')}</p> : null}
      {q.isError ? (
        <p role="alert">
          {t('keys.error')} <a href={keysPath}>{t('keys.raw')}</a>
        </p>
      ) : null}
      {q.data ? (
        <dl className="terms-keys">
          {q.data.map((k) => (
            <div key={k.keyID} className="terms-key" data-testid="terms-key">
              <dt>{t('keys.id', { id: k.keyID, alg: k.alg })}</dt>
              <dd>
                <code className="mono" data-testid="terms-fingerprint">
                  {groupFingerprint(k.fingerprint)}
                </code>
              </dd>
              <dd className="muted">
                {k.validUntil
                  ? t('keys.window', {
                      from: formatDateTime(k.validFrom, i18n.language),
                      until: formatDateTime(k.validUntil, i18n.language),
                    })
                  : t('keys.since', { from: formatDateTime(k.validFrom, i18n.language) })}
              </dd>
            </div>
          ))}
        </dl>
      ) : null}
    </section>
  );
}
