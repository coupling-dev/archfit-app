// Empties the build folder the Go binary embeds, keeping its .gitignore (vite's emptyOutDir would delete it).
import { readdirSync, rmSync } from 'node:fs';

const out = new URL('../../internal/control/ui/spa/', import.meta.url);
for (const name of readdirSync(out)) {
  if (name !== '.gitignore') rmSync(new URL(name, out), { recursive: true });
}
