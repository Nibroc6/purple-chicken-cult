// Export (§10.2): a signed, portable zip of everything the user is part of.
// Needs a fresh passkey step-up (the step-up modal opens on step_up_required).
// Owner: P0-W11 → P1-W10.
import { useMutation } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { exportAccount } from './api';
import { ErrorText, SettingsShell } from './shared';
import { exportFileName } from './vm';

// saveBlob hands the export to the browser's download manager.
export function saveBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.rel = 'noopener';
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => {
    URL.revokeObjectURL(url);
  }, 0);
}

function ExportForm() {
  const { t } = useTranslation('settings');
  const run = useMutation({
    mutationFn: exportAccount,
    onSuccess: (blob) => {
      saveBlob(blob, exportFileName(new Date()));
    },
  });
  return (
    <div className="settings-form">
      <p>{t('export.intro')}</p>
      <p className="muted">{t('export.verify')}</p>
      <div className="settings-actions">
        <button
          type="button"
          disabled={run.isPending}
          onClick={() => {
            run.mutate();
          }}
        >
          {run.isPending ? t('export.working') : t('export.button')}
        </button>
        {run.isSuccess ? (
          <p role="status" className="muted">
            {t('export.done')}
          </p>
        ) : null}
      </div>
      <ErrorText err={run.error} />
    </div>
  );
}

export default function ExportPage() {
  const { t } = useTranslation('settings');
  return <SettingsShell title={t('export.title')}>{() => <ExportForm />}</SettingsShell>;
}
