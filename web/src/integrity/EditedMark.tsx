// Edited mark (§10.2, §15.5). Owner: P0-W10 → P1-W09. Always in the DOM (every
// format renders it), shown only for an edited post, with the edit time.
import { useTranslation } from 'react-i18next';
import { formatDateTime } from '../lib/time';
import type { EditedMarkProps } from './types';
import './integrity.css';

export function EditedMark({ editedAt }: EditedMarkProps) {
  const { t, i18n } = useTranslation('integrity');
  return (
    <span
      className="integrity edited"
      data-testid="integrity-edited"
      hidden={!editedAt}
      title={editedAt ? t('editedAt', { at: formatDateTime(editedAt, i18n.language) }) : undefined}
    >
      {editedAt ? <time dateTime={editedAt.toISOString()}>{t('edited')}</time> : t('edited')}
    </span>
  );
}
