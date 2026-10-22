// Theme CSS: the advanced mode of a theme (ARCHITECTURE §15.3). It is the
// reader's own raw CSS, applied only to their own view. The editor warns that
// it can change or hide anything, the integrity elements included. Two things
// still hold: the app drops the sheet on locked routes (sign-in, recovery,
// co-signing, settings, moderation, report, the report modal), so a passkey
// prompt always looks like the app and the theme page always works to undo a
// theme; and it is dropped under high contrast. image(name) names one of the
// theme's own uploads and becomes its signed URL. The sheet is a constructed stylesheet
// (no style element: style-src 'self' stays strict), and the CSP still decides
// what it may load. gate/themes (sheet.go) checks size and image names.
// Owner: P1-W09.

export const MAX_SHEET = 32 << 10;
export const MAX_IMAGES = 8;
export const imageName = /^[a-z][a-z0-9-]{0,31}$/;
const imageRef = /image\(\s*([a-z][a-z0-9-]{0,31})\s*\)/g;

// Routes where the sheet is dropped (prefixes), and search params that open a
// locked flow as a modal over any page (the report form: ?report=<kind>:<ref>).
const lockedRoutes = ['/login', '/enroll', '/join', '/recover', '/cosign', '/settings', '/mod', '/report', '/blame', '/terms'];
const lockedParams = ['report'];

export function isLockedRoute(path: string, search = ''): boolean {
  const params = new URLSearchParams(search);
  return lockedRoutes.some((p) => path === p || path.startsWith(`${p}/`)) || lockedParams.some((k) => params.has(k));
}

export interface SheetError {
  line: number; // 0: the sheet as a whole
}

// sheetImages are the image names src uses.
export function sheetImages(src: string): Set<string> {
  return new Set([...src.matchAll(imageRef)].map((m) => m[1] ?? ''));
}

// checkSheet mirrors the server: the size limit, no NUL, and every image()
// naming one of images.
export function checkSheet(src: string, images: ReadonlySet<string>): SheetError | undefined {
  if (new TextEncoder().encode(src).length > MAX_SHEET) return { line: 0 };
  for (const [i, line] of src.split(/\r\n|\r|\n/).entries()) {
    if (line.includes('\0')) return { line: i + 1 };
    for (const m of line.matchAll(imageRef)) {
      if (!images.has(m[1] ?? '')) return { line: i + 1 };
    }
  }
  return undefined;
}

export type Scope = 'app' | 'preview';

// Signed media URLs only: anything that could break out of url("…") becomes none.
const safeURL = /^(https?:\/\/[^"'\\\s()<>]+|\/[^"'\\\s()<>]*)$/;

// buildSheet swaps each image(name) for its signed URL (none while it has
// none). The preview nests the sheet under the preview canvas.
export function buildSheet(src: string, urls: ReadonlyMap<string, string>, scope: Scope): string {
  const css = src.replace(imageRef, (_, name: string) => {
    const url = urls.get(name);
    return url && safeURL.test(url) ? `url("${url}")` : 'none';
  });
  return scope === 'preview' ? `[data-theme-preview] {\n${css}\n}` : css;
}

export interface SheetTarget {
  replace(css: string): void;
  clear(): void;
}

// documentSheet adopts one constructed stylesheet into doc (a no-op where
// the browser cannot construct stylesheets).
export function documentSheet(doc: Document = document): SheetTarget {
  let sheet: CSSStyleSheet | undefined;
  const ok = typeof CSSStyleSheet !== 'undefined' && 'adoptedStyleSheets' in doc;
  return {
    replace(css) {
      if (!ok) return;
      if (!sheet) {
        try {
          sheet = new CSSStyleSheet();
        } catch {
          return;
        }
        doc.adoptedStyleSheets = [...doc.adoptedStyleSheets, sheet];
      }
      sheet.replaceSync(css);
    },
    clear() {
      if (!sheet) return;
      const s = sheet;
      doc.adoptedStyleSheets = doc.adoptedStyleSheets.filter((x) => x !== s);
      sheet = undefined;
    },
  };
}
