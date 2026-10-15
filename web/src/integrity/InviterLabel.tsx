// Inviter label (SCAFFOLD hand-off stub). Owner: P0-W10 → P1-W09.
// Integrity element (§15.5): always rendered, visible and legible; reserved styles
// and icons are not available to user content.
import { useTranslation } from 'react-i18next';
import type { InviterLabelProps } from './types';

export function InviterLabel({ inviter }: InviterLabelProps) {
  const { t } = useTranslation('integrity');
  return (
    <span className="integrity inviter-label" data-testid="integrity-inviter-label">
      {t('inviter.label', { kind: inviter.kind, handle: inviter.handle ?? '' })}
    </span>
  );
}
