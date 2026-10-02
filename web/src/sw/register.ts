// Trusted-Types-safe service-worker registration (D18): vite-plugin-pwa's
// auto-register passes a plain string and fails under enforced Trusted Types.
import { scriptURL } from '../lib/trustedTypes';

export async function registerServiceWorker(): Promise<void> {
  if (!import.meta.env.PROD || !('serviceWorker' in navigator)) return;
  try {
    await navigator.serviceWorker.register(scriptURL('/sw.js'), { scope: '/', type: 'module' });
  } catch {
    // Offline support is best-effort; the app works without it.
  }
}
