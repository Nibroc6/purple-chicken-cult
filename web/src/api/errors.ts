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

// errorKey returns the catalog key for an error: "<ns>:errors.<code>" when the
// feature namespace defines it, else "errors:<code>".
export function errorKey(err: unknown): string {
  if (err instanceof ApiError) return `errors:${err.code}`;
  if (err instanceof TypeError) return 'errors:network';
  return 'errors:unknown';
}
