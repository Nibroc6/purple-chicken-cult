// Entry point (SCAFFOLD): Trusted Types first, then i18n, then the app.
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './app/App';
import { setupI18n } from './i18n/setup';
import { installTrustedTypes } from './lib/trustedTypes';
import { registerServiceWorker } from './sw/register';
import './styles/tokens.css';
import './styles/global.css';

installTrustedTypes();

async function start() {
  await setupI18n();
  const root = document.getElementById('root');
  if (!root) return;
  createRoot(root).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
  await registerServiceWorker();
}

void start();
