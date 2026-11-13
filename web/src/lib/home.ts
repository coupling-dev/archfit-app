import { repoPage, type Repo } from './route';
import { lineKinds, type MapModel, type MapLine } from '../components/map/model';

export interface HomeNeed {
  kind: string;
  text: string;
  href: string;
  record?: string;
}
export interface HomeSetup {
  state: string;
  missing: string[];
  first_pull?: number;
  rules: { code: string; state: string; text: string }[];
}
export interface Home {
  status: string;
  needs_you: HomeNeed[];
  map?: MapModel;
  setup?: HomeSetup;
}

function object(v: unknown): Record<string, unknown> {
  if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error('The Home answer could not be read.');
  return v as Record<string, unknown>;
}
function string(v: unknown): string {
  if (typeof v !== 'string') throw new Error('The Home answer could not be read.');
  return v;
}
function array(v: unknown): unknown[] {
  if (!Array.isArray(v)) throw new Error('The Home answer could not be read.');
  return v;
}
function number(v: unknown): number {
  if (typeof v !== 'number' || !Number.isSafeInteger(v) || v < 1) throw new Error('The Home answer could not be read.');
  return v;
}

export function readHome(value: unknown): Home {
  const v = object(value);
  const home: Home = {
    status: string(v.status),
    needs_you: array(v.needs_you ?? []).map((n) => {
      const need = object(n);
      return {
        kind: string(need.kind),
        text: string(need.text),
        href: string(need.href),
        ...(need.record === undefined ? {} : { record: string(need.record) }),
      };
    }),
  };
  if (v.map !== undefined) {
    const m = object(v.map);
    home.map = {
      layers: array(m.layers).map(string),
      modules: array(m.modules).map((entry) => {
        const n = object(entry);
        return { name: string(n.name), layer: string(n.layer), volatility: string(n.volatility) };
      }),
      lines: array(m.lines).map((entry): MapLine => {
        const l = object(entry);
        const kind = lineKinds.find((k) => k === l.kind);
        if (!kind) throw new Error('The Home answer could not be read.');
        return {
          kind,
          from: string(l.from),
          to: string(l.to),
          ...(l.rule === undefined ? {} : { rule: string(l.rule) }),
          ...(l.request === undefined ? {} : { request: number(l.request) }),
          ...(l.expires === undefined ? {} : { expires: string(l.expires) }),
        };
      }),
    };
  }
  if (v.setup !== undefined) {
    const s = object(v.setup);
    home.setup = {
      state: string(s.state),
      missing: array(s.missing).map(string),
      ...(s.first_pull === undefined ? {} : { first_pull: number(s.first_pull) }),
      rules: array(s.rules).map((entry) => {
        const r = object(entry);
        return { code: string(r.code), state: string(r.state), text: string(r.text) };
      }),
    };
  }
  return home;
}

// Rebuild only known route shapes for this repository; safeHref alone would allow another repository's route.
export function homeHref(repo: Repo, href: string): string | undefined {
  const sameRepo = (owner: string, name: string) => {
    try {
      return decodeURIComponent(owner).toLowerCase() === repo.owner.toLowerCase() &&
        decodeURIComponent(name).toLowerCase() === repo.name.toLowerCase();
    } catch { return false; }
  };
  const local = /^\/r\/([^/]+)\/([^/]+)(\/.*)$/.exec(href);
  if (local && sameRepo(local[1], local[2])) {
    const suffix = local[3];
    if (/^\/(?:policy|owner\/(?:confirm|revert))$/.test(suffix) ||
        /^\/requests\/[1-9][0-9]{0,9}$/.test(suffix) ||
        /^\/owner\/workflow\?pull=[1-9][0-9]{0,9}$/.test(suffix))
      return repoPage(repo.fullName, suffix);
  }
  const issue = /^https:\/\/github\.com\/([^/]+)\/([^/]+)\/issues\/([1-9][0-9]{0,9})$/.exec(href);
  if (issue && sameRepo(issue[1], issue[2]))
    return 'https://github.com/' + encodeURIComponent(repo.owner) + '/' + encodeURIComponent(repo.name) + '/issues/' + issue[3];
  return undefined;
}
