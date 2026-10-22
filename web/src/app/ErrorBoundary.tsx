// Route error element (SCAFFOLD): unknown routes and render errors. A page
// whose code is gone because the app was redeployed under an open tab reloads
// once by itself (then offers the reload).
import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { isRouteErrorResponse, useRouteError } from 'react-router';
import { errorKey, isStaleBuild } from '../api/errors';
import { NotFound } from './NotFound';

const RELOADED = 'pcc.reloadedFor';

export function ErrorBoundary() {
  const err = useRouteError();
  const { t } = useTranslation(['common', 'errors']);
  const stale = isStaleBuild(err);
  useEffect(() => {
    if (!stale) return;
    try {
      if (sessionStorage.getItem(RELOADED) === location.href) return;
      sessionStorage.setItem(RELOADED, location.href);
    } catch {
      return;
    }
    location.reload();
  }, [stale]);
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
