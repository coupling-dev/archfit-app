export type DiffKind = 'add' | 'del' | 'ctx' | 'hunk' | 'header';
export interface DiffLine {
  kind: DiffKind;
  text: string;
}

// A unified diff, split into lines that keep their line breaks. The "--- " and "+++ " file headers count only before
// the first hunk; after it, those prefixes are ordinary deletions and additions.
export function diffLines(text: string): DiffLine[] {
  let inHunk = false;
  return (text.match(/[^\n]*\n|[^\n]+$/g) ?? []).map((line) => {
    let kind: DiffKind = 'ctx';
    if (line.startsWith('@')) {
      inHunk = true;
      kind = 'hunk';
    } else if (!inHunk && (line.startsWith('--- ') || line.startsWith('+++ '))) {
      kind = 'header';
    } else if (line.startsWith('+')) {
      kind = 'add';
    } else if (line.startsWith('-')) {
      kind = 'del';
    }
    return { text: line, kind };
  });
}
