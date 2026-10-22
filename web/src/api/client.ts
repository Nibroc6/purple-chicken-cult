// The first-party API client (SCAFFOLD; plan §6.7). openapi-fetch over the types
// generated from api/openapi (npm run gen:api). Same-origin only: the session is
// an HttpOnly SameSite=Strict cookie and unsafe requests carry X-PCC-CSRF.
// There is no public API, no bearer token and no CORS (§16.1).
import createClient, { type Middleware } from 'openapi-fetch';
import type { paths } from './schema.gen';
import { ApiError, NetworkError } from './errors';

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

// QUIET_WAIT_S is the longest rate-limit hint the client waits out by itself:
// the server charges before the handler, so a 429 did nothing and the request
// can be sent again. Short waits (the 2 s spacing of the strict kinds) never
// reach the screen; longer ones are shown with their {seconds}.
export const QUIET_WAIT_S = 2;

// send is fetch resolved per call (tests stub fetch); a request that never got
// an answer is a NetworkError, so only it reads as "offline".
function send(req: Request): Promise<Response> {
  return globalThis.fetch(req).catch((err: unknown) => {
    throw new NetworkError(err);
  });
}

// sendPaced sends req, and once more after a short rate-limit wait. The body
// is read once into a buffer (not a cloned stream) so both sends carry it as
// a plain body. A keepalive request (the seen flush as the page goes away) is
// sent at once: awaiting the body first would start it after the page is gone.
async function sendPaced(req: Request): Promise<Response> {
  if (req.keepalive) return send(req);
  const sent = req.body ? await req.arrayBuffer() : undefined;
  const make = () => (sent === undefined ? req : new Request(req, { body: sent }));
  const res = await send(make());
  if (res.status !== 429) return res;
  const body = (await res
    .clone()
    .json()
    .catch(() => ({}))) as { retry_after_s?: unknown };
  const wait = body.retry_after_s;
  if (typeof wait !== 'number' || wait > QUIET_WAIT_S) return res;
  await new Promise((r) => setTimeout(r, wait * 1000 + 100));
  return send(make());
}

export const api = createClient<paths>({
  baseUrl: `${globalThis.location.origin}/api`,
  credentials: 'same-origin',
  fetch: sendPaced,
});
api.use(middleware);

// unwrap turns an openapi-fetch result into data or a thrown ApiError.
export function unwrap<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.response.ok && res.data !== undefined) return res.data;
  if (res.response.ok && res.response.status === 204) return undefined as T;
  throw new ApiError(res.response.status, res.error);
}
