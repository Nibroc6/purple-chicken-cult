// The scanner offer (shell global): when an acquaintance opens a friend
// upgrade with the signed-in person (live friend_upgrade event), a toast
// offers to open the scanner straight away; a closed app gets the same offer
// as a push that opens /join?scan=1. Owner: P1-W02.
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { subscribe } from '../../api/sse';
import { useSession } from '../../app/session';
import { SCAN_PATH } from './vm';
import './join.css';

interface Offer {
  handle: string;
}

export function UpgradeToast() {
  const { t } = useTranslation('join');
  const navigate = useNavigate();
  const signedIn = useSession().data?.authenticated === true;
  const [offer, setOffer] = useState<Offer | undefined>();
  useEffect(() => {
    if (!signedIn) return undefined;
    return subscribe<{ handle?: string }>('friend_upgrade', (d) => {
      if (typeof d.handle === 'string') setOffer({ handle: d.handle });
    });
  }, [signedIn]);
  if (!offer) return null;
  return (
    <div className="join-toast" role="status" data-testid="upgrade-toast">
      <p>{t('toast.body', { handle: `@${offer.handle}` })}</p>
      <div className="join-toast-actions">
        <button
          type="button"
          data-testid="upgrade-toast-scan"
          onClick={() => {
            setOffer(undefined);
            void navigate(SCAN_PATH);
          }}
        >
          {t('toast.scan')}
        </button>
        <button
          type="button"
          onClick={() => {
            setOffer(undefined);
          }}
        >
          {t('toast.dismiss')}
        </button>
      </div>
    </div>
  );
}
