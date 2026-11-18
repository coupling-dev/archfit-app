<script lang="ts">
  // Choose one of a few views or settings. It is a radio group: one tab stop, arrow keys move and choose, so a
  // keyboard user is never trapped and never has to tab through every option.
  let {
    label,
    options,
    value,
    onselect,
  }: {
    label: string;
    options: { value: string; label: string }[];
    value: string;
    onselect: (value: string) => void;
  } = $props();

  let buttons: HTMLButtonElement[] = [];
  const selected = $derived(options.findIndex((option) => option.value === value));

  function arrow(event: KeyboardEvent, index: number) {
    const step = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0;
    if (!step) return;
    event.preventDefault();
    const next = (index + step + options.length) % options.length;
    onselect(options[next].value);
    buttons[next]?.focus();
  }
</script>

<div class="segmented" role="radiogroup" aria-label={label}>
  {#each options as option, i (option.value)}
    <button
      bind:this={buttons[i]}
      type="button"
      role="radio"
      aria-checked={option.value === value}
      tabindex={i === (selected < 0 ? 0 : selected) ? 0 : -1}
      onclick={() => onselect(option.value)}
      onkeydown={(event) => arrow(event, i)}
    >{option.label}</button>
  {/each}
</div>

<style>
  .segmented { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden; background: var(--bg); }
  .segmented button {
    font: 500 12px/20px var(--font); padding: 3px 10px; color: var(--fg-muted); background: var(--bg); border: 0; cursor: pointer;
    white-space: nowrap;
  }
  .segmented button + button { border-left: 1px solid var(--border); }
  .segmented button:hover { color: var(--fg); background: var(--neutral-subtle); }
  .segmented button[aria-checked="true"] { color: var(--fg); background: var(--bg-subtle); font-weight: 600; }
  .segmented button:focus-visible { outline-offset: -3px; }
  @media (max-width: 640px), (pointer: coarse) {
    .segmented button { min-height: 44px; min-width: 44px; }
  }
</style>
