// Shell slots of the moderation feature. Owner: P0-W12 → P1-W06. A footer on
// every page links the terms (everyone), the moderation log and the blame inbox
// (signed in) and the moderator pages (moderators).
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import type { FeatureSlots } from '../../app/slots';
import { useSession } from '../../app/session';
import './moderation.css';

export function ModerationFooter() {
  const { t } = useTranslation('moderation');
  const session = useSession();
  const me = session.data?.me;
  const signedIn = session.data?.authenticated === true;
  const moderator = me?.role === 'moderator' || me?.role === 'admin';
  return (
    <footer className="moderation-footer" data-testid="site-footer">
      <nav aria-label={t('footer.label')}>
        <ul className="link-list">
          <li>
            <Link to="/terms">{t('footer.terms')}</Link>
          </li>
          {signedIn ? (
            <>
              <li>
                <Link to="/mod/log">{t('footer.log')}</Link>
              </li>
              <li>
                <Link to="/blame">{t('footer.blame')}</Link>
              </li>
            </>
          ) : null}
          {moderator ? (
            <>
              <li>
                <Link to="/mod">{t('footer.queue')}</Link>
              </li>
              <li>
                <Link to="/mod/recovery">{t('footer.recovery')}</Link>
              </li>
            </>
          ) : null}
        </ul>
      </nav>
    </footer>
  );
}

export const slots: FeatureSlots = { global: [ModerationFooter] };
