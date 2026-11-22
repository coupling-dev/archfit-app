<script lang="ts">
  import type { Step } from '../../lib/request';

  // The story of a change (story spec 8.18, section 1): the same six steps on every surface, the current one marked.
  const labels = ['change', 'check', 'ask', 'answer', 'policy', 're-check'];
  const marks: Record<Step, string> = { done: '✓', failed: '✗', current: '●', later: '○', skipped: '–' };
  const words: Record<Step, string> = { done: 'done', failed: 'failed', current: 'now', later: 'later', skipped: 'not needed' };
  let { steps, ask = '' }: { steps: Step[]; ask?: string } = $props();
</script>

<ol class="story" aria-label="Story of the change">
  {#each labels as label, i (label)}
    <li
      class:done={steps[i] === 'done'}
      class:failed={steps[i] === 'failed'}
      class:current={steps[i] === 'current'}
      class:skipped={steps[i] === 'skipped'}
      aria-current={steps[i] === 'current' ? 'step' : undefined}>
      <span class="mark" aria-hidden="true">{marks[steps[i]]}</span>{label}{i === 2 && ask && steps[i] !== 'skipped' ? ` ${ask}` : ''}<span
        class="sr-only">: {words[steps[i]]}</span
      >
    </li>
  {/each}
</ol>

<style>
  .story { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 4px 6px; font-size: 13px; }
  .story li {
    display: inline-flex; align-items: center; gap: 5px; padding: 2px 9px 2px 7px; border-radius: 999px;
    border: 1px solid var(--border); background: var(--bg); color: var(--fg-muted); white-space: nowrap;
  }
  .story li + li { margin-left: 16px; position: relative; }
  .story li + li::before { content: '→'; position: absolute; left: -15px; color: var(--fg-muted); font-weight: 400; }
  .mark { font-weight: 700; width: 1em; text-align: center; }
  .done { color: var(--success); border-color: var(--success-border); background: var(--success-subtle); }
  .failed { color: var(--danger); border-color: var(--danger-border); background: var(--danger-subtle); }
  .current { color: var(--fg); border-color: var(--accent); background: var(--accent-subtle); font-weight: 600; box-shadow: 0 0 0 2px var(--accent-border); }
  .skipped { border-style: dashed; }
</style>
