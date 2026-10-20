// The policy editor: CodeMirror 6 with YAML, line numbers, folding, history and search, and the server's problems as
// lint marks. It is imported by the policy page only, so it ships in that page's chunk.
//
// CodeMirror mounts its styles as a <style> element. The shell's CSP allows it through a per-response nonce, which
// the shell writes into <meta name="csp-nonce"> (internal/control/ui/spa.go); no inline style is allowed otherwise.

import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { yaml } from '@codemirror/lang-yaml';
import {
  bracketMatching,
  foldGutter,
  foldKeymap,
  HighlightStyle,
  indentOnInput,
  syntaxHighlighting,
} from '@codemirror/language';
import { lintGutter, setDiagnostics, type Diagnostic as Mark } from '@codemirror/lint';
import { highlightSelectionMatches, search, searchKeymap } from '@codemirror/search';
import { EditorSelection, EditorState, type Text } from '@codemirror/state';
import {
  drawSelection,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from '@codemirror/view';
import { tags } from '@lezer/highlight';
import type { Diagnostic } from './api';

// Colours are the app's tokens, so the editor follows the light and dark theme with no second set.
const theme = EditorView.theme({
  '&': { color: 'var(--fg)', backgroundColor: 'var(--bg)', fontSize: '13px' },
  '.cm-content': { fontFamily: 'var(--mono)', caretColor: 'var(--fg)' },
  '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '1.5' },
  '.cm-gutters': {
    backgroundColor: 'var(--bg-subtle)',
    color: 'var(--fg-muted)',
    borderRight: '1px solid var(--border)',
  },
  '.cm-activeLine': { backgroundColor: 'var(--neutral-subtle)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--neutral-subtle)', color: 'var(--fg)' },
  '&.cm-focused .cm-cursor': { borderLeftColor: 'var(--fg)' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground': { backgroundColor: 'var(--accent-border)' },
  '.cm-panels': { backgroundColor: 'var(--bg-subtle)', color: 'var(--fg)' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid var(--border)' },
  '.cm-textfield': { color: 'var(--fg)', backgroundColor: 'var(--bg)', border: '1px solid var(--border)' },
  '.cm-button': { color: 'var(--fg)', backgroundImage: 'none', backgroundColor: 'var(--bg-subtle)' },
  '.cm-tooltip': { color: 'var(--fg)', backgroundColor: 'var(--bg)', border: '1px solid var(--border)' },
  '.cm-foldPlaceholder': {
    color: 'var(--fg-muted)',
    backgroundColor: 'var(--bg-inset)',
    border: '1px solid var(--border)',
  },
});

const highlight = HighlightStyle.define([
  { tag: [tags.propertyName, tags.definition(tags.propertyName)], color: 'var(--accent)' },
  { tag: [tags.string, tags.special(tags.string)], color: 'var(--success)' },
  { tag: [tags.number, tags.bool, tags.null, tags.keyword], color: 'var(--attention)' },
  { tag: [tags.labelName, tags.typeName, tags.meta], color: 'var(--done)' },
  { tag: tags.comment, color: 'var(--fg-muted)', fontStyle: 'italic' },
]);

// marksOf places each problem that has a line from its column to the end of that line; a problem of the whole text
// has no mark and is only in the list. A line past the end of the text goes to the last line.
export function marksOf(doc: Text, list: Diagnostic[]): Mark[] {
  const marks: Mark[] = [];
  for (const d of list) {
    if (!d.line) continue;
    const line = doc.line(Math.min(d.line, doc.lines));
    const from = line.from + Math.min(Math.max((d.column ?? 1) - 1, 0), line.length);
    marks.push({ from, to: Math.max(from, line.to), severity: 'error', message: d.message });
  }
  return marks;
}

export interface EditorOptions {
  text: string;
  label: string; // the accessible name of the text area
  describedBy: string; // the id of the hint under the toolbar
  nonce: string;
  onChange: (text: string) => void;
}

export function createEditor(parent: HTMLElement, o: EditorOptions): EditorView {
  return new EditorView({ parent, state: stateOf(o.text, o) });
}

function stateOf(text: string, o: EditorOptions): EditorState {
  return EditorState.create({
    doc: text,
    extensions: [
      // A file with CRLF keeps it: the text sent back joins lines the way the file on the branch does.
      text.includes('\r\n') ? EditorState.lineSeparator.of('\r\n') : [],
      EditorView.cspNonce.of(o.nonce),
      EditorView.contentAttributes.of({ 'aria-label': o.label, 'aria-describedby': o.describedBy }),
      lineNumbers(),
      highlightActiveLineGutter(),
      foldGutter(),
      lintGutter(),
      history(),
      drawSelection(),
      indentOnInput(),
      bracketMatching(),
      highlightActiveLine(),
      highlightSelectionMatches(),
      search({ top: true }),
      yaml(),
      syntaxHighlighting(highlight),
      theme,
      // Tab is not bound: YAML takes no tab characters, and Tab must leave the editor for the keyboard.
      keymap.of([...defaultKeymap, ...historyKeymap, ...foldKeymap, ...searchKeymap]),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) o.onChange(u.state.doc.toString());
      }),
    ],
  });
}

// setText replaces the whole text, as loading another version does: history starts again with it.
export function setText(view: EditorView, text: string, o: EditorOptions) {
  view.setState(stateOf(text, o));
}

export function showProblems(view: EditorView, list: Diagnostic[]) {
  view.dispatch(setDiagnostics(view.state, marksOf(view.state.doc, list)));
}

// goToLine selects a line and brings it into view, for a problem in the list or a #L<n> link from the graph.
export function goToLine(view: EditorView, n: number) {
  const line = view.state.doc.line(Math.min(Math.max(n, 1), view.state.doc.lines));
  view.dispatch({
    selection: EditorSelection.range(line.from, line.to),
    effects: EditorView.scrollIntoView(line.from, { y: 'center' }),
  });
  view.focus();
}

export function textOf(view: EditorView): string {
  return view.state.doc.toString();
}
