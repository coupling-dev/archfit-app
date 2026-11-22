<script lang="ts">
  import RepositoryMap from '../components/map/RepositoryMap.svelte';
  import Setup from '../components/Setup.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import Icon from '../components/ui/Icon.svelte';
  import PageHead from '../components/ui/PageHead.svelte';
  import StoryRow from '../components/ui/StoryRow.svelte';
  import { storySteps } from '../lib/request';
  import StatusLabel from '../components/ui/StatusLabel.svelte';
  import { explainLoad, repoAPI, type ApiError } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { homeAction, homeHref, homeStateLabel, isExternalHref, needIcon, readHome, type Home } from '../lib/home';
  import { repoPage, type Repo } from '../lib/route';
  import { waitingFor } from '../lib/owner-actions';

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
  const action = $derived(body ? homeAction(body, route) : undefined);
  // A repository without a policy on the default branch has nothing to view yet.
  const hasPolicy = $derived(!!body && (!body.setup || body.setup.policy));
  const showMap = $derived(!!body && (!body.setup || body.setup.state === 'approved' || body.setup.state === 'outside_flow'));
</script>

<PageHead title={body?.setup?.setup_pull ? `Set up Archfit for ${route.fullName}` : route.fullName}>
  {#snippet status()}
    {#if failure}<p class="flash error" role="alert">{failure}</p>
    {:else if body?.setup?.setup_pull}<p>{body.status}</p>
    {:else if body}<div class="status-line"><StatusLabel label={homeStateLabel(body.state)} /><p>{body.status}</p></div>{/if}
  {/snippet}
  {#snippet actions()}
    <div class="actions">
    {#if action}
      <a class="btn primary" href={action.href} aria-describedby={action.needsPasskey ? 'home-review-note' : undefined}>
        {action.label}{#if action.external}<Icon name="link-external" size={14} /><span class="sr-only"> (opens GitHub)</span>{/if}
      </a>
    {/if}
    {#if hasPolicy}<a class="btn" href={repoPage(route.fullName, '/policy')}>View policy</a>{/if}
    </div>
  {/snippet}
  {#if action?.needsPasskey}<p id="home-review-note" class="muted">Approval needs your passkey.</p>{/if}
</PageHead>
{#if !body && !failure}<Skeleton rows={5} />{/if}
{#if body?.needs_you.length}
  <section class="box" aria-labelledby="home-needs">
    <div class="box-head"><h2 class="title-box" id="home-needs">Needs you</h2></div>
    {#each body.needs_you as need, i (i)}
      {@const href = homeHref(route, need.href)}
      <div class="box-row need">
        <span class="need-icon"><Icon name={needIcon(need.kind)} /></span>
        <div class="need-body">
          {#if href}
            <a {href}>{need.text}{#if isExternalHref(href)}<Icon name="link-external" size={12} /><span class="sr-only"> (opens GitHub)</span>{/if}</a>
          {:else}<p>{need.text}</p>{/if}
          {#if need.kind === 'request' && need.request}
            <StoryRow steps={storySteps({ state: 'open', answer: undefined })} ask={`#${need.request}`} />
          {/if}
          {#if waitingFor(need.since)}<p class="muted">Waiting {waitingFor(need.since)}</p>{/if}
          {#if need.record}<details><summary>Show the record</summary><pre>{need.record}</pre></details>{/if}
        </div>
      </div>
    {/each}
  </section>
{/if}
{#if body?.setup?.setup_pull}
  <p>Setup pull request #{body.setup.setup_pull}: open. No merge is stopped before you merge it.</p>
{:else if body?.setup}
  <Setup setup={body.setup} repo={route} showOwnerAction={!action?.needsPasskey} primaryHref={action?.href} />
{/if}
{#if body && showMap}
  {#if body.map}
    <section class="box" aria-labelledby="home-map">
      <div class="box-head"><h2 class="title-box" id="home-map">The map</h2></div>
      <div class="box-body"><RepositoryMap model={body.map} repo={route.fullName} /></div>
    </section>
  {:else}<p>The map is not available.</p>{/if}
{/if}

<style>
  section { min-width: 0; }
  .status-line { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 10px; min-width: 0; }
  .status-line p { margin: 0; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; }
  /* PageHead keeps its action row at max-content (flex: none), which is wider than a phone with two buttons.
     Drop this rule when PageHead lets the row shrink. */
  :global(.page-head .page-actions) { max-width: 100%; }
  .box-row { overflow-wrap: anywhere; }
  .need { display: flex; align-items: flex-start; gap: 10px; }
  .need-icon { display: inline-flex; padding-top: 2px; color: var(--fg-muted); }
  .need-body { min-width: 0; }
  .need-body :global(.icon) { margin-left: 4px; vertical-align: -2px; }
  .need-body p { margin: 0; }
  details { margin-top: 8px; }
  pre { max-width: 100%; overflow: auto; }
</style>
