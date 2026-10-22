// The report modal (?report=<kind>:<ref> over any page; §10.4): the report
// form of ReportPage in the shared modal, so reporting never leaves the page.
// /report?kind=&ref= stays as a page for links. Owner: P0-W12 → P1-W06.
import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal, useModalParam } from '../../app/Modal';
import { parseReport, reportParam, reportValue, type TargetKind } from './api';
import { ReportForm } from './ReportPage';
import './moderation.css';

// useOpenReport opens the report modal for a target over the current page.
export function useOpenReport(): (target: { kind: TargetKind; ref: string }) => void {
  const [, open] = useModalParam(reportParam);
  return useCallback(
    (target) => {
      open(reportValue(target.kind, target.ref));
    },
    [open],
  );
}

export function ReportModal() {
  const { t } = useTranslation('moderation');
  const [value, , close] = useModalParam(reportParam);
  const target = parseReport(value);
  if (value === null) return null;
  return (
    <Modal
      // i18n-dynamic: moderation:report.heading.
      title={target ? t(`report.heading.${target.kind}`) : t('report.title')}
      onClose={close}
      testId="report-modal"
    >
      {target ? (
        <ReportForm key={value} kind={target.kind} target={target.ref} onBack={close} />
      ) : (
        <p className="muted" data-testid="report-invalid">
          {t('report.invalid')}
        </p>
      )}
    </Modal>
  );
}
