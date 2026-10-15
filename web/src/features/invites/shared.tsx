// Shared pieces of the invites pages. Owner: P0-W04 → P1-W02.
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { errorParams, invitesErrorKey } from './vm';
import './invites.css';

// invitesKey prefixes every query of this feature.
export const invitesKey = ['invites'] as const;

// useNow re-renders every intervalMs and returns the current time.
export function useNow(intervalMs = 1000): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => {
      setNow(new Date());
    }, intervalMs);
    return () => {
      clearInterval(id);
    };
  }, [intervalMs]);
  return now;
}

// ErrorText renders an API error from the invites or errors catalogs.
export function ErrorText({ err }: { err: unknown }) {
  const { t } = useTranslation(['invites', 'errors']);
  if (!err) return null;
  return (
    <p className="invites-error" role="alert">
      {t(invitesErrorKey(err) as 'errors:unknown', errorParams(err))}
    </p>
  );
}
