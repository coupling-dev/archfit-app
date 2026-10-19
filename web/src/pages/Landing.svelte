<script lang="ts">
  import { signInHref } from '../lib/route';

  let { here }: { here: string } = $props();
</script>

<section class="blankslate">
  <h1>Archfit</h1>
  <p>Architecture checks for your pull requests, with the policy reviewed like code.</p>
  <a class="btn primary" href={signInHref(here)}>Sign in with GitHub</a>
</section>
