import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';
import { pageRoutes } from '../e2e/page-routes';
import { parseRoute } from './lib/route';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
const server = read('../../internal/control/ui/ui.go');
const client = read('./lib/route.ts');
const spec = read('../e2e/route-parity.spec.ts');

function source(text: string) {
  return ts.createSourceFile('routes.ts', text, ts.ScriptTarget.Latest, true);
}

function kinds(text: string): string[] {
  const found: string[] = [];
  function visit(node: ts.Node) {
    if (ts.isPropertySignature(node) && node.name.getText() === 'kind') {
      function literal(child: ts.Node) {
        if (ts.isStringLiteral(child)) found.push(child.text);
        ts.forEachChild(child, literal);
      }
      if (node.type) literal(node.type);
    }
    ts.forEachChild(node, visit);
  }
  const route = source(text).statements.find((node) => ts.isTypeAliasDeclaration(node) && node.name.text === 'Route');
  if (!route) throw new Error('route.ts declares no Route');
  visit(route);
  return found;
}

function pages(text: string): string[] {
  return [...text.matchAll(/mux\.Handle(?:Func)?\(\s*"([^"]+)"/g)]
    .map((m) => m[1])
    .filter(
      (pattern) =>
        pattern === '/' || (pattern.startsWith('GET /') && !/^GET \/(api|assets|auth|schema)(\/|$)/.test(pattern)),
    );
}

function matches(pattern: string, path: string): boolean {
  if (pattern === '/') return true;
  const actual = new URL(path, 'https://app.example').pathname;
  const route = pattern.slice(4).replace('{$}', '');
  const parts = route.split('/');
  const input = actual.split('/');
  return (
    input.length === parts.length &&
    parts.every((part, i) => (/^\{[^}]+\}$/.test(part) ? input[i] !== '' : input[i] === part))
  );
}

// The gate checks the actual table-driven test callback, not merely an import or an unrelated words assertion.
function wordsChecked(text: string): boolean {
  const ast = source(text);
  const support = ast.statements.some(
    (node) =>
      ts.isImportDeclaration(node) &&
      ts.isStringLiteral(node.moduleSpecifier) &&
      node.moduleSpecifier.text === './support' &&
      node.importClause?.namedBindings?.getText().includes('expectNoNotUsedWord'),
  );
  const table = ast.statements.some(
    (node) =>
      ts.isImportDeclaration(node) &&
      ts.isStringLiteral(node.moduleSpecifier) &&
      node.moduleSpecifier.text === './page-routes' &&
      node.importClause?.namedBindings?.getText().includes('pageRoutes'),
  );
  let checked = false;
  function visit(node: ts.Node) {
    if (ts.isForOfStatement(node) && node.expression.getText() === 'pageRoutes') {
      function test(child: ts.Node) {
        if (ts.isCallExpression(child) && child.expression.getText() === 'test') {
          const callback = child.arguments.at(-1);
          if (callback && ts.isArrowFunction(callback)) {
            let opened = false;
            let words = false;
            function call(inner: ts.Node) {
              if (ts.isAwaitExpression(inner) && ts.isCallExpression(inner.expression)) {
                const expr = inner.expression;
                if (expr.expression.getText() === 'open' && expr.arguments[1]?.getText() === 'route.path')
                  opened = true;
                if (expr.expression.getText() === 'expectNoNotUsedWord' && expr.arguments[0]?.getText() === 'page')
                  words = true;
              }
              ts.forEachChild(inner, call);
            }
            call(callback.body);
            checked ||= opened && words;
          }
        }
        ts.forEachChild(child, test);
      }
      test(node.statement);
    }
    ts.forEachChild(node, visit);
  }
  visit(ast);
  return support && table && checked;
}

function parity(serverText: string, clientText: string, specText: string, cases = pageRoutes): string[] {
  const routes = pages(serverText);
  const declared = kinds(clientText);
  const errors: string[] = [];
  const checked = wordsChecked(specText);
  for (const pattern of routes) {
    if (!checked || !cases.some((entry) => (pattern === '/' ? entry.kind === 'unknown' : matches(pattern, entry.path))))
      errors.push(`server route has no words-checked spec: ${pattern}`);
  }
  for (const kind of declared) {
    const examples = cases.filter((entry) => entry.kind === kind);
    if (!checked || examples.length === 0) errors.push(`kind has no words-checked spec: ${kind}`);
    if (
      examples.length === 0 ||
      examples.some(
        (entry) =>
          !routes.some((pattern) =>
            kind === 'unknown' ? pattern === '/' : pattern !== '/' && matches(pattern, entry.path),
          ),
      )
    )
      errors.push(`kind has no server route: ${kind}`);
  }
  return errors;
}

describe('page route parity', () => {
  it('lists every server page and client kind with a words-checked browser spec', () => {
    expect(pages(server).length).toBeGreaterThan(1);
    expect(parity(server, client, spec)).toEqual([]);
    for (const route of pageRoutes) {
      const url = new URL(route.path, 'https://app.example');
      expect(parseRoute(url.pathname, url.search).kind, route.path).toBe(route.kind);
    }
  });

  it('rejects an uncovered new server page', () => {
    expect(parity(server + '\nmux.Handle("GET /new-page", page)', client, spec)).toContain(
      'server route has no words-checked spec: GET /new-page',
    );
  });

  it('rejects a client kind without a server route even when it has a words spec case', () => {
    const changed = client.replace("| { kind: 'home' }", "| { kind: 'home' | 'new-kind' }");
    const cases = [...pageRoutes, { kind: 'new-kind', path: '/new-kind', heading: 'New page' }];
    expect(parity(server, changed, spec, cases)).toContain('kind has no server route: new-kind');
  });

  it('rejects a route whose words-checked spec was removed', () => {
    const cases = pageRoutes.filter((entry) => entry.kind !== 'policy');
    expect(parity(server, client, spec, cases)).toContain(
      'server route has no words-checked spec: GET /r/{o}/{n}/policy',
    );
  });

  it('rejects a spec that no longer calls the words helper', () => {
    expect(parity(server, client, spec.replace('await expectNoNotUsedWord(page);', ''))).toContain(
      'kind has no words-checked spec: home',
    );
  });

  it('rejects a client kind whose server page was removed', () => {
    const changed = server.replace('mux.Handle("GET /r/{o}/{n}/policy", page)', '');
    expect(parity(changed, client, spec)).toContain('kind has no server route: policy');
  });
});
