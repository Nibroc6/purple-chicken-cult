// The first-party API client (SCAFFOLD; plan §6.7). openapi-fetch over the types
// generated from api/openapi (npm run gen:api). Same-origin only: the session is
// an HttpOnly SameSite=Strict cookie and unsafe requests carry X-PCC-CSRF.
// There is no public API, no bearer token and no CORS (§16.1).
import createClient, { type Middleware } from 'openapi-fetch';
import type { paths } from './schema.gen';
import { ApiError } from './errors';

let csrf: string | undefined;

// setCSRF stores the CSRF token from /api/session (memory only).
export function setCSRF(token: string | undefined): void {
  csrf = token;
}

type Listener = (err: ApiError) => void;
const stepUpListeners = new Set<Listener>();
const sessionExpiredListeners = new Set<Listener>();

// onStepUp subscribes to 401 step_up_required (the step-up slot opens its modal).
export function onStepUp(fn: Listener): () => void {
  stepUpListeners.add(fn);
  return () => stepUpListeners.delete(fn);
}

// onFeedSessionExpired subscribes to 409 feed_session_expired (the feed restarts).
export function onFeedSessionExpired(fn: Listener): () => void {
  sessionExpiredListeners.add(fn);
  return () => sessionExpiredListeners.delete(fn);
}

const safe = new Set(['GET', 'HEAD', 'OPTIONS']);

const middleware: Middleware = {
  onRequest({ request }) {
    if (csrf && !safe.has(request.method)) request.headers.set('X-PCC-CSRF', csrf);
    return request;
  },
  async onResponse({ response }) {
    if (response.ok) return response;
    const body: unknown = await response
      .clone()
      .json()
      .catch(() => ({}));
    const err = new ApiError(response.status, body);
    if (err.isStepUp) {
      stepUpListeners.forEach((fn) => {
        fn(err);
      });
    }
    if (err.status === 409 && err.code === 'feed_session_expired') {
      sessionExpiredListeners.forEach((fn) => {
        fn(err);
      });
    }
    return response;
  },
};

export const api = createClient<paths>({
  baseUrl: `${globalThis.location.origin}/api`,
  credentials: 'same-origin',
  fetch: (req: Request) => globalThis.fetch(req), // resolved per call (tests stub fetch)
});
api.use(middleware);

// unwrap turns an openapi-fetch result into data or a thrown ApiError.
export function unwrap<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.response.ok && res.data !== undefined) return res.data;
  if (res.response.ok && res.response.status === 204) return undefined as T;
  throw new ApiError(res.response.status, res.error);
}
