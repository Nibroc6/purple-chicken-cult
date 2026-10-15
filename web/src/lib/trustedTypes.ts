// Trusted Types (plan §6.6, D18). Two policies:
//  - pcc-forbid: createHTML / createScript always throw (any HTML or script sink
//    use is a bug, surfaced in e2e as a console error);
//  - pcc-script-url: createScriptURL allows only same-origin /sw.js and /assets/*
//    (Vite's hashed output). It is the only way to obtain the TrustedScriptURL
//    that new Worker(...) and navigator.serviceWorker.register(...) require.
// The dev server registers pcc-dev-script-url instead, which also allows /src/,
// /@vite/ and /node_modules/ module workers; the production policy never does.

interface TTPolicy {
  createScriptURL(input: string): unknown;
}
interface TTRules {
  createHTML?: (s: string) => string;
  createScript?: (s: string) => string;
  createScriptURL?: (s: string) => string;
}
interface TTFactory {
  createPolicy(name: string, rules: TTRules): TTPolicy;
}

let scriptPolicy: TTPolicy | undefined;
let installed = false;

function factory(): TTFactory | undefined {
  return (globalThis as { trustedTypes?: TTFactory }).trustedTypes;
}

export function allowedScriptURL(u: string, base: string, dev: boolean): boolean {
  const url = new URL(u, base);
  if (url.origin !== new URL(base).origin) return false;
  if (url.pathname === '/sw.js' || url.pathname.startsWith('/assets/')) return true;
  return (
    dev &&
    (url.pathname.startsWith('/src/') || url.pathname.startsWith('/@vite/') || url.pathname.startsWith('/node_modules/'))
  );
}

export function installTrustedTypes(): void {
  if (installed) return;
  installed = true;
  const tt = factory();
  if (!tt) return;
  const deny = (): string => {
    throw new TypeError('pcc-forbid');
  };
  tt.createPolicy('pcc-forbid', { createHTML: deny, createScript: deny });
  const dev = import.meta.env.DEV;
  scriptPolicy = tt.createPolicy(dev ? 'pcc-dev-script-url' : 'pcc-script-url', {
    createScriptURL: (u: string) => {
      if (!allowedScriptURL(u, location.href, dev)) throw new TypeError('pcc-script-url');
      return u;
    },
  });
}

// scriptURL returns a TrustedScriptURL where the browser has Trusted Types, else u.
export function scriptURL(u: string): string {
  if (!scriptPolicy) return u;
  return scriptPolicy.createScriptURL(u) as string;
}
