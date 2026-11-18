<script lang="ts">
  import { get } from 'svelte/store';
  import Header from './Header.svelte';
  import About from './components/About.svelte';
  import Skeleton from './components/Skeleton.svelte';
  import StatusLabel from './components/ui/StatusLabel.svelte';
  import { ApiError, getJSON, type Repos as RepoList, type Session } from './lib/api';
  import { hint, prefetch } from './lib/early';
  import { forget, signedIn } from './lib/memory';
  import { refused, visit, waiting } from './lib/gate';
  import { missingSentence } from './lib/notfound';
  import { linkURL } from './lib/nav';
  import { guideURL, parseRoute, repoPage, serverError } from './lib/route';
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

  // A link inside the App moves this location without a document load; the page is keyed on it below.
  let at = $state(new URL(location.href));
  const route = $derived(parseRoute(at.pathname, at.search));
  const here = $derived(at.pathname + at.search);
  // The server's error page is this shell with the error's sentence; it then shows instead of the route, as does a
  // repository API's 404 (lib/gate).
  const errorAtStart = serverError(document.querySelector('meta[name="app-error"]')?.getAttribute('content'));
  let serverSentence = $state(errorAtStart);
  const pageError = $derived(serverSentence || $refused);
  const notFound = $derived(!!$refused || serverSentence === 'Not found.' || route.kind === 'unknown');
  // The repositories the signed-in user can see. A missing page names its repository only when it is in this list:
  // a 404 for a repository the user cannot see looks the same, and must not reveal that it exists.
  let visible = $state<string[] | null>(null);
  let lookedUp = false;
  // GitHub names are case-insensitive, so the URL's spelling is matched against the canonical one.
  const knownRepo = $derived(
    notFound && 'fullName' in route && visible?.some((name) => name.toLowerCase() === route.fullName.toLowerCase()) ? route.fullName : '',
  );
  $effect(() => {
    if (!notFound || !('fullName' in route) || lookedUp) return;
    lookedUp = true;
    // The overview is the one repository list the App has; a 404 is rare, so its cost is paid only here.
    getJSON<RepoList>('/api/repos').then(
      (b) => (visible = b.repos.map((r) => r.full_name)),
      () => (visible = []),
    );
  });
  const repoList = $derived(at.pathname === '/repos');

  // loading, then the signed-in session, signed out (401), or a failure to show.
  let session = $state<Session | null>(null);
  let signedOut = $state(false);
  let failure = $state('');

  // The page's first reads and its chunk start beside the session, so the page waits for the slowest, not their sum.
  if (!errorAtStart) prefetch(parseRoute(location.pathname, location.search));
  getJSON<Session>('/api/session').then(
    (s) => {
      signedIn(s.login);
      session = s;
      if (s.operator) countWaiting(s);
    },
    (e: ApiError) => {
      if (e.status === 401) {
        signedIn(null);
        signedOut = true;
      } else failure = e.message;
    },
  );
  // A link inside the App shows its page without a document load: the session and the App's chrome stay, the
  // repository's refusal and the waiting account of the previous page do not, and back and forward walk the history.
  // Each visit remounts the page, so a "Try again" to the same address reads the API again.
  // A page the user was on before shows the last answer at once, and says so until the current one is in: a different
  // or refused answer then replaces it (the page is mounted again), so a view that could not be refreshed never stays.
  let refreshing = $state(false);
  function show(url: URL) {
    refused.set('');
    waiting.set('');
    serverSentence = '';
    at = url;
    visit.update((n) => n + 1);
    const mine = get(visit);
    const current = prefetch(parseRoute(url.pathname, url.search), undefined, true);
    refreshing = !!current;
    current?.then((changed) => {
      if (get(visit) !== mine) return;
      refreshing = false;
      if (changed) visit.update((n) => n + 1);
    });
  }
  function onClick(event: MouseEvent) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const url = linkURL(event.target, new URL(location.href));
    clearTimeout(resting);
    // A failed session read is retried by the browser's own load, so a link is left to it.
    if (!url || failure) return;
    event.preventDefault();
    const same = url.href === location.href;
    history[same ? 'replaceState' : 'pushState'](null, '', url);
    if (!same) window.scrollTo(0, 0);
    show(url);
  }
  // A pointer that rests on a link, or a link that takes the focus, starts the read of its page, so the click finds it
  // under way. The pointer waits a moment, so one passing over a list of links starts nothing.
  const restMs = 120;
  let resting: ReturnType<typeof setTimeout> | undefined;
  function rest(target: EventTarget | null) {
    clearTimeout(resting);
    resting = setTimeout(() => {
      const url = linkURL(target, new URL(location.href));
      if (url && !failure && url.href !== location.href) hint(parseRoute(url.pathname, url.search));
    }, restMs);
  }
  // Signing out ends the session: nothing read under it is shown again, even before the browser leaves the page.
  function onSubmit(event: SubmitEvent) {
    if (event.target instanceof HTMLFormElement && new URL(event.target.action, location.href).pathname === '/auth/logout') {
      signedIn(null);
      forget();
    }
  }
  // A fragment (#raw) also fires popstate; the page is already shown, so it stays mounted and keeps its state.
  function onPop() {
    const url = new URL(location.href);
    if (url.pathname === at.pathname && url.search === at.search) return;
    show(url);
  }
  // The operator's count fills the header once the session shows; -1 when it cannot be read.
  function countWaiting(s: Session) {
    getJSON<{ waiting: number }>('/api/admin/waiting').then(
      (b) => (session = { ...s, waiting: b.waiting }),
      () => (session = { ...s, waiting: -1 }),
    );
  }

  const title = $derived(
    notFound
      ? 'Page not available'
      : pageError ? 'Page could not be loaded'
      : $waiting
        ? 'Waiting to be admitted'
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
    knownRepo ||
      (!pageError && !$waiting && route.kind !== 'home' && route.kind !== 'admin' && route.kind !== 'unknown' && session
        ? route.fullName
        : ''),
  );
  $effect(() => {
    document.title = `${title} · Archfit`;
  });
</script>

<svelte:window
  onclick={onClick}
  onpopstate={onPop}
  onmouseover={(event) => rest(event.target)}
  onmouseout={() => clearTimeout(resting)}
  onfocusin={(event) => rest(event.target)}
  onsubmit={onSubmit}
/>
<a class="skip-link" href="#main">Skip to main content</a>
<Header
  {session}
  {repo}
  tab={route.kind === 'repo'
    ? 'home'
    : route.kind === 'policy' || route.kind === 'passkeys'
      ? route.kind
      : ''}
  pull={route.kind === 'pull' ? route.number : 0}
/>
<main class="page" id="main" tabindex="-1">
  {#key $visit}
  {#if notFound}
    <div class="blankslate">
      <h1>Page not available</h1>
      {#if knownRepo}
        <p>{missingSentence(route)}</p>
        <a class="btn" href={repoPage(knownRepo)}>Back to {knownRepo}</a>
      {:else if repoList}
        <p>Repositories are listed on the home page.</p>
        <a class="btn" href="/">Repositories</a>
      {:else}
        <p>This page does not exist or is not available to your account.</p>
        <a class="btn" href="/">Repositories</a>
      {/if}
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
  {/key}
</main>
{#if refreshing}
  <p class="refreshing" role="status"><StatusLabel label="waiting" /><span>Showing your last answer. Reading the current one.</span></p>
{/if}
<footer class="app-footer">
  <a href={guideURL}>Documentation</a>
  <span class="footer-separator" aria-hidden="true">·</span>
  <a href="/licenses.txt">Third-party licenses</a>
  {#if session}<About build={session.build} />{/if}
</footer>

<style>
  /* Fixed, so the page does not move when it shows or goes. */
  .refreshing {
    position: fixed; z-index: 10; inset-block-end: 16px; inset-inline: 16px; margin: 0 auto; width: fit-content; max-width: calc(100% - 32px);
    display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; padding: 6px 12px;
    background: var(--bg); color: var(--fg-muted); border: 1px solid var(--border); border-radius: var(--radius); box-shadow: var(--shadow-sm);
  }
</style>
