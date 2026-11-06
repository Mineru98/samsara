import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { isIssueDone, unblockedSuccessors } from './issue-common.mjs';

/*
 * 이슈 #138. 그래프 없이 close 로 새로 풀린 이슈를 트래커 조회만으로 찾는다.
 * 트래커는 issueList / issueView 만 흉내 낸다.
 */

function fakeTracker(issues, { listFails = false } = {}) {
  const calls = { list: [], view: [] };
  return {
    calls,
    issueList(opts) {
      calls.list.push(opts);
      if (listFails) return null;
      return Object.values(issues).filter((i) => i.state === 'OPEN' && String(i.body ?? '').includes(`#${opts.search.match(/#(\d+)/)[1]}`));
    },
    issueView(n) {
      calls.view.push(n);
      return issues[n] ?? null;
    },
  };
}

const body = (preds) => `## 배경\n\n설명\n\n## 선행 이슈\n\n${preds}\n\n## 후행 이슈\n\n없음\n`;

test('닫은 이슈만 선행으로 둔 열린 이슈는 ready 로 나온다', () => {
  const tracker = fakeTracker({
    10: { number: 10, state: 'CLOSED', labels: [], body: '' },
    20: { number: 20, state: 'OPEN', title: 'B', url: 'u20', labels: [], body: body('- [ ] [#10](https://github.com/o/r/issues/10) A') },
  });
  const r = unblockedSuccessors(tracker, 10);
  assert.equal(r.ok, true);
  assert.deepEqual(r.ready, [20]);
  assert.deepEqual(r.candidates[0].predecessors, [{ number: 10, done: true }]);
  assert.match(tracker.calls.list[0].search, /"#10" in:body/);
  assert.equal(tracker.calls.list[0].state, 'open');
});

test('다른 선행이 남아 있으면 후보지만 ready 가 아니다', () => {
  const tracker = fakeTracker({
    10: { number: 10, state: 'CLOSED', labels: [], body: '' },
    11: { number: 11, state: 'OPEN', labels: [{ name: 'status:review' }], body: '' },
    20: { number: 20, state: 'OPEN', labels: [], body: body('- [x] #10\n- [ ] #11') },
  });
  const r = unblockedSuccessors(tracker, 10);
  assert.deepEqual(r.ready, []);
  assert.deepEqual(r.candidates.map((c) => c.number), [20]);
  assert.deepEqual(r.candidates[0].predecessors, [{ number: 10, done: true }, { number: 11, done: false }]);
});

test('선행 섹션 밖에서만 번호를 언급한 이슈는 후보가 아니다', () => {
  const tracker = fakeTracker({
    10: { number: 10, state: 'CLOSED', labels: [], body: '' },
    30: { number: 30, state: 'OPEN', labels: [], body: '## 참고\n\n- #10 과 비슷함\n\n## 선행 이슈\n\n없음\n' },
  });
  const r = unblockedSuccessors(tracker, 10);
  assert.deepEqual(r.candidates, []);
  assert.deepEqual(r.ready, []);
});

test('status:close 라벨도 끝난 것으로 보고, 조회 실패한 선행은 ready 를 막는다', () => {
  assert.equal(isIssueDone({ state: 'OPEN', labels: [{ name: 'status:close' }] }), true);
  const tracker = fakeTracker({
    10: { number: 10, state: 'OPEN', labels: [{ name: 'status:close' }], body: '' },
    20: { number: 20, state: 'OPEN', labels: [], body: body('- [ ] #10\n- [ ] #404') },
  });
  const r = unblockedSuccessors(tracker, 10);
  assert.deepEqual(r.candidates[0].predecessors, [{ number: 10, done: true }, { number: 404, done: null }]);
  assert.deepEqual(r.ready, []);
});

test('검색이 실패하면 ok:false 로 알린다', () => {
  const r = unblockedSuccessors(fakeTracker({}, { listFails: true }), 10);
  assert.equal(r.ok, false);
  assert.deepEqual(r.ready, []);
});

test('unblocked 명령은 번호 없이 부르면 실패한다', () => {
  const script = path.join(path.dirname(fileURLToPath(import.meta.url)), 'issue-merge.mjs');
  const r = spawnSync(process.execPath, [script, 'unblocked'], { encoding: 'utf8' });
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /--issue/);
});
