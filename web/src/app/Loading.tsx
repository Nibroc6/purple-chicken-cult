import { useTranslation } from 'react-i18next';

export function Loading() {
  const { t } = useTranslation('common');
  return (
    <p className="muted" role="status">
      {t('loading')}
    </p>
  );
}
