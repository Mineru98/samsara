import assert from 'node:assert/strict';
import { readFileSync, mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  assertAction,
  loadOntology,
  validateActionDocument,
  validateGraphDocument,
} from './validate.mjs';
import {
  EDGE_KINDS,
  EDGE_TYPES,
  validateGraphV2,
} from '../../skills/issue-onboard/scripts/issue-graph-v2.mjs';
import { readyFact } from '../../skills/issue-start/scripts/issue-start.mjs';
import { resolveSkillScript } from '../../skills/issue-create/scripts/issue-common.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const fixture = (name) => JSON.parse(readFileSync(
  path.join(root, 'tools/issue-ontology/fixtures', name),
  'utf8',
));

test('valid graph passes Ajv and the schema contains the live snapshot keys', () => {
  const graph = fixture('graph-valid.json');
  const result = validateGraphDocument(graph);
  assert.equal(result.valid, true);
  assert.deepEqual(Object.keys(graph.snapshot).sort(), ['digest', 'fetchedAt', 'reason', 'status']);
});

test('Ajv rejects blocks and unknown graph root properties', () => {
  const graph = fixture('graph-valid.json');
  graph.edges[0].type = 'blocks';
  assert.equal(validateGraphDocument(graph).valid, false);

  const extra = fixture('graph-valid.json');
  extra.pr = {};
  assert.equal(validateGraphDocument(extra).valid, false);
});

test('the walker rejects a parent-of cycle after Ajv shape validation', () => {
  const graph = fixture('graph-parent-cycle.json');
  assert.equal(validateGraphDocument(graph).valid, true);
  assert.ok(validateGraphV2(graph).some((problem) => problem.includes('parent-of 순환')));
});

test('the schema edge enum stays aligned with the onboard walker', () => {
  const schema = loadOntology().ajv.getSchema(
    'https://samsara.local/schemas/graph-v2.schema.json',
  );
  assert.ok(schema);
  const edgeSchema = schema.schema.$defs.edge.properties.type;
  assert.deepEqual(edgeSchema.enum, EDGE_TYPES);
});

test('the schema edge kind enum stays aligned with the onboard walker', () => {
  const schema = loadOntology().ajv.getSchema(
    'https://samsara.local/schemas/graph-v2.schema.json',
  );
  const kindSchema = schema.schema.$defs.edge.properties.kind;
  assert.deepEqual([...kindSchema.enum].sort(), [...EDGE_KINDS].sort());
});

// 회귀 방어: cmdSync 가 실제로 써 넣는 풍부한 엣지 shape 와 최상위 staleEdges 를
// 스키마가 그대로 받아들여야 한다. (예전에는 kind/context/evidence/schemaVersion/
// cacheKey/status:active 와 staleEdges 가 스키마에 없어 sync 결과가 Ajv 를 통과하지
// 못했다.) 이 shape 는 issue-onboard.mjs cmdSync + issue-llm.mjs applyEnrichment 산출물.
test('Ajv accepts the rich sync edge shape and top-level staleEdges', () => {
  const contextValue = (value) => ({ value, reason: 'test', source: 'test' });
  const node = (number) => ({
    id: `github:Mineru98/samsara#${number}`,
    number,
    title: `#${number}`,
    status: 'open',
    labels: [],
    url: `https://github.com/Mineru98/samsara/issues/${number}`,
    context: {
      problem: contextValue('unknown'),
      outcome: contextValue('unknown'),
      scope: contextValue('unknown'),
      acceptance: contextValue('unknown'),
      result: contextValue('unknown'),
      components: contextValue('unknown'),
      decisions: contextValue('unknown'),
      evidence: contextValue('unknown'),
    },
    provenance: {
      url: `https://github.com/Mineru98/samsara/issues/${number}`,
      revision: '2026-08-26T00:00:00Z',
      observedAt: '2026-08-26T00:00:00.000Z',
    },
  });
  const digest = `sha256:${'a'.repeat(64)}`;
  const richSyncEdge = {
    from: 6,
    to: 5,
    type: 'depends-on',
    kind: 'blocked-by',
    rationale: '#6 본문이 "depends on #5" 로 #5 을(를) 참조',
    context: {
      summary: '#6 본문이 "depends on #5" 로 #5 을(를) 참조',
      label: 'depends on #5',
      keywords: ['auth'],
      sharedConcepts: ['auth'],
      generatedBy: 'llm',
      model: 'haiku',
      promptVersion: 1,
      confidence: 'high',
      generatedAt: '2026-08-26T00:00:00.000Z',
    },
    evidence: [{
      issue: 6,
      field: 'body',
      commentId: null,
      author: 'octocat',
      authoredAt: '2026-08-26T00:00:00Z',
      quote: 'depends on #5',
      start: 0,
      end: 13,
      url: 'https://github.com/Mineru98/samsara/issues/6',
      digest,
    }],
    status: 'active',
    schemaVersion: 1,
    createdBy: 'sync',
    createdAt: '2026-08-26T00:00:00.000Z',
    cacheKey: `sha256:${'b'.repeat(64)}`,
    provenance: { url: 'https://github.com/Mineru98/samsara/issues/6', digest },
  };
  const graph = {
    version: 2,
    provider: 'github',
    repository: 'Mineru98/samsara',
    updatedAt: '2026-08-26T00:00:00.000Z',
    snapshot: { status: 'complete', fetchedAt: '2026-08-26T00:00:00.000Z', digest, reason: null },
    nodes: { 5: node(5), 6: node(6) },
    edges: [richSyncEdge],
    staleEdges: [{ ...richSyncEdge, from: 5, to: 6, status: 'stale', staleAt: '2026-08-25T00:00:00.000Z' }],
  };
  const result = validateGraphDocument(graph);
  assert.deepEqual(result.errors, []);
  assert.equal(result.valid, true);
});

// 회귀 방어: GitHub 네이티브 의존성(blockedBy)에서 파생한 엣지 shape 도 통과해야 한다.
test('Ajv accepts a github-native dependency edge', () => {
  const graph = fixture('graph-valid.json');
  graph.edges.push({
    from: 101,
    to: 102,
    type: 'depends-on',
    kind: 'blocked-by',
    rationale: '#101 은 GitHub 네이티브 의존성에서 #102 에 blocked-by',
    context: { generatedBy: 'github-native', confidence: 'high', generatedAt: '2026-08-26T00:00:00.000Z' },
    evidence: [],
    status: 'active',
    schemaVersion: 1,
    createdBy: 'github-native',
    createdAt: '2026-08-26T00:00:00.000Z',
    provenance: { url: 'https://github.com/Mineru98/samsara/issues/101', digest: `sha256:${'e'.repeat(64)}` },
  });
  assert.equal(validateGraphDocument(graph).valid, true);
});

test('start requires an open tracker issue and a ready result when checked', () => {
  const base = {
    gitRepo: true,
    trackerAuth: true,
    issueExists: true,
    trackerStateOpen: true,
  };
  assert.doesNotThrow(() => assertAction('start', { ...base, readyChecked: false }));
  assert.doesNotThrow(() => assertAction('start', {
    ...base,
    readyChecked: true,
    ready: true,
  }));
  assert.throws(() => assertAction('start', { ...base, readyChecked: true }), /schema validation failed/);
  assert.throws(() => assertAction('start', {
    ...base,
    trackerStateOpen: false,
    readyChecked: false,
  }), /schema validation failed/);
});

test('create has only git and tracker preconditions', () => {
  const schema = JSON.parse(readFileSync(
    path.join(root, 'tools/issue-ontology/schemas/actions/create.schema.json'),
    'utf8',
  ));
  assert.equal(Object.hasOwn(schema.properties, 'issue'), false);
  assert.equal(validateActionDocument({
    action: 'create',
    observed: { gitRepo: true, trackerAuth: true },
  }).valid, true);
  assert.throws(() => assertAction('create', {
    gitRepo: true,
    trackerAuth: false,
  }), /schema validation failed/);
});

test('end and merge require complete before and after evidence', () => {
  const end = { gitRepo: true, trackerAuth: true, issueExists: true, evidenceComplete: false };
  assert.throws(() => assertAction('end', end), /schema validation failed/);
  assert.doesNotThrow(() => assertAction('end', { ...end, evidenceComplete: true }));

  const merge = {
    action: 'merge',
    graphPresent: true,
    observed: { gitRepo: true, trackerAuth: true, issueExists: true, evidenceComplete: false },
  };
  assert.throws(() => assertAction(merge), /schema validation failed/);
  assert.doesNotThrow(() => assertAction({
    ...merge,
    observed: { ...merge.observed, evidenceComplete: true },
  }));
});

test('missing island is an explicit skip for the human end guard', () => {
  const empty = mkdtempSync(path.join(tmpdir(), 'issue-ontology-'));
  const script = path.join(root, 'skills/issue-end/scripts/issue-end.mjs');
  const result = spawnSync(process.execPath, [script, 'ontology-guard', '--skip-ok'], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, ISSUE_ONTOLOGY_ROOT: empty },
  });
  assert.equal(result.status, 0);
  assert.match(result.stdout, /ONTOLOGY_SKIPPED=1/);
});

test('sibling skills resolve from the plugin root without hidden install directories', () => {
  const script = resolveSkillScript('', 'issue-end', 'issue-end.mjs', { root });
  assert.equal(script, path.join(root, 'skills/issue-end/scripts/issue-end.mjs'));
});

test('a failed sibling plan probe keeps start ready unchecked', () => {
  const isolated = mkdtempSync(path.join(tmpdir(), 'issue-start-'));
  mkdirSync(path.join(isolated, '.issue'), { recursive: true });
  writeFileSync(path.join(isolated, '.issue', 'graph.json'), '{}');
  assert.deepEqual(readyFact(101, isolated), { readyChecked: false });
});
