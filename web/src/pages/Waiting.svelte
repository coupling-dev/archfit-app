<script lang="ts">
  import Pitch from '../components/Pitch.svelte';
  import { guideURL } from '../lib/route';

  // Waiting is any repository page of an account no operator approved: the gate does nothing for it, so the page says
  // why and what Archfit is, instead of an empty repository.
  let { account }: { account: string } = $props();
</script>

<Pitch>
  {#snippet top()}
    <span class="pill"><span class="pulse" aria-hidden="true"></span>Waiting for approval · {account}</span>
  {/snippet}
  {#snippet status()}
    <div class="statusbox" role="status">
      <span class="big-ic" aria-hidden="true">⏳</span>
      <div>
        <strong>Your installation on {account} is waiting for approval.</strong>
        <p>
          Archfit is an experiment, and we approve installations by hand. Until then nothing runs and nothing is posted
          on your pull requests. Nothing to do now: this page changes once it is approved.
        </p>
        <p><a href="{guideURL}#who-the-app-serves" rel="noreferrer">How approval works</a></p>
      </div>
    </div>
  {/snippet}
</Pitch>
