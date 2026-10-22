<script lang="ts">
  import Pitch from '../components/Pitch.svelte';
  import { signInHref } from '../lib/route';

  let { here }: { here: string } = $props();
</script>

<Pitch>
  {#snippet top()}
    <a class="btn primary" href={signInHref(here)}>Sign in with GitHub</a>
  {/snippet}
</Pitch>
