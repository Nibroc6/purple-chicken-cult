// rawICU finds an ICU argument with a format type (plural, select, number, …)
// left in text: what i18next-icu shows when a message misses its arguments.
const pattern = /\{\s*[A-Za-z_]\w*\s*,\s*(?:plural|selectordinal|select|number|date|time)\b[^]{0,40}/;

export function rawICU(text: string): string | undefined {
  return pattern.exec(text)?.[0];
}
