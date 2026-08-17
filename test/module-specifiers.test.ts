import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// The package is `"type": "module"` and ships plain `tsc` output, so relative
// specifiers are emitted byte-for-byte into dist. Node's ESM resolver does not
// add extensions, which makes an extensionless `./sandbox` a hard
// ERR_MODULE_NOT_FOUND on `import '@postgrip/agent-sdk'` — the whole package,
// not just that module.
//
// Nothing caught it: typecheck resolves it happily (tsc uses its own bundler
// resolution here), `build` only compiles, and the tests import from `src/`
// through vitest, which also resolves it. Every existing module already used
// `.js`, so the rule was real but only conventional. This makes it checkable.
const SRC = new URL('../src', import.meta.url).pathname;

function relativeSpecifiers(source: string): string[] {
  // `from './x'`, `import './x'`, and `import('./x')` alike.
  return [...source.matchAll(/(?:from|import)\s*\(?\s*['"](\.[^'"]*)['"]/g)].map((m) => m[1]);
}

describe('published module specifiers', () => {
  const files = readdirSync(SRC).filter((f) => f.endsWith('.ts') && !f.endsWith('.d.ts'));

  it('has source files to check', () => {
    expect(files.length).toBeGreaterThan(0);
  });

  it.each(files)('%s uses extensioned relative specifiers', (file) => {
    const source = readFileSync(join(SRC, file), 'utf8');
    const bare = relativeSpecifiers(source).filter((s) => !s.endsWith('.js') && !s.endsWith('.json'));
    expect(bare, `${file} imports ${bare.join(', ')} without a .js extension`).toEqual([]);
  });

  it('detects a bare specifier', () => {
    // Guard the guard: a regex that quietly stopped matching would turn this
    // into a green light for exactly the bug it exists to catch.
    expect(relativeSpecifiers(`import { X } from './sandbox';`)).toEqual(['./sandbox']);
    expect(relativeSpecifiers(`export type { A } from "./types";`)).toEqual(['./types']);
    expect(relativeSpecifiers(`const m = await import('./lazy');`)).toEqual(['./lazy']);
    // And that a correct one is not reported.
    expect(relativeSpecifiers(`import { X } from './sandbox.js';`)).toEqual(['./sandbox.js']);
  });
});
