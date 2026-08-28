import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { graphBootstrapReason, loadGraph, runSyncBootstrap, syncBootstrapComplete } from './issue-onboard.mjs';

const now = '2026-08-28T00:00:00.000Z';
const contextFields = ['problem', 'outcome', 'scope', 'acceptance', 'result', 'components', 'decisions', 'evidence'];

function validNode(number, { title = 'Issue ' + number, revision = 'r' } = {}) {
  const url = 'https://github.com/o/r/issues/' + number;
  const source = { url, revision, observedAt: now };
  const context = Object.fromEntries(contextFields.map((field) => [
    field,
    { value: 'unknown', reason: 'fixture', source },
  ]));
  return {
    id: 'github:o/r#' + number,
    number,
    title,
    status: 'open',
    labels: [],
    url,
    context,
    provenance: source,
  };
}

function completeGraph(nodes = { '1': validNode(1) }) {
  return {
    version: 2,
    provider: 'github',
    repository: 'o/r',
    updatedAt: now,
    snapshot: {
      status: 'complete',
      fetchedAt: now,
      digest: 'sha256:' + 'a'.repeat(64),
      reason: null,
    },
    nodes,
    edges: [],
  };
}

test('missing, incomplete, or invalid caches request a sync bootstrap', () => {
  assert.equal(graphBootstrapReason(completeGraph(), { fileExists: false }), 'missing');
  assert.equal(
    graphBootstrapReason({ ...completeGraph(), snapshot: { status: 'partial' } }),
    'snapshot-incomplete',
  );
  assert.equal(
    graphBootstrapReason({ ...completeGraph(), nodes: {} }, { openIssues: [{ number: 1 }] }),
    'empty',
  );
  assert.equal(
    graphBootstrapReason({
      ...completeGraph(),
      nodes: { '1': { number: 1, title: 'Issue 1', status: 'open', labels: [], url: 'https://github.com/o/r/issues/1' } },
    }),
    'invalid',
  );
});

test('malformed graph caches can be handed to the onboarding bootstrap path', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'issue-onboard-bootstrap-'));
  mkdirSync(path.join(root, '.issue'));
  writeFileSync(path.join(root, '.issue', 'graph.json'), '{ malformed', 'utf8');
  const entry = new URL('./issue-onboard.mjs', import.meta.url).href;
  const script = `import { loadGraph } from ${JSON.stringify(entry)};
const graph = loadGraph(process.argv[1], 'github', { tolerateParseError: true });
console.log(graph.snapshot.status);`;
  try {
    const result = spawnSync(process.execPath, ['--input-type=module', '-e', script, root], { encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), 'invalid');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('only a complete issue-sync result can unlock onboarding', () => {
  assert.equal(syncBootstrapComplete({ status: 0, stdout: 'SNAPSHOT_STATUS=complete\nGRAPH_SYNC=ok\n' }), true);
  assert.equal(syncBootstrapComplete({ status: 0, stdout: 'SNAPSHOT_STATUS=partial\n' }), false);
  assert.equal(syncBootstrapComplete({ status: 0, stdout: 'SNAPSHOT_STATUS=complete\n' }), false);
  assert.equal(syncBootstrapComplete({ status: 2, stdout: 'SNAPSHOT_STATUS=complete\nGRAPH_SYNC=ok\n' }), false);
});

test('cache coverage and revisions are compared with current open issues', () => {
  const graph = completeGraph();
  assert.equal(graphBootstrapReason(graph, {
    openIssues: [{ number: 1, title: 'Issue 1', updatedAt: 'r' }],
  }), null);
  assert.equal(graphBootstrapReason(graph, { openIssues: [{ number: 2 }] }), 'open-issue-missing');
  assert.equal(graphBootstrapReason(graph, {
    openIssues: [{ number: 1, title: 'Renamed issue', updatedAt: 'r' }],
  }), 'open-issue-changed');
  assert.equal(graphBootstrapReason(graph, {
    openIssues: [{ number: 1, title: 'Issue 1', updatedAt: 'new-revision' }],
  }), 'open-issue-changed');
  const missingRevision = structuredClone(graph);
  delete missingRevision.nodes['1'].provenance.revision;
  assert.equal(graphBootstrapReason(missingRevision, {
    openIssues: [{ number: 1, title: 'Issue 1', updatedAt: 'new-revision' }],
  }), 'invalid');
});

test('runSyncBootstrap executes the discovered issue-sync entrypoint in the repository', () => {
  const calls = [];
  const result = runSyncBootstrap('/repo', {
    resolve: (root, skill, script) => {
      calls.push({ root, skill, script });
      return '/skills/issue-sync/scripts/issue-sync.mjs';
    },
    spawn: (command, args, options) => {
      calls.push({ command, args, options });
      return { status: 0, stdout: 'SNAPSHOT_STATUS=complete\\n', stderr: '' };
    },
  });

  assert.equal(result.status, 0);
  assert.deepEqual(calls, [
    { root: '/repo', skill: 'issue-sync', script: 'issue-sync.mjs' },
    {
      command: process.execPath,
      args: ['/skills/issue-sync/scripts/issue-sync.mjs'],
      options: { cwd: '/repo', encoding: 'utf8' },
    },
  ]);
});
