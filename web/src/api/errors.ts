// API error mapping (SCAFFOLD). The server sends codes, never prose (§15.4); the
// client localizes them from the errors namespace (and <ns>:errors.<code>).
// i18n-dynamic: errors:

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly field: string | undefined;
  readonly retryAfterS: number | undefined;
  readonly feature: string | undefined;

  constructor(status: number, body: unknown) {
    const b = (body ?? {}) as { error?: unknown; field?: unknown; retry_after_s?: unknown; feature?: unknown };
    const code = typeof b.error === 'string' && /^[a-z][a-z0-9_]*$/.test(b.error) ? b.error : 'unknown';
    super(code);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.field = typeof b.field === 'string' ? b.field : undefined;
    this.retryAfterS = typeof b.retry_after_s === 'number' ? b.retry_after_s : undefined;
    this.feature = typeof b.feature === 'string' ? b.feature : undefined;
  }

  get isNotFound(): boolean {
    return this.status === 404;
  }

  get isStepUp(): boolean {
    return this.status === 401 && this.code === 'step_up_required';
  }
}

// NetworkError is a request that never reached the server (the API client
// wraps fetch's own failure in it).
export class NetworkError extends Error {
  constructor(cause: unknown) {
    super('network', { cause });
    this.name = 'NetworkError';
  }
}

// isStaleBuild: a page's code could not be loaded, as when the app was
// redeployed while the tab still runs the old build (its chunks are gone).
export function isStaleBuild(err: unknown): boolean {
  return (
    err instanceof Error &&
    /dynamically imported module|Importing a module script failed|Unable to preload CSS/i.test(err.message)
  );
}

// errorKey returns the catalog key for an error: "<ns>:errors.<code>" when the
// feature namespace defines it, else "errors:<code>". Only a request that
// failed on the network, or a browser that says it is offline, reads as
// offline; any other error (a bug in a page included) is "unknown".
export function errorKey(err: unknown): string {
  if (err instanceof ApiError) return `errors:${err.code}`;
  if (isStaleBuild(err)) return 'errors:updated';
  if (err instanceof NetworkError) return 'errors:network';
  if (typeof navigator !== 'undefined' && !navigator.onLine) return 'errors:network';
  return 'errors:unknown';
}
