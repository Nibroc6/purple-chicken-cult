// Types for the userland punycode package (plan §2.2; it ships none). Imported
// by path so bundlers never substitute Node's deprecated builtin.
declare module 'punycode/punycode.es6.js' {
  export function decode(input: string): string;
  export function encode(input: string): string;
  export function toUnicode(input: string): string;
  export function toASCII(input: string): string;
}
