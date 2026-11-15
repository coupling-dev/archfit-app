<script lang="ts">
  import Map from '../components/map/Map.svelte';
  import Setup from '../components/Setup.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { explainLoad, repoAPI, type ApiError } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { homeHref, readHome, type Home } from '../lib/home';
  import { type Repo } from '../lib/route';

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
</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={5} />
{:else}
  <p class="home-status"><span aria-hidden="true">●</span> {body.status}</p>
{/if}
<div class="page-head"><div><h1>Home</h1><p class="muted">{route.fullName}</p></div></div>
{#if body?.setup}
  <Setup setup={body.setup} repo={route} />
{:else if body}
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
  .home-status { display: flex; align-items: baseline; gap: 8px; overflow-wrap: anywhere; }
  section { margin-top: 16px; min-width: 0; }
  .box-row { overflow-wrap: anywhere; }
  details { margin-top: 8px; }
  pre { max-width: 100%; overflow: auto; }
</style>
