import assert from 'node:assert/strict';
import test from 'node:test';

import { decisionCommentBody } from './issue-onboard.mjs';
import { parseDecisionComments, resolveDecisions, decisionEdge } from './issue-graph-v2.mjs';
import { validateGraphDocument } from '../../../tools/issue-ontology/validate.mjs';

// cmdLink 가 게시하는 승인 코멘트가 sync 의 파서/해소기를 그대로 통과해 depends-on
// 엣지가 되는지 확인한다. 이 커플링이 깨지면 link 로 만든 관계가 그래프에 안 뜬다.
const approvePayload = {
  version: 1,
  id: 'relation-9-5-depends-on',
  action: 'relation',
  decision: 'approved',
  type: 'depends-on',
  from: 9,
  to: 5,
  graphRevision: `sha256:${'a'.repeat(64)}`,
  rationale: '착수 순서 강제',
  evidence: ['https://github.com/o/r/issues/9', 'https://github.com/o/r/issues/5'],
};

const comment = (body, when) => ({ id: 'c1', body, url: 'https://github.com/o/r/issues/9#issuecomment-1', author: { login: 'octocat' }, createdAt: when, updatedAt: when });

test('cmdLink approval comment round-trips into a valid depends-on edge', () => {
  const body = decisionCommentBody(approvePayload, '관계 승인: #9 --depends-on--> #5');
  const decisions = parseDecisionComments([comment(body, '2026-08-26T00:00:00Z')]);
  assert.equal(decisions.length, 1);

  const approved = resolveDecisions(decisions);
  assert.equal(approved.length, 1);

  const edge = decisionEdge(approved[0]);
  assert.ok(edge, 'decisionEdge 가 엣지를 만들어야 한다');
  assert.equal(edge.from, 9);
  assert.equal(edge.to, 5);
  assert.equal(edge.type, 'depends-on');
  assert.equal(edge.createdBy, 'decision');
  assert.equal(edge.decisionId, 'relation-9-5-depends-on');

  // 만들어진 결정 엣지가 스키마도 통과해야 한다.
  const contextValue = { value: 'unknown', reason: 't', source: 't' };
  const node = (n) => ({
    id: `github:o/r#${n}`, number: n, title: `#${n}`, status: 'open', labels: [], url: `https://github.com/o/r/issues/${n}`,
    context: Object.fromEntries(['problem', 'outcome', 'scope', 'acceptance', 'result', 'components', 'decisions', 'evidence'].map((f) => [f, contextValue])),
    provenance: { url: `https://github.com/o/r/issues/${n}`, revision: 'r', observedAt: '2026-08-26T00:00:00.000Z' },
  });
  const graph = {
    version: 2, provider: 'github', repository: 'o/r', updatedAt: '2026-08-26T00:00:00.000Z',
    snapshot: { status: 'complete', fetchedAt: '2026-08-26T00:00:00.000Z', digest: `sha256:${'a'.repeat(64)}`, graphDigest: `sha256:${'b'.repeat(64)}`, reason: null },
    nodes: { 5: node(5), 9: node(9) }, edges: [edge],
  };
  assert.deepEqual(validateGraphDocument(graph).errors, []);
});

test('cmdUnlink revoke comment drops the edge on the next resolve', () => {
  const approveBody = decisionCommentBody(approvePayload, 'approve');
  const revokeBody = decisionCommentBody(
    { version: 1, id: 'relation-9-5-depends-on', action: 'relation', decision: 'revoked', type: 'depends-on', from: 9, to: 5 },
    'revoke',
  );
  const decisions = parseDecisionComments([
    comment(approveBody, '2026-08-26T00:00:00Z'),
    comment(revokeBody, '2026-08-27T00:00:00Z'), // 더 최신 → revoke 가 이긴다
  ]);
  const approved = resolveDecisions(decisions);
  assert.deepEqual(approved, [], 'revoke 가 최신이면 승인 엣지가 사라져야 한다');
});
