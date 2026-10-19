// What the graph page has selected, shared by the list, the canvas, the matrix and the inspector. Clicking the selected
// thing again clears it.

export type Selection = { kind: 'link'; key: string } | { kind: 'node'; name: string } | null;

export const ui = $state<{ sel: Selection; emphasis: boolean }>({ sel: null, emphasis: true });

export function pickLink(key: string) {
  ui.sel = ui.sel?.kind === 'link' && ui.sel.key === key ? null : { kind: 'link', key };
}

export function pickNode(name: string) {
  ui.sel = ui.sel?.kind === 'node' && ui.sel.name === name ? null : { kind: 'node', name };
}
