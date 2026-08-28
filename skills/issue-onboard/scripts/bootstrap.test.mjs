import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
import { chmodSync, cpSync, existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  bootstrapEnvironment,
  graphBootstrapReason,
  graphDocumentDigest,
  fetchCompleteIssueList,
  issueSnapshotDigest,
  loadGraph,
  runSyncBootstrap,
  saveGraph,
  syncBootstrapComplete,
} from './issue-onboard.mjs';
import { run } from './issue-common.mjs';
import { digest, extractQuote } from './issue-graph-v2.mjs';

const now = '2026-08-28T00:00:00.000Z';
const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const ontologyRoot = path.join(repositoryRoot, 'tools', 'issue-ontology');
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
  const graph = {
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
  graph.snapshot.graphDigest = graphDocumentDigest(graph);
  return graph;
}

function writeExecutable(file, contents) {
  writeFileSync(file, contents, 'utf8');
  chmodSync(file, 0o755);
}

function writeCliTestLoader(root) {
  const loader = path.join(root, 'test-loader.mjs');
  writeFileSync(loader, `import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import path from 'node:path';

const fixtureRoot = process.env.ISSUE_ONBOARD_TEST_FIXTURE_ROOT;
if (fixtureRoot) {
  const originalSpawnSync = childProcess.spawnSync;
  childProcess.spawnSync = (command, args, options) => {
    if (path.basename(String(command)) === 'gh') {
      return originalSpawnSync(path.join(fixtureRoot, 'bin', 'gh'), args, options);
    }
    return originalSpawnSync(command, args, options);
  };
  syncBuiltinESMExports();
}
`, 'utf8');
  return loader;
}

function cliFixture({ syncMode = 'complete', graph = null, initialGraph = null, ontologyPath = ontologyRoot } = {}) {
  const root = mkdtempSync(path.join(os.tmpdir(), 'issue-onboard-cli-'));
  const bin = path.join(root, 'bin');
  mkdirSync(bin, { recursive: true });
  const git = spawnSync('git', ['init', '--quiet', root], { encoding: 'utf8' });
  assert.equal(git.status, 0, git.stderr);

  writeExecutable(path.join(bin, 'gh'), `#!/bin/sh
case "$*" in
  *"issue list"*) printf '%s\\n' '[{"number":1,"title":"Issue 1","labels":[],"url":"https://github.com/o/r/issues/1","state":"OPEN","updatedAt":"r"}]' ;;
  *"repo view"*) printf '%s\\n' '{"nameWithOwner":"o/r"}' ;;
  *"api graphql"*) printf '%s\\n' '{"data":{"repository":{"issue":{"blockedBy":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}' ;;
  *) printf '%s\\n' '{}' ;;
esac
`);
  const testLoader = writeCliTestLoader(root);

  cpSync(
    path.join(repositoryRoot, 'skills', 'issue-onboard'),
    path.join(root, 'skills', 'issue-onboard'),
    { recursive: true },
  );
  const fixtureOntology = path.join(root, 'tools', 'issue-ontology');
  mkdirSync(fixtureOntology, { recursive: true });
  cpSync(path.join(ontologyRoot, 'validate.mjs'), path.join(fixtureOntology, 'validate.mjs'));
  cpSync(path.join(ontologyRoot, 'schemas'), path.join(fixtureOntology, 'schemas'), { recursive: true });
  cpSync(path.join(ontologyRoot, 'node_modules'), path.join(fixtureOntology, 'node_modules'), { recursive: true });
  const syncFile = path.join(root, 'skills', 'issue-sync', 'scripts', 'issue-sync.mjs');
  mkdirSync(path.dirname(syncFile), { recursive: true });
  const fixtureIssue = { number: 1, title: 'Issue 1', labels: [], url: 'https://github.com/o/r/issues/1', state: 'OPEN', updatedAt: 'r', body: '', comments: [] };
  const graphForSync = structuredClone(graph ?? completeGraph());
  graphForSync.snapshot.digest = issueSnapshotDigest([fixtureIssue]);
  graphForSync.snapshot.graphDigest = graphDocumentDigest(graphForSync);
  const graphLiteral = JSON.stringify(graphForSync);
  writeFileSync(syncFile, `import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const mode = ${JSON.stringify(syncMode)};
if (mode === 'partial') {
  console.log('SNAPSHOT_STATUS=partial');
  console.log('GRAPH_SYNC=failed');
  process.exit(0);
}
if (mode === 'failed') {
  console.log('SYNC_FAILED=1');
  process.exit(7);
}
const graph = ${graphLiteral};
mkdirSync(path.join(process.cwd(), '.issue'), { recursive: true });
writeFileSync(path.join(process.cwd(), '.issue', 'graph.json'), JSON.stringify(graph) + '\\n', 'utf8');
console.log('SNAPSHOT_STATUS=complete');
console.log('GRAPH_SYNC=ok');
`, 'utf8');

  if (initialGraph) {
    mkdirSync(path.join(root, '.issue'), { recursive: true });
    writeFileSync(path.join(root, '.issue', 'graph.json'), JSON.stringify(initialGraph) + '\n', 'utf8');
  }

  return {
    root,
    entry: path.join(root, 'skills', 'issue-onboard', 'scripts', 'issue-onboard.mjs'),
    env: {
      ...process.env,
      ISSUE_ONTOLOGY_ROOT: ontologyPath === ontologyRoot ? fixtureOntology : ontologyPath,
      ISSUE_ONBOARD_TEST_FIXTURE_ROOT: root,
      NODE_OPTIONS: `--import=${testLoader}`,
    },
  };
}

function runCliOnboard(fixture) {
  return spawnSync(process.execPath, [fixture.entry, 'onboard', '--all', '--no-llm'], {
    cwd: fixture.root,
    env: fixture.env,
    encoding: 'utf8',
  });
}

function runCliMode(fixture, mode, args = []) {
  return spawnSync(process.execPath, [fixture.entry, mode, ...args], {
    cwd: fixture.root,
    env: fixture.env,
    encoding: 'utf8',
  });
}

function trustedInstallWithRepositoryFallback() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'issue-onboard-trust-'));
  const bin = path.join(root, 'bin');
  const install = path.join(root, 'trusted-install');
  mkdirSync(bin, { recursive: true });
  assert.equal(spawnSync('git', ['init', '--quiet', root], { encoding: 'utf8' }).status, 0);
  writeExecutable(path.join(bin, 'gh'), `#!/bin/sh
case "$*" in
  *"issue list"*) printf '%s\\n' '[{"number":1,"title":"Issue 1","labels":[],"url":"https://github.com/o/r/issues/1","state":"OPEN","updatedAt":"r"}]' ;;
  *"repo view"*) printf '%s\\n' '{"nameWithOwner":"o/r"}' ;;
  *"api graphql"*) printf '%s\\n' '{"data":{"repository":{"issue":{"blockedBy":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}' ;;
  *) printf '%s\\n' '{}' ;;
esac
`);
  const testLoader = writeCliTestLoader(root);
  cpSync(path.join(repositoryRoot, 'skills', 'issue-onboard'), path.join(install, 'skills', 'issue-onboard'), { recursive: true });
  const ontology = path.join(install, 'tools', 'issue-ontology');
  mkdirSync(ontology, { recursive: true });
  cpSync(path.join(ontologyRoot, 'validate.mjs'), path.join(ontology, 'validate.mjs'));
  cpSync(path.join(ontologyRoot, 'schemas'), path.join(ontology, 'schemas'), { recursive: true });
  cpSync(path.join(ontologyRoot, 'node_modules'), path.join(ontology, 'node_modules'), { recursive: true });

  const localSync = path.join(root, 'skills', 'issue-sync', 'scripts', 'issue-sync.mjs');
  const observed = path.join(root, 'repository-sync-ran');
  mkdirSync(path.dirname(localSync), { recursive: true });
  writeExecutable(localSync, `import { writeFileSync } from 'node:fs';
writeFileSync(${JSON.stringify(observed)}, process.env.BOOTSTRAP_SENTINEL ?? 'missing', 'utf8');
console.log('SNAPSHOT_STATUS=complete');
console.log('GRAPH_SYNC=ok');
`);
  return {
    root,
    entry: path.join(install, 'skills', 'issue-onboard', 'scripts', 'issue-onboard.mjs'),
    observed,
    env: {
      ...process.env,
      ISSUE_ONTOLOGY_ROOT: ontology,
      BOOTSTRAP_SENTINEL: 'must-not-reach-repository-script',
      ISSUE_ONBOARD_TEST_FIXTURE_ROOT: root,
      NODE_OPTIONS: `--import=${testLoader}`,
    },
  };
}

function trustedInstallWithProjectFlavor() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'issue-onboard-project-skill-'));
  const bin = path.join(root, 'bin');
  const install = path.join(root, 'trusted-install');
  mkdirSync(bin, { recursive: true });
  assert.equal(spawnSync('git', ['init', '--quiet', root], { encoding: 'utf8' }).status, 0);
  writeExecutable(path.join(bin, 'gh'), `#!/bin/sh
case "$*" in
  *"issue list"*) printf '%s\\n' '[{"number":1,"title":"Issue 1","labels":[],"url":"https://github.com/o/r/issues/1","state":"OPEN","updatedAt":"r"}]' ;;
  *"repo view"*) printf '%s\\n' '{"nameWithOwner":"o/r"}' ;;
  *"api graphql"*) printf '%s\\n' '{"data":{"repository":{"issue":{"blockedBy":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}' ;;
  *) printf '%s\\n' '{}' ;;
esac
`);
  const testLoader = writeCliTestLoader(root);
  cpSync(path.join(repositoryRoot, 'skills', 'issue-onboard'), path.join(install, 'skills', 'issue-onboard'), { recursive: true });
  const ontology = path.join(install, 'tools', 'issue-ontology');
  mkdirSync(ontology, { recursive: true });
  cpSync(path.join(ontologyRoot, 'validate.mjs'), path.join(ontology, 'validate.mjs'));
  cpSync(path.join(ontologyRoot, 'schemas'), path.join(ontology, 'schemas'), { recursive: true });
  cpSync(path.join(ontologyRoot, 'node_modules'), path.join(ontology, 'node_modules'), { recursive: true });

  const projectSync = path.join(root, '.codex', 'skills', 'issue-sync', 'scripts', 'issue-sync.mjs');
  const observed = path.join(root, 'project-sync-ran');
  const fixtureIssue = { number: 1, title: 'Issue 1', labels: [], url: 'https://github.com/o/r/issues/1', state: 'OPEN', updatedAt: 'r', body: '', comments: [] };
  const graph = completeGraph();
  graph.snapshot.digest = issueSnapshotDigest([fixtureIssue]);
  graph.snapshot.graphDigest = graphDocumentDigest(graph);
  mkdirSync(path.dirname(projectSync), { recursive: true });
  writeFileSync(projectSync, `import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
writeFileSync(${JSON.stringify(observed)}, process.env.GH_TOKEN ?? 'absent', 'utf8');
mkdirSync(path.join(process.cwd(), '.issue'), { recursive: true });
writeFileSync(path.join(process.cwd(), '.issue', 'graph.json'), ${JSON.stringify(JSON.stringify(graph) + '\n')}, 'utf8');
console.log('SNAPSHOT_STATUS=complete');
console.log('GRAPH_SYNC=ok');
`, 'utf8');

  return {
    root,
    entry: path.join(install, 'skills', 'issue-onboard', 'scripts', 'issue-onboard.mjs'),
    observed,
    env: {
      ...process.env,
      ISSUE_ONTOLOGY_ROOT: ontology,
      GH_TOKEN: 'fake-project-token',
      ISSUE_ONBOARD_TEST_FIXTURE_ROOT: root,
      NODE_OPTIONS: `--import=${testLoader}`,
    },
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
  const schemaInvalid = completeGraph();
  schemaInvalid.nodes['1'].labels = 'bug';
  assert.equal(graphBootstrapReason(schemaInvalid), 'invalid');
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
  assert.equal(syncBootstrapComplete({ status: 0, stdout: 'SNAPSHOT_STATUS=complete-but-not-a-marker\nGRAPH_SYNC=okay\n' }), false);
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
  assert.equal(graphBootstrapReason(graph), 'open-issue-closed');
  const reopened = completeGraph();
  reopened.nodes['1'].status = 'close';
  assert.equal(graphBootstrapReason(reopened, {
    openIssues: [{ number: 1, title: 'Issue 1', updatedAt: 'r' }],
  }), 'open-issue-reopened');
});

test('complete caches require a matching live snapshot and cache integrity digest', () => {
  const issue = { number: 1, title: 'Issue 1', labels: [], url: 'https://github.com/o/r/issues/1', state: 'OPEN', updatedAt: 'r', body: '', comments: [] };
  const graph = completeGraph();
  graph.snapshot.digest = issueSnapshotDigest([issue]);
  graph.snapshot.graphDigest = graphDocumentDigest(graph);
  const live = { openIssues: [issue], allIssues: [issue], allIssuesComplete: true };
  assert.equal(graphBootstrapReason(graph, live), null);
  assert.equal(graphBootstrapReason(graph, { ...live, allIssuesComplete: false }), 'issue-list-incomplete');

  const tampered = structuredClone(graph);
  tampered.nodes['1'].labels = ['bug'];
  assert.equal(graphBootstrapReason(tampered, live), 'cache-integrity-failed');
  assert.equal(graphBootstrapReason(graph, { ...live, allIssues: [{ ...issue, updatedAt: 'new' }] }), 'snapshot-changed');
});

test('complete caches reject a repository identity that differs from the live checkout', () => {
  const graph = completeGraph();
  assert.equal(graphBootstrapReason(graph, {
    openIssues: [], allIssues: [], allIssuesComplete: true, repository: 'other/repository',
  }), 'repository-changed');
});

test('complete caches reject graph edges that are absent from the live issue sources', () => {
  const issue1 = { number: 1, title: 'Issue 1', labels: [], url: 'https://github.com/o/r/issues/1', state: 'OPEN', updatedAt: 'r', body: '', comments: [] };
  const issue2 = { number: 2, title: 'Issue 2', labels: [], url: 'https://github.com/o/r/issues/2', state: 'OPEN', updatedAt: 'r', body: '', comments: [] };
  const graph = completeGraph({ '1': validNode(1), '2': validNode(2) });
  graph.snapshot.digest = issueSnapshotDigest([issue1, issue2]);
  graph.edges = [{
    from: 1, to: 2, type: 'depends-on', kind: 'blocked-by', rationale: 'forged',
    context: { generatedBy: 'sync', confidence: 'high', generatedAt: now }, evidence: [],
    status: 'active', schemaVersion: 1, createdBy: 'sync', createdAt: now,
    provenance: { url: issue1.url, digest: 'sha256:' + 'f'.repeat(64) },
  }];
  graph.snapshot.graphDigest = graphDocumentDigest(graph);
  const live = { openIssues: [issue1, issue2], allIssues: [issue1, issue2], allIssuesComplete: true };
  assert.equal(graphBootstrapReason(graph, live), 'edge-source-changed');
});

test('complete caches reject forged metadata on a live relationship edge', () => {
  const body = 'depends on #2';
  const issue1 = { number: 1, title: 'Issue 1', labels: [], url: 'https://github.com/o/r/issues/1', state: 'OPEN', updatedAt: 'r', body, comments: [] };
  const issue2 = { number: 2, title: 'Issue 2', labels: [], url: 'https://github.com/o/r/issues/2', state: 'OPEN', updatedAt: 'r', body: '', comments: [] };
  const quote = extractQuote(body, 0, body.length);
  const graph = completeGraph({ '1': validNode(1), '2': validNode(2) });
  graph.snapshot.digest = issueSnapshotDigest([issue1, issue2]);
  graph.edges = [{
    from: 1, to: 2, type: 'depends-on', kind: 'blocked-by',
    rationale: '#1 본문이 "depends on #2" 로 #2 을(를) 참조',
    context: { generatedBy: 'deterministic', confidence: 'high', generatedAt: now },
    evidence: [{ issue: 1, field: 'body', commentId: null, author: null, authoredAt: null, quote: quote.quote, start: quote.start, end: quote.end, url: issue1.url, digest: digest(body) }],
    status: 'active', schemaVersion: 1, createdBy: 'sync', createdAt: now,
    provenance: { url: issue1.url, digest: digest(body) },
  }];
  graph.snapshot.graphDigest = graphDocumentDigest(graph);
  const live = { openIssues: [issue1, issue2], allIssues: [issue1, issue2], allIssuesComplete: true };
  assert.equal(graphBootstrapReason(graph, live), null);

  for (const change of [
    (edge) => { edge.rationale = 'attacker rationale'; },
    (edge) => { edge.createdBy = 'attacker'; },
    (edge) => { edge.provenance.digest = digest('attacker provenance'); },
  ]) {
    const tampered = structuredClone(graph);
    change(tampered.edges[0]);
    tampered.snapshot.graphDigest = graphDocumentDigest(tampered);
    assert.equal(graphBootstrapReason(tampered, live), 'edge-source-changed');
  }
});

test('an empty complete cache still requires both integrity digests', () => {
  const graph = completeGraph({});
  graph.snapshot.digest = issueSnapshotDigest([]);
  delete graph.snapshot.graphDigest;
  assert.equal(graphBootstrapReason(graph, {
    openIssues: [], allIssues: [], allIssuesComplete: true,
  }), 'invalid');
});

test('default issue-list probing continues past a full page before declaring completeness', () => {
  const calls = [];
  const result = fetchCompleteIssueList({
    issueList({ limit }) {
      calls.push(limit);
      return limit === 200
        ? Array.from({ length: 200 }, (_, number) => ({ number: number + 1 }))
        : [{ number: 1 }];
    },
  });
  assert.deepEqual(calls, [200, 400]);
  assert.equal(result.complete, true);
  assert.equal(result.items.length, 1);
});

test('Jira pagination requires authoritative totals and follows startAt', () => {
  const calls = [];
  const tracker = {
    provider: 'jira',
    issueListPage({ startAt, limit }) {
      calls.push({ startAt, limit });
      if (startAt === 0) return { items: [{ number: 1 }, { number: 2 }], startAt: 0, total: 3, complete: false };
      return { items: [{ number: 3 }], startAt: 2, total: 3, complete: true };
    },
  };
  const result = fetchCompleteIssueList(tracker, { initialLimit: 2, state: 'all' });
  assert.equal(result.complete, true);
  assert.deepEqual(result.items.map((item) => item.number), [1, 2, 3]);
  assert.deepEqual(calls, [{ startAt: 0, limit: 2 }, { startAt: 2, limit: 2 }]);

  const unknown = fetchCompleteIssueList({
    provider: 'jira',
    issueListPage: () => ({ items: [{ number: 1 }], startAt: 0, total: null, complete: false }),
  });
  assert.equal(unknown.complete, false);
});

test('the onboarding CLI bootstraps a complete sync before recommending issues', () => {
  const fixture = cliFixture();
  try {
    const result = runCliOnboard(fixture);
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /GRAPH_BOOTSTRAP=issue-sync/);
    assert.match(result.stdout, /SNAPSHOT_STATUS=complete/);
    assert.match(result.stdout, /GRAPH_SYNC=ok/);
    assert.match(result.stdout, /ONBOARD_COUNT=1/);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test('next and plan use the same live bootstrap validation as onboard', () => {
  const nextFixture = cliFixture();
  const planFixture = cliFixture();
  try {
    const next = runCliMode(nextFixture, 'next');
    assert.equal(next.status, 0, next.stderr);
    assert.match(next.stdout, /GRAPH_BOOTSTRAP=issue-sync/);
    assert.match(next.stdout, /NEXT_ISSUE=1/);

    const plan = runCliMode(planFixture, 'plan', ['--json']);
    assert.equal(plan.status, 0, plan.stderr);
    assert.match(plan.stdout, /GRAPH_BOOTSTRAP=issue-sync/);
    assert.match(plan.stdout, /"ready": \[\s*1\s*\]/);
  } finally {
    rmSync(nextFixture.root, { recursive: true, force: true });
    rmSync(planFixture.root, { recursive: true, force: true });
  }
});

test('the onboarding CLI never recommends after partial or failed sync', () => {
  for (const syncMode of ['partial', 'failed']) {
    const fixture = cliFixture({ syncMode });
    try {
      const result = runCliOnboard(fixture);
      assert.notEqual(result.status, 0, `${syncMode} sync unexpectedly succeeded`);
      assert.doesNotMatch(result.stdout, /ONBOARD_COUNT=/);
      assert.doesNotMatch(result.stderr, /PRIORITY=/);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test('the onboarding CLI rejects a graph that remains stale after a reported-success sync', () => {
  const staleGraph = completeGraph();
  staleGraph.nodes['1'].title = 'Old title';
  staleGraph.nodes['1'].provenance.revision = 'old-revision';
  const fixture = cliFixture({ graph: staleGraph });
  try {
    const result = runCliOnboard(fixture);
    const output = result.stdout + result.stderr;
    assert.notEqual(result.status, 0);
    assert.match(output, /동기화 후 그래프가 최신 열린 이슈와 일치하지 않는다: open-issue-changed/);
    assert.doesNotMatch(output, /ONBOARD_COUNT=/);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test('the onboarding CLI fails closed when the ontology validator is unavailable', () => {
  const fixture = cliFixture({
    initialGraph: completeGraph(),
    ontologyPath: '/definitely-not-present',
  });
  try {
    const result = runCliOnboard(fixture);
    const output = result.stdout + result.stderr;
    assert.notEqual(result.status, 0);
    assert.match(output, /Ajv|온톨로지/);
    assert.doesNotMatch(output, /ONBOARD_COUNT=/);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test('automatic onboarding does not import an ontology outside its trusted installation', () => {
  const outside = mkdtempSync(path.join(os.tmpdir(), 'issue-onboard-ontology-'));
  const marker = path.join(outside, 'ontology-imported');
  writeFileSync(path.join(outside, 'validate.mjs'), `import { writeFileSync } from 'node:fs';
writeFileSync(${JSON.stringify(marker)}, 'imported', 'utf8');
export const ontologyAvailable = true;
export function validateGraphDocument() { return { valid: true, errors: [] }; }
`, 'utf8');
  const fixture = cliFixture({
    initialGraph: completeGraph(),
    ontologyPath: outside,
  });
  try {
    const result = runCliOnboard(fixture);
    assert.notEqual(result.status, 0);
    assert.match(result.stdout + result.stderr, /Ajv|온톨로지/);
    assert.equal(existsSync(marker), false);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
    rmSync(outside, { recursive: true, force: true });
  }
});

test('automatic bootstrap does not execute a repository-local sync outside its trusted installation', () => {
  const fixture = trustedInstallWithRepositoryFallback();
  try {
    const result = runCliOnboard(fixture);
    assert.notEqual(result.status, 0);
    assert.match(result.stdout + result.stderr, /issue-sync/);
    assert.equal(existsSync(fixture.observed), false);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test('automatic bootstrap preserves project-local flavor skill discovery', () => {
  const fixture = trustedInstallWithProjectFlavor();
  try {
    const result = runCliOnboard(fixture);
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /GRAPH_BOOTSTRAP=issue-sync/);
    assert.match(result.stdout, /ONBOARD_COUNT=1/);
    assert.equal(existsSync(fixture.observed), true);
    assert.equal(readFileSync(fixture.observed, 'utf8'), 'absent');
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test('issue-sync does not fall back to a project-local issue-onboard entrypoint', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'issue-sync-trust-'));
  const install = path.join(root, 'trusted-install');
  const syncDir = path.join(install, 'skills', 'issue-sync', 'scripts');
  const projectOnboard = path.join(root, '.codex', 'skills', 'issue-onboard', 'scripts', 'issue-onboard.mjs');
  const marker = path.join(root, 'project-onboard-ran');
  try {
    assert.equal(spawnSync('git', ['init', '--quiet', root], { encoding: 'utf8' }).status, 0);
    mkdirSync(syncDir, { recursive: true });
    cpSync(path.join(repositoryRoot, 'skills', 'issue-sync', 'scripts', 'issue-common.mjs'), path.join(syncDir, 'issue-common.mjs'));
    cpSync(path.join(repositoryRoot, 'skills', 'issue-sync', 'scripts', 'issue-sync.mjs'), path.join(syncDir, 'issue-sync.mjs'));
    mkdirSync(path.dirname(projectOnboard), { recursive: true });
    writeFileSync(projectOnboard, `import { writeFileSync } from 'node:fs';
writeFileSync(${JSON.stringify(marker)}, process.env.GH_TOKEN ?? 'missing', 'utf8');
`, 'utf8');

    const result = spawnSync(process.execPath, [path.join(syncDir, 'issue-sync.mjs')], {
      cwd: root,
      env: { ...process.env, GH_TOKEN: 'must-not-reach-project-onboard' },
      encoding: 'utf8',
    });
    assert.notEqual(result.status, 0);
    assert.match(result.stdout + result.stderr, /issue-onboard/);
    assert.equal(existsSync(marker), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('bootstrap subprocess receives only the explicit environment allowlist', () => {
  const env = bootstrapEnvironment({
    PATH: '/bin',
    GH_TOKEN: 'token-for-gh',
    BOOTSTRAP_SENTINEL: 'must-not-leak',
    NODE_OPTIONS: '--require=attacker-module',
  }, { includeCredentials: true });
  assert.equal(env.GH_TOKEN, 'token-for-gh');
  assert.notEqual(env.PATH, '/bin');
  assert.ok(env.PATH.split(path.delimiter).includes('/bin'));
  assert.doesNotMatch(env.PATH, /attacker/);
  assert.equal(env.NODE_OPTIONS, undefined);
  assert.equal(env.ISSUE_ONBOARD_TEST_FIXTURE_ROOT, undefined);
});

test('project-local bootstrap scripts do not receive provider credentials', () => {
  const env = bootstrapEnvironment({
    PATH: '/bin',
    GH_TOKEN: 'must-not-reach-project-skill',
    JIRA_API_TOKEN: 'must-not-reach-project-skill',
  });
  assert.equal(env.GH_TOKEN, undefined);
  assert.equal(env.JIRA_API_TOKEN, undefined);
  assert.equal(env.PATH, '/usr/bin:/usr/sbin:/bin:/sbin:/System/Cryptexes/App/usr/bin');
});

test('production CLI rejects test-only command directory overrides', () => {
  const fixture = cliFixture();
  try {
    const result = spawnSync(process.execPath, [
      fixture.entry,
      'onboard',
      '--test-command-dir',
      path.join(fixture.root, 'bin'),
    ], {
      cwd: fixture.root,
      env: { ...fixture.env, NODE_OPTIONS: '' },
      encoding: 'utf8',
    });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /알 수 없는 옵션: --test-command-dir/);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test('explicit command environments do not regain ambient variables in the shared runner', () => {
  const key = 'ISSUE_ONBOARD_TEST_UNTRUSTED_SENTINEL';
  const previous = process.env[key];
  process.env[key] = 'must-not-leak';
  try {
    const result = run(process.execPath, ['-e', `process.stdout.write(process.env[${JSON.stringify(key)}] ?? '')`], {
      env: { PATH: process.env.PATH ?? '/bin' },
    });
    assert.equal(result.out, '');
  } finally {
    if (previous === undefined) delete process.env[key];
    else process.env[key] = previous;
  }
});

test('runSyncBootstrap executes the discovered issue-sync entrypoint with bounded output and time', () => {
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
    env: { PATH: '/bin', BOOTSTRAP_SENTINEL: 'must-not-leak' },
  });

  assert.equal(result.status, 0);
  assert.deepEqual(calls, [
    { root: '/repo', skill: 'issue-sync', script: 'issue-sync.mjs' },
    {
      command: process.execPath,
      args: ['/skills/issue-sync/scripts/issue-sync.mjs'],
      options: {
        cwd: '/repo',
        encoding: 'utf8',
        env: bootstrapEnvironment({ PATH: '/bin' }),
        timeout: 120000,
        maxBuffer: 16 * 1024 * 1024,
      },
    },
  ]);
});

test('saveGraph rejects a symlinked .issue directory before writing outside the repository', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'issue-onboard-safe-'));
  const outside = mkdtempSync(path.join(os.tmpdir(), 'issue-onboard-outside-'));
  try {
    symlinkSync(outside, path.join(root, '.issue'), 'dir');
    assert.throws(() => saveGraph(root, completeGraph()), /심볼릭 링크/);
    assert.equal(existsSync(path.join(outside, 'graph.json')), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(outside, { recursive: true, force: true });
  }
});
