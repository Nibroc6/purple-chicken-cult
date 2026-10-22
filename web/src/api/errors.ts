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
// feature namespace defines it, else "errors:<code>". A rate_limited answer
// without a retry hint gets the wording that needs no {seconds}.
export function errorKey(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'rate_limited' && err.retryAfterS === undefined) return 'errors:rate_limited_soon';
    return `errors:${err.code}`;
  }
  if (err instanceof TypeError) return 'errors:network';
  return 'errors:unknown';
}

// errorParams are the ICU arguments every error message may need: {seconds}
// for rate_limited (whole seconds, at least 1). Pass them with every error key,
// whichever namespace it comes from, or the message shows its raw template.
export function errorParams(err: unknown): Record<string, number> {
  if (err instanceof ApiError && err.retryAfterS !== undefined) {
    return { seconds: Math.max(1, Math.ceil(err.retryAfterS)) };
  }
  return {};
}

// ErrorMsg is a failure to show later: its catalog key and ICU arguments. Keep
// one of these in state, never a bare key, so the arguments are not lost.
export interface ErrorMsg {
  key: string;
  params: Record<string, number>;
}

// errorMsg captures err for display, with a feature's own key when given.
export function errorMsg(err: unknown, key: string = errorKey(err)): ErrorMsg {
  return { key, params: errorParams(err) };
}

// errorMsgFor is errorMsg through a feature's key mapper, or undefined when
// the mapper says nothing should be shown.
export function errorMsgFor(err: unknown, keyFor: (err: unknown) => string | undefined): ErrorMsg | undefined {
  const key = keyFor(err);
  return key === undefined ? undefined : errorMsg(err, key);
}

// Translate is the slice of i18next's t that errorText needs.
type Translate = (key: 'errors:unknown', params: Record<string, number>) => string;

// errorText renders an error (or a captured ErrorMsg) with its arguments: the
// one way UI code turns a failure into words. keyFor is a feature's key mapper
// (postErrorKey, …); it defaults to errorKey.
export function errorText(t: Translate, err: unknown, keyFor: (err: unknown) => string | undefined = errorKey): string {
  if (isErrorMsg(err)) return t(err.key as 'errors:unknown', err.params);
  return t((keyFor(err) ?? errorKey(err)) as 'errors:unknown', errorParams(err));
}

function isErrorMsg(x: unknown): x is ErrorMsg {
  return typeof x === 'object' && x !== null && !(x instanceof Error) && typeof (x as ErrorMsg).key === 'string' && typeof (x as ErrorMsg).params === 'object';
}
