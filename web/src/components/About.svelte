<script lang="ts">
  import type { AppBuild } from '../lib/api';
  let { build }: { build?: AppBuild } = $props();
  const missing = 'Not recorded';
</script>

<details class="app-about">
  <summary>About this App · {build?.version || build?.source_revision?.slice(0, 7) || 'build not recorded'}</summary>
  <div class="box-body stack-sm">
    <p>Serving App build, not the Engine or Action that produced a PR analysis.</p>
    <dl class="analysis-details">
      <dt>Release version</dt><dd>{build?.version || missing}</dd>
      <dt>Source revision</dt><dd><code>{build?.source_revision || missing}</code>{#if build?.modified} (modified source){/if}</dd>
      <dt>Built (UTC)</dt><dd>{build?.built_at || missing}</dd>
      <dt>Runtime revision</dt><dd>{build?.runtime_revision || missing}</dd>
      <dt>Image digest</dt><dd>Not recorded in the binary. Operators: see the release build.json.</dd>
      <dt>Deployment time / environment</dt><dd>{missing}</dd>
    </dl>
    <p>Build time is not deployment time. Missing metadata is never taken from a current checkout.</p>
  </div>
</details>
