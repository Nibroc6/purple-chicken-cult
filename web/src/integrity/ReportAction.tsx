// Report action (§10.4, §15.5). Owner: P0-W10 → P1-W09. Hands the target to the
// page's report flow (the report modal belongs to moderation).
import { useTranslation } from 'react-i18next';
import { Icon } from '../lib/Icon';
import type { ReportActionProps } from './types';
import './integrity.css';

export function ReportAction({ target, onReport }: ReportActionProps) {
  const { t } = useTranslation('integrity');
  return (
    <button
      type="button"
      className="integrity report"
      data-testid="integrity-report"
      data-target={target.kind}
      onClick={() => {
        onReport(target);
      }}
    >
      <Icon name="report" />
      {t('report.action')}
    </button>
  );
}
