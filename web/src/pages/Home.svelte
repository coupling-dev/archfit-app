<script lang="ts">
  import Status from '../components/Status.svelte';
  import Map from '../components/map/Map.svelte';
  import Setup from '../components/Setup.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { explainLoad, repoAPI, type ApiError } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { approvalNeedsReview, ownerReviewLabel, homeHref, readHome, type Home } from '../lib/home';
  import { repoPage, type Repo } from '../lib/route';

  let { route, here }: { route: Repo; here: string } = $props();
  let body = $state<Home | null>(null);
  let failure = $state('');
  function load() {
  firstJSON<unknown>(repoAPI(route.owner, route.name, 'home')).then(readHome).then(
    (b) => (body = b),
    (e: ApiError) => (failure = explainLoad(e, here)),
  );
  }
  load();
  const reviewHref = $derived(body?.setup && approvalNeedsReview(body.setup) && body.setup.owner_action?.href ? homeHref(route, body.setup.owner_action.href) : undefined);
</script>

<div class="page-head">
  <div class="stack-sm page-identity">
    <h1>Home</h1>
    <p class="lede">{route.fullName}</p>
    {#if failure}<p class="flash error" role="alert">{failure}</p>
    {:else if body}<Status>{body.status}</Status>{/if}
    {#if body}
      <div class="row">
        {#if reviewHref && body.setup?.owner_action}
          <div><a class="btn primary" href={reviewHref}>{ownerReviewLabel(body.setup)}</a><p class="muted">Approval needs your passkey.</p></div>
        {:else if body.setup}
          <a class="btn primary" href="#setup-title">Continue setup</a>
        {:else if body.needs_you.length}
          <a class="btn primary" href="#home-needs">See what needs you</a>
        {/if}
        <a class="btn" class:primary={!body.setup && !body.needs_you.length} href={repoPage(route.fullName, '/policy')}>View policy</a>
      </div>
    {/if}
  </div>
</div>
{#if !body && !failure}<Skeleton rows={5} />{/if}
{#if body?.setup}
  <Setup setup={body.setup} repo={route} showOwnerAction={!reviewHref} />
{/if}
{#if body && (!body.setup || body.setup.state === 'approved' || body.setup.state === 'outside_flow')}
  <section class="box" aria-labelledby="home-needs">
    <div class="box-head"><h2 id="home-needs">Needs you</h2></div>
    {#each body.needs_you as need, i (i)}
      {@const href = homeHref(route, need.href)}
      <div class="box-row">
        {#if href}<a href={href}>{need.text}</a>{:else}<p>{need.text}</p>{/if}
        {#if need.record}<details><summary>Show the record</summary><pre>{need.record}</pre></details>{/if}
      </div>
    {:else}<p class="box-row muted">Nothing needs you.</p>{/each}
  </section>
  {#if body.map}
    <section class="box" aria-labelledby="home-map">
      <div class="box-head"><h2 id="home-map">The map</h2></div>
      <div class="box-body"><Map model={body.map} repo={route.fullName} /></div>
    </section>
  {:else}<p>The map is not available.</p>{/if}
{/if}

<style>
  a { text-decoration: underline; text-underline-offset: 2px; }
  section { margin-top: 16px; min-width: 0; }
  .box-row { overflow-wrap: anywhere; }
  details { margin-top: 8px; }
  pre { max-width: 100%; overflow: auto; }
</style>
