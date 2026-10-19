<script lang="ts">
  import Header from './Header.svelte';
  import { ApiError, getJSON, type Session } from './lib/api';
  import { parseRoute } from './lib/route';
  import Landing from './pages/Landing.svelte';
  import Overview from './pages/Overview.svelte';
  import Repos from './pages/Repos.svelte';

  const route = parseRoute(location.pathname);
  const here = location.pathname + location.search;

  // loading, then the signed-in session, signed out (401), or a failure to show.
  let session = $state<Session | null>(null);
  let signedOut = $state(false);
  let failure = $state('');

  getJSON<Session>('/api/session').then(
    (s) => (session = s),
    (e: ApiError) => {
      if (e.status === 401) signedOut = true;
      else failure = e.message;
    },
  );

  const title = $derived(route.kind === 'repo' ? route.fullName : signedOut ? 'Sign in' : 'Repositories');
  $effect(() => {
    document.title = `${title} · Archfit`;
  });
</script>

<Header {session} repo={route.kind === 'repo' && session ? route.fullName : ''} />
<main class="page" id="main">
  {#if failure}
    <p class="flash error" role="alert">{failure}</p>
  {:else if signedOut}
    <Landing {here} />
  {:else if !session}
    <p class="muted" aria-live="polite">Loading…</p>
  {:else if route.kind === 'home'}
    <Repos />
  {:else if route.kind === 'repo'}
    <Overview {route} {session} {here} />
  {:else}
    <div class="blankslate">
      <h3>Not found</h3>
      <p>This page does not exist. <a href="/">See your repositories.</a></p>
    </div>
  {/if}
</main>
