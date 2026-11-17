<script lang="ts">
  import Header from './Header.svelte';
  import About from './components/About.svelte';
  import Skeleton from './components/Skeleton.svelte';
  import { ApiError, getJSON, type Session } from './lib/api';
  import { prefetch } from './lib/early';
  import { refused, waiting } from './lib/gate';
  import { guideURL, parseRoute, serverError } from './lib/route';
  import Admin from './pages/Admin.svelte';
  import Landing from './pages/Landing.svelte';
  import Home from './pages/Home.svelte';
  import Passkeys from './pages/Passkeys.svelte';
  import Pull from './pages/Pull.svelte';
  import Policy from './pages/Policy.svelte';
  import Repos from './pages/Repos.svelte';
  import Request from './pages/Request.svelte';
  import Waiting from './pages/Waiting.svelte';
  import Owner from './pages/Owner.svelte';

  const route = parseRoute(location.pathname, location.search);
  const here = location.pathname + location.search;
  // The server's error page is this shell with the error's sentence; it then shows instead of the route, as does a
  // repository API's 404 (lib/gate).
  const serverSentence = serverError(document.querySelector('meta[name="app-error"]')?.getAttribute('content'));
  const pageError = $derived(serverSentence || $refused);
  const notFound = $derived(!!$refused || serverSentence === 'Not found.' || route.kind === 'unknown');

  // loading, then the signed-in session, signed out (401), or a failure to show.
  let session = $state<Session | null>(null);
  let signedOut = $state(false);
  let failure = $state('');

  // The page's first reads and its chunk start beside the session, so the page waits for the slowest, not their sum.
  if (!serverSentence) prefetch(route);
  getJSON<Session>('/api/session').then(
    (s) => (session = s),
    (e: ApiError) => {
      if (e.status === 401) signedOut = true;
      else failure = e.message;
    },
  );

  const title = $derived(
    notFound
      ? 'Page not available'
      : pageError ? 'Page could not be loaded'
      : $waiting
        ? 'Waiting for approval'
        : route.kind === 'admin'
          ? 'Installations'
          : route.kind === 'pull'
        ? `${route.fullName} #${route.number}`
        : route.kind === 'owner'
          ? `${route.fullName} owner action`
        : route.kind === 'request'
          ? `${route.fullName} request #${route.number}`
        : route.kind === 'repo'
          ? `${route.fullName}`
          : route.kind === 'passkeys' || route.kind === 'policy'
            ? `${route.fullName} ${route.kind}`
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

<a class="skip-link" href="#main">Skip to main content</a>
<Header
  {session}
  {repo}
  tab={route.kind === 'repo'
    ? 'home'
    : route.kind === 'policy'
      ? route.kind
      : ''}
  pull={route.kind === 'pull' ? route.number : 0}
/>
<main class="page" id="main" tabindex="-1">
  {#if notFound}
    <div class="blankslate">
      <h1>Page not available</h1>
      <p>This page does not exist or is not available to your account.</p>
      <a class="btn" href="/">Repositories</a>
    </div>
  {:else if pageError}
    <div class="blankslate">
      <h1>Page could not be loaded</h1>
      <p>{pageError}</p>
      <a class="btn" href="/">Repositories</a>
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
    <Home {route} {here} />
  {:else if route.kind === 'policy'}
    <Policy {route} {here} {session} />
  {:else if route.kind === 'owner'}
    <Owner {route} {session} {here} />
  {:else if route.kind === 'request'}
    <Request {route} {session} {here} />
  {:else if route.kind === 'passkeys'}
    <Passkeys {route} {session} {here} />
  {:else if route.kind === 'pull'}
    <Pull {route} {here} />
  {/if}
</main>
<footer class="app-footer">
  <a href={guideURL}>Documentation</a>
  <span class="footer-separator" aria-hidden="true">·</span>
  <a href="/licenses.txt">Third-party licenses</a>
  {#if session}<About build={session.build} />{/if}
</footer>
