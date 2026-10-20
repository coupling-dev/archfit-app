<script lang="ts">
  import { statuses, statusWord, strengths, strengthWidth, strengthWord, unmeasuredWidth } from '../../lib/graph';

  // The legend in the words of Balanced Coupling, folded: one line per visual channel.
  let { pull }: { pull: boolean } = $props();
</script>

<details class="g-legend" open>
  <summary>Legend</summary>
  <dl>
    <dt>Colour</dt>
    <dd>
      the status of the dependency:
      {#each statuses as s (s)}<span class="g-swatch {s}"><i aria-hidden="true"></i>{statusWord[s]}</span>{/each}
    </dd>
    <dt>Width</dt>
    <dd>
      integration strength, weakest thinnest:
      {#each strengths as s (s)}
        <span class="g-swatch">
          <svg width="28" height="10" aria-hidden="true"><path d="M1 5H27" class="g-legend-line" stroke-width={strengthWidth[s]} /></svg>
          {strengthWord[s]}
        </span>
      {/each}
    </dd>
    <dt>Dashed line</dt>
    <dd>
      <span class="g-swatch">
        <svg width="28" height="10" aria-hidden="true"><path d="M1 5H27" class="g-legend-line unmeasured" stroke-width={unmeasuredWidth} /></svg>
        strength not measured: no seam of the report names the dependency, or it names a strength this version does not
        know
      </span>
    </dd>
    <dt>Label</dt>
    <dd>the number of imports behind the dependency; · when only a finding names the two modules</dd>
    <dt>Faint, thin</dt>
    <dd>accepted earlier: known debt, not a problem</dd>
    <dt>Dashed module</dt>
    <dd>not in the policy</dd>
    {#if pull}
      <dt>Halo</dt>
      <dd>the dependency was added by this pull request, as the PR comment says</dd>
    {/if}
  </dl>
  <p class="muted small">Position does not establish permission: only the policy permits a dependency.</p>
</details>
