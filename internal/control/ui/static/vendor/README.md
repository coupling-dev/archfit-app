# Vendored editor bundle

This folder holds the prebuilt Monaco + `monaco-yaml` bundle. It is empty until someone builds it: the
policy editor then runs as a plain `<textarea>`, which saves without JavaScript. The server checks for
`monaco.bundle.js` at start and only then adds the bundle to the editor page.

## Expected files

| File                 | Content                                        |
| -------------------- | ---------------------------------------------- |
| `monaco.bundle.js`   | Monaco, `monaco-yaml` and the entry below, IIFE |
| `monaco.bundle.css`  | Monaco styles (emitted by esbuild)             |
| `editor.worker.js`   | Monaco base worker                             |
| `yaml.worker.js`     | `monaco-yaml` worker                           |

The Content-Security-Policy is `script-src 'self'; worker-src 'self' blob:`. Load nothing from a CDN.

## Contract with `../editor.js`

The bundle sets `window.ArchfitEditor = { mount(container, options) }` with
`options = { value, schemaUrl, line, onChange(value, diagnosticCount) }`. `mount` creates a YAML model with
`value`, configures `monaco-yaml` with the schema at `schemaUrl` (`/schema/policy.json`), reveals `line` when
it is not 0, and calls `onChange` on every content or marker change. It must name its workers
`/static/vendor/editor.worker.js` and `/static/vendor/yaml.worker.js` in `MonacoEnvironment.getWorker`.

## Build (manual, needs Node; not part of `make`, CI or the image)

Write `entry.js` to that contract in a scratch folder outside this repository, then:

```sh
npm install monaco-editor monaco-yaml esbuild
npx esbuild entry.js --bundle --minify --format=iife --loader:.ttf=dataurl --outfile=monaco.bundle.js
npx esbuild node_modules/monaco-editor/esm/vs/editor/editor.worker.js --bundle --minify --format=iife --outfile=editor.worker.js
npx esbuild node_modules/monaco-yaml/yaml.worker.js --bundle --minify --format=iife --outfile=yaml.worker.js
```

Copy the four files here and commit them with the versions of `monaco-editor` and `monaco-yaml` in the commit message.
