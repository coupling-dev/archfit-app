<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import PageHead from '../components/ui/PageHead.svelte';
  import { explain, explainLoad, getJSON, repoAPI, type ApiError, type Passkey, type Passkeys, type Session } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { addPasskey, removePasskey } from '../lib/passkey';
  import { passkeyReturn } from '../lib/passkey-return';
  import { type Repo } from '../lib/route';
  import { passkeyFailure } from '../lib/webauthn';

  // The user's own passkeys in this App installation (design 9.2): register, list, remove. A second passkey and every
  // removal are confirmed with a passkey the user already has.
  let { route, session, here }: { route: Repo; session: Session; here: string } = $props();

  const returnTo = $derived(passkeyReturn(route.fullName, new URLSearchParams(window.location.search).get('return')));
  const api = (suffix: string) => repoAPI(route.owner, route.name, suffix);

  let list = $state<Passkey[] | null>(null);
  let failure = $state('');
  let busy = $state(false);
  let removing = $state(''); // the passkey whose removal waits for a second click
  let notice = $state('');
  let error = $state('');

  function load(first = false) {
    const path = api('passkeys');
    (first ? firstJSON<Passkeys>(path) : getJSON<Passkeys>(path)).then(
      (b) => (list = b.passkeys),
      (e: ApiError) => (failure = explainLoad(e, here)),
    );
  }
  load(true);

  async function run(step: () => Promise<{ notice?: string }>, returnAfter = false) {
    busy = true;
    notice = error = '';
    try {
      notice = (await step()).notice ?? '';
      if (returnAfter && returnTo) { location.assign(returnTo); return; }
      load();
    } catch (e) {
      error = passkeyFailure(e) ?? explain(e as ApiError, here);
    } finally {
      busy = false;
      removing = '';
    }
  }

  const add = () => run(() => addPasskey(api, session.csrf, (list?.length ?? 0) > 0), true);
  const remove = (id: string) => run(() => removePasskey(api, session.csrf, id));
  const day = (iso: string) => iso.slice(0, 10);
</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !list}
  <Skeleton rows={2} box={false} />
  <Skeleton rows={3} />
{:else}
  <PageHead title="Passkeys" repo={route.fullName}>
    <p>Your passkeys work in every repository of this account where the policy names you as owner.</p>
    {#if returnTo}<a href={returnTo}>Back to {returnTo.includes('/requests/') ? 'request' : returnTo.includes('/policy') ? 'policy' : 'approval'}</a>{/if}
  </PageHead>

  <section class="box" aria-labelledby="your-passkeys">
    <div class="box-head">
      <h2 class="title-box" id="your-passkeys">Your passkeys <span class="counter">{list.length}</span></h2>
      <div class="stack-sm passkey-add">
        <button class="btn sm primary" type="button" disabled={busy} onclick={add}>Add a passkey</button>
        <p class="small muted">{list.length ? 'A passkey after your first is confirmed with one you have.' : 'Only an owner named in the policy can add one.'}</p>
      </div>
    </div>
    {#if list.length}
      <ul class="plain" aria-label="Your passkeys">
        {#each list as p (p.id)}
          <li class="box-row row-between">
            <span class="stack-sm">
              <strong>Added {day(p.created)}</strong>
              <span class="small muted">{p.last_used ? `Last used ${day(p.last_used)}` : 'Not used yet'} · ID <code>{p.id.slice(0, 8)}</code></span>
            </span>
            <div class="stack-sm passkey-remove">
              {#if removing === p.id}
                <span class="btn-group">
                  <button class="btn sm" type="button" aria-describedby={list.length === 1 ? 'last-key' : undefined} disabled={busy} onclick={() => remove(p.id)}>Confirm removal</button>
                  <button class="btn sm" type="button" disabled={busy} onclick={() => (removing = '')}>Keep it</button>
                </span>
              {:else}
                <button class="btn sm" type="button" aria-label={`Remove the passkey added ${day(p.created)} (ID ${p.id.slice(0, 8)})`}
                  aria-describedby={list.length === 1 ? 'last-key' : undefined} disabled={busy} onclick={() => (removing = p.id)}>Remove</button>
              {/if}
              {#if list.length === 1}<p class="small" id="last-key">Removing your last passkey opens an issue for the owners. You need a passkey to confirm an approval.</p>{/if}
            </div>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="box-body muted">No passkeys yet. Add one to approve policy changes.</p>
    {/if}
    {#if busy}<p class="box-foot" role="status">Waiting for your passkey…</p>{/if}
    {#if notice}<p class="flash success" role="status">{notice}</p>{/if}
    {#if error}<p class="flash error" role="alert">{error}</p>{/if}
  </section>
{/if}
