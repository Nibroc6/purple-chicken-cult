// Report action (SCAFFOLD hand-off stub). Owner: P0-W10 → P1-W09.
// Integrity element (§15.5): always rendered, visible and legible; reserved styles
// and icons are not available to user content.
import { useTranslation } from 'react-i18next';
import type { ReportActionProps } from './types';

export function ReportAction({ target, onReport }: ReportActionProps) {
  const { t } = useTranslation('integrity');
  return (
    <button
      type="button"
      className="integrity report"
      data-testid="integrity-report"
      onClick={() => {
        onReport(target);
      }}
    >
      {t('report.action')}
    </button>
  );
}
