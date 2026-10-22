// Report action (§10.4, §15.5). Owner: P0-W10 → P1-W09. Hands the target to the
// page's report flow (the report modal belongs to moderation). An icon button
// whose tooltip and accessible name say what it does.
import { useTranslation } from 'react-i18next';
import { IconButton } from '../lib/IconButton';
import type { ReportActionProps } from './types';
import './integrity.css';

export function ReportAction({ target, onReport }: ReportActionProps) {
  const { t } = useTranslation('integrity');
  return (
    <IconButton
      icon="report"
      label={t('report.action')}
      className="integrity report"
      data-testid="integrity-report"
      data-target={target.kind}
      onClick={() => {
        onReport(target);
      }}
    />
  );
}
