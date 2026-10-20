<script lang="ts">
  import Header from './Header.svelte';
  import { ApiError, getJSON, type Session } from './lib/api';
  import { parseRoute, serverError } from './lib/route';
  import DataSharing from './pages/DataSharing.svelte';
  import Landing from './pages/Landing.svelte';
  import Overview from './pages/Overview.svelte';
  import Pull from './pages/Pull.svelte';
  import Repos from './pages/Repos.svelte';

  const route = parseRoute(location.pathname);
  const here = location.pathname + location.search;
  // The server's error page is this shell with the error's sentence; it then shows instead of the route.
  const pageError = serverError(document.querySelector('meta[name="app-error"]')?.getAttribute('content'));

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

  const title = $derived(
    pageError
      ? 'Error'
      : route.kind === 'pull' || route.kind === 'pull-graph'
        ? `${route.fullName} #${route.number}${route.kind === 'pull-graph' ? ' graph' : ''}`
        : route.kind === 'repo' || route.kind === 'graph'
          ? `${route.fullName}${route.kind === 'graph' ? ' architecture' : ''}`
          : route.kind === 'data-sharing' || route.kind === 'policy'
            ? `${route.fullName} ${route.kind === 'policy' ? 'policy' : 'data sharing'}`
            : signedOut
              ? 'Sign in'
              : 'Repositories',
  );
  const repo = $derived(
    !pageError && route.kind !== 'home' && route.kind !== 'unknown' && session ? route.fullName : '',
  );
  // The graph pages carry the drawing libraries and the policy page the editor: each a chunk of its own, loaded on
  // those pages only.
  const graphPage = () => import('./pages/Graph.svelte');
  const policyPage = () => import('./pages/Policy.svelte');
  $effect(() => {
    document.title = `${title} · Archfit`;
  });
</script>

<Header
  {session}
  {repo}
  tab={route.kind === 'repo' ? 'overview' : route.kind === 'graph' || route.kind === 'policy' ? route.kind : ''}
  pull={route.kind === 'pull' || route.kind === 'pull-graph' ? route.number : 0}
/>
<main class="page" id="main">
  {#if pageError}
    <div class="blankslate">
      <h1>This page cannot be shown</h1>
      <p>{pageError} <a href="/">See your repositories.</a></p>
    </div>
  {:else if failure}
    <p class="flash error" role="alert">{failure}</p>
  {:else if signedOut}
    <Landing {here} />
  {:else if !session}
    <p class="muted" aria-live="polite">Loading…</p>
  {:else if route.kind === 'home'}
    <Repos {here} />
  {:else if route.kind === 'repo'}
    <Overview {route} {session} {here} />
  {:else if route.kind === 'data-sharing'}
    <DataSharing {route} {session} {here} />
  {:else if route.kind === 'pull'}
    <Pull {route} {here} />
  {:else if route.kind === 'graph' || route.kind === 'pull-graph'}
    {#await graphPage()}
      <p class="muted" aria-live="polite">Loading…</p>
    {:then m}
      <m.default {route} {here} />
    {:catch}
      <p class="flash error" role="alert">The graph could not be loaded. Reload the page.</p>
    {/await}
  {:else if route.kind === 'policy'}
    {#await policyPage()}
      <p class="muted" aria-live="polite">Loading…</p>
    {:then m}
      <m.default {route} {session} {here} />
    {:catch}
      <p class="flash error" role="alert">The editor could not be loaded. Reload the page.</p>
    {/await}
  {:else}
    <div class="blankslate">
      <h3>Not found</h3>
      <p>This page does not exist. <a href="/">See your repositories.</a></p>
    </div>
  {/if}
</main>
<footer class="app-footer">
  <a href="/licenses.txt">Third-party licences</a>
</footer>
