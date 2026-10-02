// Vite config (SCAFFOLD-0b). React, PWA (injectManifest, our own TT-safe
// registration), dev proxy to the Go server, and the dev CSP.
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import { VitePWA } from 'vite-plugin-pwa';

const api = process.env.PCC_DEV_API ?? 'http://127.0.0.1:8080';

// The dev nonce only exists on the dev server; production builds have no inline
// script or style at all.
const DEV_NONCE = 'pcc-dev-nonce';

// devCSP sends the production CSP on the dev server, with the nonce Vite puts on
// its inline React Refresh preamble and style tags. Trusted Types run in
// report-only mode in dev (Vite's HMR client is not TT-clean); the production
// header (internal/platform/secheaders) enforces them.
function devCSP(): Plugin {
  const csp = [
    "default-src 'none'",
    `script-src 'self' 'nonce-${DEV_NONCE}'`,
    `style-src 'self' 'nonce-${DEV_NONCE}'`,
    "img-src 'self' blob: data:",
    "font-src 'self'",
    "connect-src 'self' ws: wss:",
    "worker-src 'self'",
    "manifest-src 'self'",
    "media-src 'self' blob:",
    "frame-ancestors 'none'",
    "base-uri 'none'",
    "form-action 'self'",
    "object-src 'none'",
  ].join('; ');
  const tt = "require-trusted-types-for 'script'; trusted-types pcc-forbid pcc-script-url pcc-dev-script-url";
  return {
    name: 'pcc-dev-csp',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((_req, res, next) => {
        res.setHeader('Content-Security-Policy', csp);
        res.setHeader('Content-Security-Policy-Report-Only', tt);
        res.setHeader('X-Frame-Options', 'DENY');
        res.setHeader('tdm-reservation', '1');
        res.setHeader('Referrer-Policy', 'no-referrer');
        next();
      });
    },
  };
}

export default defineConfig(({ command }) => ({
  plugins: [
    react(),
    devCSP(),
    VitePWA({
      strategies: 'injectManifest',
      srcDir: 'src/sw',
      filename: 'sw.ts',
      injectRegister: false,
      manifest: false,
      injectManifest: { globPatterns: ['**/*.{js,css,html,svg,woff2}'] },
      devOptions: { enabled: false },
    }),
  ],
  html: command === 'serve' ? { cspNonce: DEV_NONCE } : {},
  build: {
    outDir: process.env.PCC_WEB_OUT_DIR ?? 'dist',
    emptyOutDir: true,
    sourcemap: false,
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: api, changeOrigin: false },
      '/m': { target: api, changeOrigin: false },
    },
  },
}));
