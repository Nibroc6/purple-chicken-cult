// How the site works, from a member's point of view (/about). Owner: landing.
// Static text from the about catalog, open to signed-out visitors like /terms:
// no app data at all.
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import './landing.css';

// Each section's paragraphs, in order (about:sections.<id>.p<n>).
// i18n-dynamic: about:sections.
const sections = [
  { id: 'joining', paras: 3 },
  { id: 'handshake', paras: 3, link: { to: '/sound/check', label: 'links.soundCheck' } },
  { id: 'connections', paras: 3 },
  { id: 'probation', paras: 3 },
  { id: 'distance', paras: 4 },
  { id: 'privacy', paras: 4, link: { to: '/settings/privacy', label: 'links.privacy' } },
  { id: 'pace', paras: 3 },
  { id: 'safety', paras: 2 },
  { id: 'moderation', paras: 2, link: { to: '/mod/log', label: 'links.modLog' } },
] as const;

export default function AboutPage() {
  const { t } = useTranslation('about');
  const tr = t as unknown as (k: string) => string;
  return (
    <article className="page about-page" data-testid="about-page">
      <h1>{t('title')}</h1>
      <p className="landing-lead">{t('lead')}</p>
      <nav aria-label={t('contents')} className="about-contents">
        <ul>
          {sections.map((s) => (
            <li key={s.id}>
              <a href={`#${s.id}`}>{tr(`sections.${s.id}.title`)}</a>
            </li>
          ))}
        </ul>
      </nav>
      {sections.map((s) => (
        <section key={s.id} id={s.id} aria-labelledby={`about-${s.id}`} data-testid={`about-${s.id}`}>
          <h2 id={`about-${s.id}`}>{tr(`sections.${s.id}.title`)}</h2>
          {Array.from({ length: s.paras }, (_, i) => (
            <p key={i}>{tr(`sections.${s.id}.p${i + 1}`)}</p>
          ))}
          {'link' in s ? (
            <p>
              <Link to={s.link.to}>{t(s.link.label)}</Link>
            </p>
          ) : null}
        </section>
      ))}
      <p className="landing-links">
        <Link to="/terms">{t('terms')}</Link>
        <Link to="/">{t('home')}</Link>
      </p>
    </article>
  );
}
