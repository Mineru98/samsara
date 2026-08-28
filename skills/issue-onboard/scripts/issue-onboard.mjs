#!/usr/bin/env node
/**
 * issue-onboard.mjs — 이슈 그래프 기반 온보딩을 위한 보조 스크립트.
 *
 * 기존 파이프라인(issue-create → issue-start → issue-end → issue-merge)은 이슈를
 * 독립 단위로만 다루고 이슈 사이의 의존/순서를 저장하지 않는다. 이 스크립트는
 * `.issue/graph.json` 에 노드(이슈) + 타입 엣지(의존)를 두고, ready-frontier 와
 * 우선순위를 반영한 todo 를 산출한다.
 *
 * 서브커맨드:
 *   sync                트래커에서 열린·닫힌 이슈를 노드로 갱신하고, 본문의
 *                       "depends on #N" 류 참조를 엣지로 자동 감지한다.
 *   link <from> <to>    from --<type>--> to 엣지를 근거와 함께 추가한다.
 *   unlink <from> <to>  일치하는 엣지를 제거한다.
 *   plan (todo)         위상정렬 + ready/blocked/in-progress/done 분류로 todo 를 낸다.
 *   next                의존·우선순위를 반영해 다음 착수 이슈 1건을 추천한다.
 *   validate            사이클·dangling 엣지·close 불일치를 점검한다.
 *
 * 엣지 방향 규약: `from --depends-on--> to` = "from 은 to 가 close 전엔 착수 불가".
 * blocks 는 depends-on 의 역방향으로 취급한다(A blocks B == B depends-on A).
 * relates-on/parent-of/duplicate-of 는 순서 제약이 아니라 정보성 엣지다.
 *
 * 이슈 백엔드는 ~/.issue/settings.json 의 provider 설정이 정한다(github 기본 | jira).
 * 트래커 호출은 전부 issue-tracker.mjs 를 거친다.
 *
 * 요구사항: git, Node 18+, (github 면 gh 로그인 / jira 면 baseUrl·projectKey·토큰)
 */
import { mkdirSync, writeFileSync, readFileSync, existsSync, realpathSync, renameSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { repoRoot, WORKSPACE_DIR, GRAPH_FILE_NAME, isStatusLabel, typeLabels, parseIssueNumber, resolveSkillScript } from './issue-common.mjs';
import { createTracker, gitHost } from './issue-tracker.mjs';
import { GRAPH_VERSION as V2_GRAPH_VERSION, EDGE_TYPES as V2_EDGE_TYPES, ORDERING_TYPES as V2_ORDERING_TYPES, CONTEXT_FIELDS, EDGE_CONTEXT_VERSION, DECISION_MARKER, digest, normalizeEdge, edgeKey, parseDecisionComments, decisionEdge, resolveDecisions, auditGraph, migrateGraphV1, kindOfType, extractQuote, sharedConcepts, carryStaleEdges } from './issue-graph-v2.mjs';
import { detectLlmCommand, enrichEdges } from './issue-llm.mjs';
import { fetchBlockedBy, splitSlug } from './issue-native-deps.mjs';

export const GRAPH_VERSION = V2_GRAPH_VERSION;
export const GRAPH_FILE = GRAPH_FILE_NAME;

/** 저장 가능한 엣지 타입. 앞 둘만 순서 제약이다. */
export const EDGE_TYPES = V2_EDGE_TYPES;
export const ORDERING_TYPES = V2_ORDERING_TYPES;

/** 진행 상태를 세 부류로. done 만 "끝난 것"으로 본다. */
const DONE = 'close';
const IN_PROGRESS = new Set(['plan', 'in-process', 'review']);
function unknownField(reason, source) { return { value: 'unknown', reason, source }; }

function ontologyEntry(start = process.cwd()) {
  if (process.env.ISSUE_ONTOLOGY_ROOT) {
    return path.join(path.resolve(process.env.ISSUE_ONTOLOGY_ROOT), 'validate.mjs');
  }
  let current = path.resolve(start);
  while (true) {
    const candidate = path.join(current, 'tools', 'issue-ontology', 'validate.mjs');
    if (existsSync(candidate)) return candidate;
    const parent = path.dirname(current);
    if (parent === current) break;
    current = parent;
  }
  return null;
}

const ONTOLOGY_ENTRY = ontologyEntry();
let ontologyModule = null;
let ontologyLoadError = null;
if (ONTOLOGY_ENTRY) {
  try {
    ontologyModule = await import(pathToFileURL(ONTOLOGY_ENTRY).href);
  } catch (error) {
    ontologyLoadError = error;
  }
}

/* --------------------------------------------------------------- graph I/O */

export function graphPath(root) {
  return path.join(root, WORKSPACE_DIR, GRAPH_FILE);
}

export function emptyGraph(provider = 'github') {
  return { version: GRAPH_VERSION, provider, repository: null, updatedAt: null, snapshot: { status: 'missing' }, nodes: {}, edges: [] };
}

export function loadGraph(root, provider = 'github', { tolerateParseError = false } = {}) {
  const file = graphPath(root);
  if (!existsSync(file)) return emptyGraph(provider);
  try {
    const g = JSON.parse(readFileSync(file, 'utf8'));
    return { ...emptyGraph(provider), ...g, nodes: g.nodes ?? {}, edges: g.edges ?? [] };
  } catch (e) {
    console.error(`✗ ${WORKSPACE_DIR}/${GRAPH_FILE} 파싱 실패: ${e.message}`);
    if (tolerateParseError) return { ...emptyGraph(provider), snapshot: { status: 'invalid', reason: 'graph parse failed' } };
    process.exit(1);
  }
}

/** 결정적 순서로 저장한다 — diff 가 안정되도록 노드는 번호순, 엣지는 (from,to,type) 순. */
export function saveGraph(root, graph, { now } = {}) {
  const nodes = {};
  for (const k of Object.keys(graph.nodes).sort((a, b) => Number(a) - Number(b))) nodes[k] = graph.nodes[k];
  const edges = [...graph.edges].map(normalizeEdge).sort((a, b) =>
    a.from - b.from || a.to - b.to || String(a.type).localeCompare(String(b.type)));
  const out = { ...graph, version: GRAPH_VERSION, updatedAt: now ?? graph.updatedAt, nodes, edges };
  const file = graphPath(root);
  mkdirSync(path.dirname(file), { recursive: true });
  const temporary = `${file}.tmp-${process.pid}`;
  writeFileSync(temporary, `${JSON.stringify(out, null, 2)}\n`, 'utf8');
  renameSync(temporary, file);
  return file;
}

/* --------------------------------------------------------------- 상태 파생 */

/** 트래커의 종료 state를 먼저 반영하고, 열린 이슈만 status:* 라벨로 세부 상태를 정한다. */
export function deriveStatus(labels = [], state) {
  if (['CLOSED', 'MERGED'].includes(String(state ?? '').toUpperCase())) return 'close';
  const status = labels.map((l) => (typeof l === 'string' ? l : l.name)).find(isStatusLabel);
  if (status) return status.slice('status:'.length);
  return 'open';
}

/** 노드에서 우선순위 랭크를 뽑는다. P0=0 … 라벨 없으면 뒤로. */
export function priorityRank(node) {
  if (typeof node.priority === 'number') return node.priority;
  for (const name of node.labels ?? []) {
    const m = /^p([0-3])$/i.exec(name);
    if (m) return Number(m[1]);
  }
  return 9;
}

/* --------------------------------------------------------- 의존 그래프 계산 */

/**
 * 순서 제약 엣지를 "prereq(선행) 맵" 으로 정규화한다.
 * depends-on {from,to} → from 의 선행에 to.
 * blocks     {from,to} → to 의 선행에 from.
 * 반환: Map<number, Set<number>> — 노드 → 선행 노드 집합.
 */
export function prereqMap(graph) {
  const map = new Map();
  const add = (node, dep) => {
    if (!map.has(node)) map.set(node, new Set());
    map.get(node).add(dep);
  };
  for (const num of Object.keys(graph.nodes)) map.set(Number(num), map.get(Number(num)) ?? new Set());
  for (const e of graph.edges) {
    if (e.type === 'depends-on') add(e.from, e.to);
  }
  return map;
}

/** 순서 엣지에서 사이클을 찾는다. 있으면 노드 배열(사이클 경로), 없으면 null. */
export function findCycle(graph) {
  const prereq = prereqMap(graph);
  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map();
  const stack = [];
  let cycle = null;
  const visit = (n) => {
    if (cycle) return;
    color.set(n, GRAY);
    stack.push(n);
    for (const dep of prereq.get(n) ?? []) {
      if (!prereq.has(dep)) continue; // 알 수 없는 노드는 dangling — validate 가 따로 잡는다
      const c = color.get(dep) ?? WHITE;
      if (c === GRAY) { cycle = [...stack.slice(stack.indexOf(dep)), dep]; return; }
      if (c === WHITE) visit(dep);
      if (cycle) return;
    }
    stack.pop();
    color.set(n, BLACK);
  };
  for (const n of prereq.keys()) if ((color.get(n) ?? WHITE) === WHITE) visit(n);
  return cycle;
}

/**
 * 각 노드를 ready / blocked / in-progress / done 으로 분류한다.
 * blocked: 선행 중 close 가 아닌 것이 있음.
 * in-progress: plan/in-process/review.
 * ready: open + 선행이 전부 close.
 */
export function classify(graph) {
  const prereq = prereqMap(graph);
  const statusOf = (num) => graph.nodes[String(num)]?.status ?? 'open';
  const out = { ready: [], blocked: [], inProgress: [], done: [] };
  for (const num of Object.keys(graph.nodes).map(Number)) {
    const st = statusOf(num);
    if (st === DONE) { out.done.push(num); continue; }
    // 그래프에 없는 선행(다른 저장소 참조·오타·fetch 창 밖)은 blocker 로 치지 않는다.
    // 안 그러면 그 이슈가 영원히 blocked 로 남아 ready/next 에서 사라진다. dangling 은 validate 가 계속 경고한다.
    const blockers = [...(prereq.get(num) ?? [])].filter((d) => graph.nodes[String(d)] && statusOf(d) !== DONE);
    if (blockers.length) { out.blocked.push({ num, blockers }); continue; }
    if (IN_PROGRESS.has(st)) { out.inProgress.push(num); continue; }
    out.ready.push(num);
  }
  const byPrio = (a, b) => priorityRank(graph.nodes[String(a)]) - priorityRank(graph.nodes[String(b)]) || a - b;
  out.ready.sort(byPrio);
  out.inProgress.sort(byPrio);
  out.blocked.sort((a, b) => byPrio(a.num, b.num));
  out.done.sort((a, b) => a - b);
  return out;
}

/* --------------------------------------------------------------- sync 파싱 */

/** 본문에서 의존 참조를 뽑는다. 반환: [{ type, to, index, matched }]. NFC 정규화된 본문 기준 offset. */
export function parseDependencies(body = '') {
  const refs = [];
  const text = String(body).normalize('NFC');
  const grab = (re, type) => {
    let m;
    while ((m = re.exec(text)) !== null) refs.push({ type, to: Number(m[1]), index: m.index, matched: m[0] });
  };
  // "depends on #N", "depends-on #N", "blocked by #N", "needs #N" → depends-on
  grab(/\bdepends[\s-]?on\s+#(\d{1,6})/gi, 'depends-on');
  grab(/\bblocked[\s-]?by\s+#(\d{1,6})/gi, 'depends-on');
  grab(/\bneeds\s+#(\d{1,6})/gi, 'depends-on');
  let m;
  const blocks = /\bblocks\s+#(\d{1,6})/gi;
  while ((m = blocks.exec(text)) !== null) refs.push({ type: 'depends-on', from: Number(m[1]), reverse: true, index: m.index, matched: m[0] });
  return refs;
}

/**
 * GitHub 네이티브 의존성(blocked-by)을 depends-on 후보로 모은다.
 * X 가 Y 에 blocked-by 면 Y 가 선수 → { from: X, to: Y }.
 * seen 에 이미 있는 키(본문 마커가 만든 엣지)는 건너뛰어 본문 근거를 우선한다.
 * 어떤 실패든 sync 를 막지 않는다: API 미지원이거나 첫 노드부터 실패하면 중단한다.
 */
export function collectNativeDependencies({ list = [], seen = new Set(), owner, repo, root, fetch = fetchBlockedBy } = {}) {
  const stats = { queried: 0, edges: 0, skipped: null };
  const candidates = [];
  if (!owner || !repo) { stats.skipped = 'repo-unknown'; return { candidates, stats }; }
  for (const it of list) {
    const res = fetch({ owner, repo, number: it.number, cwd: root });
    if (res && res.unsupported) { stats.skipped = 'api-unsupported'; break; }
    if (!res || !Array.isArray(res.numbers)) {
      if (stats.queried === 0) { stats.skipped = 'unavailable'; break; }
      continue;
    }
    stats.queried += 1;
    for (const dep of res.numbers) {
      const from = it.number;
      const to = dep;
      if (to === from) continue;
      const key = `${from}|${to}|depends-on`;
      if (seen.has(key)) continue;
      seen.add(key);
      candidates.push({ from, to });
      stats.edges += 1;
    }
  }
  return { candidates, stats };
}

/** 네이티브 의존성 후보를 그래프 엣지(depends-on)로 만든다. graph-v2 스키마와 일치해야 한다. */
export function buildNativeEdge({ from, to, toClosed = false, url = null, now }) {
  return {
    from, to, type: 'depends-on', kind: 'blocked-by',
    rationale: `#${from} 은(는) GitHub 네이티브 의존성에서 #${to} 에 blocked-by`,
    context: { summary: `#${from} 이(가) #${to} 에 blocked-by (GitHub 네이티브 의존성)`, label: `blocked-by #${to}`, keywords: [], sharedConcepts: [], generatedBy: 'github-native', confidence: 'high', generatedAt: now },
    evidence: [],
    status: toClosed ? 'resolved' : 'active',
    schemaVersion: EDGE_CONTEXT_VERSION,
    createdBy: 'github-native', createdAt: now,
    provenance: { url, digest: digest({ nativeDependency: true, from, to }) },
  };
}

/* ------------------------------------------------------------------- 명령 */

function cmdSync(root, tracker, opts) {
  const state = opts.state ?? 'all';
  const limit = Number(opts.limit ?? 200);
  const list = tracker.issueList({
    state,
    limit,
    fields: 'number,title,labels,url,state,body,comments,updatedAt,author,createdAt',
  });
  if (list === null) {
    console.log('SYNCED=0');
    console.log('SYNC_FAILED=1');
    return;
  }
  const graph = loadGraph(root, tracker.provider, { tolerateParseError: true });
  graph.version = GRAPH_VERSION;
  graph.provider = tracker.provider;
  graph.repository = opts.repo ?? graph.repository ?? null;
  graph.nodes = {};

  const now = opts.now ?? new Date().toISOString();
  // 노드 갱신 (제목·상태·라벨·url 은 트래커가 정본).
  for (const it of list) {
    const labels = (it.labels ?? []).map((l) => l.name);
    const source = { url: it.url, revision: it.updatedAt ?? 'unknown', observedAt: now };
    graph.nodes[String(it.number)] = {
      id: `github:${graph.repository ?? 'unknown'}#${it.number}`,
      number: it.number,
      title: it.title,
      status: deriveStatus(it.labels ?? [], it.state),
      labels: typeLabels(labels),
      url: it.url,
      context: Object.fromEntries(CONTEXT_FIELDS.map((field) => [field, unknownField(`GitHub 본문에 구조화된 ${field} 필드가 없음`, source)])),
      provenance: source,
    };
  }

  // V2 캐시는 GitHub에서 다시 만들 수 있는 auto/decision 엣지만 보관한다.
  const previousEdges = [...(graph.edges ?? []), ...(graph.staleEdges ?? [])];
  const candidates = [];
  const decisions = [];
  const seen = new Set();
  const itemByNumber = new Map(list.map((it) => [it.number, it]));
  for (const it of list) {
    for (const ref of parseDependencies(it.body ?? '')) {
      const from = ref.reverse ? ref.from : it.number;
      const to = ref.reverse ? it.number : ref.to;
      if (to === from) continue;
      const key = `${from}|${to}|${ref.type}`;
      if (seen.has(key)) continue;
      seen.add(key);
      candidates.push({ from, to, type: ref.type, ref, it });
    }
    decisions.push(...parseDecisionComments(it.comments ?? []));
  }
  // 하이브리드: GitHub 네이티브 의존성(blocked-by)도 depends-on 후보로 읽는다. 본문 마커가
  // 이미 만든 엣지는 seen 으로 걸러 우선하고, 실패는 조용히 흡수한다 (#하이브리드 그래프).
  const nativeSlug = splitSlug(opts.repo ?? graph.repository ?? gitHost.repoInfo(root)?.nameWithOwner ?? '');
  const native = opts.noNative
    ? { candidates: [], stats: { queried: 0, edges: 0, skipped: 'disabled-by-flag' } }
    : collectNativeDependencies({ list, seen, owner: nativeSlug?.owner, repo: nativeSlug?.repo, root });
  const referenced = [...new Set([...candidates, ...native.candidates].flatMap((c) => [c.from, c.to]))].filter((number) => !graph.nodes[String(number)]);
  const unresolved = [];
  for (const number of referenced) {
    const item = tracker.issueView(number);
    if (!item) { unresolved.push(number); continue; }
    itemByNumber.set(number, item);
    const labels = (item.labels ?? []).map((label) => label.name);
    const source = { url: item.url, revision: item.updatedAt ?? 'unknown', observedAt: now, kind: 'referenced' };
    graph.nodes[String(number)] = { id: `github:${graph.repository ?? 'unknown'}#${number}`, number, title: item.title, status: deriveStatus(item.labels ?? [], item.state), labels: typeLabels(labels), url: item.url, context: Object.fromEntries(CONTEXT_FIELDS.map((field) => [field, unknownField(`참조된 GitHub 항목의 구조화된 ${field} 필드가 없음`, source)])), provenance: source };
  }
  // 결정론 근거 조립: #N 참조 주변 문장을 verbatim 발췌하고 공유 개념을 추출한다 (#93).
  const auto = candidates.map(({ from, to, type, ref, it }) => {
    const other = itemByNumber.get(ref.reverse ? from : to);
    const extracted = extractQuote(it.body ?? '', ref.index, ref.matched.length);
    const shared = sharedConcepts(
      { body: it.body, title: it.title, labels: (it.labels ?? []).map((label) => label.name ?? label) },
      other ? { body: other.body, title: other.title, labels: (other.labels ?? []).map((label) => label.name ?? label) } : {},
    );
    const summary = `#${it.number} 본문이 "${ref.matched}" 로 #${ref.reverse ? from : to} 을(를) 참조`;
    const toClosed = graph.nodes[String(to)]?.status === 'close';
    return {
      from, to, type,
      kind: kindOfType(type),
      rationale: summary,
      context: { summary, label: ref.matched.slice(0, 20), keywords: shared.slice(0, 4), sharedConcepts: shared, generatedBy: 'deterministic', confidence: extracted ? 'high' : 'low', generatedAt: now },
      evidence: extracted ? [{ issue: it.number, field: 'body', commentId: null, author: it.author?.login ?? it.author ?? null, authoredAt: it.createdAt ?? null, quote: extracted.quote, start: extracted.start, end: extracted.end, url: it.url, digest: digest(it.body ?? '') }] : [],
      status: type === 'depends-on' && toClosed ? 'resolved' : 'active',
      schemaVersion: EDGE_CONTEXT_VERSION,
      createdBy: 'sync', createdAt: now,
      provenance: { url: it.url, digest: digest(it.body ?? '') },
    };
  });
  const nativeEdges = native.candidates.map(({ from, to }) => buildNativeEdge({
    from, to,
    toClosed: graph.nodes[String(to)]?.status === 'close',
    url: itemByNumber.get(from)?.url ?? graph.nodes[String(from)]?.url ?? null,
    now,
  }));
  const approved = resolveDecisions(decisions).map(decisionEdge).filter(Boolean).filter((edge) => { const key = edgeKey(edge); if (seen.has(key)) return false; seen.add(key); return true; });
  graph.edges = [...auto, ...nativeEdges, ...approved];
  // 재감지되지 않은 sync 엣지는 삭제하지 않고 stale 로 이관한다 — 소비 코드는 graph.edges 만 읽으므로 스케줄링에 영향 없음 (#93).
  graph.staleEdges = carryStaleEdges(previousEdges, new Set(graph.edges.map(edgeKey)), now);
  // LLM 보강 패스 (#94) — 실패·부재 시 결정론 산출물 유지, sync 는 정상 진행.
  let llmStats = { enriched: 0, cached: 0, discarded: 0, skipped: opts.noLlm ? 'disabled-by-flag' : null };
  if (!opts.noLlm) {
    const command = detectLlmCommand();
    const result = enrichEdges(graph.edges, { itemByNumber, previousEdges, command });
    graph.edges = result.edges;
    llmStats = result.stats;
  }
  const complete = state === 'all' && list.length < limit && unresolved.length === 0;
  graph.snapshot = { status: complete ? 'complete' : 'partial', fetchedAt: now, digest: digest(list.map((it) => ({ number: it.number, updatedAt: it.updatedAt ?? null, body: it.body ?? '', comments: it.comments ?? [] }))), reason: complete ? null : unresolved.length ? `참조 GitHub 항목을 조회할 수 없음: ${unresolved.map((number) => `#${number}`).join(', ')}` : 'state filter 또는 limit로 전체 GitHub 이슈 목록을 증명할 수 없음' };

  const file = saveGraph(root, graph, { now });
  const cycle = findCycle(graph);

  console.log(`✓ sync 완료 — 노드 ${Object.keys(graph.nodes).length}개, 엣지 ${graph.edges.length}개 (자동 ${auto.length}, 네이티브 ${nativeEdges.length}, 승인 ${approved.length})`);
  console.log(`  저장: ${path.relative(root, file)}`);
  if (cycle) console.log(`  ! 경고: 순환 의존 감지 ${cycle.join(' → ')} (validate 로 확인)`);
  console.log('');
  console.log(`SYNCED=${Object.keys(graph.nodes).length}`);
  console.log(`EDGES=${graph.edges.length}`);
  console.log(`AUTO_EDGES=${auto.length}`);
  console.log(`NATIVE_EDGES=${nativeEdges.length}`);
  console.log(`NATIVE_QUERIED=${native.stats.queried}`);
  console.log(`NATIVE_SKIPPED=${native.stats.skipped ?? ''}`);
  console.log(`DECISION_EDGES=${approved.length}`);
  console.log(`RESOLVED_REFERENCES=${referenced.length - unresolved.length}`);
  console.log(`UNRESOLVED_REFERENCES=${unresolved.join(' ')}`);
  console.log(`SNAPSHOT_STATUS=${graph.snapshot.status}`);
  console.log(`LLM_ENRICHED=${llmStats.enriched}`);
  console.log(`LLM_CACHED=${llmStats.cached}`);
  console.log(`LLM_DISCARDED=${llmStats.discarded}`);
  console.log(`LLM_SKIPPED=${llmStats.skipped ?? ''}`);
  console.log(`CYCLE=${cycle ? cycle.join('>') : ''}`);
}

/** 결정 코멘트 본문을 만든다. sync 의 parseDecisionComments 가 그대로 파싱한다. */
export function decisionCommentBody(payload, human) {
  return `<!-- ${DECISION_MARKER}\n${JSON.stringify(payload)}\n-->\n\n${human}`;
}

/** 결정 코멘트를 대상 이슈에 게시한다. 임시 파일을 거쳐 tracker.issueComment 로 올린다. */
function postDecision(tracker, issueNumber, payload, human) {
  const file = path.join(tmpdir(), `issue-relation-${payload.id}.md`);
  writeFileSync(file, decisionCommentBody(payload, human));
  return tracker.issueComment(issueNumber, file);
}

// V2 는 GitHub 을 정본으로 두고 graph.json 은 재생성 캐시다. 그래서 link 는 로컬 엣지를
// 쓰는 대신, sync 가 읽는 구조화된 승인 결정 코멘트를 대상(from) 이슈에 남기고 다시
// sync 해 엣지를 실체화한다. 순서 엣지(depends-on)는 순환이 되면 게시 전에 거부한다.
function cmdLink(root, tracker, from, to, opts) {
  if (from == null || to == null) { console.error('✗ from 과 to 이슈 번호가 필요하다 (예: link 61 60)'); process.exit(1); }
  const type = opts.type ?? 'depends-on';
  if (!EDGE_TYPES.includes(type)) {
    console.error(`✗ 알 수 없는 엣지 타입: ${type} — ${EDGE_TYPES.join(' | ')} 중 하나`);
    console.log('LINKED=0');
    process.exit(1);
  }
  if (from === to) { console.error('✗ 자기 자신을 가리키는 엣지는 만들 수 없다.'); console.log('LINKED=0'); process.exit(1); }

  const graph = loadGraph(root, tracker.provider);
  // 순서 엣지는 순환을 만들면 게시하지 않는다 (dag-ops 의 계약).
  if (ORDERING_TYPES.has(type)) {
    const cycle = findCycle({ nodes: graph.nodes ?? {}, edges: [...(graph.edges ?? []), { from, to, type }] });
    if (cycle) { console.error(`✗ 순환이 되어 거부: ${cycle.join(' → ')}`); console.log('LINKED=0'); process.exit(2); }
  }

  const fromView = tracker.issueView(from);
  const toView = tracker.issueView(to);
  if (!fromView) { console.error(`✗ #${from} 이슈를 조회할 수 없다.`); console.log('LINKED=0'); process.exit(1); }
  if (!toView) { console.error(`✗ #${to} 이슈를 조회할 수 없다.`); console.log('LINKED=0'); process.exit(1); }

  const evidence = [fromView.url, toView.url].filter(Boolean);
  if (!evidence.length) evidence.push(`#${from} → #${to}`);
  const graphRevision = graph.snapshot?.digest ?? digest(graph);
  const id = `relation-${from}-${to}-${type}`;
  const payload = {
    version: 1, id, action: 'relation', decision: 'approved',
    type, from, to, graphRevision, rationale: opts.why ?? '', evidence,
  };
  const human = `관계 승인: #${from} --${type}--> #${to}${opts.why ? `\n\n근거: ${opts.why}` : ''}`;
  const posted = postDecision(tracker, from, payload, human);
  if (!posted.ok) { console.error(`✗ 결정 코멘트 게시 실패: ${posted.err ?? ''}`); console.log('LINKED=0'); process.exit(1); }

  console.log(`✓ #${from} 에 관계 승인 코멘트 게시 (${id})`);
  console.log('  그래프 재동기화 중...');
  console.log('');
  cmdSync(root, tracker, opts);
  console.log('');
  console.log('LINKED=1');
  console.log(`LINK=${from}|${to}|${type}`);
  console.log(`DECISION_ID=${id}`);
}

// unlink 는 같은 id 의 revoked 결정 코멘트를 남긴다. resolveDecisions 가 최신 결정을
// 골라 revoked 를 걸러내므로 다음 sync 에서 결정 엣지가 사라진다. 단, 본문 마커
// (`depends on #N`)나 GitHub 네이티브 의존성으로 생긴 엣지는 이 방식으로 지워지지
// 않는다 — 본문을 고치거나 GitHub 에서 의존성 링크를 직접 해제해야 한다.
function cmdUnlink(root, tracker, from, to, opts) {
  if (from == null || to == null) { console.error('✗ from 과 to 이슈 번호가 필요하다.'); console.log('UNLINKED=0'); process.exit(1); }
  const type = opts.type ?? 'depends-on';
  if (!EDGE_TYPES.includes(type)) {
    console.error(`✗ 알 수 없는 엣지 타입: ${type} — ${EDGE_TYPES.join(' | ')} 중 하나`);
    console.log('UNLINKED=0');
    process.exit(1);
  }
  const id = `relation-${from}-${to}-${type}`;
  const payload = { version: 1, id, action: 'relation', decision: 'revoked', type, from, to };
  const human = `관계 철회: #${from} --${type}--> #${to}`;
  const posted = postDecision(tracker, from, payload, human);
  if (!posted.ok) { console.error(`✗ 철회 코멘트 게시 실패: ${posted.err ?? ''}`); console.log('UNLINKED=0'); process.exit(1); }

  console.log(`✓ #${from} 에 관계 철회 코멘트 게시 (${id})`);
  console.log('  본문 마커·GitHub 네이티브 의존성으로 생긴 엣지는 이 방식으로 지워지지 않는다.');
  console.log('  그래프 재동기화 중...');
  console.log('');
  cmdSync(root, tracker, opts);
  console.log('');
  console.log('UNLINKED=1');
  console.log(`UNLINK=${from}|${to}|${type}`);
  console.log(`DECISION_ID=${id}`);
}

function label(graph, num) {
  const n = graph.nodes[String(num)];
  return n ? `#${num} ${n.title}` : `#${num} (그래프에 없음)`;
}

function cmdPlan(root, tracker, opts) {
  const graph = loadGraph(root, tracker.provider);
  const problems = auditGraph(graph);
  const cycle = findCycle(graph);
  if (cycle) problems.push(`순환 의존: ${cycle.join(' → ')}`);
  if (problems.length) { console.error(`✗ 안전하지 않은 그래프라 plan을 만들지 않는다: ${problems.join(' / ')}`); console.log('READY_NUMBERS='); process.exit(2); }
  if (!Object.keys(graph.nodes).length) {
    console.log('그래프가 비어 있다. 먼저 `sync` 를 실행하라.');
    console.log('READY_NUMBERS=');
    return;
  }
  const c = classify(graph);
  const prio = (num) => { const r = priorityRank(graph.nodes[String(num)]); return r < 9 ? ` [P${r}]` : ''; };

  if (opts.json) {
    console.log(JSON.stringify({
      ready: c.ready, blocked: c.blocked, inProgress: c.inProgress, done: c.done,
    }, null, 2));
    return;
  }

  console.log('# 이슈 DAG todo\n');
  console.log(`## ▶ 착수 가능 (ready) — ${c.ready.length}개`);
  if (c.ready.length) for (const n of c.ready) console.log(`  - ${label(graph, n)}${prio(n)}`);
  else console.log('  (없음)');
  console.log('');
  console.log(`## ⏳ 진행 중 (in-progress) — ${c.inProgress.length}개`);
  if (c.inProgress.length) for (const n of c.inProgress) console.log(`  - ${label(graph, n)} (${graph.nodes[String(n)].status})`);
  else console.log('  (없음)');
  console.log('');
  console.log(`## ⛔ 막힘 (blocked) — ${c.blocked.length}개`);
  if (c.blocked.length) for (const b of c.blocked) console.log(`  - ${label(graph, b.num)}  ← 대기: ${b.blockers.map((x) => `#${x}`).join(', ')}`);
  else console.log('  (없음)');
  console.log('');
  console.log(`## ✔ 완료 (done) — ${c.done.length}개`);
  if (c.done.length) console.log(`  ${c.done.map((x) => `#${x}`).join(', ')}`);
  else console.log('  (없음)');
  console.log('');
  console.log(`READY_NUMBERS=${c.ready.join(' ')}`);
  console.log(`BLOCKED_NUMBERS=${c.blocked.map((b) => b.num).join(' ')}`);
  console.log(`IN_PROGRESS_NUMBERS=${c.inProgress.join(' ')}`);
  console.log(`DONE_NUMBERS=${c.done.join(' ')}`);
}

function cmdNext(root, tracker) {
  const graph = loadGraph(root, tracker.provider);
  const problems = auditGraph(graph);
  const cycle = findCycle(graph);
  if (cycle) problems.push(`순환 의존: ${cycle.join(' → ')}`);
  if (problems.length) { console.error(`✗ 안전하지 않은 그래프라 next를 추천하지 않는다: ${problems.join(' / ')}`); console.log('NEXT_ISSUE='); process.exit(2); }
  const c = classify(graph);
  if (!c.ready.length) {
    console.log(c.inProgress.length
      ? `착수 가능한 이슈가 없다. 진행 중: ${c.inProgress.map((n) => `#${n}`).join(', ')}`
      : '착수 가능한 이슈가 없다. `sync` 로 그래프를 갱신하거나 막힌 이슈의 선행을 끝내라.');
    console.log('NEXT_ISSUE=');
    return;
  }
  const n = c.ready[0];
  console.log(`다음 착수 추천: ${label(graph, n)}`);
  console.log('');
  console.log(`NEXT_ISSUE=${n}`);
  console.log(`NEXT=/issue-start #${n}`);
}

function graphFromFile(root, file) {
  const resolved = path.resolve(root, file);
  if (!existsSync(resolved)) {
    console.error('✗ 그래프 파일이 없습니다: ' + resolved);
    process.exit(1);
  }
  try {
    return JSON.parse(readFileSync(resolved, 'utf8'));
  } catch (error) {
    console.error('✗ 그래프 JSON 파싱 실패: ' + error.message);
    process.exit(1);
  }
}

function ontologyProblems(graph, { required = false } = {}) {
  if (!ontologyModule || ontologyModule.ontologyAvailable === false) {
    if (required) {
      console.error('✗ Ajv 온톨로지를 사용할 수 없습니다. tools/issue-ontology에서 npm install을 실행하세요.');
      if (ontologyLoadError) console.error('  ' + ontologyLoadError.message);
      process.exit(2);
    }
    return [];
  }
  try {
    const result = ontologyModule.validateGraphDocument(graph);
    return result.valid ? [] : result.errors.map((error) =>
      (error.instancePath || '/') + ' ' + error.message);
  } catch (error) {
    if (required) {
      console.error('✗ Ajv 온톨로지 검증 실패: ' + error.message);
      process.exit(2);
    }
    throw error;
  }
}

function cmdValidate(root, tracker, opts = {}) {
  const graph = opts.graph ? graphFromFile(root, opts.graph) : loadGraph(root, tracker.provider);
  const shapeProblems = ontologyProblems(graph, { required: true });
  if (shapeProblems.length) {
    console.log('AJV_VALID=0');
    for (const problem of shapeProblems) console.log('  - ' + problem);
    console.log('VALID=0');
    console.log('PROBLEMS=' + shapeProblems.length);
    process.exit(1);
  }
  console.log('AJV_VALID=1');
  const problems = auditGraph(graph);

  const cycle = findCycle(graph);
  if (cycle) problems.push(`순환 의존: ${cycle.join(' → ')}`);

  for (const e of graph.edges) {
    if (!graph.nodes[String(e.from)]) problems.push(`dangling 엣지: from #${e.from} 노드 없음 (${e.from}→${e.to})`);
    if (!graph.nodes[String(e.to)]) problems.push(`dangling 엣지: to #${e.to} 노드 없음 (${e.from}→${e.to})`);
    if (!EDGE_TYPES.includes(e.type)) problems.push(`알 수 없는 엣지 타입: ${e.type} (${e.from}→${e.to})`);
  }

  // close 불일치: done 인 노드가 아직 done 이 아닌 선행에 의존.
  const prereq = prereqMap(graph);
  for (const num of Object.keys(graph.nodes).map(Number)) {
    if (graph.nodes[String(num)].status !== DONE) continue;
    for (const dep of prereq.get(num) ?? []) {
      const d = graph.nodes[String(dep)];
      if (d && d.status !== DONE) problems.push(`close 불일치: #${num} 는 done 인데 선행 #${dep} 가 미완`);
    }
  }

  if (!problems.length) {
    console.log('✓ 문제 없음 — V2 snapshot·관계·DAG 정상');
    console.log('');
    console.log('VALID=1');
    console.log('PROBLEMS=0');
    return;
  }
  console.log('✗ 문제 발견:');
  for (const p of problems) console.log(`  - ${p}`);
  console.log('');
  console.log('VALID=0');
  console.log(`PROBLEMS=${problems.length}`);
  process.exit(cycle ? 2 : 1);
}

function cmdMigrate(root, tracker, opts) {
  const graph = loadGraph(root, tracker.provider);
  if (graph.version === GRAPH_VERSION) { console.log('MIGRATED=0'); console.log('MIGRATION_STATUS=already-v2'); return; }
  if (graph.version !== 1) { console.error(`✗ 지원하지 않는 마이그레이션 원본: V${graph.version}`); console.log('MIGRATED=0'); process.exit(2); }
  const migrated = migrateGraphV1(graph, { now: opts.now });
  const file = saveGraph(root, migrated, { now: migrated.updatedAt });
  console.log(`✓ V1 → V2 마이그레이션 완료: ${path.relative(root, file)}`);
  console.log('  이 캐시는 migrating 상태다. GitHub sync 전에는 plan/next를 실행하지 않는다.');
  console.log('MIGRATED=1'); console.log('MIGRATION_STATUS=migrating');
}

function cmdAudit(root, tracker) {
  const problems = auditGraph(loadGraph(root, tracker.provider));
  if (problems.length) { console.log('AUDIT=0'); console.log(`PROBLEMS=${problems.length}`); for (const problem of problems) console.log(`  - ${problem}`); process.exit(2); }
  console.log('AUDIT=1'); console.log('PROBLEMS=0');
}

/** 형제 스킬 스크립트. 프로젝트 로컬·홈 전역·링크 개발 설치를 모두 본다. */
function siblingSkill(root, skill, script) {
  return resolveSkillScript(import.meta.url, skill, script, { root });
}

/** 못 찾았을 때 어디를 확인해야 하는지 알려 주는 메시지. */
function missingSkill(skill) {
  return `${skill} 스킬을 찾지 못했다. 플러그인의 skills/ 또는 사용자 스킬 디렉터리에 설치돼 있는지 확인하라.`;
}

/**
 * 온보딩 전에 그래프 캐시를 다시 만들어야 하는 이유를 반환한다.
 * null 이면 완전한 캐시가 현재 열린 이슈 목록과 일치한다.
 */
export function graphBootstrapReason(graph, { fileExists = true, openIssues = [] } = {}) {
  if (!fileExists) return 'missing';
  if (graph.snapshot?.status === 'invalid') return 'invalid';
  if (graph.snapshot?.status !== 'complete') return 'snapshot-incomplete';

  if (ontologyProblems(graph).length) return 'invalid';

  const nodeCount = Object.keys(graph.nodes ?? {}).length;
  if (!nodeCount) return openIssues.length ? 'empty' : null;

  const problems = auditGraph(graph);
  if (problems.length) return 'invalid';

  for (const issue of openIssues) {
    const node = graph.nodes[String(issue.number)];
    if (!node) return 'open-issue-missing';
    if (issue.title && node.title !== issue.title) return 'open-issue-changed';
    if (issue.updatedAt && node.provenance?.revision !== issue.updatedAt) return 'open-issue-changed';
  }
  return null;
}

export function runSyncBootstrap(root, { resolve = siblingSkill, spawn = spawnSync } = {}) {
  const sync = resolve(root, 'issue-sync', 'issue-sync.mjs');
  if (!sync) throw new Error(missingSkill('issue-sync'));
  return spawn(process.execPath, [sync], { cwd: root, encoding: 'utf8' });
}

export function syncBootstrapComplete(result) {
  const stdout = String(result.stdout ?? '');
  return result.status === 0
    && stdout.includes('SNAPSHOT_STATUS=complete')
    && stdout.includes('GRAPH_SYNC=ok');
}

function bootstrapWithSync(root, reason) {
  const result = runSyncBootstrap(root);
  process.stdout.write(result.stdout ?? '');
  process.stderr.write(result.stderr ?? '');
  if (result.status !== 0) process.exit(result.status ?? 1);
  if (!syncBootstrapComplete(result)) {
    console.error('✗ issue-sync가 complete snapshot과 GRAPH_SYNC=ok를 확인하지 못했다.');
    process.exit(1);
  }
  console.log('GRAPH_BOOTSTRAP=issue-sync');
  console.log('GRAPH_BOOTSTRAP_REASON=' + reason);
}

function cmdOnboard(root, tracker, opts) {
  let graph = loadGraph(root, tracker.provider, { tolerateParseError: true });
  let openIssues = tracker.issueList({ state: 'open', limit: 200, fields: 'number,title,labels,url,state,updatedAt' });
  if (openIssues === null) throw new Error('GitHub 열린 이슈를 조회하지 못했다.');
  const reason = graphBootstrapReason(graph, { fileExists: existsSync(graphPath(root)), openIssues });
  if (reason) {
    bootstrapWithSync(root, reason);
    graph = loadGraph(root, tracker.provider);
    openIssues = tracker.issueList({ state: 'open', limit: 200, fields: 'number,title,labels,url,state,updatedAt' });
    if (openIssues === null) throw new Error('동기화 후 GitHub 열린 이슈를 조회하지 못했다.');
    const postSyncReason = graphBootstrapReason(graph, { openIssues });
    if (postSyncReason) throw new Error(`동기화 후 그래프가 최신 열린 이슈와 일치하지 않는다: ${postSyncReason}`);
  }
  const problems = auditGraph(graph);
  problems.push(...ontologyProblems(graph));
  const cycle = findCycle(graph);
  if (cycle) problems.push(`순환 의존: ${cycle.join(' → ')}`);
  if (problems.length) throw new Error(`안전하지 않은 그래프: ${problems.join(' / ')}`);
  const groups = classify(graph);
  const openNumbers = new Set(openIssues.map((issue) => issue.number));
  const ordered = [...groups.ready, ...groups.inProgress, ...groups.blocked.map((item) => item.num)]
    .filter((number) => openNumbers.has(number));
  const visible = opts.all ? ordered : ordered.slice(0, 6);
  console.log(`OPEN_ISSUES=${openIssues.length}`);
  console.log(`ONBOARD_COUNT=${visible.length}`);
  for (const number of visible) console.log(`PRIORITY=#${number}\t${graph.nodes[String(number)].title}`);
  console.log(`MORE_AVAILABLE=${ordered.length > visible.length ? 1 : 0}`);
  console.log('NEXT_ACTIONS=issue-start,issue-merge,issue-create');
}

/* ------------------------------------------------------------------- usage */

function usage(exitCode = 1) {
  console.error(`Usage:
  node issue-onboard.mjs [--all]
  node issue-onboard.mjs sync [--state open|closed|all] [--limit <n>] [--no-llm] [--no-native]
  node issue-onboard.mjs link <from> <to> [--type ${EDGE_TYPES.join('|')}] [--why "<근거>"]
  node issue-onboard.mjs unlink <from> <to> [--type <type>]
  node issue-onboard.mjs plan [--json]
  node issue-onboard.mjs next
  node issue-onboard.mjs validate [--graph <path>]
  node issue-onboard.mjs audit
  node issue-onboard.mjs migrate

엣지 방향: from --depends-on--> to = "from 은 to 가 close 전엔 착수 불가" (to 가 선수).
link/unlink 는 대상(from) 이슈에 구조화된 승인/철회 결정 코멘트를 남기고 재-sync 한다.
sync 는 본문 마커(depends on #N)·결정 코멘트·GitHub 네이티브 의존성(blocked-by)을 함께 읽는다.
그래프: ${WORKSPACE_DIR}/${GRAPH_FILE} (GitHub 정본에서 재생성하는 로컬 캐시).
이슈 백엔드는 ~/.issue/settings.json 의 provider.type 이 정한다 (github 기본 | jira).
`);
  process.exit(exitCode);
}

/* ------------------------------------------------------------------- main */

function main() {
  const argv = process.argv.slice(2);
  if (argv.includes('-h') || argv.includes('--help')) usage(0);

  const optionFirst = argv[0]?.startsWith('-');
  let mode = optionFirst ? 'onboard' : argv[0] ?? 'onboard';
  const MODES = ['onboard', 'sync', 'link', 'unlink', 'plan', 'next', 'validate', 'audit', 'migrate'];
  if (!MODES.includes(mode)) { console.error(`✗ 알 수 없는 모드: ${argv[0]}`); usage(); }

  const opts = {};
  const positionals = [];
  for (let i = optionFirst ? 0 : 1; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--json') opts.json = true;
    else if (arg === '--type') opts.type = argv[++i];
    else if (arg === '--why') opts.why = argv[++i];
    else if (arg === '--state') opts.state = argv[++i];
    else if (arg === '--limit') opts.limit = argv[++i];
    else if (arg === '--repo') opts.repo = argv[++i];
    else if (arg === '--all') opts.all = true;
    else if (arg === '--no-llm') opts.noLlm = true;
    else if (arg === '--no-native') opts.noNative = true;
    else if (arg === '--graph') opts.graph = argv[++i];
    else if (arg.startsWith('-')) { console.error(`✗ 알 수 없는 옵션: ${arg}`); usage(); }
    else positionals.push(arg);
  }

  const root = repoRoot();
  const tracker = createTracker(root, { repo: opts.repo });

  if (mode === 'onboard') cmdOnboard(root, tracker, opts);
  else if (mode === 'sync') cmdSync(root, tracker, opts);
  else if (mode === 'link') cmdLink(root, tracker, parseIssueNumber(positionals[0]), parseIssueNumber(positionals[1]), opts);
  else if (mode === 'unlink') cmdUnlink(root, tracker, parseIssueNumber(positionals[0]), parseIssueNumber(positionals[1]), opts);
  else if (mode === 'plan') cmdPlan(root, tracker, opts);
  else if (mode === 'next') cmdNext(root, tracker);
  else if (mode === 'validate') cmdValidate(root, tracker, opts);
  else if (mode === 'audit') cmdAudit(root, tracker);
  else if (mode === 'migrate') cmdMigrate(root, tracker, opts);
}

/** 심볼릭 링크로 설치돼도 진입점 판별이 어긋나지 않게 realpath 로 비교한다. */
function isMainModule(metaUrl) {
  const entry = process.argv[1];
  if (!entry) return false;
  const here = fileURLToPath(metaUrl);
  const resolved = path.resolve(entry);
  if (here === resolved) return true;
  try { return realpathSync(here) === realpathSync(resolved); } catch { return false; }
}

if (isMainModule(import.meta.url)) main();
