<script lang="ts">
 import { postJSON, explain, type ApiError, type Session } from '../lib/api';
 import { addPasskey, type RepoAPI } from '../lib/passkey';
 import { passkeyFailure } from '../lib/webauthn';
 let { api, session, done }: {api: RepoAPI; session: Session; done: () => void} = $props();
 let attempt = $state('');
 let installation = $state(0);
 let token = $state('');
 let busy = $state(false);
 let error = $state('');
 let confirmed = $state(false);
 async function request() {
  busy=true;error='';
  try { const v=await postJSON<{attempt:string;installation:number}>(api('passkeys/confirmation'),session.csrf,{});attempt=v.attempt;installation=v.installation; }
  catch(e){error=explain(e as ApiError,window.location.pathname);}
  finally{busy=false;}
 }
 async function register() {
  busy=true;error='';
  const submitted=token; token='';
  try { await addPasskey(api,session.csrf,false,{attempt,confirmation:submitted}); confirmed=true;done(); }
  catch(e){const detail=passkeyFailure(e) ? 'The browser did not finish registration.' : explain(e as ApiError,window.location.pathname);
   attempt='';installation=0;
   error=detail+' Confirmation may have been consumed. Request confirmation again and ask the operator to issue a token for the new attempt.';}
  finally{busy=false;}
 }
</script>
<section class="box" aria-labelledby="confirm-owner">
 <div class="box-body stack">
 <h2 id="confirm-owner">Confirm it is you</h2>
 <p>Before you register your first passkey, ask an Archfit operator to confirm that you are <strong>{session.login}</strong>. Use the private channel you used to join the beta. The operator sends a one-time token in that channel. It is valid for 10 minutes.</p>
 {#if !attempt}<button class="btn primary" disabled={busy} onclick={request}>Request confirmation</button>
 {:else}
 <p>Waiting for an operator</p>
 <p>Share this attempt in your trusted private channel: installation {installation}, attempt <code>{attempt}</code>.</p>
 <label>Token from the operator <input type="password" autocomplete="off" bind:value={token} disabled={busy} /></label>
 <button class="btn primary" disabled={busy || !token} onclick={register}>Confirm and register a passkey</button>
 {/if}
 <p class="muted">Do not paste the token into a GitHub issue, a pull request, a shared document, or a screenshot. If you lose it or someone else sees it, ask the operator to revoke it and issue a new one.</p>
 {#if busy}<p role="status">Confirming and registering your passkey…</p>{/if}
 {#if confirmed}<p role="status">Confirmed. Now register your passkey.</p>{/if}
 {#if error}<p class="flash error" role="alert">{error}</p>{/if}
 </div>
</section>
