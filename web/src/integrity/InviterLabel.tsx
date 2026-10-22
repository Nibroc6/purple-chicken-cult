// Inviter label (§3, §15.5). Owner: P0-W10 → P1-W09. A non-interactive tag
// ("invited by @handle", "genesis", "invited by a deleted account"): never a
// link, never a ref. Next to it, "report the inviter" routes through the
// invitee (§3), because the inviter may be outside the reader's horizon.
import { useTranslation } from 'react-i18next';
import { Icon } from '../lib/Icon';
import type { InviterLabelProps } from './types';
import './integrity.css';

export function InviterLabel({ inviter, invitee, onReport }: InviterLabelProps) {
  const { t } = useTranslation('integrity');
  return (
    <span className="inviter-wrap">
      <span className="integrity inviter-label" data-testid="integrity-inviter-label" data-kind={inviter.kind}>
        <Icon name="inviter" />
        {t('inviter.label', { kind: inviter.kind, handle: inviter.handle ?? '' })}
      </span>
      {inviter.kind === 'user' ? (
        <button
          type="button"
          className="integrity inviter-report"
          data-testid="integrity-inviter-report"
          aria-label={t('inviter.report')}
          title={t('inviter.report')}
          onClick={() => {
            onReport({ kind: 'inviter', ref: invitee });
          }}
        >
          <Icon name="report" />
        </button>
      ) : null}
    </span>
  );
}
