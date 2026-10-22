// Landing page for signed-out visitors at "/" (§17.3(1): ordinary crawlers get a
// landing page and nothing else). Owner: P0-W12. It shows no app data at all:
// no members, posts, counts or handles; only what the network is and the ways
// in (sign in with a passkey, join with an in-person invite).
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import './landing.css';

const points = ['inPerson', 'tiers', 'private', 'noTraining'] as const;

export default function LandingPage() {
  const { t } = useTranslation('landing');
  return (
    <section className="page landing-page" data-testid="landing-page">
      <h1>{t('page.title')}</h1>
      <p className="landing-lead">{t('page.lead')}</p>
      <ul className="landing-points">
        {points.map((p) => (
          <li key={p}>
            {/* i18n-dynamic: landing:points. */}
            <strong>{t(`points.${p}.title`)}</strong> <span>{t(`points.${p}.body`)}</span>
          </li>
        ))}
      </ul>
      <div className="landing-actions">
        <Link className="landing-button" to="/login" data-testid="landing-login">
          {t('page.login')}
        </Link>
        <Link className="landing-button landing-secondary" to="/join" data-testid="landing-join">
          {t('page.join')}
        </Link>
      </div>
      <p className="muted">{t('page.noInvite')}</p>
      <p className="landing-links">
        <Link to="/about" data-testid="landing-about">
          {t('page.about')}
        </Link>
        <Link to="/terms" data-testid="landing-terms">
          {t('page.terms')}
        </Link>
      </p>
    </section>
  );
}
