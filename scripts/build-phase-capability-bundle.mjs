#!/usr/bin/env node
/**
 * Rebuilds skills/issue-{start,end,merge}/contracts/issue-phase-capability-bundle-v1.json.
 *
 * Closure paths are relative to the plugin root (`skills/<name>/...`), which is the layout
 * both Claude Code and Codex install. Phases and fixed fields are carried over from the
 * existing bundle; only the closure, mirrors and digests are recomputed.
 *
 *   node scripts/build-phase-capability-bundle.mjs                 # write the three copies
 *   node scripts/build-phase-capability-bundle.mjs --check         # compare only, exit 1 on drift
 *   node scripts/build-phase-capability-bundle.mjs --check --root <plugin-root>
 */
import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const SKILLS = ['issue-start', 'issue-end', 'issue-merge'];
const BUNDLE = 'contracts/issue-phase-capability-bundle-v1.json';
const GENERATOR = 'scripts/build-phase-capability-bundle.mjs';
const MIRRORS = [{ id: 'claude', root: '.' }, { id: 'codex', root: '.' }];

const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');
const posix = (...parts) => path.posix.join(...parts);

function listFiles(root, rel, accept) {
  const dir = path.join(root, rel);
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const child = posix(rel, entry.name);
    if (entry.isDirectory()) return listFiles(root, child, accept);
    return entry.isFile() && accept(entry.name) ? [child] : [];
  });
}

export function closurePaths(root) {
  const paths = [GENERATOR];
  for (const skill of SKILLS) {
    const base = posix('skills', skill);
    paths.push(
      posix(base, 'SKILL.md'),
      posix(base, 'contracts', `${skill}-phase-api-v1.json`),
      ...listFiles(root, posix(base, 'schemas', 'issue-phase'), () => true),
      ...listFiles(root, posix(base, 'scripts'), (name) => name.endsWith('.mjs') && !name.endsWith('.test.mjs')),
    );
  }
  return [...new Set(paths)].sort();
}

async function loadCanonical(root) {
  const contract = path.join(root, 'skills', 'issue-merge', 'scripts', 'issue-phase-contract.mjs');
  return import(pathToFileURL(contract).href);
}

export async function buildBundle(root) {
  const { canonicalJsonBytes, canonicalJsonSha256 } = await loadCanonical(root);
  const previous = JSON.parse(readFileSync(path.join(root, 'skills', 'issue-merge', BUNDLE), 'utf8'));
  const missing = [];
  const entries = closurePaths(root).flatMap((rel) => {
    const file = path.join(root, rel);
    if (!existsSync(file)) {
      missing.push(rel);
      return [];
    }
    return [{ path: rel, sha256: sha256(readFileSync(file)) }];
  });
  if (missing.length > 0) throw new Error(`closure files missing: ${missing.join(', ')}`);

  const capabilities = {
    apiVersion: previous.apiVersion,
    bundleId: previous.bundleId,
    mirrors: MIRRORS,
    phases: previous.phases,
    repositoryOrigin: previous.repositoryOrigin,
    schemaVersion: previous.schemaVersion,
  };
  const algorithm = 'sha256-raw-bytes';
  const bundle = {
    ...capabilities,
    capabilitySetSha256: canonicalJsonSha256(capabilities),
    closure: { algorithm, entries, sha256: canonicalJsonSha256({ algorithm, entries }) },
  };
  return canonicalJsonBytes(bundle);
}

export async function checkBundle(root) {
  const problems = [];
  const expected = await buildBundle(root);
  const { entries } = JSON.parse(expected.toString('utf8')).closure;
  for (const skill of SKILLS) {
    const rel = posix('skills', skill, BUNDLE);
    const file = path.join(root, rel);
    if (!existsSync(file)) problems.push(`missing bundle: ${rel}`);
    else if (!readFileSync(file).equals(expected)) problems.push(`stale bundle: ${rel}`);
  }
  if (problems.length === 0) return { ok: true, problems, total: entries.length, matched: entries.length };

  // Report which closure entries the shipped bundle no longer matches.
  const shipped = JSON.parse(readFileSync(path.join(root, 'skills', 'issue-merge', BUNDLE), 'utf8'));
  const actual = new Map(entries.map((entry) => [entry.path, entry.sha256]));
  let matched = 0;
  for (const entry of shipped.closure?.entries ?? []) {
    if (actual.get(entry.path) === entry.sha256) matched += 1;
    else problems.push(`closure drift: ${entry.path}`);
  }
  for (const rel of actual.keys()) {
    if (!(shipped.closure?.entries ?? []).some((entry) => entry.path === rel)) problems.push(`closure missing: ${rel}`);
  }
  return { ok: false, problems, total: entries.length, matched };
}

async function main(argv) {
  const check = argv.includes('--check');
  const rootIndex = argv.indexOf('--root');
  const root = path.resolve(rootIndex >= 0
    ? argv[rootIndex + 1]
    : path.join(path.dirname(fileURLToPath(import.meta.url)), '..'));

  if (check) {
    const result = await checkBundle(root);
    for (const problem of result.problems) console.error(problem);
    console.log(`closure: ${result.matched}/${result.total} match`);
    if (!result.ok) {
      console.error('capability bundle is stale — run: node scripts/build-phase-capability-bundle.mjs');
      process.exit(1);
    }
    console.log('capability bundle: 3 copies match');
    return;
  }

  const bytes = await buildBundle(root);
  for (const skill of SKILLS) writeFileSync(path.join(root, 'skills', skill, BUNDLE), bytes);
  const { closure } = JSON.parse(bytes.toString('utf8'));
  console.log(`capability bundle written: ${closure.entries.length} entries, sha256 ${sha256(bytes)}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(error.message);
    process.exit(1);
  });
}
