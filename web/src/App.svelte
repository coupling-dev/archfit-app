<script lang="ts">
  import Header from './Header.svelte';
  import Skeleton from './components/Skeleton.svelte';
  import { ApiError, getJSON, type Session } from './lib/api';
  import { prefetch } from './lib/early';
  import { refused, waiting } from './lib/gate';
  import { parseRoute, serverError } from './lib/route';
  import Admin from './pages/Admin.svelte';
  import DataSharing from './pages/DataSharing.svelte';
  import Landing from './pages/Landing.svelte';
  import Overview from './pages/Overview.svelte';
  import Pull from './pages/Pull.svelte';
  import Repos from './pages/Repos.svelte';
  import Waiting from './pages/Waiting.svelte';

  const route = parseRoute(location.pathname);
  const here = location.pathname + location.search;
  // The server's error page is this shell with the error's sentence; it then shows instead of the route, as does a
  // repository API's 404 (lib/gate).
  const serverSentence = serverError(document.querySelector('meta[name="app-error"]')?.getAttribute('content'));
  const pageError = $derived(serverSentence || $refused);

  // loading, then the signed-in session, signed out (401), or a failure to show.
  let session = $state<Session | null>(null);
  let signedOut = $state(false);
  let failure = $state('');

  // The page's first reads and its chunk start beside the session, so the page waits for the slowest, not their sum.
  if (!serverSentence) prefetch(route);
  // The graph pages carry the drawing libraries and the policy page the editor: each a chunk of its own, loaded on
  // those pages only.
  const graphPage = route.kind === 'graph' || route.kind === 'pull-graph' ? import('./pages/Graph.svelte') : null;
  const policyPage = route.kind === 'policy' ? import('./pages/Policy.svelte') : null;
  for (const chunk of [graphPage, policyPage]) chunk?.catch(() => {}); // a page shown later handles the failure

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
      : $waiting
        ? 'Waiting for approval'
        : route.kind === 'admin'
          ? 'Installations'
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
    !pageError && !$waiting && route.kind !== 'home' && route.kind !== 'admin' && route.kind !== 'unknown' && session
      ? route.fullName
      : '',
  );
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
  {:else if $waiting}
    <Waiting account={$waiting} />
  {:else if failure}
    <p class="flash error" role="alert">{failure}</p>
  {:else if signedOut}
    <Landing {here} />
  {:else if !session}
    <Skeleton rows={4} />
  {:else if route.kind === 'home'}
    <Repos {here} />
  {:else if route.kind === 'admin'}
    <Admin {session} {here} />
  {:else if route.kind === 'repo'}
    <Overview {route} {session} {here} />
  {:else if route.kind === 'data-sharing'}
    <DataSharing {route} {session} {here} />
  {:else if route.kind === 'pull'}
    <Pull {route} {here} />
  {:else if (route.kind === 'graph' || route.kind === 'pull-graph') && graphPage}
    {#await graphPage}
      <Skeleton rows={6} />
    {:then m}
      <m.default {route} {here} />
    {:catch}
      <p class="flash error" role="alert">The graph could not be loaded. Reload the page.</p>
    {/await}
  {:else if route.kind === 'policy' && policyPage}
    {#await policyPage}
      <Skeleton rows={6} />
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
