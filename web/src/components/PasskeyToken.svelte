<script lang="ts">
  import { explain, postJSON, type ApiError, type Session } from '../lib/api';
  import type { RepoAPI } from '../lib/passkey';
  import { repoPage } from '../lib/route';

  // The first passkey needs a token from an Archfit operator. This says so where an owner is first sent to approve,
  // and starts the request there. A request from the same session returns the same attempt, so the Passkeys page
  // finishes it with the token the operator issued for this one.
  let { api, session, repo, returnTo }: { api: RepoAPI; session: Session; repo: string; returnTo: string } = $props();
  let attempt = $state('');
  let installation = $state(0);
  let busy = $state(false);
  let error = $state('');

  async function request() {
    busy = true; error = '';
    try {
      const v = await postJSON<{ attempt: string; installation: number }>(api('passkeys/confirmation'), session.csrf, {});
      attempt = v.attempt; installation = v.installation;
    } catch (e) { error = explain(e as ApiError, window.location.pathname); }
    finally { busy = false; }
  }
</script>

<div class="token stack-sm">
  <p>Approving needs a passkey. Your first passkey needs a one-time token from an Archfit operator: request it now.</p>
  {#if !attempt}
    <button class="btn" type="button" disabled={busy} onclick={request}>{busy ? 'Requesting…' : 'Request the token'}</button>
  {:else}
    <p role="status">Requested. Send the operator this attempt through the private channel you used to join the beta: installation {installation}, attempt <code>{attempt}</code>.</p>
  {/if}
  <p><a href={repoPage(repo, '/passkeys?return=' + encodeURIComponent(returnTo))}>Register your passkey</a> when the token arrives. It is valid for 10 minutes.</p>
  {#if error}<p class="flash error" role="alert">{error}</p>{/if}
</div>

<style>
  .token p { margin: 0; }
  .token > .btn { align-self: flex-start; }
</style>
