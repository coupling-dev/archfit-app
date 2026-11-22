<script lang="ts">
 import { postJSON, explain, type ApiError, type Session } from '../lib/api';
 let { session }: {session:Session} = $props();
 let installation=$state('');
 let attempt=$state('');
 let metadata=$state<{UserID:number;RepositoryID:number;AttemptID:string}|null>(null);
 let token=$state('');
 let verified=$state(false);
 let error=$state('');
 let busy=$state(false);
 async function act(action:'lookup'|'issue'|'revoke') {
  busy=true;token='';error='';
  try {
   const body={installation:Number(installation),attempt};
   if(action==='lookup'){metadata=null;verified=false;metadata=await postJSON('/api/admin/confirmations/lookup',session.csrf,body);}
   else if(action==='issue'){const v=await postJSON<{token:string}>('/api/admin/confirmations/issue',session.csrf,body);token=v.token;}
   else {await postJSON('/api/admin/confirmations/revoke',session.csrf,body);verified=false;}
  } catch(e){error=explain(e as ApiError,window.location.pathname);}
  finally{busy=false;}
 }
 function changed(){metadata=null;token='';verified=false;}
</script>
<section class="box">
 <div class="box-body stack">
 <h2>First-passkey confirmations</h2>
 <p>Verify the owner through the existing trusted private channel used for beta admission. Never send a token through a GitHub issue, pull request, shared document, or a contact supplied by the request.</p>
 <label>Installation ID <input type="text" inputmode="numeric" bind:value={installation} oninput={changed} /></label>
 <label>Attempt ID <input type="text" bind:value={attempt} oninput={changed} /></label>
 <button class="btn" disabled={busy || !installation || !attempt} onclick={() => act('lookup')}>Find confirmation</button>
 {#if metadata}
 <p>GitHub user ID {metadata.UserID}, repository ID {metadata.RepositoryID}.</p>
 <label><input type="checkbox" bind:checked={verified} /> I verified this owner through the trusted private channel.</label>
 <button class="btn primary" disabled={busy || !verified} onclick={() => act('issue')}>Issue token</button>
 <button class="btn danger" disabled={busy} onclick={() => act('revoke')}>Revoke token</button>
 {/if}
 {#if token}<div role="status"><p>Shown once. Send only through the verified private channel. Single use; expires in 10 minutes. Do not log or screenshot it.</p><code>{token}</code><button class="btn" onclick={() => token=''}>Hide token</button></div>{/if}
 {#if error}<p class="flash error" role="alert">{error}</p>{/if}
 </div>
</section>
