/**
 * Compare two non-negative decimal strings without converting to number
 * (EVT-09: int64 seq/version must never lose precision in JavaScript).
 */
export function cmpDec(a: string, b: string): number {
  const x = a.replace(/^0+(?=\d)/, "");
  const y = b.replace(/^0+(?=\d)/, "");
  if (x.length !== y.length) return x.length < y.length ? -1 : 1;
  return x < y ? -1 : x > y ? 1 : 0;
}

export function isDec(v: unknown): v is string {
  return typeof v === "string" && /^\d+$/.test(v);
}
