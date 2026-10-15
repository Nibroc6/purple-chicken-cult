// Edited mark (SCAFFOLD hand-off stub). Owner: P0-W10 → P1-W09.
// Integrity element (§15.5): always rendered, visible and legible; reserved styles
// and icons are not available to user content.
import { useTranslation } from 'react-i18next';
import type { EditedMarkProps } from './types';

export function EditedMark({ editedAt }: EditedMarkProps) {
  const { t } = useTranslation('integrity');
  return (
    <span className="integrity edited" data-testid="integrity-edited" hidden={!editedAt}>
      {t('edited')}
    </span>
  );
}
