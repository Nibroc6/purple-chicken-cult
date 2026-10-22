// Shell slots of the themes feature: the signed-in reader's theme applies on
// every page and every device (it is stored server-side). Signed out, or on the
// default, the app's own tokens apply. Its theme CSS applies outside locked
// routes. Owner: P1-W09.
import { useLocation } from 'react-router';
import type { FeatureSlots } from '../../app/slots';
import { useMe } from '../../app/session';
import { isLockedRoute } from './sheet';
import { useAppliedSheet, useAppliedTheme, useStoredTheme } from './useTheme';

const noImages: [] = [];

export function ThemeApplier() {
  const me = useMe();
  const theme = useStoredTheme();
  const { pathname, search } = useLocation();
  const stored = me ? (theme.data ?? undefined) : undefined;
  useAppliedTheme(stored?.tokens);
  // Theme CSS stays off the surfaces that must look like the app (sign-in,
  // recovery, settings, moderation, report): sheet.ts.
  useAppliedSheet(stored && !isLockedRoute(pathname, search) ? stored.css || undefined : undefined, stored?.images ?? noImages, 'app');
  return null;
}

export const slots: FeatureSlots = { global: [ThemeApplier] };
