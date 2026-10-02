/// <reference lib="webworker" />
// Service worker (SCAFFOLD; plan §6.8): precaches the app shell only and NEVER
// caches /api/*. P1-W05 adds push handling in ./push.ts, imported here.
import { precacheAndRoute } from 'workbox-precaching';
import './push'; // pre-listed (P1-W05 owns push.ts)

type ManifestEntry = string | { url: string; revision: string | null };
declare const self: ServiceWorkerGlobalScope & { __WB_MANIFEST: ManifestEntry[] };

precacheAndRoute(self.__WB_MANIFEST.filter((e) => !(typeof e === 'string' ? e : e.url).startsWith('/api/')));

self.addEventListener('install', () => {
  void self.skipWaiting();
});
