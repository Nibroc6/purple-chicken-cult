// Shell slots of the themes feature: the signed-in reader's theme applies on
// every page and every device (it is stored server-side). Signed out, or on the
// default, the app's own tokens apply. Owner: P1-W09.
import type { FeatureSlots } from '../../app/slots';
import { useMe } from '../../app/session';
import { useAppliedTheme, useStoredTheme } from './useTheme';

export function ThemeApplier() {
  const me = useMe();
  const theme = useStoredTheme();
  useAppliedTheme(me ? (theme.data?.tokens ?? undefined) : undefined);
  return null;
}

export const slots: FeatureSlots = { global: [ThemeApplier] };
