<script lang="ts">
  import { statuses, statusWord, strengths, strengthWord, unmeasuredWidth } from '../../lib/graph';

  // The legend in one line, in the words of Balanced Coupling: colour is the status, width the integration strength.
  // The rarer marks fold under "More".
  let { pull }: { pull: boolean } = $props();
</script>

<div class="g-legend" role="group" aria-label="Legend">
  {#each statuses as s (s)}<span class="g-swatch {s}"><i aria-hidden="true"></i>{statusWord[s]}</span>{/each}
  <span class="g-legend-sep" aria-hidden="true"></span>
  <span class="g-swatch" title={strengths.map((s) => strengthWord[s]).join(', ') + ': weakest thinnest'}>
    <svg width="40" height="10" aria-hidden="true"><path d="M1 4.25L39 2.5V7.5L1 5.75Z" class="g-legend-wedge" /></svg>
    line width = integration strength
  </span>
  <details class="g-legend-more">
    <summary>More</summary>
    <dl>
      <dt>
        <svg width="28" height="10" aria-hidden="true"><path d="M1 5H27" class="g-legend-line unmeasured" stroke-width={unmeasuredWidth} /></svg>
        Dashed line
      </dt>
      <dd>strength not measured: no seam of the report names the dependency, or it names a strength this version does not know</dd>
      <dt>Faint, thin line</dt>
      <dd>accepted earlier: known debt, not a problem</dd>
      <dt>Label</dt>
      <dd>the number of imports behind the dependency; · when only a finding names the two modules</dd>
      <dt>Dashed module</dt>
      <dd>not in the policy</dd>
      {#if pull}
        <dt>Halo</dt>
        <dd>the pair contains evidence of an introduced finding, not proof that each dependency or source site was added</dd>
      {/if}
    </dl>
    <p class="muted">Position does not establish permission: only the policy permits a dependency.</p>
  </details>
</div>
