<script lang="ts">
  import type { Session } from './lib/api';
  import { repoPage } from './lib/route';
  import { applyTheme, saveTheme, storedTheme, themes, type Theme } from './lib/theme';

  // tab is the current tab of a repository page, '' on a page in no tab (a pull request page, Data sharing); pull is the
  // pull request number on a pull request page or its graph, 0 elsewhere.
  let {
    session,
    repo = '',
    tab = '',
    pull = 0,
  }: { session: Session | null; repo?: string; tab?: 'overview' | 'graph' | 'policy' | ''; pull?: number } = $props();

  let theme = $state<Theme>(storedTheme());
  const themeNames: Record<Theme, string> = { auto: 'Auto', light: 'Light', dark: 'Dark' };

  function pick(t: Theme) {
    theme = t;
    applyTheme(t);
    saveTheme(t);
  }

  const tabs = $derived(
    repo
      ? [
          { label: 'Overview', href: repoPage(repo), current: tab === 'overview' },
          { label: 'Architecture', href: repoPage(repo, '/graph'), current: tab === 'graph' },
          { label: 'Policy', href: repoPage(repo, '/policy'), current: tab === 'policy' },
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
      <strong><a href={repoPage(repo)} aria-current={tab === 'overview' ? 'page' : undefined}>{repo.split('/')[1]}</a></strong>
      {#if pull}
        <span class="sep" aria-hidden="true">/</span>
        <a href={repoPage(repo, `/pull/${pull}`)} aria-current="page">#{pull}</a>
      {/if}
    </nav>
  {/if}
  <span class="header-spacer"></span>
  {#if session?.operator}
    <a class="admin-link" href="/admin">
      Admin{#if session.waiting && session.waiting > 0}<span class="label attention">{session.waiting} waiting</span>{/if}
    </a>
  {/if}
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
