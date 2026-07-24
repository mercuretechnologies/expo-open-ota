#!/usr/bin/env node

// Silence DEP0040 (punycode) emitted by node-fetch@2 -> whatwg-url@5 on Node 21+.
process.noDeprecation = true;

// eslint-disable-next-line unicorn/prefer-top-level-await
(async () => {
  const oclif = await import('@oclif/core')
  await oclif.execute({dir: __dirname})
})()
