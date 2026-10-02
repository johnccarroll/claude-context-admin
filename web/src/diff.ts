// Line diff (longest common subsequence). Memory files are small, so O(n·m) is fine.
export type Line = { op: ' ' | '-' | '+'; text: string };

export function diffLines(a: string, b: string): Line[] {
  const x = a.split('\n'), y = b.split('\n');
  // Same line count (in-place edits, the common case): compare line by line.
  if (x.length === y.length) {
    return x.flatMap((l, i): Line[] => (l === y[i] ? [{ op: ' ', text: l }] : [{ op: '-', text: l }, { op: '+', text: y[i] }]));
  }
  // ~16 MB of table at most; beyond that, show the whole file as replaced.
  if (x.length * y.length > 8_000_000) return [...x.map((t) => ({ op: '-' as const, text: t })), ...y.map((t) => ({ op: '+' as const, text: t }))];
  const n = x.length, m = y.length;
  const L = Array.from({ length: n + 1 }, () => new Uint16Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) L[i][j] = x[i] === y[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
  const out: Line[] = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) { out.push({ op: ' ', text: x[i] }); i++; j++; }
    else if (L[i + 1][j] >= L[i][j + 1]) out.push({ op: '-', text: x[i++] });
    else out.push({ op: '+', text: y[j++] });
  }
  while (i < n) out.push({ op: '-', text: x[i++] });
  while (j < m) out.push({ op: '+', text: y[j++] });
  return out;
}

/** Changed lines with up to `ctx` unchanged lines of context around each. */
export function hunks(lines: Line[], ctx = 2): Line[] {
  const keep = lines.map((l) => l.op !== ' ');
  return lines.filter((_, i) => keep.slice(Math.max(0, i - ctx), i + ctx + 1).some(Boolean));
}
