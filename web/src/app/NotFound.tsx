// The client-side not-found page. Like the API's uniform 404, it never says
// whether something exists but is hidden.
import { useTranslation } from 'react-i18next';

export function NotFound() {
  const { t } = useTranslation('common');
  return (
    <section className="page">
      <h1>{t('notFound.title')}</h1>
      <p>{t('notFound.body')}</p>
    </section>
  );
}
