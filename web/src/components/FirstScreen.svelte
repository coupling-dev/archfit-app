<script lang="ts">
  import type { ScreenLine } from '../lib/api';

  // The first screen of the PR comment for this result, word for word: the server words it with the code that words
  // the comment, and every line is plain text here. Runs of items form a list.
  let { lines }: { lines: ScreenLine[] } = $props();

  type Block = { kind: 'p' | 'h3' | 'ul'; text: string; items: string[] };
  const head = $derived(lines.find((l) => l.kind === 'headline')?.text ?? '');
  const blocks = $derived(
    lines
      .filter((l) => l.kind !== 'headline')
      .reduce<Block[]>((out, l) => {
        const last = out[out.length - 1];
        if (l.kind === 'item' && last?.kind === 'ul') last.items.push(l.text);
        else if (l.kind === 'item') out.push({ kind: 'ul', text: '', items: [l.text] });
        else out.push({ kind: l.kind === 'heading' ? 'h3' : 'p', text: l.text, items: [] });
        return out;
      }, []),
  );
</script>

<section class="box" aria-labelledby="first-screen-title" id="first-screen">
  <div class="box-head"><h2 id="first-screen-title">{head}</h2></div>
  <div class="box-body stack-sm">
    {#each blocks as b, i (i)}
      {#if b.kind === 'h3'}
        <h3>{b.text}</h3>
      {:else if b.kind === 'ul'}
        <ul>{#each b.items as item, j (j)}<li>{item}</li>{/each}</ul>
      {:else}
        <p>{b.text}</p>
      {/if}
    {/each}
  </div>
</section>
