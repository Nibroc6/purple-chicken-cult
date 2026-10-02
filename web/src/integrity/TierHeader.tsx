// Tier header (SCAFFOLD hand-off stub). Owner: P0-W10 → P1-W09.
// Integrity element (§15.5): always rendered, visible and legible; reserved styles
// and icons are not available to user content.
import { useTranslation } from 'react-i18next';
import type { TierHeaderProps } from './types';

export function TierHeader({ tier, kind }: TierHeaderProps) {
  const { t } = useTranslation('integrity');
  const text = kind === 'start' ? t('tier.start', { tier }) : kind === 'end' ? t('tier.end', { tier }) : t('tier.caughtUp');
  return (
    <h2 className="integrity tier-header" data-testid="integrity-tier-header" data-tier={tier}>
      {text}
    </h2>
  );
}
