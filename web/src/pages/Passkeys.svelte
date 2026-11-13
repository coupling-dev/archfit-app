<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import { explain, explainLoad, getJSON, repoAPI, type ApiError, type Passkey, type Passkeys, type Session } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { addPasskey, removePasskey } from '../lib/passkey';
  import { type Repo } from '../lib/route';
  import { passkeyFailure } from '../lib/webauthn';

  // The user's own passkeys in this App installation (design 9.2): register, list, remove. A second passkey and every
  // removal are confirmed with a passkey the user already has.
  let { route, session, here }: { route: Repo; session: Session; here: string } = $props();

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

  async function run(step: () => Promise<{ notice?: string }>) {
    busy = true;
    notice = error = '';
    try {
      notice = (await step()).notice ?? '';
      load();
    } catch (e) {
      error = passkeyFailure(e) ?? explain(e as ApiError, here);
    } finally {
      busy = false;
      removing = '';
    }
  }

  const add = () => run(() => addPasskey(api, session.csrf, (list?.length ?? 0) > 0));
  const remove = (id: string) => run(() => removePasskey(api, session.csrf, id));
  const day = (iso: string) => iso.slice(0, 10);
</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !list}
  <Skeleton rows={2} box={false} />
  <Skeleton rows={3} />
{:else}
  <div class="page-head">
    <div class="stack-sm">
      <h1>Passkeys</h1>
      <p class="lede">
        {list.length
          ? `You have ${list.length} ${list.length === 1 ? 'passkey' : 'passkeys'} in this App installation.`
          : 'You have no passkey in this App installation yet.'}
      </p>
    </div>
  </div>

  <section class="box" aria-labelledby="your-passkeys">
    <div class="box-head">
      <h2 id="your-passkeys">Your passkeys <span class="counter">{list.length}</span></h2>
      <button class="btn sm primary" type="button" disabled={busy} onclick={add}>Add a passkey</button>
    </div>
    <div class="box-body stack-sm">
      <p class="small">
        An owner's answer that loosens the policy needs a passkey with user verification: a fingerprint, a face, or a PIN.
        Only an owner named in the policy can register one, or a repository administrator before the first policy.
        {#if list.length}A new passkey is confirmed with one you have.{/if}
        A new passkey, and the removal of your last one, open an issue for the owners.
      </p>
    </div>
    {#if list.length}
      <ul class="plain" aria-label="Your passkeys">
        {#each list as p, i (p.id)}
          <li class="box-row row-between">
            <span>
              Passkey {i + 1} <code class="muted">{p.id.slice(0, 8)}</code> · added {day(p.created)} ·
              {p.last_used ? `last used ${day(p.last_used)}` : 'not used yet'}
            </span>
            {#if removing === p.id}
              <span class="btn-group">
                <button class="btn sm" type="button" disabled={busy} onclick={() => remove(p.id)}>Confirm removal</button>
                <button class="btn sm" type="button" disabled={busy} onclick={() => (removing = '')}>Keep it</button>
              </span>
            {:else}
              <button class="btn sm" type="button" disabled={busy} onclick={() => (removing = p.id)}>Remove</button>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
    {#if busy}<p class="box-foot" role="status">Waiting for your passkey…</p>{/if}
    {#if notice}<p class="flash success" role="status">{notice}</p>{/if}
    {#if error}<p class="flash error" role="alert">{error}</p>{/if}
  </section>
{/if}
