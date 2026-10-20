<script lang="ts">
  import type { Session } from './lib/api';
  import { repoPage } from './lib/route';
  import { applyTheme, saveTheme, storedTheme, themes, type Theme } from './lib/theme';

  // overview is false on a repository page outside the tabs (Data sharing): no tab is current there.
  let { session, repo = '', overview = true }: { session: Session | null; repo?: string; overview?: boolean } = $props();

  let theme = $state<Theme>(storedTheme());
  const themeNames: Record<Theme, string> = { auto: 'Auto', light: 'Light', dark: 'Dark' };

  function pick(t: Theme) {
    theme = t;
    applyTheme(t);
    saveTheme(t);
  }

  // The other tabs are still server-rendered pages of the App.
  const tabs = $derived(
    repo
      ? [
          { label: 'Overview', href: repoPage(repo), current: overview },
          { label: 'Architecture', href: repoPage(repo, '/graph'), current: false },
          { label: 'Policy', href: repoPage(repo, '/policy'), current: false },
        ]
      : [],
  );
</script>

<header class="app-header">
  <a class="brand" href="/">
    <svg class="brand-mark" viewBox="0 0 24 24" aria-hidden="true">
      <rect x="1" y="1" width="22" height="22" rx="6" fill="currentColor" opacity="0.12" />
      <path d="M6 17 12 6l6 11M8.5 13h7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
    </svg>
    Archfit
  </a>
  {#if repo}
    <nav class="crumbs" aria-label="Breadcrumb">
      <span class="sep" aria-hidden="true">/</span>
      <span>{repo.split('/')[0]}</span>
      <span class="sep" aria-hidden="true">/</span>
      <strong><a href={repoPage(repo)} aria-current={overview ? 'page' : undefined}>{repo.split('/')[1]}</a></strong>
    </nav>
  {/if}
  <span class="header-spacer"></span>
  <div class="theme-toggle" role="group" aria-label="Theme">
    {#each themes as t (t)}
      <button type="button" aria-pressed={theme === t} onclick={() => pick(t)}>{themeNames[t]}</button>
    {/each}
  </div>
  {#if session}
    <form class="user-chip" method="post" action="/auth/logout">
      <span class="avatar" aria-hidden="true">{session.login.slice(0, 1).toUpperCase()}</span>
      <span>@{session.login}</span>
      <input type="hidden" name="csrf" value={session.csrf} />
      <button class="btn sm" type="submit">Sign out</button>
    </form>
  {/if}
</header>
{#if tabs.length}
  <nav class="unav" aria-label="Repository">
    {#each tabs as tab (tab.label)}
      <a href={tab.href} aria-current={tab.current ? 'page' : undefined}>{tab.label}</a>
    {/each}
  </nav>
{/if}
