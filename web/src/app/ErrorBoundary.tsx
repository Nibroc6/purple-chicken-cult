// Route error element (SCAFFOLD): unknown routes and render errors.
import { useTranslation } from 'react-i18next';
import { isRouteErrorResponse, useRouteError } from 'react-router';
import { errorKey } from '../api/errors';
import { NotFound } from './NotFound';

export function ErrorBoundary() {
  const err = useRouteError();
  const { t } = useTranslation(['common', 'errors']);
  if (isRouteErrorResponse(err) && err.status === 404) return <NotFound />;
  return (
    <section className="page" role="alert">
      <h1>{t('common:error.title')}</h1>
      <p>{t(errorKey(err) as 'errors:unknown')}</p>
      <button
        type="button"
        onClick={() => {
          location.reload();
        }}
      >
        {t('common:error.retry')}
      </button>
    </section>
  );
}
