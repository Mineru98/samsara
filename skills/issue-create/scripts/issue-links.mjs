// !!! VENDORED FILE — issue-create / issue-start / issue-merge 사본이 바이트 단위로 같아야 한다 !!!
// verify: sh scripts/check-shared.sh
/**
 * issue-links.mjs — 이슈 본문의 선행·후행 연결 (이슈 #98).
 *
 * 본문 형식 계약
 *
 *   ## 선행 이슈            이 이슈보다 먼저 끝나야 하는 이슈. 체크리스트
 *   - [ ] #12               아직 안 끝남
 *   - [x] #9                끝남 (issue-merge close 가 자동으로 체크한다)
 *
 *   ## 후행 이슈            이 이슈가 끝나야 진행할 수 있는 이슈. 목록
 *   - #15
 *
 * 연결은 양방향이다. X 의 선행에 P 가 있으면 P 의 후행에 X 가 있다.
 * 섹션이 없는 본문은 연결이 없는 것으로 읽고, 아무것도 바꾸지 않는다(하위 호환).
 *
 * 순수 함수(parse/add/check)와 트래커를 주입받는 함수(prereqStatus/linkIssues/markSuccessors)로 나뉜다.
 * 트래커는 issueView(number) → { state, body, labels } 와 issueEditBody(number, body) → { ok, err } 만 쓴다.
 *
 * 의존성 없음. Node 18+.
 */

export const PRED_HEADING = '선행 이슈';
export const SUCC_HEADING = '후행 이슈';

const PRED_ITEM = /^\s*[-*]\s+\[( |x|X)\]\s+#(\d+)\b/;
const SUCC_ITEM = /^\s*[-*]\s+#(\d+)\b/;
const PLACEHOLDER = /^\s*(?:[-*]\s+)?(?:없음|\(없음\)|none|n\/a)\s*$/i;
const FENCE = /^\s*(```|~~~)/;
const HEADING = /^(#{1,6})\s/;

function splitBody(body) {
  const text = String(body ?? '');
  const eol = text.includes('\r\n') ? '\r\n' : '\n';
  return { eol, lines: text.split(/\r?\n/) };
}

/**
 * 코드 펜스 밖의 `## <heading>` 섹션 범위(`###`·`####` 도 인정). 없으면 null.
 * end 는 같거나 높은 레벨의 다음 헤딩 줄(또는 lines.length).
 */
function findSection(lines, heading) {
  const target = new RegExp(`^(#{2,4})\\s+${heading}\\s*$`);
  let fenced = false;
  let start = -1;
  let level = 0;
  for (let i = 0; i < lines.length; i += 1) {
    if (FENCE.test(lines[i])) { fenced = !fenced; continue; }
    if (fenced) continue;
    if (start === -1) {
      const m = lines[i].match(target);
      if (m) { start = i; level = m[1].length; }
    } else {
      const h = lines[i].match(HEADING);
      if (h && h[1].length <= level) return { start, end: i };
    }
  }
  return start === -1 ? null : { start, end: lines.length };
}

function findHeading(lines, heading) {
  return findSection(lines, heading)?.start ?? -1;
}

function sectionItems(lines, section, pattern) {
  if (!section) return [];
  const items = [];
  for (let i = section.start + 1; i < section.end; i += 1) {
    const m = lines[i].match(pattern);
    if (m) items.push({ index: i, match: m });
  }
  return items;
}

/** `12` · `#12` · Jira 키 `ACME-12` 를 번호로. 그 외(`1e3`, `0x10` 등)는 null. */
const toNumber = (value) => {
  const s = String(value ?? '').trim();
  const m = s.match(/^#?(\d{1,6})$/) ?? s.match(/^[A-Za-z][A-Za-z0-9_]*-(\d{1,6})$/);
  const n = m ? Number(m[1]) : NaN;
  return Number.isSafeInteger(n) && n > 0 ? n : null;
};

/** 본문에서 선행·후행 목록을 읽는다. 같은 번호가 겹치면 처음 것만 쓴다. */
export function parseLinks(body) {
  const { lines } = splitBody(body);
  const predecessors = [];
  const successors = [];
  for (const { match } of sectionItems(lines, findSection(lines, PRED_HEADING), PRED_ITEM)) {
    const number = Number(match[2]);
    if (!predecessors.some((p) => p.number === number)) {
      predecessors.push({ number, checked: match[1] !== ' ' });
    }
  }
  for (const { match } of sectionItems(lines, findSection(lines, SUCC_HEADING), SUCC_ITEM)) {
    const number = Number(match[1]);
    if (!successors.includes(number)) successors.push(number);
  }
  return { predecessors, successors };
}

/** 섹션이 없으면 만든다. 선행은 후행 앞, 그 외에는 `## 참고` 앞, 그것도 없으면 본문 끝. */
function ensureSection(lines, heading) {
  const found = findSection(lines, heading);
  if (found) return found;
  let at = -1;
  if (heading === PRED_HEADING) at = findHeading(lines, SUCC_HEADING);
  if (heading === SUCC_HEADING) {
    const pred = findSection(lines, PRED_HEADING);
    if (pred) at = pred.end;
  }
  if (at === -1) at = findHeading(lines, '참고');
  if (at === -1) {
    while (lines.length && lines[lines.length - 1].trim() === '') lines.pop();
    at = lines.length;
  }
  const block = [`## ${heading}`, ''];
  const before = at > 0 && lines[at - 1].trim() !== '' ? [''] : [];
  const after = at < lines.length ? [''] : [];
  lines.splice(at, 0, ...before, ...block, ...after);
  return findSection(lines, heading);
}

/** 섹션 안에 항목 한 줄을 넣는다. 마지막 항목 뒤, 항목이 없으면 자리표시(`없음`)를 지우고 헤딩 뒤. */
function insertItem(lines, section, pattern, line) {
  const items = sectionItems(lines, section, pattern);
  if (items.length) {
    lines.splice(items[items.length - 1].index + 1, 0, line);
    return;
  }
  let end = section.end;
  for (let i = end - 1; i > section.start; i -= 1) {
    if (PLACEHOLDER.test(lines[i])) { lines.splice(i, 1); end -= 1; }
  }
  const first = section.start + 1;
  while (first < end && lines[first].trim() === '') { lines.splice(first, 1); end -= 1; }
  // 헤딩 · 빈 줄 · 항목, 뒤에 내용이 이어지면 빈 줄 하나
  const tail = first < lines.length ? [''] : [];
  lines.splice(first, 0, '', line, ...tail);
}

function finish(lines, eol, original) {
  const body = lines.join(eol);
  return { body, changed: body !== original };
}

/**
 * 선행 항목을 넣는다. 이미 있으면 done=true 일 때만 체크하고, 체크를 풀지는 않는다.
 * 반환: { body, changed }
 */
export function addPredecessor(body, number, { done = false } = {}) {
  const n = toNumber(number);
  if (!n) throw new Error(`잘못된 이슈 번호: ${number}`);
  const original = String(body ?? '');
  const { eol, lines } = splitBody(original);
  const existing = sectionItems(lines, findSection(lines, PRED_HEADING), PRED_ITEM)
    .find(({ match }) => Number(match[2]) === n);
  if (existing) {
    if (done && existing.match[1] === ' ') {
      lines[existing.index] = lines[existing.index].replace(/\[ \]/, '[x]');
    }
    return finish(lines, eol, original);
  }
  const section = ensureSection(lines, PRED_HEADING);
  insertItem(lines, section, PRED_ITEM, `- [${done ? 'x' : ' '}] #${n}`);
  return finish(lines, eol, original);
}

/** 후행 항목을 넣는다. 이미 있으면 그대로. 반환: { body, changed } */
export function addSuccessor(body, number) {
  const n = toNumber(number);
  if (!n) throw new Error(`잘못된 이슈 번호: ${number}`);
  const original = String(body ?? '');
  const { eol, lines } = splitBody(original);
  const exists = sectionItems(lines, findSection(lines, SUCC_HEADING), SUCC_ITEM)
    .some(({ match }) => Number(match[1]) === n);
  if (exists) return { body: original, changed: false };
  const section = ensureSection(lines, SUCC_HEADING);
  insertItem(lines, section, SUCC_ITEM, `- #${n}`);
  return finish(lines, eol, original);
}

/**
 * 선행 항목 `- [ ] #n` 을 `- [x] #n` 으로 바꾼다. 멱등.
 * 반환: { body, changed, found } — found=false 면 본문에 그 선행 항목이 없다.
 */
export function checkPredecessor(body, number) {
  const n = toNumber(number);
  if (!n) throw new Error(`잘못된 이슈 번호: ${number}`);
  const original = String(body ?? '');
  const { eol, lines } = splitBody(original);
  const hit = sectionItems(lines, findSection(lines, PRED_HEADING), PRED_ITEM)
    .find(({ match }) => Number(match[2]) === n);
  if (!hit) return { body: original, changed: false, found: false };
  if (hit.match[1] === ' ') lines[hit.index] = lines[hit.index].replace(/\[ \]/, '[x]');
  return { ...finish(lines, eol, original), found: true };
}

const labelNames = (issue) => (issue?.labels ?? []).map((l) => (typeof l === 'string' ? l : l?.name)).filter(Boolean);

/** 진행 상태 라벨(`status:*`). 없으면 null. */
export function statusOf(issue) {
  return labelNames(issue).find((name) => /^status:/i.test(name))?.toLowerCase() ?? null;
}

/** 끝난 이슈 = 닫힘 또는 `status:close`(issue-merge close 가 닫기 직전에 붙인다). */
export function isIssueDone(issue) {
  if (!issue) return false;
  return String(issue.state ?? '').toUpperCase() === 'CLOSED' || statusOf(issue) === 'status:close';
}

/* ------------------------------------------------------------ 트래커 사용 */

/**
 * 착수 전 선행 확인. 본문에 적힌 선행 이슈를 하나씩 조회한다.
 * 반환: { ok, issue, predecessors: [{ number, checked, state, status, done, error }], pending: number[] }
 * 조회에 실패한 선행도 pending 으로 센다 — 모르면 막는다.
 */
export function prereqStatus(tracker, number) {
  const issue = tracker.issueView(number);
  if (!issue) return { ok: false, error: `이슈 #${number} 조회 실패`, predecessors: [], pending: [] };
  const { predecessors } = parseLinks(issue.body);
  const rows = predecessors.map(({ number: n, checked }) => {
    const pred = tracker.issueView(n);
    if (!pred) return { number: n, checked, state: null, status: null, done: false, error: '조회 실패' };
    return {
      number: n,
      checked,
      state: String(pred.state ?? '').toUpperCase() || null,
      status: statusOf(pred),
      done: isIssueDone(pred),
      error: null,
    };
  });
  return { ok: true, predecessors: rows, pending: rows.filter((r) => !r.done).map((r) => r.number) };
}

function normalizeList(list, self) {
  const out = [];
  for (const raw of list ?? []) {
    const n = toNumber(raw);
    if (!n) throw new Error(`잘못된 이슈 번호: ${raw}`);
    if (n === self) throw new Error(`자기 자신(#${n})은 선행·후행이 될 수 없다`);
    if (!out.includes(n)) out.push(n);
  }
  return out;
}

/** --after/--before 목록을 검증한다. 같은 번호가 양쪽에 오면 순환이라 거절한다. */
export function validateLinkTargets(self, { after = [], before = [] } = {}) {
  const a = normalizeList(after, self);
  const b = normalizeList(before, self);
  const both = a.filter((n) => b.includes(n));
  if (both.length) throw new Error(`같은 이슈가 선행이면서 후행일 수 없다: ${both.map((n) => `#${n}`).join(', ')}`);
  return { after: a, before: b };
}

/**
 * 본문만 계산한다(트래커에 쓰지 않음). create 가 등록 전 자기 본문을 만들 때 쓴다.
 * afterStates: { [number]: issue } — 선행 이슈의 조회 결과(닫힘 여부로 [x] 결정)
 */
export function applyOwnLinks(body, { after = [], before = [] }, afterStates = {}) {
  let next = String(body ?? '');
  for (const p of after) next = addPredecessor(next, p, { done: isIssueDone(afterStates[p]) }).body;
  for (const s of before) next = addSuccessor(next, s).body;
  return next;
}

function editIfChanged(tracker, number, before, after, dryRun) {
  if (after === before) return { issue: number, changed: false, ok: true, err: null };
  if (dryRun) return { issue: number, changed: true, ok: true, err: null, dryRun: true };
  const r = tracker.issueEditBody(number, after);
  return { issue: number, changed: true, ok: Boolean(r?.ok), err: r?.ok ? null : (r?.err ?? '본문 수정 실패') };
}

/**
 * 이슈 X 를 선행(after)·후행(before)과 양방향으로 잇는다. 멱등 — 이미 이어진 항목은 건드리지 않는다.
 *   X 본문     선행에 `- [ ]/[x] #P`, 후행에 `- #S`
 *   P 본문     후행에 `- #X`
 *   S 본문     선행에 `- [ ]/[x] #X` (X 가 끝났으면 [x])
 * includeSelf=false 면 X 본문은 건드리지 않는다(create 가 등록 전에 이미 반영한 경우).
 * 반환: { edits: [{ issue, changed, ok, err }], ok }
 */
export function linkIssues(tracker, number, targets, { dryRun = false, includeSelf = true } = {}) {
  const self = toNumber(number);
  if (!self) throw new Error(`잘못된 이슈 번호: ${number}`);
  const { after, before } = validateLinkTargets(self, targets);
  const views = new Map();
  const view = (n) => {
    if (!views.has(n)) views.set(n, tracker.issueView(n));
    return views.get(n);
  };
  const missing = [self, ...after, ...before].filter((n) => (n !== self || includeSelf) && !view(n));
  if (missing.length) {
    return { ok: false, error: `조회 실패: ${missing.map((n) => `#${n}`).join(', ')}`, edits: [] };
  }
  const edits = [];
  if (includeSelf) {
    const own = view(self);
    const states = Object.fromEntries(after.map((n) => [n, view(n)]));
    edits.push(editIfChanged(tracker, self, own.body ?? '', applyOwnLinks(own.body ?? '', { after, before }, states), dryRun));
  }
  const selfDone = includeSelf ? isIssueDone(view(self)) : false;
  for (const p of after) {
    const body = view(p).body ?? '';
    edits.push(editIfChanged(tracker, p, body, addSuccessor(body, self).body, dryRun));
  }
  for (const s of before) {
    const body = view(s).body ?? '';
    edits.push(editIfChanged(tracker, s, body, addPredecessor(body, self, { done: selfDone }).body, dryRun));
  }
  return { ok: edits.every((e) => e.ok), edits };
}

/**
 * 이슈 X 가 끝났을 때 X 의 후행 이슈마다 본문의 `- [ ] #X` 를 `- [x] #X` 로 바꾼다. 멱등.
 * 후행 본문에 X 항목이 없으면(한쪽만 이어진 경우) 선행 섹션에 `- [x] #X` 로 넣어 양방향을 복구한다.
 * 반환: { ok, successors: [{ issue, changed, ok, err }] }
 */
export function markSuccessors(tracker, number, { dryRun = false } = {}) {
  const self = toNumber(number);
  if (!self) throw new Error(`잘못된 이슈 번호: ${number}`);
  const issue = tracker.issueView(self);
  if (!issue) return { ok: false, error: `이슈 #${self} 조회 실패`, successors: [] };
  const results = [];
  for (const s of parseLinks(issue.body).successors) {
    const succ = tracker.issueView(s);
    if (!succ) {
      results.push({ issue: s, changed: false, ok: false, err: '조회 실패' });
      continue;
    }
    const body = succ.body ?? '';
    const checked = checkPredecessor(body, self);
    const next = checked.found ? checked.body : addPredecessor(body, self, { done: true }).body;
    results.push(editIfChanged(tracker, s, body, next, dryRun));
  }
  return { ok: results.every((r) => r.ok), successors: results };
}
