// The pages a signed-in user opens, each in a chunk of its own: the sign-in page and the first paint of the shell do not
// carry them. A chunk is asked for once, when its page is first shown or when a link to it is pointed at.
const loaders = {
  home: () => import('../pages/Repos.svelte'),
  admin: () => import('../pages/Admin.svelte'),
  repo: () => import('../pages/Home.svelte'),
  policy: () => import('../pages/Policy.svelte'),
  owner: () => import('../pages/Owner.svelte'),
  request: () => import('../pages/Request.svelte'),
  passkeys: () => import('../pages/Passkeys.svelte'),
  pull: () => import('../pages/Pull.svelte'),
} as const;

export type PageKind = keyof typeof loaders;
type Loaded<K extends PageKind> = Awaited<ReturnType<(typeof loaders)[K]>>;

const asked = new Map<PageKind, Promise<unknown>>();

export function loadPage<K extends PageKind>(kind: K): Promise<Loaded<K>> {
  let chunk = asked.get(kind);
  if (!chunk) {
    chunk = loaders[kind]();
    // A chunk that did not load is asked for again on the next visit; the page shown says so (App.svelte).
    chunk.catch(() => asked.delete(kind));
    asked.set(kind, chunk);
  }
  return chunk as Promise<Loaded<K>>;
}

export const isPage = (kind: string): kind is PageKind => kind in loaders;
