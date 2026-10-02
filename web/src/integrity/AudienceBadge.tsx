// Audience badge (SCAFFOLD hand-off stub). Owner: P0-W10 → P1-W09.
// Integrity element (§15.5): always rendered, visible and legible; reserved styles
// and icons are not available to user content.
import { useTranslation } from 'react-i18next';
import type { AudienceBadgeProps } from './types';

export function AudienceBadge({ audience }: AudienceBadgeProps) {
  const { t } = useTranslation('integrity');
  return (
    <span className="integrity audience" data-testid="integrity-audience" data-audience={audience}>
      {t('audience.label', { audience })}
    </span>
  );
}
