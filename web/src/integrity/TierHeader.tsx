// Tier header (§10.1.7, §15.5). Owner: P0-W10 → P1-W09. Rendered only from the
// API's tier markers, never from post content; the text is exactly the catalog
// string (the glyph is reserved CSS).
import { useTranslation } from 'react-i18next';
import type { TierHeaderProps } from './types';
import './integrity.css';

export function TierHeader({ tier, kind }: TierHeaderProps) {
  const { t } = useTranslation('integrity');
  const text = kind === 'start' ? t('tier.start', { tier }) : kind === 'end' ? t('tier.end', { tier }) : t('tier.caughtUp');
  return (
    <h2 className="integrity tier-header" data-testid="integrity-tier-header" data-tier={tier} data-kind={kind}>
      {text}
    </h2>
  );
}
