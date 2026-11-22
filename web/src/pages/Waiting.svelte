<script lang="ts">
  import Pitch from '../components/Pitch.svelte';
  import Banner from '../components/ui/Banner.svelte';
  import { guideURL } from '../lib/route';


  // Waiting is any repository page of an account no operator admitted: the gate does nothing for it, so the page says
  // why and what Archfit is, instead of an empty repository.
  let { account }: { account: string } = $props();
</script>

<Pitch>
  {#snippet top()}
    <span class="pill"><span class="pulse" aria-hidden="true"></span>Waiting to be admitted · {account}</span>
  {/snippet}
  {#snippet status()}
    <Banner tone="attention" role="status">
      <p>Your installation on {account} is waiting to be admitted.</p>
      <p>Archfit is in a closed beta. Ask for access: <a href="https://github.com/coupling-dev/archfit/issues/new?template=beta-access.yml">https://github.com/coupling-dev/archfit/issues/new?template=beta-access.yml</a> Until then nothing runs and nothing is posted on your pull requests. Nothing to do now: this page changes once it is admitted.</p>
      <p><a href="{guideURL}#who-the-app-serves" rel="noreferrer">How admission works</a></p>
    </Banner>
  {/snippet}
</Pitch>
