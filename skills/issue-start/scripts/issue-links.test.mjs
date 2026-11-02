import assert from 'node:assert/strict';
import test from 'node:test';
import {
  parseLinks, addPredecessor, addSuccessor, checkPredecessor, isIssueDone,
  prereqStatus, linkIssues, markSuccessors, validateLinkTargets, applyOwnLinks,
} from './issue-links.mjs';

/* 이슈 #98 — 본문 선행·후행 연결 파싱·갱신과 트래커 흐름 */

const TEMPLATE = [
  '## 배경', '', '내용', '',
  '## 완료 기준', '', '- [ ] 테스트 통과', '',
  '## 선행 이슈', '', '없음', '',
  '## 후행 이슈', '', '없음', '',
  '## 참고', '', '없음', '',
].join('\n');

function fakeTracker(issues) {
  const store = new Map(Object.entries(issues).map(([n, v]) => [Number(n), { labels: [], ...v }]));
  const edits = [];
  return {
    edits,
    body: (n) => store.get(n).body,
    issueView: (n) => (store.has(Number(n)) ? { number: Number(n), ...store.get(Number(n)) } : null),
    issueEditBody(n, body) {
      edits.push(Number(n));
      store.get(Number(n)).body = body;
      return { ok: true };
    },
  };
}

test('본문에 연결 섹션이 없으면 빈 목록이고 아무것도 바꾸지 않는다 (하위 호환)', () => {
  const body = '## 배경\n\n내용\n\n## 완료 기준\n\n- [ ] #3 처럼 보이는 완료 기준\n';
  assert.deepEqual(parseLinks(body), { predecessors: [], successors: [] });
  assert.equal(checkPredecessor(body, 3).changed, false);
  assert.equal(checkPredecessor(body, 3).found, false);
});

test('선행 체크리스트와 후행 목록을 읽는다', () => {
  const body = '## 선행 이슈\n\n- [ ] #12\n- [x] #9 API 먼저\n- [X] #7\n\n## 후행 이슈\n\n- #15\n* #16\n';
  assert.deepEqual(parseLinks(body), {
    predecessors: [{ number: 12, checked: false }, { number: 9, checked: true }, { number: 7, checked: true }],
    successors: [15, 16],
  });
});

test('코드 펜스 안의 섹션 예시는 연결로 읽지 않는다', () => {
  const body = '## 참고\n\n```markdown\n## 선행 이슈\n\n- [ ] #1\n```\n';
  assert.deepEqual(parseLinks(body).predecessors, []);
});

test('템플릿의 없음 자리표시를 지우고 항목을 넣는다', () => {
  let { body, changed } = addPredecessor(TEMPLATE, 12);
  assert.equal(changed, true);
  ({ body } = addPredecessor(body, 9, { done: true }));
  ({ body } = addSuccessor(body, 15));
  assert.match(body, /## 선행 이슈\n\n- \[ \] #12\n- \[x\] #9\n\n## 후행 이슈\n\n- #15\n\n## 참고/);
  assert.deepEqual(parseLinks(body), {
    predecessors: [{ number: 12, checked: false }, { number: 9, checked: true }],
    successors: [15],
  });
});

test('섹션이 없으면 참고 앞에 선행, 그 뒤에 후행 섹션을 만든다', () => {
  const base = '## 배경\n\n내용\n\n## 참고\n\n없음\n';
  const { body } = addSuccessor(addPredecessor(base, 3).body, 5);
  assert.equal(body, '## 배경\n\n내용\n\n## 선행 이슈\n\n- [ ] #3\n\n## 후행 이슈\n\n- #5\n\n## 참고\n\n없음\n');
});

test('참고 섹션도 없으면 본문 끝에 붙인다', () => {
  const { body } = addPredecessor('## 배경\n\n내용\n\n', 3);
  assert.equal(body, '## 배경\n\n내용\n\n## 선행 이슈\n\n- [ ] #3');
});

test('추가는 멱등이고, 이미 있는 선행은 done 일 때만 체크한다', () => {
  const once = addPredecessor(TEMPLATE, 12).body;
  assert.equal(addPredecessor(once, 12).changed, false);
  const checked = addPredecessor(once, 12, { done: true });
  assert.equal(checked.changed, true);
  assert.equal(addPredecessor(checked.body, 12, { done: false }).changed, false, '체크를 풀지 않는다');
  const succ = addSuccessor(TEMPLATE, 15).body;
  assert.equal(addSuccessor(succ, 15).changed, false);
});

test('checkPredecessor 는 해당 항목만 체크하고 다시 돌려도 같다', () => {
  const body = '## 선행 이슈\n\n- [ ] #12\n- [ ] #13\n';
  const first = checkPredecessor(body, 12);
  assert.equal(first.body, '## 선행 이슈\n\n- [x] #12\n- [ ] #13\n');
  const second = checkPredecessor(first.body, 12);
  assert.equal(second.changed, false);
  assert.equal(second.body, first.body);
});

test('CRLF 본문은 줄바꿈을 보존한다', () => {
  const body = '## 선행 이슈\r\n\r\n- [ ] #4\r\n';
  assert.equal(checkPredecessor(body, 4).body, '## 선행 이슈\r\n\r\n- [x] #4\r\n');
});

test('끝난 이슈 = CLOSED 또는 status:close', () => {
  assert.equal(isIssueDone({ state: 'CLOSED', labels: [] }), true);
  assert.equal(isIssueDone({ state: 'OPEN', labels: [{ name: 'status:close' }] }), true);
  assert.equal(isIssueDone({ state: 'OPEN', labels: [{ name: 'status:review' }] }), false);
  assert.equal(isIssueDone(null), false);
});

test('validateLinkTargets 는 자기 자신과 양쪽 중복을 거절한다', () => {
  assert.deepEqual(validateLinkTargets(5, { after: ['#3', 3, '4'], before: [] }), { after: [3, 4], before: [] });
  assert.throws(() => validateLinkTargets(5, { after: [5] }), /자기 자신/);
  assert.throws(() => validateLinkTargets(5, { after: [3], before: [3] }), /선행이면서 후행/);
  assert.throws(() => validateLinkTargets(5, { after: ['x'] }), /잘못된 이슈 번호/);
});

test('applyOwnLinks — 닫힌 선행은 [x], 열린 선행은 [ ]', () => {
  const body = applyOwnLinks(TEMPLATE, { after: [1, 2], before: [9] }, {
    1: { state: 'CLOSED' }, 2: { state: 'OPEN' },
  });
  assert.deepEqual(parseLinks(body), {
    predecessors: [{ number: 1, checked: true }, { number: 2, checked: false }],
    successors: [9],
  });
});

test('prereqStatus — 미완료 선행과 조회 실패를 pending 으로 낸다', () => {
  const tracker = fakeTracker({
    10: { state: 'OPEN', body: '## 선행 이슈\n\n- [x] #1\n- [ ] #2\n- [ ] #3\n- [ ] #4\n' },
    1: { state: 'CLOSED', body: '' },
    2: { state: 'OPEN', body: '', labels: [{ name: 'status:in-process' }] },
    3: { state: 'OPEN', body: '', labels: [{ name: 'status:close' }] },
  });
  const r = prereqStatus(tracker, 10);
  assert.equal(r.ok, true);
  assert.deepEqual(r.pending, [2, 4]);
  assert.equal(r.predecessors.find((p) => p.number === 2).status, 'status:in-process');
  assert.equal(r.predecessors.find((p) => p.number === 4).error, '조회 실패');
});

test('prereqStatus — 연결 섹션이 없으면 확인할 선행이 없다', () => {
  const tracker = fakeTracker({ 10: { state: 'OPEN', body: '## 배경\n\n내용\n' } });
  assert.deepEqual(prereqStatus(tracker, 10).pending, []);
});

test('linkIssues — 양방향으로 잇고, 다시 돌려도 쓰지 않는다', () => {
  const tracker = fakeTracker({
    20: { state: 'OPEN', body: TEMPLATE },
    11: { state: 'CLOSED', body: '## 배경\n\n선행\n' },
    30: { state: 'OPEN', body: TEMPLATE },
  });
  const r = linkIssues(tracker, 20, { after: [11], before: [30] });
  assert.equal(r.ok, true);
  assert.deepEqual(parseLinks(tracker.body(20)), { predecessors: [{ number: 11, checked: true }], successors: [30] });
  assert.deepEqual(parseLinks(tracker.body(11)).successors, [20]);
  assert.deepEqual(parseLinks(tracker.body(30)).predecessors, [{ number: 20, checked: false }]);
  const writes = tracker.edits.length;
  const again = linkIssues(tracker, 20, { after: [11], before: [30] });
  assert.equal(again.edits.every((e) => !e.changed), true);
  assert.equal(tracker.edits.length, writes);
});

test('linkIssues — 없는 이슈가 있으면 아무것도 쓰지 않는다', () => {
  const tracker = fakeTracker({ 20: { state: 'OPEN', body: '' } });
  const r = linkIssues(tracker, 20, { after: [404] });
  assert.equal(r.ok, false);
  assert.match(r.error, /#404/);
  assert.equal(tracker.edits.length, 0);
});

test('linkIssues dry-run 은 쓰지 않고 바뀔 이슈만 알린다', () => {
  const tracker = fakeTracker({ 20: { state: 'OPEN', body: '' }, 11: { state: 'OPEN', body: '' } });
  const r = linkIssues(tracker, 20, { after: [11] }, { dryRun: true });
  assert.deepEqual(r.edits.map((e) => [e.issue, e.changed]), [[20, true], [11, true]]);
  assert.equal(tracker.edits.length, 0);
});

test('linkIssues includeSelf=false 는 자기 본문을 건드리지 않는다 (create 직후)', () => {
  const tracker = fakeTracker({ 20: { state: 'OPEN', body: 'own' }, 11: { state: 'OPEN', body: '' } });
  linkIssues(tracker, 20, { after: [11] }, { includeSelf: false });
  assert.equal(tracker.body(20), 'own');
  assert.deepEqual(tracker.edits, [11]);
});

test('markSuccessors — 후행 본문의 선행 항목을 [x] 로, 재실행은 무변경', () => {
  const tracker = fakeTracker({
    11: { state: 'CLOSED', body: '## 후행 이슈\n\n- #20\n- #21\n' },
    20: { state: 'OPEN', body: '## 선행 이슈\n\n- [ ] #11\n- [ ] #12\n' },
    21: { state: 'OPEN', body: '## 배경\n\n한쪽만 이어짐\n' },
  });
  const r = markSuccessors(tracker, 11);
  assert.equal(r.ok, true);
  assert.equal(tracker.body(20), '## 선행 이슈\n\n- [x] #11\n- [ ] #12\n');
  assert.deepEqual(parseLinks(tracker.body(21)).predecessors, [{ number: 11, checked: true }]);
  const writes = tracker.edits.length;
  const again = markSuccessors(tracker, 11);
  assert.equal(again.successors.every((s) => !s.changed && s.ok), true);
  assert.equal(tracker.edits.length, writes);
});

test('markSuccessors — 후행이 없으면 아무것도 하지 않는다', () => {
  const tracker = fakeTracker({ 11: { state: 'CLOSED', body: '## 배경\n' } });
  assert.deepEqual(markSuccessors(tracker, 11), { ok: true, successors: [] });
});

test('### 레벨 섹션도 읽고 갱신하며 중복 섹션을 만들지 않는다', () => {
  const body = '### 선행 이슈\n\n- [ ] #1\n\n#### 메모\n\n- #9 는 아님\n\n## 참고\n';
  assert.deepEqual(parseLinks(body).predecessors, [{ number: 1, checked: false }]);
  const next = addPredecessor(body, 4).body;
  assert.equal((next.match(/선행 이슈/g) ?? []).length, 1);
  assert.deepEqual(parseLinks(next).predecessors.map((p) => p.number), [1, 4]);
});

test('번호는 숫자·#숫자·Jira 키만 받는다', () => {
  assert.deepEqual(validateLinkTargets(5, { after: ['ACME-3', '#4'] }).after, [3, 4]);
  assert.throws(() => validateLinkTargets(5, { after: ['1e3'] }), /잘못된 이슈 번호/);
  assert.throws(() => validateLinkTargets(5, { after: ['0x10'] }), /잘못된 이슈 번호/);
});
