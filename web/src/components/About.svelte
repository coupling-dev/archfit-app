<script lang="ts">
  import type { AppBuild } from '../lib/api';
  let { build }: { build?: AppBuild } = $props();

  // A row without a value is left out: an empty row says nothing the reader can use.
  const rows = $derived(
    [
      { label: 'Version', value: build?.version },
      { label: 'Source revision', value: build?.source_revision, modified: build?.modified },
      { label: 'Built (UTC)', value: build?.built_at },
      { label: 'Runtime revision', value: build?.runtime_revision },
    ].filter((row) => row.value),
  );
</script>

<details class="app-about">
  <summary>About this App · {build?.version || build?.source_revision?.slice(0, 7) || 'build not recorded'}</summary>
  <div class="box-body stack-sm">
    <p>This is the build of the App. The engine and Action versions are on each pull request.</p>
    {#if rows.length}
      <dl class="analysis-details">
        {#each rows as row (row.label)}
          <dt>{row.label}</dt>
          <dd>{#if row.label === 'Source revision'}<code>{row.value}</code>{#if row.modified} (modified source){/if}{:else}{row.value}{/if}</dd>
        {/each}
      </dl>
    {/if}
    <p>Built is when the build ran, not when it was deployed.</p>
  </div>
</details>
