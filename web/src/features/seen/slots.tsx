// Shell slots of the seen feature: a global flusher that sends the pending
// seen batch when the page is hidden or unloaded. Owner: P0-W06 → P1-W08.
import { useEffect } from 'react';
import type { FeatureSlots } from '../../app/slots';
import { flushSeen } from './tracker';

function SeenFlusher() {
  useEffect(() => {
    const onHide = () => {
      if (document.visibilityState === 'hidden') void flushSeen(true);
    };
    const onPageHide = () => {
      void flushSeen(true);
    };
    document.addEventListener('visibilitychange', onHide);
    window.addEventListener('pagehide', onPageHide);
    return () => {
      document.removeEventListener('visibilitychange', onHide);
      window.removeEventListener('pagehide', onPageHide);
    };
  }, []);
  return null;
}

export const slots: FeatureSlots = { global: [SeenFlusher] };
