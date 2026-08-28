import assert from 'node:assert/strict';
import test from 'node:test';

import { fetchBlockedBy, splitSlug } from './issue-native-deps.mjs';
import { buildNativeEdge, classify, collectNativeDependencies, findCycle } from './issue-onboard.mjs';
import { validateGraphDocument } from '../../../tools/issue-ontology/validate.mjs';

const ok = (nodes) => ({ status: 0, stdout: JSON.stringify({ data: { repository: { issue: { blockedBy: { nodes, pageInfo: { hasNextPage: false } } } } } }), stderr: '' });

test('splitSlug parses owner/repo and rejects garbage', () => {
  assert.deepEqual(splitSlug('Mineru98/samsara'), { owner: 'Mineru98', repo: 'samsara' });
  assert.equal(splitSlug('nope'), null);
  assert.equal(splitSlug(''), null);
  assert.equal(splitSlug(null), null);
});

test('fetchBlockedBy returns predecessor numbers from GraphQL nodes', () => {
  const runner = () => ok([{ number: 5 }, { number: 7 }, { number: 0 }, { number: 'x' }]);
  const res = fetchBlockedBy({ owner: 'o', repo: 'r', number: 9, runner });
  assert.deepEqual(res, { numbers: [5, 7] });
});

test('fetchBlockedBy flags unsupported when the field is missing', () => {
  const runner = () => ({ status: 1, stdout: '', stderr: "Field 'blockedBy' doesn't exist on type 'Issue'" });
  assert.deepEqual(fetchBlockedBy({ owner: 'o', repo: 'r', number: 9, runner }), { unsupported: true });

  const gqlErr = () => ({ status: 0, stdout: JSON.stringify({ errors: [{ message: "Field 'blockedBy' doesn't exist" }] }), stderr: '' });
  assert.deepEqual(fetchBlockedBy({ owner: 'o', repo: 'r', number: 9, runner: gqlErr }), { unsupported: true });
});

test('fetchBlockedBy returns null on transient failure and bad args', () => {
  assert.equal(fetchBlockedBy({ owner: 'o', repo: 'r', number: 9, runner: () => ({ status: 1, stdout: '', stderr: 'network' }) }), null);
  assert.equal(fetchBlockedBy({ owner: 'o', repo: 'r', number: 9, runner: () => ({ status: 0, stdout: 'not json', stderr: '' }) }), null);
  assert.equal(fetchBlockedBy({ owner: 'o', repo: 'r', number: 9, runner: () => ({ status: 0, stdout: JSON.stringify({ data: { repository: { issue: { blockedBy: { nodes: [] } } } } }), stderr: '' }) }), null);
  assert.equal(fetchBlockedBy({ owner: '', repo: 'r', number: 9 }), null);
  assert.equal(fetchBlockedBy({ owner: 'o', repo: 'r', number: 1.5 }), null);
});

test('collectNativeDependencies builds from->to (blocked-by) candidates and honors seen', () => {
  const list = [{ number: 9 }, { number: 10 }];
  const fetch = ({ number }) => (number === 9 ? { numbers: [5, 9] } : { numbers: [5] });
  const seen = new Set(['10|5|depends-on']); // 본문 마커가 이미 만든 엣지는 건너뛴다
  const { candidates, stats } = collectNativeDependencies({ list, seen, owner: 'o', repo: 'r', fetch });
  // 9->5 만 남는다: 9->9 는 self, 10->5 는 seen.
  assert.deepEqual(candidates, [{ from: 9, to: 5 }]);
  assert.equal(stats.queried, 2);
  assert.equal(stats.edges, 1);
});

test('collectNativeDependencies skips cleanly when repo unknown or API unsupported', () => {
  assert.equal(collectNativeDependencies({ list: [{ number: 1 }], owner: '', repo: '' }).stats.skipped, 'repo-unknown');

  const unsupported = collectNativeDependencies({ list: [{ number: 1 }], owner: 'o', repo: 'r', fetch: () => ({ unsupported: true }) });
  assert.equal(unsupported.stats.skipped, 'api-unsupported');
  assert.deepEqual(unsupported.candidates, []);

  const flaky = collectNativeDependencies({ list: [{ number: 1 }, { number: 2 }], owner: 'o', repo: 'r', fetch: () => null });
  assert.equal(flaky.stats.skipped, 'unavailable');
});

test('collectNativeDependencies marks any later node failure as incomplete', () => {
  const calls = [];
  const result = collectNativeDependencies({
    list: [{ number: 1 }, { number: 2 }],
    owner: 'o',
    repo: 'r',
    fetch: ({ number }) => {
      calls.push(number);
      return number === 1 ? { numbers: [] } : null;
    },
  });
  assert.deepEqual(calls, [1, 2]);
  assert.equal(result.stats.queried, 1);
  assert.equal(result.stats.skipped, 'unavailable');
});

test('buildNativeEdge produces a schema-valid depends-on edge', () => {
  const now = '2026-08-26T00:00:00.000Z';
  const edge = buildNativeEdge({ from: 9, to: 5, toClosed: true, url: 'https://github.com/o/r/issues/9', now });
  assert.equal(edge.type, 'depends-on');
  assert.equal(edge.createdBy, 'github-native');
  assert.equal(edge.status, 'resolved'); // to 가 닫혀 있으면 resolved

  const contextValue = { value: 'unknown', reason: 't', source: 't' };
  const node = (n) => ({
    id: `github:o/r#${n}`, number: n, title: `#${n}`, status: 'open', labels: [], url: `https://github.com/o/r/issues/${n}`,
    context: Object.fromEntries(['problem', 'outcome', 'scope', 'acceptance', 'result', 'components', 'decisions', 'evidence'].map((f) => [f, contextValue])),
    provenance: { url: `https://github.com/o/r/issues/${n}`, revision: 'r', observedAt: now },
  });
  const graph = {
    version: 2, provider: 'github', repository: 'o/r', updatedAt: now,
    snapshot: { status: 'complete', fetchedAt: now, digest: `sha256:${'a'.repeat(64)}`, graphDigest: `sha256:${'b'.repeat(64)}`, reason: null },
    nodes: { 5: node(5), 9: node(9) }, edges: [edge],
  };
  assert.deepEqual(validateGraphDocument(graph).errors, []);
});

// 선수/후속 연결이 실제로 ready/blocked 스케줄링을 움직이는지 (end-to-end 그래프 계층).
// 9 --depends-on--> 5 : 5 가 선수. 5 가 열려 있으면 9 는 blocked, 5 는 ready.
test('a depends-on edge blocks the successor until the predecessor closes', () => {
  const now = '2026-08-26T00:00:00.000Z';
  const node = (n, status) => ({ number: n, title: `#${n}`, status, labels: [] });
  const edge = buildNativeEdge({ from: 9, to: 5, toClosed: false, url: null, now });

  const openGraph = { nodes: { 5: node(5, 'open'), 9: node(9, 'open') }, edges: [edge] };
  let c = classify(openGraph);
  assert.deepEqual(c.blocked.map((b) => b.num), [9]);
  assert.deepEqual(c.blocked[0].blockers, [5]);
  assert.deepEqual(c.ready, [5]);

  // 선수(5)가 닫히면 후속(9)이 ready 로 풀린다.
  const closedGraph = { nodes: { 5: node(5, 'close'), 9: node(9, 'open') }, edges: [edge] };
  c = classify(closedGraph);
  assert.deepEqual(c.ready, [9]);
  assert.deepEqual(c.done, [5]);
  assert.deepEqual(c.blocked, []);
});

test('findCycle rejects a predecessor/successor cycle', () => {
  const now = '2026-08-26T00:00:00.000Z';
  const acyclic = { nodes: { 5: { number: 5, status: 'open' }, 9: { number: 9, status: 'open' } }, edges: [buildNativeEdge({ from: 9, to: 5, now })] };
  assert.equal(findCycle(acyclic), null);

  const cyclic = { nodes: { 5: { number: 5, status: 'open' }, 9: { number: 9, status: 'open' } }, edges: [buildNativeEdge({ from: 9, to: 5, now }), buildNativeEdge({ from: 5, to: 9, now })] };
  const cycle = findCycle(cyclic);
  assert.ok(Array.isArray(cycle) && cycle.length >= 2);
});
