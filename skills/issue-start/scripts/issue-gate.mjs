#!/usr/bin/env node
/**
 * issue-gate.mjs — issue-start 단계 잠금(gate), 실행 broker, Claude PreToolUse hook.
 *
 * pure phase API(`issue-start.mjs phase --request`)는 요청에 담긴 state·effectResult 를 그대로 믿는다.
 * 이 파일은 그 API 앞에 서는 신뢰 orchestrator 다.
 *   - 상태는 호출자가 아니라 git common dir 아래 ledger 에서만 만든다.
 *   - 단계마다 lock(owner·fence)을 원자적으로 잡고, 실제 파일·git·tracker 사실을 확인한 뒤에만
 *     API 에 effectResult 를 넘겨 다음 단계를 연다. 호출자의 성공 주장을 받는 입력은 없다.
 *   - 명령 실행은 controller 가 등록한 profile 과 승인한 tree digest 로만 한다(gate exec).
 *   - hook 은 Claude 도구 호출을 lock·단계·세션에 맞춰 허용/거절하고, Bash 는 읽기 전용 명령과
 *     신뢰 경로의 gate CLI 만 통과시킨다.
 *
 * 보장 범위: Claude 도구 호출과 이 CLI·issue-start.mjs 를 거치는 경로. 같은 OS 사용자의 임의
 * 프로세스(직접 연 터미널, 승인된 profile 이 실행한 코드 내부)를 막는 OS sandbox 가 아니다.
 *
 * 종료 코드: 0 성공 · 2 사용법/순서/API 거절 · 3 선행조건·사실 확인·승인·격리 미충족
 *           4 효과·명령 실행 실패 · 5 다른 owner 잠금/소유자 아님/controller 전용
 *           6 UNKNOWN(죽은 owner) — 자식 종료 확인 또는 운영자 재조정 필요 · 7 legacy 경로 거절
 */
import {
  appendFileSync, existsSync, linkSync, lstatSync, mkdirSync, readFileSync,
  readdirSync, realpathSync, renameSync, statSync, unlinkSync, writeFileSync,
} from 'node:fs';
import { createHash, randomUUID } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { canonicalJsonBytes, canonicalJsonSha256 } from './issue-phase-contract.mjs';

const SELF = realpathSync(fileURLToPath(import.meta.url));
const HERE = path.dirname(SELF);
const ISSUE_START = path.join(HERE, 'issue-start.mjs');

export const PHASES = Object.freeze([
  'intake', 'fetch', 'classify', 'plan', 'worktree', 'before', 'implement',
  'commit', 'after', 'publish-evidence', 'comment', 'sync-base', 'handback',
]);
// gate 프로세스가 proposedEffect 를 직접 실행하는 단계
const RUN_PHASES = new Set(['intake', 'fetch', 'worktree', 'commit', 'publish-evidence', 'comment', 'sync-base', 'handback']);
// 세션(Claude)이 직접 작업하고 gate 가 사실로 완료를 확인하는 단계
const SESSION_PHASES = new Set(['classify', 'plan', 'before', 'implement', 'after']);
const EXEC_PHASES = new Set(['before', 'implement', 'after']);
const ADOPT_LIMIT = 'before';
const CLASSIFICATIONS = ['frontend', 'backend', 'both', 'neither'];

class GateError extends Error {
  constructor(code, exitCode, message, detail = {}) {
    super(message);
    this.code = code;
    this.exitCode = exitCode;
    this.detail = detail;
  }
}

const fq = (phase) => `issue-start.${phase}`;
const sha256 = (buf) => createHash('sha256').update(buf).digest('hex');

function run(cmd, args, opts = {}) {
  const r = spawnSync(cmd, args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, ...opts });
  return { code: r.status ?? 1, out: (r.stdout || '').trim(), err: (r.stderr || '').trim() };
}
const git = (cwd, args, env) => run('git', args, { cwd, ...(env ? { env: { ...process.env, ...env } } : {}) });

function realOrSelf(p) {
  try { return realpathSync(p); } catch { return p; }
}

/** 존재하지 않는 파일도 가장 가까운 실제 조상으로 정규화한다(symlink 우회 방지). */
function realTarget(p) {
  const parts = [];
  let current = path.resolve(p);
  while (!existsSync(current)) {
    const parent = path.dirname(current);
    if (parent === current) break;
    parts.unshift(path.basename(current));
    current = parent;
  }
  return path.join(realOrSelf(current), ...parts);
}

/* ------------------------------------------------------------ git 위치 */

/** git 을 띄우지 않고 target 이 속한 checkout·common dir·branch 를 찾는다 (hook 은 빨라야 한다). */
export function gitContext(target) {
  let current = realTarget(target);
  while (!existsSync(current)) current = path.dirname(current);
  if (!lstatSync(current).isDirectory()) current = path.dirname(current);
  while (true) {
    const dotGit = path.join(current, '.git');
    if (existsSync(dotGit)) {
      let gitDir = dotGit;
      if (!statSync(dotGit).isDirectory()) {
        const m = readFileSync(dotGit, 'utf8').match(/^gitdir:\s*(.+)$/m);
        if (!m) return null;
        gitDir = path.resolve(current, m[1].trim());
      }
      let commonDir = gitDir;
      const commonFile = path.join(gitDir, 'commondir');
      if (existsSync(commonFile)) commonDir = path.resolve(gitDir, readFileSync(commonFile, 'utf8').trim());
      let branch = null;
      try {
        const head = readFileSync(path.join(gitDir, 'HEAD'), 'utf8').trim();
        if (head.startsWith('ref: refs/heads/')) branch = head.slice('ref: refs/heads/'.length);
      } catch { /* detached 또는 손상 — branch 없음 */ }
      return { top: current, gitDir: realOrSelf(gitDir), commonDir: realOrSelf(commonDir), branch };
    }
    const parent = path.dirname(current);
    if (parent === current) return null;
    current = parent;
  }
}

export function inferIssue(branch) {
  if (!branch) return null;
  const m = branch.match(/(?:^|[/_])(\d{1,6})(?:[-_/]|$)/);
  return m ? Number(m[1]) : null;
}

const storeRoot = (commonDir) => path.join(commonDir, 'samsara-gate');
const issueStore = (commonDir, issue) => path.join(storeRoot(commonDir), `issue-${issue}`);

function isWithin(root, target) {
  const rel = path.relative(root, target);
  return rel === '' || (!path.isAbsolute(rel) && rel !== '..' && !rel.startsWith(`..${path.sep}`));
}

/* ------------------------------------------------------------ 영속 파일 */

function writeJsonAtomic(file, value) {
  const tmp = `${file}.${process.pid}.${randomUUID()}.tmp`;
  writeFileSync(tmp, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
  renameSync(tmp, file);
}

function readJson(file) {
  return JSON.parse(readFileSync(file, 'utf8'));
}

const ledgerFile = (dir) => path.join(dir, 'ledger.json');
const lockFile = (dir) => path.join(dir, 'lock.json');
const controllerFile = (dir) => path.join(dir, 'controller.json');

/** controller 가 승인한 profile·tree. 이슈 lock 과 분리돼 있어 세션이 단계 lock 을 쥔 동안에도 승인할 수 있다. */
function readController(dir) {
  return existsSync(controllerFile(dir)) ? readJson(controllerFile(dir)) : { profiles: {}, execApprovals: [] };
}

function readLedger(dir) {
  if (!existsSync(ledgerFile(dir))) return null;
  const ledger = readJson(ledgerFile(dir));
  if (ledger?.schema !== 'samsara-gate/v1') throw new GateError('LEDGER_INVALID', 2, `ledger schema mismatch: ${dir}`);
  return ledger;
}

/** lock 이 없으면 null. 읽을 수 없으면 예외 — 호출자는 fail-closed 로 처리한다. */
function readLock(dir) {
  if (!existsSync(lockFile(dir))) return null;
  const lock = readJson(lockFile(dir));
  if (!lock || !Number.isSafeInteger(lock.fence) || !lock.owner || !Number.isSafeInteger(lock.owner.pid)) {
    throw new GateError('LOCK_INVALID', 2, `lock file is malformed: ${lockFile(dir)}`);
  }
  return lock;
}

function logAttempt(dir, entry) {
  appendFileSync(path.join(dir, 'attempts.jsonl'), `${JSON.stringify({ at: new Date().toISOString(), ...entry })}\n`);
}

/* ------------------------------------------------------------ owner·lock */

function pidStart(pid) {
  const r = run('ps', ['-o', 'lstart=', '-p', String(pid)]);
  return r.code === 0 && r.out ? r.out : null;
}

/** pid 생존 + 시작 시각 일치. pid 재사용이나 zombie 를 산 owner 로 오인하지 않는다. */
export function ownerAlive(owner) {
  if (!owner || !Number.isSafeInteger(owner.pid) || owner.pid <= 0) return false;
  try {
    process.kill(owner.pid, 0);
  } catch (error) {
    if (error?.code !== 'EPERM') return false;
  }
  const stat = run('ps', ['-o', 'stat=', '-p', String(owner.pid)]);
  if (stat.code !== 0 || stat.out.startsWith('Z')) return false;
  return pidStart(owner.pid) === owner.pidStart;
}

/** 효과를 실행한 자식 프로세스 그룹에 아직 살아 있는 프로세스가 있는지. */
function groupAlive(pgid) {
  if (!Number.isSafeInteger(pgid) || pgid <= 0) return false;
  try {
    process.kill(-pgid, 0);
  } catch (error) {
    return error?.code === 'EPERM';
  }
  const r = run('ps', ['-A', '-o', 'pgid=,stat=']);
  return r.out.split('\n').some((line) => {
    const [g, s] = line.trim().split(/\s+/);
    return Number(g) === pgid && !String(s).startsWith('Z');
  });
}

function sessionOwner() {
  const session = process.env.CLAUDE_CODE_SESSION_ID;
  const pid = Number(process.env.CLAUDE_PID);
  if (!session || !Number.isSafeInteger(pid) || pid <= 0) {
    throw new GateError('SESSION_REQUIRED', 2, 'begin/complete/abort/exec need CLAUDE_CODE_SESSION_ID and CLAUDE_PID (run inside a Claude Code session)');
  }
  const start = pidStart(pid);
  if (!start) throw new GateError('SESSION_REQUIRED', 2, `CLAUDE_PID ${pid} is not a live process`);
  return { kind: 'session', session, pid, pidStart: start };
}

function processOwner() {
  return {
    kind: 'process',
    session: process.env.CLAUDE_CODE_SESSION_ID || null,
    pid: process.pid,
    pidStart: pidStart(process.pid),
  };
}

const sameOwner = (a, b) => a.kind === b.kind && a.session === b.session && a.pid === b.pid && a.pidStart === b.pidStart;

/** 원자적 잠금: 완성된 내용을 tmp 에 쓰고 link 로 만든다(EEXIST 면 실패). 빈 lock 이 보이는 순간이 없다. */
function acquire(dir, owner, phase) {
  const ledger = readLedger(dir);
  const draft = { fence: ledger.fence + 1, phase, owner, stage: 'acquired', acquiredAt: new Date().toISOString() };
  const tmp = `${lockFile(dir)}.${process.pid}.${randomUUID()}.tmp`;
  writeFileSync(tmp, JSON.stringify(draft), { mode: 0o600 });
  try {
    linkSync(tmp, lockFile(dir));
  } catch (error) {
    if (error?.code !== 'EEXIST') throw error;
    throw heldError(readLock(dir));
  } finally {
    unlinkSync(tmp);
  }
  // 이전 holder 가 같은 fence 를 썼을 수 있으므로 잠금을 쥔 뒤 ledger 를 다시 읽어 단조 증가시킨다.
  const fresh = readLedger(dir);
  const lock = { ...draft, fence: Math.max(draft.fence, fresh.fence + 1) };
  if (lock.fence !== draft.fence) writeJsonAtomic(lockFile(dir), lock);
  fresh.fence = lock.fence;
  writeJsonAtomic(ledgerFile(dir), fresh);
  return lock;
}

/** controller.json 전용 mutex(link 기반). 죽은 보유자는 회수한다. */
function withControllerMutex(dir, fn) {
  const mutex = path.join(dir, 'controller.lock');
  const tmp = `${mutex}.${process.pid}.${randomUUID()}.tmp`;
  writeFileSync(tmp, JSON.stringify(processOwner()));
  try {
    try {
      linkSync(tmp, mutex);
    } catch (error) {
      if (error?.code !== 'EEXIST') throw error;
      let other = null;
      try { other = readJson(mutex); } catch { /* 손상 — 죽은 것으로 본다 */ }
      if (other && ownerAlive(other)) throw new GateError('LOCKED', 5, 'another controller update is running');
      unlinkSync(mutex);
      linkSync(tmp, mutex);
    }
  } finally {
    unlinkSync(tmp);
  }
  try {
    const controller = readController(dir);
    const result = fn(controller);
    writeJsonAtomic(controllerFile(dir), controller);
    return result;
  } finally {
    unlinkSync(mutex);
  }
}

function heldError(held) {
  if (held && ownerAlive(held.owner)) {
    return new GateError('LOCKED', 5, `issue lock is held by a live owner (phase ${held.phase}, fence ${held.fence})`, { lock: held });
  }
  return new GateError('RECONCILE_REQUIRED', 6, `previous owner of phase ${held?.phase} is gone; result is UNKNOWN — run gate reconcile`, { lock: held });
}

/** 죽은 owner 의 lock 이 남아 있으면 어떤 단계도 시작하지 않는다(UNKNOWN 우선). */
function assertNoUnknown(dir) {
  const held = readLock(dir);
  if (held && !ownerAlive(held.owner)) throw heldError(held);
}

function assertHolder(dir, lock) {
  const current = readLock(dir);
  if (!current || current.fence !== lock.fence || !sameOwner(current.owner, lock.owner)) {
    throw new GateError('FENCE_LOST', 5, `lock fence ${lock.fence} is no longer held`);
  }
}

function updateLock(dir, lock, patch) {
  assertHolder(dir, lock);
  Object.assign(lock, patch);
  writeJsonAtomic(lockFile(dir), lock);
}

function release(dir, lock) {
  const current = readLock(dir);
  if (current && current.fence === lock.fence && sameOwner(current.owner, lock.owner)) unlinkSync(lockFile(dir));
}

function writeLedgerAsHolder(dir, lock, ledger) {
  assertHolder(dir, lock);
  writeJsonAtomic(ledgerFile(dir), ledger);
}

/* ------------------------------------------------------------ pure phase API 연결 */

/**
 * `git status --porcelain=v1 -z` 를 trim 없이 NUL 단위로 파싱한다. 선행 공백(` M`)·공백 경로·rename 원경로를 보존한다.
 * 실패하면 null.
 */
export function statusEntries(cwd, untracked) {
  const r = spawnSync('git', ['status', '--porcelain=v1', '-z', `--untracked-files=${untracked}`], { cwd, maxBuffer: 64 * 1024 * 1024 });
  if (r.status !== 0) return null;
  const parts = r.stdout.toString('utf8').split('\0');
  const entries = [];
  for (let i = 0; i < parts.length; i += 1) {
    const item = parts[i];
    if (item.length < 4) continue;
    const xy = item.slice(0, 2);
    const entry = { xy, path: item.slice(3), orig: null };
    if (xy.includes('R') || xy.includes('C')) entry.orig = parts[++i] ?? null;
    entries.push(entry);
  }
  return entries;
}

const inIssueWorkspace = (p) => p === '.issue' || p.startsWith('.issue/');
const outsideIssue = (e) => !inIssueWorkspace(e.path) || (e.orig !== null && !inIssueWorkspace(e.orig));
const describe = (e) => `${e.xy} ${e.orig ? `${e.orig} -> ` : ''}${e.path}`;

function baseDirty(base) {
  const entries = statusEntries(base, 'no');
  if (!entries) return true;
  return entries.some(outsideIssue);
}

function phaseInput(ledger) {
  const n = String(ledger.issue);
  const base = ledger.baseCheckout;
  const wt = ledger.intended.worktree;
  const issueMd = path.join(base, '.issue', n, 'issue.md');
  const body = existsSync(issueMd) ? readFileSync(issueMd, 'utf8').trim() : '';
  const evidence = path.join(wt, '.issue', n, 'evidence');
  return {
    baseDirty: baseDirty(base),
    issueBody: body || `issue #${n}`,
    classification: ledger.classification,
    planPath: path.join(base, '.issue', n, 'plan.md'),
    intendedWorktree: wt,
    intendedBranch: ledger.intended.branch,
    commitMessage: ledger.commitMessage || `fix: #${n}`,
    beforeArtifact: path.join(evidence, 'before'),
    afterArtifact: path.join(evidence, 'after'),
    commentFile: path.join(evidence, 'comment.md'),
    baseBranch: ledger.intended.baseBranch,
  };
}

/** 기존 공개 계약 그대로 `issue-start.mjs phase --request` 를 부른다. state 는 ledger 에서만 온다. */
function callPhaseApi(dir, ledger, phase, effectResult) {
  const request = {
    apiVersion: 'issue-phase/v1',
    contractId: 'issue-start-phase-api-v1',
    phaseId: fq(phase),
    checkpoint: {
      id: `${fq(phase)}:${ledger.state.completedPhases.length}`,
      owner: 'issue-start',
      attempt: ledger.phaseAttempts[phase] ?? 1,
    },
    state: ledger.state,
    input: phaseInput(ledger),
    effectResult,
  };
  const file = path.join(dir, `request-${process.pid}-${randomUUID()}.json`);
  writeFileSync(file, canonicalJsonBytes(request), { mode: 0o600 });
  try {
    const r = spawnSync(process.execPath, [ISSUE_START, 'phase', '--request', file], { encoding: 'utf8' });
    let envelope = null;
    try { envelope = JSON.parse(r.stdout); } catch { /* exit 2 는 stdout 이 비어 있다 */ }
    if (r.status === 2 || !envelope) {
      throw new GateError('PHASE_API_REJECTED', 2, (r.stderr || r.stdout || 'phase API failed').trim());
    }
    return { exit: r.status, envelope };
  } finally {
    unlinkSync(file);
  }
}

/* ------------------------------------------------------------ 사실 확인 */

function trackedChanges(wt) {
  const entries = statusEntries(wt, 'all');
  if (!entries) throw new GateError('GIT_FAILED', 3, `git status failed in ${wt}`);
  return entries.filter(outsideIssue).map(describe);
}

function evidenceFiles(dir) {
  if (!existsSync(dir)) return [];
  const out = [];
  const walk = (d) => {
    for (const name of readdirSync(d).sort()) {
      const p = path.join(d, name);
      const st = lstatSync(p);
      if (st.isDirectory()) walk(p);
      else if (st.isFile() && st.size > 0) out.push({ path: path.relative(dir, p), sha256: sha256(readFileSync(p)) });
    }
  };
  walk(dir);
  return out;
}

const head = (wt) => {
  const r = git(wt, ['rev-parse', 'HEAD']);
  return r.code === 0 ? r.out : null;
};
const isAncestor = (wt, a, b) => git(wt, ['merge-base', '--is-ancestor', a, b]).code === 0;

/** .issue 를 뺀 작업 트리 전체(추적 + 미추적, ignore 제외)의 git tree hash. 실제 index 는 건드리지 않는다. */
function treeHash(wt, dir) {
  const index = path.join(dir, `index-${process.pid}-${randomUUID()}`);
  try {
    const env = { GIT_INDEX_FILE: index };
    // .issue 가 통째로 ignore 된 저장소도 있으므로 pathspec 으로 제외하지 않고, 전부 담은 뒤 .issue 만 HEAD 로 되돌린다
    for (const args of [['read-tree', 'HEAD'], ['add', '-A', '--', '.'], ['reset', '-q', 'HEAD', '--', '.issue']]) {
      const r = git(wt, args, env);
      if (r.code !== 0) throw new GateError('GIT_FAILED', 3, `git ${args[0]} failed: ${r.err}`);
    }
    const r = git(wt, ['write-tree'], env);
    if (r.code !== 0) throw new GateError('GIT_FAILED', 3, `git write-tree failed: ${r.err}`);
    return r.out;
  } finally {
    if (existsSync(index)) unlinkSync(index);
  }
}

/** 로컬 ref 전체 + worktree HEAD. 실행 전후 비교로 몰래 만든 commit·push(추적 ref 갱신)를 잡는다. */
function refSnapshot(wt) {
  const refs = git(wt, ['for-each-ref', '--format=%(refname) %(objectname)']).out;
  return sha256(Buffer.from(`${head(wt)}\n${refs}`));
}

function registeredWorktree(base, wt, branch) {
  const r = git(base, ['worktree', 'list', '--porcelain']);
  if (r.code !== 0) return false;
  let current = null;
  for (const line of r.out.split('\n')) {
    if (line.startsWith('worktree ')) current = realOrSelf(line.slice(9));
    else if (line === `branch refs/heads/${branch}` && current === wt) return true;
  }
  return false;
}

const ok = (facts = {}) => ({ ok: true, facts });
const no = (reason) => ({ ok: false, reason });

function remoteHead(wt, ref) {
  const r = git(wt, ['ls-remote', 'origin', ref]);
  return r.code === 0 ? (r.out.split(/\s+/)[0] || null) : null;
}

/** 단계 완료를 실제 사실로 확인한다. 호출자 주장은 입력으로 받지 않는다. */
function verifyPhase(phase, ledger, dir) {
  const n = String(ledger.issue);
  const base = ledger.baseCheckout;
  const wt = ledger.intended.worktree;
  const evidence = path.join(wt, '.issue', n, 'evidence');
  const prior = ledger.facts;
  switch (phase) {
    case 'intake':
    case 'handback':
      return ok();
    case 'fetch': {
      const file = path.join(base, '.issue', n, 'issue.json');
      if (!existsSync(file)) return no(`missing ${file}`);
      let data;
      try { data = readJson(file); } catch { return no(`unreadable ${file}`); }
      if (Number(data?.number) !== ledger.issue) return no(`${file} is not issue #${n}`);
      return ok({ issueJsonSha256: sha256(readFileSync(file)) });
    }
    case 'classify':
      return CLASSIFICATIONS.includes(ledger.classification) ? ok({ classification: ledger.classification }) : no('classification is not set');
    case 'plan': {
      const file = path.join(base, '.issue', n, 'plan.md');
      if (!existsSync(file)) return no(`missing ${file}`);
      const text = readFileSync(file, 'utf8');
      if (!/증거|evidence/i.test(text)) return no('plan.md has no evidence plan section');
      return ok({ planSha256: sha256(Buffer.from(text)) });
    }
    case 'worktree': {
      if (!existsSync(wt) || !registeredWorktree(base, wt, ledger.intended.branch)) {
        return no(`${wt} is not a registered worktree on ${ledger.intended.branch}`);
      }
      return ok({ head: head(wt) });
    }
    case 'before': {
      const files = evidenceFiles(path.join(evidence, 'before'));
      if (files.length === 0) return no('no before evidence files');
      const dirty = trackedChanges(wt);
      if (dirty.length) return no(`worktree is not pure before implementation: ${dirty.slice(0, 5).join(', ')}`);
      const h = head(wt);
      if (prior.worktree?.head && h !== prior.worktree.head) return no('HEAD moved between worktree creation and before capture');
      return ok({ head: h, files });
    }
    case 'implement': {
      const beforeHead = prior.before?.head;
      if (head(wt) !== beforeHead) return no('HEAD moved during implement — commits must go through gate run --phase commit');
      const changes = trackedChanges(wt);
      if (changes.length === 0) return no('no implementation changes in the worktree');
      const tree = treeHash(wt, dir);
      // 필수 검증 profile 은 지금 tree 에서 마지막 실행이 성공이어야 한다(실패·오래된 tree 의 PASS 불가)
      const required = Object.values(readController(dir).profiles).filter((p) => p.required && p.phases.includes('implement'));
      const passed = [];
      for (const p of required) {
        const last = [...(ledger.execResults || [])].reverse().find((r) => r.profile === p.name && r.phase === 'implement');
        if (!last || last.digest !== p.digest || last.tree !== tree || last.exit !== 0) {
          return no(`required profile ${p.name} has no passing run on the current tree ${tree} (last: ${last ? `exit ${last.exit} on ${last.tree}` : 'none'})`);
        }
        passed.push({ profile: p.name, tree });
      }
      return ok({ changes, tree, passed });
    }
    case 'commit': {
      const beforeHead = prior.before?.head;
      const now = head(wt);
      if (!now || now === beforeHead) return no('no new commit after before capture');
      if (git(wt, ['rev-parse', `${now}^`]).out !== beforeHead) return no('implementation must be exactly one commit on top of the before HEAD');
      const files = git(wt, ['diff', '--name-only', beforeHead, now]).out.split('\n').filter(Boolean);
      if (files.length === 0) return no('implementation commit is empty');
      if (files.some((f) => f.startsWith('.issue/'))) return no('implementation commit contains .issue files');
      const committedTree = git(wt, ['rev-parse', `${now}^{tree}`]).out;
      if (prior.implement?.tree && committedTree !== prior.implement.tree) return no('committed tree differs from the verified implement tree');
      const dirty = trackedChanges(wt);
      if (dirty.length) return no(`uncommitted changes remain: ${dirty.slice(0, 5).join(', ')}`);
      return ok({ implementationCommit: now, files });
    }
    case 'after': {
      const files = evidenceFiles(path.join(evidence, 'after'));
      if (files.length === 0) return no('no after evidence files');
      if (head(wt) !== prior.commit?.implementationCommit) return no('HEAD changed after the implementation commit');
      if (trackedChanges(wt).length) return no('code changed after the implementation commit');
      return ok({ files });
    }
    case 'publish-evidence': {
      const impl = prior.commit?.implementationCommit;
      const now = head(wt);
      if (!impl || now === impl || !isAncestor(wt, impl, now)) return no('no evidence commit on top of the implementation commit');
      const files = git(wt, ['diff', '--name-only', impl, now]).out.split('\n').filter(Boolean);
      if (files.some((f) => !f.startsWith(`.issue/${n}/evidence/`))) return no('evidence commit touches non-evidence files');
      if (remoteHead(wt, `refs/heads/${ledger.intended.branch}`) !== now) return no('branch is not pushed at the evidence commit');
      // proposedEffect 의 evidence-mirror 단계: base 또는 폴백 evidence/issue-<n> 에 같은 증거 tree 가 있어야 한다
      const evidenceTree = git(wt, ['rev-parse', `${now}:.issue/${n}/evidence`]).out;
      for (const ref of [ledger.intended.baseBranch, `evidence/issue-${n}`]) {
        const sha = remoteHead(wt, `refs/heads/${ref}`);
        if (!sha) continue;
        if (git(wt, ['fetch', '--quiet', 'origin', `refs/heads/${ref}`]).code !== 0) continue;
        const mirrored = git(wt, ['rev-parse', `${sha}:.issue/${n}/evidence`]);
        if (mirrored.code === 0 && mirrored.out === evidenceTree) return ok({ evidenceCommit: now, mirrorRef: ref, mirrorCommit: sha });
      }
      return no('evidence mirror is not on the remote base or evidence/issue-<n> branch');
    }
    case 'comment': {
      const file = path.join(evidence, 'comment.md');
      if (!existsSync(file)) return no('comment.md missing');
      const body = readFileSync(file, 'utf8').trim();
      const r = run('gh', ['issue', 'view', n, '--json', 'comments'], { cwd: wt });
      if (r.code !== 0) return no(`gh issue view failed: ${r.err}`);
      const found = (JSON.parse(r.out).comments || []).find((c) => String(c.body || '').trim() === body);
      return found ? ok({ commentUrl: found.url ?? null }) : no('no tracker comment matches comment.md');
    }
    case 'sync-base': {
      const b = ledger.intended.baseBranch;
      if (git(base, ['fetch', 'origin', b]).code !== 0) return no('git fetch failed');
      return isAncestor(base, `origin/${b}`, `refs/heads/${b}`) ? ok() : no(`${b} is behind origin/${b}`);
    }
    default:
      return no(`unknown phase ${phase}`);
  }
}

/** ledger 가 주장하는 과거 완료 사실이 지금도 남아 있는지 본다(위조된 ledger 방어). */
function verifyPriorFacts(ledger) {
  const n = String(ledger.issue);
  const wt = ledger.intended.worktree;
  const evidence = path.join(wt, '.issue', n, 'evidence');
  const failures = [];
  for (const fqPhase of ledger.state.completedPhases) {
    const phase = fqPhase.slice('issue-start.'.length);
    const facts = ledger.facts[phase];
    if (!facts && !['intake', 'handback'].includes(phase)) { failures.push(`${phase}: no recorded facts`); continue; }
    if (['fetch', 'plan', 'worktree', 'classify'].includes(phase)) {
      const r = verifyPhase(phase, ledger, null);
      if (!r.ok) failures.push(`${phase}: ${r.reason}`);
    } else if (phase === 'before' || phase === 'after') {
      const now = evidenceFiles(path.join(evidence, phase));
      if (canonicalJsonSha256(now) !== canonicalJsonSha256(facts.files)) failures.push(`${phase}: evidence changed or missing`);
    } else if (phase === 'commit') {
      const now = head(wt);
      if (!now || !isAncestor(wt, facts.implementationCommit, now)) failures.push('commit: implementation commit is not in HEAD');
    }
  }
  if (failures.length) throw new GateError('PRIOR_FACTS_INVALID', 3, `recorded phase facts no longer hold: ${failures.join('; ')}`);
}

/* ------------------------------------------------------------ 공통 */

function context(opts) {
  const ctx = gitContext(opts.cwd || process.cwd());
  if (!ctx) throw new GateError('NOT_A_REPO', 2, 'run gate inside a git checkout');
  if (!Number.isSafeInteger(opts.issue) || opts.issue < 1) throw new GateError('USAGE', 2, '--issue <n> is required');
  const dir = issueStore(ctx.commonDir, opts.issue);
  return { ctx, dir };
}

function loadLedger(dir, issue) {
  const ledger = readLedger(dir);
  if (!ledger) throw new GateError('NOT_INITIALIZED', 2, `issue #${issue} has no gate ledger — run gate init or gate adopt`);
  return ledger;
}

function nextPhase(ledger) {
  return PHASES[ledger.state.completedPhases.length] ?? null;
}

function ensureNext(ledger, phase) {
  if (!PHASES.includes(phase)) throw new GateError('USAGE', 2, `unknown phase ${phase}`);
  const expected = nextPhase(ledger);
  if (phase !== expected) {
    throw new GateError('PHASE_SEQUENCE', 2, `expected ${expected ?? 'no further phase'}, received ${phase}`, { nextPhase: expected });
  }
}

function assertNotQuarantined(ledger) {
  if (ledger.quarantine) {
    throw new GateError('QUARANTINED', 3, `issue #${ledger.issue} is quarantined: ${ledger.quarantine.reason} — controller must review and run gate clear-quarantine`, { quarantine: ledger.quarantine });
  }
}

/** controller 전용 명령은 이 저장소 어느 이슈에도 worker 로 묶인 세션에서 실행할 수 없다. */
function assertController(ctx) {
  const session = process.env.CLAUDE_CODE_SESSION_ID;
  if (!session) return;
  for (const g of gatedIssues(ctx.commonDir)) {
    if ((g.ledger.sessions || []).includes(session)) {
      throw new GateError('CONTROLLER_ONLY', 5, `session ${session} is a bound worker of issue #${g.issue}; controller commands must come from the orchestrator`);
    }
  }
}

function proposal(dir, ledger, phase) {
  const { envelope } = callPhaseApi(dir, ledger, phase, null);
  if (envelope.handback.disposition === 'complete') return { effect: null };
  if (envelope.data.reason !== 'EFFECT_APPROVAL_OR_EXECUTION_REQUIRED') {
    throw new GateError('PRECONDITION', 3, `phase API held ${phase}: ${envelope.data.reason}`, { reason: envelope.data.reason });
  }
  return { effect: envelope.proposedEffect };
}

/** 사실 확인을 통과한 뒤에만 API 에 성공 receipt 를 넘기고 ledger 를 다음 단계로 옮긴다. */
function completePhase(dir, lock, ledger, phase, facts, effect, how) {
  const receipt = `samsara-gate/v1;${how};fence=${lock.fence};facts=${canonicalJsonSha256(facts)}`;
  const effectResult = effect ? { approvalId: effect.approvalId, status: 'succeeded', receipt } : null;
  const { envelope } = callPhaseApi(dir, ledger, phase, effectResult);
  if (envelope.handback.disposition !== 'complete') {
    throw new GateError('PRECONDITION', 3, `phase API did not complete ${phase}: ${envelope.data.reason}`);
  }
  ledger.state = envelope.data.state;
  ledger.facts[phase] = facts;
  ledger.history.push({ phase, fence: lock.fence, how, receipt, at: new Date().toISOString() });
  writeLedgerAsHolder(dir, lock, ledger);
  logAttempt(dir, { phase, fence: lock.fence, outcome: 'complete', how });
  return { phase, nextPhase: nextPhase(ledger), receipt };
}

function failAttempt(dir, lock, ledger, phase, outcome, detail) {
  ledger.phaseAttempts[phase] = (ledger.phaseAttempts[phase] ?? 1) + 1;
  writeLedgerAsHolder(dir, lock, ledger);
  logAttempt(dir, { phase, fence: lock.fence, outcome, detail });
}

function bindSession(ledger, session) {
  ledger.sessions ??= [];
  if (session && !ledger.sessions.includes(session)) ledger.sessions.push(session);
}

/* ------------------------------------------------------------ 명령 */

function newLedger(opts, ctx) {
  const base = realOrSelf(opts.baseCheckout || ctx.top);
  const wt = opts.worktree;
  if (!wt || !path.isAbsolute(wt) || path.resolve(wt) !== wt) throw new GateError('USAGE', 2, '--worktree must be a normalized absolute path');
  if (!opts.branch || inferIssue(opts.branch) !== opts.issue) throw new GateError('USAGE', 2, `--branch must carry issue number ${opts.issue}`);
  if (!opts.base) throw new GateError('USAGE', 2, '--base <branch> is required');
  if (wt === base) throw new GateError('USAGE', 2, 'worktree must differ from the base checkout');
  return {
    schema: 'samsara-gate/v1',
    issue: opts.issue,
    baseCheckout: base,
    intended: { worktree: realTarget(wt), branch: opts.branch, baseBranch: opts.base },
    classification: null,
    commitMessage: null,
    fence: 0,
    sessions: opts.session ? [opts.session] : [],
    execResults: [],
    quarantine: null,
    phaseAttempts: {},
    facts: {},
    history: [],
    state: {
      issue: opts.issue,
      baseCheckout: base,
      issueWorktree: null,
      branch: null,
      completedPhases: [],
      beforeCaptured: false,
      implementationCommitted: false,
      evidenceCommitted: false,
      evidencePushed: false,
      commentPublished: false,
      baseSynced: false,
    },
  };
}

function cmdInit(opts) {
  const { ctx, dir } = context(opts);
  assertController(ctx);
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const ledger = newLedger(opts, ctx);
  const tmp = `${ledgerFile(dir)}.${process.pid}.tmp`;
  writeFileSync(tmp, `${JSON.stringify(ledger, null, 2)}\n`, { mode: 0o600 });
  try {
    linkSync(tmp, ledgerFile(dir));
  } catch (error) {
    if (error?.code === 'EEXIST') throw new GateError('ALREADY_INITIALIZED', 2, `issue #${opts.issue} already has a gate ledger`);
    throw error;
  } finally {
    unlinkSync(tmp);
  }
  return { status: 'initialized', store: dir, nextPhase: nextPhase(ledger) };
}

function cmdStatus(opts) {
  const { dir } = context(opts);
  const ledger = loadLedger(dir, opts.issue);
  const lock = readLock(dir);
  const alive = lock ? ownerAlive(lock.owner) : false;
  const wt = ledger.intended.worktree;
  return {
    issue: ledger.issue,
    store: dir,
    completedPhases: ledger.state.completedPhases.map((p) => p.slice('issue-start.'.length)),
    nextPhase: nextPhase(ledger),
    condition: !lock ? 'idle' : alive ? 'locked' : 'unknown',
    lock: lock ? { phase: lock.phase, fence: lock.fence, stage: lock.stage, owner: lock.owner, alive, step: lock.step ?? null } : null,
    quarantine: ledger.quarantine,
    sessions: ledger.sessions,
    profiles: readController(dir).profiles,
    tree: existsSync(wt) ? treeHash(wt, dir) : null,
    caller: { session: process.env.CLAUDE_CODE_SESSION_ID || null, pid: Number(process.env.CLAUDE_PID) || null },
    worktree: wt,
    branch: ledger.intended.branch,
  };
}

function cmdBind(opts) {
  const { ctx, dir } = context(opts);
  assertController(ctx);
  if (!opts.session) throw new GateError('USAGE', 2, '--session <claude session id> is required');
  loadLedger(dir, opts.issue);
  const lock = acquire(dir, processOwner(), 'bind');
  try {
    const ledger = loadLedger(dir, opts.issue);
    bindSession(ledger, opts.session);
    writeLedgerAsHolder(dir, lock, ledger);
    return { status: 'bound', sessions: ledger.sessions };
  } finally {
    release(dir, lock);
  }
}

function cmdBegin(opts) {
  const { dir } = context(opts);
  const owner = sessionOwner();
  if (!SESSION_PHASES.has(opts.phase)) {
    throw new GateError('USE_RUN', 2, `${opts.phase} is executed by the gate itself — use gate run --phase ${opts.phase}`);
  }
  assertNoUnknown(dir);
  let ledger = loadLedger(dir, opts.issue);
  assertNotQuarantined(ledger);
  ensureNext(ledger, opts.phase);
  const lock = acquire(dir, owner, opts.phase);
  try {
    ledger = loadLedger(dir, opts.issue);
    assertNotQuarantined(ledger);
    ensureNext(ledger, opts.phase);
    verifyPriorFacts(ledger);
    const { effect } = proposal(dir, ledger, opts.phase);
    if (opts.phase === 'before') {
      const dirty = trackedChanges(ledger.intended.worktree);
      if (dirty.length) throw new GateError('PRECONDITION', 3, `worktree must be pure before capture: ${dirty.slice(0, 5).join(', ')}`);
    }
    bindSession(ledger, owner.session);
    writeLedgerAsHolder(dir, lock, ledger);
    updateLock(dir, lock, { stage: 'active', effect: effect ? { type: effect.type, approvalId: effect.approvalId } : null });
    logAttempt(dir, { phase: opts.phase, fence: lock.fence, outcome: 'begin', owner });
    return { status: 'locked', phase: opts.phase, fence: lock.fence, allowed: allowedFor(opts.phase) };
  } catch (error) {
    release(dir, lock);
    throw error;
  }
}

function allowedFor(phase) {
  return {
    classify: 'gate complete --phase classify --classification <frontend|backend|both|neither>',
    plan: 'Write/Edit <base>/.issue/<n>/plan.md',
    before: 'Write <worktree>/.issue/<n>/evidence/before/*; gate exec --profile <approved>; read-only shell',
    implement: 'Edit/Write tracked worktree files; gate exec --profile <approved tree>; read-only shell',
    after: 'Write <worktree>/.issue/<n>/evidence/after/* and comment.md; gate exec --profile <approved>; read-only shell',
  }[phase];
}

function holderLock(dir, phases) {
  const owner = sessionOwner();
  const lock = readLock(dir);
  if (!lock || !sameOwner(lock.owner, owner) || !phases.includes(lock.phase) || lock.stage !== 'active') {
    throw new GateError('NOT_OWNER', 5, `this session does not hold the ${phases.join('/')} lock`, { lock });
  }
  return lock;
}

function cmdComplete(opts) {
  const { dir } = context(opts);
  let ledger = loadLedger(dir, opts.issue);
  if (ledger.state.completedPhases.includes(fq(opts.phase))) {
    // replay: 이미 확인된 완료 — 아무것도 쓰지 않고 같은 결과를 돌려준다
    return { status: 'already-complete', phase: opts.phase, nextPhase: nextPhase(ledger) };
  }
  const lock = holderLock(dir, [opts.phase]);
  if (lock.exec?.pgid && groupAlive(lock.exec.pgid)) {
    throw new GateError('EXEC_RUNNING', 3, `profile ${lock.exec.profile} is still running`);
  }
  ledger = loadLedger(dir, opts.issue);
  assertNotQuarantined(ledger);
  ensureNext(ledger, opts.phase);
  if (opts.phase === 'classify') {
    if (!CLASSIFICATIONS.includes(opts.classification)) throw new GateError('USAGE', 2, `--classification must be one of ${CLASSIFICATIONS.join('|')}`);
    ledger.classification = opts.classification;
  }
  const verdict = verifyPhase(opts.phase, ledger, dir);
  if (!verdict.ok) {
    logAttempt(dir, { phase: opts.phase, fence: lock.fence, outcome: 'verify-failed', detail: verdict.reason });
    throw new GateError('VERIFY_FAILED', 3, `${opts.phase} is not actually complete: ${verdict.reason}`);
  }
  const { effect } = proposal(dir, ledger, opts.phase);
  const result = completePhase(dir, lock, ledger, opts.phase, verdict.facts, effect, 'session');
  release(dir, lock);
  return { status: 'complete', ...result };
}

function cmdAbort(opts) {
  const { dir } = context(opts);
  const lock = holderLock(dir, [opts.phase]);
  if (lock.exec?.pgid && groupAlive(lock.exec.pgid)) throw new GateError('EXEC_RUNNING', 3, 'a brokered command is still running');
  const ledger = loadLedger(dir, opts.issue);
  failAttempt(dir, lock, ledger, opts.phase, 'aborted', opts.reason || null);
  release(dir, lock);
  return { status: 'aborted', phase: opts.phase, nextPhase: nextPhase(ledger) };
}

const PURE_STAGE_ARGV = ['git', 'add', '-A', '--', ':!.issue'];

/**
 * pure API 의 commit staging step(`git add -A -- :!.issue`)은 .issue 를 통째로 ignore 하는 저장소에서
 * git 이 pathspec 을 거절해 실패한다. 같은 의미(증거 제외 전부 stage, ignore 존중)를 유지하는 adapter:
 * 추적·미추적(ignore 제외) 경로를 NUL 단위로 모아 .issue 만 빼고 literal pathspec 으로 넘긴다. -f 는 쓰지 않는다.
 */
function stagingAdapter(step, dir) {
  if (canonicalJsonSha256(step.argv) !== canonicalJsonSha256(PURE_STAGE_ARGV)) return null;
  const r = spawnSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '-z'], { cwd: step.cwd });
  if (r.status !== 0) throw new GateError('GIT_FAILED', 4, `git ls-files failed: ${String(r.stderr).trim()}`);
  const paths = [...new Set(r.stdout.toString('utf8').split('\0').filter(Boolean))]
    .filter((p) => p !== '.issue' && !p.startsWith('.issue/'));
  const file = path.join(dir, `pathspec-${process.pid}-${randomUUID()}`);
  writeFileSync(file, paths.join('\0'), { mode: 0o600 });
  return {
    argv: ['git', '--literal-pathspecs', 'add', '-A', `--pathspec-from-file=${file}`, '--pathspec-file-nul'],
    file,
    count: paths.length,
  };
}

/** 자식을 자기 프로세스 그룹으로 띄우고 pgid 를 lock 에 남긴다. gate 가 죽어도 재조정이 자식 생존을 확인할 수 있다. */
function spawnTracked(argv, cwd, env, stdio, onSpawn) {
  return new Promise((resolve) => {
    const child = spawn(argv[0], argv.slice(1), { cwd, env, stdio, detached: true });
    child.on('error', (error) => resolve({ code: null, signal: null, error }));
    if (child.pid) onSpawn(child.pid);
    let out = '';
    child.stdout?.on('data', (d) => { out += d; });
    child.stderr?.on('data', (d) => { out += d; });
    child.on('close', (code, signal) => resolve({ code, signal, out }));
  });
}

async function cmdRun(opts) {
  const { dir } = context(opts);
  if (!RUN_PHASES.has(opts.phase)) {
    throw new GateError('USE_BEGIN', 2, `${opts.phase} is session work — use gate begin/complete --phase ${opts.phase}`);
  }
  assertNoUnknown(dir);
  let ledger = loadLedger(dir, opts.issue);
  assertNotQuarantined(ledger);
  ensureNext(ledger, opts.phase);
  const lock = acquire(dir, processOwner(), opts.phase);
  try {
    ledger = loadLedger(dir, opts.issue);
    assertNotQuarantined(ledger);
    ensureNext(ledger, opts.phase);
    verifyPriorFacts(ledger);
    if (opts.phase === 'commit' && opts.message) {
      ledger.commitMessage = opts.message;
      writeLedgerAsHolder(dir, lock, ledger);
    }
    const { effect } = proposal(dir, ledger, opts.phase);
    if (effect?.classification === 'approval-required' && opts.approval !== effect.approvalId) {
      throw new GateError('APPROVAL_REQUIRED', 3, `phase ${opts.phase} needs orchestrator approval: --approval ${effect.approvalId}`, { approvalId: effect.approvalId, effect });
    }
    if (effect) {
      const steps = effect.request.steps ?? [effect.request];
      if (steps.some((s) => !Array.isArray(s.argv) || typeof s.cwd !== 'string')) {
        throw new GateError('EFFECT_NOT_EXECUTABLE', 2, `effect ${effect.type} has no executable argv`);
      }
      updateLock(dir, lock, { stage: 'executing', effect: { type: effect.type, approvalId: effect.approvalId, digest: canonicalJsonSha256(effect) } });
      for (const [index, step] of steps.entries()) {
        // legacy CLI 자식은 이 step argv·ppid·stage 를 보고 자신이 gate 가 띄운 현재 단계인지 확인한다
        const adapted = stagingAdapter(step, dir);
        const argv = adapted ? adapted.argv : step.argv;
        updateLock(dir, lock, { step: { index, argv, cwd: step.cwd, pgid: null, ...(adapted ? { proposedArgv: step.argv, pathspecCount: adapted.count } : {}) } });
        const env = { ...process.env, SAMSARA_GATE_FENCE: String(lock.fence), SAMSARA_GATE_ISSUE: String(opts.issue) };
        let r;
        try {
          r = adapted?.count === 0
            ? { code: 0, signal: null }
            : await spawnTracked(argv, step.cwd, env, ['ignore', 2, 2], (pid) => updateLock(dir, lock, { step: { ...lock.step, pgid: pid } }));
        } finally {
          if (adapted && existsSync(adapted.file)) unlinkSync(adapted.file);
        }
        logAttempt(dir, { phase: opts.phase, fence: lock.fence, outcome: 'step', detail: { index, argv, proposedArgv: adapted ? step.argv : undefined, exit: r.code, signal: r.signal } });
        if (r.code !== 0) {
          failAttempt(dir, lock, ledger, opts.phase, 'effect-failed', { argv, exit: r.code, signal: r.signal });
          throw new GateError('EFFECT_FAILED', 4, `${argv.join(' ')} exited ${r.code ?? r.signal ?? r.error?.message}; next phase stays locked`);
        }
      }
    }
    const verdict = verifyPhase(opts.phase, ledger, dir);
    if (!verdict.ok) {
      failAttempt(dir, lock, ledger, opts.phase, 'verify-failed', verdict.reason);
      throw new GateError('VERIFY_FAILED', 3, `${opts.phase} effect ran but is not verifiable: ${verdict.reason}`);
    }
    return { status: 'complete', ...completePhase(dir, lock, ledger, opts.phase, verdict.facts, effect, 'run') };
  } finally {
    release(dir, lock);
  }
}

/* ---- 실행 broker: controller 가 profile(argv) 과 실행할 tree 를 승인한다 ---- */

function profileDigest(p) {
  return canonicalJsonSha256({ name: p.name, phases: p.phases, dir: p.dir, argv: p.argv, required: p.required });
}

function cmdProfile(opts) {
  const { ctx, dir } = context(opts);
  assertController(ctx);
  loadLedger(dir, opts.issue);
  if (!opts.name || !opts.argv?.length) throw new GateError('USAGE', 2, 'profile needs --name <n> [--phases before,implement,after] [--dir <rel>] [--required] -- <argv...>');
  const phases = (opts.phases || 'before,implement,after').split(',');
  if (phases.some((p) => !EXEC_PHASES.has(p))) throw new GateError('USAGE', 2, '--phases must be among before,implement,after');
  const rel = opts.dir || '.';
  if (path.isAbsolute(rel) || rel.split(/[\\/]/).includes('..')) throw new GateError('USAGE', 2, '--dir must be relative inside the worktree');
  const profile = { name: opts.name, phases, dir: rel, argv: opts.argv, required: Boolean(opts.required) };
  profile.digest = profileDigest(profile);
  withControllerMutex(dir, (c) => { c.profiles[opts.name] = profile; });
  return { status: 'profile-registered', profile };
}

function cmdApproveExec(opts) {
  const { ctx, dir } = context(opts);
  assertController(ctx);
  loadLedger(dir, opts.issue);
  if (!opts.profile || !/^[0-9a-f]{40,64}$/.test(opts.tree || '')) throw new GateError('USAGE', 2, 'approve-exec needs --profile <name> --tree <tree hash>');
  return withControllerMutex(dir, (c) => {
    const profile = c.profiles[opts.profile];
    if (!profile) throw new GateError('UNKNOWN_PROFILE', 2, `no profile ${opts.profile}`);
    c.execApprovals.push({ profile: opts.profile, digest: profile.digest, tree: opts.tree, at: new Date().toISOString() });
    return { status: 'approved', profile: opts.profile, tree: opts.tree };
  });
}

function cmdClearQuarantine(opts) {
  const { ctx, dir } = context(opts);
  assertController(ctx);
  if (!opts.reason) throw new GateError('USAGE', 2, '--reason <review note> is required');
  if (!loadLedger(dir, opts.issue).quarantine) return { status: 'not-quarantined' };
  // 격리를 남긴 세션 lock 이 살아 있으면 먼저 abort 해야 한다(LOCKED)
  const lock = acquire(dir, processOwner(), 'clear-quarantine');
  try {
    const ledger = loadLedger(dir, opts.issue);
    ledger.history.push({ phase: 'quarantine-cleared', reason: opts.reason, previous: ledger.quarantine, at: new Date().toISOString() });
    ledger.quarantine = null;
    writeLedgerAsHolder(dir, lock, ledger);
    logAttempt(dir, { phase: 'quarantine', fence: lock.fence, outcome: 'cleared', detail: opts.reason });
    return { status: 'cleared' };
  } finally {
    release(dir, lock);
  }
}

async function cmdExec(opts) {
  const { dir } = context(opts);
  let ledger = loadLedger(dir, opts.issue);
  assertNotQuarantined(ledger);
  const controller = readController(dir);
  const profile = controller.profiles[opts.profile];
  if (!profile) throw new GateError('UNKNOWN_PROFILE', 2, `no approved profile ${opts.profile}`);
  if (profileDigest(profile) !== profile.digest) throw new GateError('PROFILE_TAMPERED', 3, `profile ${opts.profile} digest mismatch`);
  const lock = holderLock(dir, profile.phases);
  if (lock.exec?.pgid && groupAlive(lock.exec.pgid)) throw new GateError('EXEC_RUNNING', 3, 'another brokered command is still running');
  const wt = ledger.intended.worktree;
  const tree = treeHash(wt, dir);
  const pureTree = git(wt, ['rev-parse', 'HEAD^{tree}']).out;
  const preApproved = lock.phase === 'before' && tree === pureTree && head(wt) === ledger.facts.worktree?.head;
  const approved = preApproved || controller.execApprovals.some((a) => a.profile === profile.name && a.digest === profile.digest && a.tree === tree);
  if (!approved) {
    throw new GateError('EXEC_APPROVAL_REQUIRED', 3, `tree ${tree} is not approved for profile ${profile.name}; controller runs: issue-gate.mjs approve-exec --issue ${ledger.issue} --profile ${profile.name} --tree ${tree}`, { tree });
  }
  const refsBefore = refSnapshot(wt);
  const cwd = path.join(wt, profile.dir);
  const r = await spawnTracked(profile.argv, cwd, { ...process.env, SAMSARA_GATE_EXEC: profile.name }, ['ignore', 'pipe', 'pipe'], (pid) => {
    updateLock(dir, lock, { exec: { profile: profile.name, pgid: pid, tree } });
  });
  updateLock(dir, lock, { exec: null });
  const violations = [];
  if (refSnapshot(wt) !== refsBefore) violations.push('git refs or HEAD changed during brokered execution');
  if (lock.phase !== 'implement' && treeHash(wt, dir) !== tree) violations.push(`source tree changed during ${lock.phase}`);
  const record = {
    profile: profile.name, argv: profile.argv, dir: profile.dir, tree, exit: r.code, signal: r.signal ?? null,
  };
  if (lock.phase !== 'implement') {
    const outDir = path.join(wt, '.issue', String(ledger.issue), 'evidence', lock.phase);
    mkdirSync(outDir, { recursive: true });
    writeFileSync(path.join(outDir, `exec-${profile.name}.txt`), `$ ${profile.argv.join(' ')}  (cwd ${profile.dir}, tree ${tree})\n${r.out ?? ''}\nexit=${r.code}${r.signal ? ` signal=${r.signal}` : ''}\n`);
  } else {
    process.stderr.write(r.out ?? '');
  }
  if (violations.length) {
    ledger = loadLedger(dir, opts.issue);
    ledger.quarantine = { reason: violations.join('; '), profile: profile.name, fence: lock.fence, at: new Date().toISOString() };
    writeLedgerAsHolder(dir, lock, ledger);
    logAttempt(dir, { phase: lock.phase, fence: lock.fence, outcome: 'quarantined', detail: violations });
    throw new GateError('QUARANTINED', 3, `brokered command violated the phase boundary: ${violations.join('; ')}`, record);
  }
  ledger = loadLedger(dir, opts.issue);
  ledger.execResults ??= [];
  ledger.execResults.push({ profile: profile.name, digest: profile.digest, phase: lock.phase, tree, exit: r.code, at: new Date().toISOString() });
  writeLedgerAsHolder(dir, lock, ledger);
  logAttempt(dir, { phase: lock.phase, fence: lock.fence, outcome: 'exec', detail: record });
  if (r.code !== 0) throw new GateError('COMMAND_FAILED', 4, `${profile.name} exited ${r.code ?? r.signal}`, record);
  return { status: 'executed', ...record };
}

/**
 * 죽은 owner 의 결과(UNKNOWN)를 정리한다.
 * - 효과 자식이 아직 살아 있으면 잠금을 그대로 둔다(STILL_RUNNING).
 * - 사실이 완료를 보여 주면 완료로 기록한다.
 * - 자식 종료가 확인됐고 사실이 없으면 실패로 기록해 같은 단계 재시도를 허용한다.
 * - 자식 종료를 확인할 수 없으면 운영자의 `--resolve failed` 없이는 풀지 않는다.
 */
function cmdReconcile(opts) {
  const { ctx, dir } = context(opts);
  const lock = readLock(dir);
  if (!lock) return { status: 'nothing-to-reconcile' };
  if (ownerAlive(lock.owner)) throw new GateError('LOCKED', 5, `owner of ${lock.phase} is alive`, { lock });
  if (opts.resolve && opts.resolve !== 'failed') throw new GateError('USAGE', 2, '--resolve accepts only "failed"');
  if (opts.resolve) assertController(ctx);
  const mutex = path.join(dir, 'reconcile.lock');
  const me = processOwner();
  const tmp = `${mutex}.${process.pid}.tmp`;
  writeFileSync(tmp, JSON.stringify(me));
  try {
    try {
      linkSync(tmp, mutex);
    } catch (error) {
      if (error?.code !== 'EEXIST') throw error;
      let other = null;
      try { other = readJson(mutex); } catch { /* 손상 — 죽은 것으로 본다 */ }
      if (other && ownerAlive(other)) throw new GateError('LOCKED', 5, 'another reconcile is running');
      unlinkSync(mutex);
      linkSync(tmp, mutex);
    }
  } finally {
    unlinkSync(tmp);
  }
  try {
    const again = readLock(dir);
    if (!again || again.fence !== lock.fence) return { status: 'nothing-to-reconcile' };
    const ledger = loadLedger(dir, opts.issue);
    // 죽은 owner 를 대신해 같은 fence 로 기록한다. lock.json 은 끝까지 남아 새 acquire 를 막는다.
    const holder = { ...again };
    const pgid = again.stage === 'executing' ? again.step?.pgid : again.exec?.pgid;
    if (pgid && groupAlive(pgid)) {
      throw new GateError('STILL_RUNNING', 6, `child process group ${pgid} of ${again.phase} is still running; lock stays UNKNOWN`, { pgid });
    }
    let outcome;
    let result = null;
    if (again.owner.kind === 'process' && again.stage === 'executing' && nextPhase(ledger) === again.phase) {
      const verdict = verifyPhase(again.phase, ledger, dir);
      if (verdict.ok) {
        const { effect } = proposal(dir, ledger, again.phase);
        result = completePhase(dir, holder, ledger, again.phase, verdict.facts, effect, 'reconcile');
        outcome = 'reconciled-complete';
      } else if (pgid || opts.resolve === 'failed') {
        failAttempt(dir, holder, ledger, again.phase, 'reconciled-failed', { reason: verdict.reason, childExitConfirmed: Boolean(pgid), operator: opts.resolve === 'failed' });
        outcome = 'reconciled-failed';
      } else {
        throw new GateError('OPERATOR_RESOLUTION_REQUIRED', 6, `${again.phase} child process was never recorded, so its exit cannot be confirmed; controller must run reconcile --resolve failed after checking`, { reason: verdict.reason });
      }
    } else {
      failAttempt(dir, holder, ledger, again.phase, 'abandoned', `owner ${again.owner.kind}:${again.owner.pid} died at stage ${again.stage}`);
      outcome = 'abandoned';
    }
    unlinkSync(lockFile(dir));
    const fresh = loadLedger(dir, opts.issue);
    return { status: outcome, phase: again.phase, fence: again.fence, nextPhase: nextPhase(fresh), ...(result ? { receipt: result.receipt } : {}) };
  } finally {
    unlinkSync(mutex);
  }
}

/** 진행 중 이슈 편입: intake~before 까지 실제 사실을 하나씩 확인하며 기록한다. 주장만으로는 넘기지 않는다. */
function cmdAdopt(opts) {
  const { ctx, dir } = context(opts);
  assertController(ctx);
  const through = opts.through || ADOPT_LIMIT;
  if (!PHASES.includes(through) || PHASES.indexOf(through) > PHASES.indexOf(ADOPT_LIMIT)) {
    throw new GateError('USAGE', 2, `--through must be a phase up to ${ADOPT_LIMIT}`);
  }
  if (!readLedger(dir)) cmdInit(opts);
  const lock = acquire(dir, processOwner(), 'adopt');
  const done = [];
  try {
    let ledger = loadLedger(dir, opts.issue);
    verifyPriorFacts(ledger);
    if (opts.classification) ledger.classification = opts.classification;
    bindSession(ledger, opts.session);
    writeLedgerAsHolder(dir, lock, ledger);
    while (PHASES.indexOf(nextPhase(ledger)) <= PHASES.indexOf(through)) {
      const phase = nextPhase(ledger);
      const verdict = verifyPhase(phase, ledger, dir);
      if (!verdict.ok) {
        logAttempt(dir, { phase, fence: lock.fence, outcome: 'adopt-stopped', detail: verdict.reason });
        throw new GateError('VERIFY_FAILED', 3, `adopt stopped at ${phase}: ${verdict.reason}`, { adopted: done, nextPhase: phase });
      }
      const { effect } = proposal(dir, ledger, phase);
      completePhase(dir, lock, ledger, phase, verdict.facts, effect, 'adopt');
      done.push(phase);
      ledger = loadLedger(dir, opts.issue);
    }
    return { status: 'adopted', adopted: done, nextPhase: nextPhase(ledger), store: dir, repo: ctx.top, sessions: ledger.sessions };
  } finally {
    release(dir, lock);
  }
}

/* ------------------------------------------------------------ legacy CLI 차단 */

/**
 * issue-start.mjs 의 부수효과 서브커맨드가 gate 밖에서 불리면 거절한다.
 * fence 환경값은 공개 상태라 복제할 수 있으므로 믿지 않는다. 대신 이 프로세스가
 * (1) 살아 있는 gate run owner 의 직접 자식이고 (2) lock 이 executing 이며
 * (3) lock 에 기록된 현재 step argv 와 정확히 같은지 확인한다.
 */
export function legacyGateCheck(issue, cwd = process.cwd()) {
  const ctx = gitContext(cwd);
  if (!ctx) return { ok: true };
  const n = issue ? Number(issue) : inferIssue(ctx.branch);
  if (!n) return { ok: true };
  const dir = issueStore(ctx.commonDir, n);
  if (!existsSync(ledgerFile(dir))) return { ok: true };
  let lock = null;
  try { lock = readLock(dir); } catch { return { ok: false, reason: 'gate lock is unreadable' }; }
  const self = [process.execPath, realOrSelf(process.argv[1]), ...process.argv.slice(2)];
  const stepArgv = lock?.step?.argv ? [lock.step.argv[0], realOrSelf(lock.step.argv[1]), ...lock.step.argv.slice(2)] : null;
  const trusted = lock
    && lock.owner.kind === 'process'
    && lock.stage === 'executing'
    && process.ppid === lock.owner.pid
    && ownerAlive(lock.owner)
    && stepArgv
    && canonicalJsonSha256(stepArgv) === canonicalJsonSha256(self);
  if (trusted) return { ok: true };
  return { ok: false, reason: `issue #${n} is gated — side effects run only as the current step of issue-gate.mjs run (lock ${lock ? `${lock.phase}/${lock.stage}` : 'none'})` };
}

/* ------------------------------------------------------------ PreToolUse hook */

const FILE_TOOLS = new Set(['Edit', 'Write', 'MultiEdit', 'NotebookEdit']);
const WORKER_GATE_COMMANDS = new Set(['status', 'begin', 'complete', 'abort', 'run', 'reconcile', 'exec']);
const RAW_EFFECTS = [
  /\bgit\b[^;&|\n]*?\s(commit|push|merge|rebase|reset|cherry-pick|am|worktree|update-ref|tag|switch|revert)\b/,
  /\bgh\s+(issue|pr)\s+(comment|create|merge|edit|close|reopen)\b/,
  /\bissue-start\.mjs\b[^;&|\n]*?\s(fetch|worktree|evidence-commit|evidence-mirror|sync-base|status)\b/,
];

/**
 * 따옴표만 해석하는 최소 shell 분해기. 확장·리다이렉트·제어 연산자가 하나라도 있으면 null.
 * null 이면 읽기 전용도 gate 호출도 아니다.
 */
export function shellWords(command) {
  const words = [];
  let cur = null;
  for (let i = 0; i < command.length;) {
    const c = command[i];
    if (c === ' ' || c === '\t') {
      if (cur !== null) { words.push(cur); cur = null; }
      i += 1;
    } else if (c === "'") {
      const j = command.indexOf("'", i + 1);
      if (j < 0) return null;
      cur = (cur ?? '') + command.slice(i + 1, j);
      i = j + 1;
    } else if (c === '"') {
      const j = command.indexOf('"', i + 1);
      if (j < 0) return null;
      const seg = command.slice(i + 1, j);
      if (/[$`\\!]/.test(seg)) return null;
      cur = (cur ?? '') + seg;
      i = j + 1;
    } else if (/[;&|<>()$`\\{}!#\n\r=]/.test(c) && !(c === '=' && cur !== null && cur.startsWith('-'))) {
      // `VAR=x cmd` 같은 환경 주입도 막는다(옵션 값의 = 만 허용)
      return null;
    } else {
      cur = (cur ?? '') + c;
      i += 1;
    }
  }
  if (cur !== null) words.push(cur);
  return words;
}

function trustedGateInvocation(words) {
  if (!words || words.length < 3) return false;
  if (words[0] !== 'node' && words[0] !== process.execPath) return false;
  if (!path.isAbsolute(words[1]) || realOrSelf(words[1]) !== SELF) return false;
  return WORKER_GATE_COMMANDS.has(words[2]);
}

const READ_ONLY_GIT = new Set(['status', 'diff', 'log', 'show', 'rev-parse', 'ls-files', 'blame']);
const READ_ONLY_BIN = new Set(['ls', 'cat', 'head', 'tail', 'wc', 'grep', 'pwd', 'stat', 'file', 'find', 'rg']);
const DENY_ARGS = /^(--output|--ext-diff|--textconv|--exec|-exec|-execdir|-ok|-okdir|-delete|-fprint|-fls|--pre|--hostname-bin|--search-zip|-z$)/;

function readOnlyCommand(words) {
  if (!words || words.length === 0) return false;
  if (words.slice(1).some((w) => DENY_ARGS.test(w))) return false;
  if (words[0] === 'git') return READ_ONLY_GIT.has(words[1]) && !words.includes('-c');
  return READ_ONLY_BIN.has(words[0]);
}

function deny(reason) {
  return { decision: 'deny', reason: `[samsara gate] ${reason}` };
}

function gatedTarget(target) {
  const ctx = gitContext(target);
  if (!ctx || !existsSync(storeRoot(ctx.commonDir))) return { ctx, gated: null };
  const issue = inferIssue(ctx.branch);
  if (issue) {
    const dir = issueStore(ctx.commonDir, issue);
    const ledger = readLedger(dir);
    if (ledger && ctx.top === ledger.intended.worktree) return { ctx, gated: { issue, dir, ledger } };
  }
  return { ctx, gated: null };
}

function gatedIssues(commonDir) {
  const root = storeRoot(commonDir);
  if (!existsSync(root)) return [];
  return readdirSync(root).filter((d) => /^issue-\d+$/.test(d)).map((d) => {
    const dir = path.join(root, d);
    return { issue: Number(d.slice(6)), dir, ledger: readLedger(dir) };
  }).filter((g) => g.ledger);
}

function holder(gated, sessionId) {
  const lock = readLock(gated.dir);
  if (!lock || lock.owner.kind !== 'session' || lock.owner.session !== sessionId) return { lock, holds: false };
  return { lock, holds: lock.stage === 'active' && ownerAlive(lock.owner) };
}

function decideFile(gated, file, sessionId) {
  const n = gated.issue;
  const wt = gated.ledger.intended.worktree;
  const rel = path.relative(wt, file);
  const { lock, holds } = holder(gated, sessionId);
  const phase = holds ? lock.phase : null;
  const where = `issue #${n} next=${nextPhase(gated.ledger)} lock=${lock ? `${lock.phase}/${lock.owner.kind}` : 'none'}`;
  const evidence = `.issue/${n}/evidence/`;
  if (rel === '.git' || rel.startsWith('.git/') || rel.startsWith('.claude/') || rel === '.claude') {
    return deny(`git metadata and Claude settings in the worktree are not writable (${where})`);
  }
  if (rel.startsWith(`${evidence}before/`)) return phase === 'before' ? null : deny(`before evidence is writable only while this session holds the before lock (${where})`);
  if (rel.startsWith(`${evidence}after/`)) return phase === 'after' ? null : deny(`after evidence is writable only while this session holds the after lock (${where})`);
  if (rel === `${evidence}comment.md`) {
    const commentWindow = !lock && nextPhase(gated.ledger) === 'comment';
    return phase === 'after' || commentWindow ? null : deny(`comment.md is writable only in after or before comment (${where})`);
  }
  if (rel.startsWith('.issue/')) return deny(`issue workspace files other than this phase's evidence are not writable (${where})`);
  if (phase !== 'implement') {
    return deny(`code edits need this session to hold the implement lock — run issue-gate.mjs begin --issue ${n} --phase implement after before is complete (${where})`);
  }
  // ignore 된 경로는 tree digest·커밋 검증 밖이라 implement 중에도 쓰지 못한다
  if (git(wt, ['check-ignore', '-q', '--no-index', '--', rel]).code === 0) {
    return deny(`${rel} is git-ignored; edits outside tracked content escape verification (${where})`);
  }
  return null;
}

/** hook 입력에서 이 결정에 관련된 저장소 store·gated 이슈·worker 바인딩을 모은다. */
function scope(targets, sessionId) {
  const contexts = new Map();
  for (const target of targets) {
    const { ctx, gated } = gatedTarget(target);
    if (ctx) contexts.set(ctx.top, { ctx, gated });
  }
  const commonDirs = [...new Set([...contexts.values()].map((c) => c.ctx.commonDir))].filter((d) => existsSync(storeRoot(d)));
  const issues = commonDirs.flatMap((d) => gatedIssues(d));
  const workerOf = issues.filter((g) => sessionId && (g.ledger.sessions || []).includes(sessionId));
  return { contexts, commonDirs, issues, workerOf };
}

export function decideHook(input) {
  const tool = input.tool_name;
  const ti = input.tool_input || {};
  const sessionId = input.session_id || null;
  const cwd = input.cwd || process.cwd();
  if (FILE_TOOLS.has(tool)) {
    const raw = ti.file_path || ti.notebook_path;
    if (!raw) return null;
    const file = realTarget(path.resolve(cwd, raw));
    const { contexts, commonDirs, issues, workerOf } = scope([cwd, file], sessionId);
    if (commonDirs.length === 0) return null;
    if (commonDirs.some((d) => isWithin(d, file))) return deny('git metadata and the gate store are written only by git and issue-gate.mjs');
    const target = [...contexts.values()].find((c) => isWithin(c.ctx.top, file) && c.gated)?.gated
      ?? (gatedTarget(file).gated);
    if (target) return decideFile(target, file, sessionId);
    // base checkout 의 .issue/<n>/plan.md 는 plan 단계 lock 보유 중에만 쓸 수 있다
    for (const g of issues) {
      if (file === path.join(g.ledger.baseCheckout, '.issue', String(g.issue), 'plan.md')) {
        const { lock, holds } = holder(g, sessionId);
        return holds && lock.phase === 'plan' ? null : deny(`plan.md of issue #${g.issue} is writable only during the plan lock`);
      }
    }
    if (workerOf.length) {
      return deny(`session is a bound worker of issue #${workerOf.map((g) => g.issue).join(', #')}; it may write only inside its gated worktree`);
    }
    return null;
  }
  if (tool !== 'Bash') return null;
  const command = String(ti.command || '');
  const pathTokens = (command.match(/(?:^|[\s='"])(\/[^\s'";&|<>()]+)/g) || []).map((t) => t.replace(/^[\s='"]/, ''));
  const { contexts, commonDirs, issues, workerOf } = scope([cwd, ...pathTokens], sessionId);
  if (commonDirs.length === 0) return null;
  if (/samsara-gate/.test(command)) return deny('direct access to the gate store is not allowed');
  const gatedList = [...contexts.values()].filter((c) => c.gated).map((c) => c.gated);
  if (gatedList.length || workerOf.length) {
    // gated worktree 또는 worker 세션: 신뢰 경로 gate CLI 와 엄격한 읽기 전용 명령만 통과한다
    const words = shellWords(command);
    if (trustedGateInvocation(words)) return null;
    if (readOnlyCommand(words)) return null;
    const n = (gatedList[0] ?? workerOf[0]).issue;
    return deny(`issue #${n}: shell is limited to read-only commands and \`node ${SELF} <status|begin|complete|abort|run|reconcile|exec> ...\`; run tests through gate exec --profile`);
  }
  // controller·무관 세션: gated 이슈 번호를 겨눈 tracker·legacy 부수효과만 막는다
  for (const g of issues) {
    const n = String(g.issue);
    const tracker = new RegExp(`\\bgh\\s+(?:issue|pr)\\s+\\S+\\s+#?${n}\\b`);
    const legacy = new RegExp(`\\bissue-start\\.mjs\\b[^;&|\\n]*\\s#?${n}\\b`);
    if ((tracker.test(command) || legacy.test(command)) && RAW_EFFECTS.some((re) => re.test(command))) {
      return deny(`issue #${n} side effects run only through issue-gate.mjs run`);
    }
  }
  return null;
}

function cmdHook() {
  let decision;
  try {
    const input = JSON.parse(readFileSync(0, 'utf8') || '{}');
    decision = decideHook(input);
  } catch (error) {
    // fail-closed: 판단할 수 없으면 막는다
    decision = deny(`hook could not evaluate the gate (${error?.code || 'ERROR'}: ${error?.message || error})`);
  }
  if (decision) {
    process.stdout.write(`${JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'deny',
        permissionDecisionReason: decision.reason,
      },
    })}\n`);
  }
  return 0;
}

/* ------------------------------------------------------------ hook 설치 */

export function hookCommand(script = SELF) {
  return `node '${script}' hook || exit 2`;
}

function cmdInstallHook(opts) {
  if (!opts.worktree || !path.isAbsolute(opts.worktree)) throw new GateError('USAGE', 2, '--worktree <absolute path> is required');
  const ctx = gitContext(opts.worktree);
  if (!ctx) throw new GateError('NOT_A_REPO', 2, `${opts.worktree} is not a git checkout`);
  const settingsFile = path.join(ctx.top, '.claude', 'settings.local.json');
  mkdirSync(path.dirname(settingsFile), { recursive: true });
  const settings = existsSync(settingsFile) ? readJson(settingsFile) : {};
  settings.hooks ??= {};
  settings.hooks.PreToolUse ??= [];
  const command = hookCommand();
  const present = settings.hooks.PreToolUse.some((m) => (m.hooks || []).some((h) => h.command === command));
  if (!present) {
    settings.hooks.PreToolUse.push({
      matcher: 'Edit|Write|MultiEdit|NotebookEdit|Bash',
      hooks: [{ type: 'command', command, timeout: 30, onFailure: 'block' }],
    });
    writeJsonAtomic(settingsFile, settings);
  }
  // 로컬 설정 파일이 구현 커밋(git add -A)에 섞이지 않게 저장소 로컬 exclude 에 넣는다
  const exclude = path.join(ctx.commonDir, 'info', 'exclude');
  mkdirSync(path.dirname(exclude), { recursive: true });
  const current = existsSync(exclude) ? readFileSync(exclude, 'utf8') : '';
  if (!current.split('\n').includes('.claude/settings.local.json')) {
    appendFileSync(exclude, `${current && !current.endsWith('\n') ? '\n' : ''}.claude/settings.local.json\n`);
  }
  return { status: present ? 'already-installed' : 'installed', settings: settingsFile, command };
}

/* ------------------------------------------------------------ CLI */

function parseOpts(argv) {
  const opts = {};
  const flags = {
    '--issue': 'issue', '--phase': 'phase', '--worktree': 'worktree', '--branch': 'branch', '--base': 'base',
    '--base-checkout': 'baseCheckout', '--classification': 'classification', '--message': 'message',
    '--approval': 'approval', '--through': 'through', '--reason': 'reason', '--cwd': 'cwd',
    '--session': 'session', '--name': 'name', '--phases': 'phases', '--dir': 'dir', '--profile': 'profile',
    '--tree': 'tree', '--resolve': 'resolve',
  };
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--') { opts.argv = argv.slice(i + 1); break; }
    if (argv[i] === '--required') { opts.required = true; continue; }
    const key = flags[argv[i]];
    if (!key || i + 1 >= argv.length) throw new GateError('USAGE', 2, `unknown or incomplete option ${argv[i]}`);
    opts[key] = argv[++i];
  }
  if (opts.issue !== undefined) opts.issue = Number(String(opts.issue).replace(/^#/, ''));
  if (opts.worktree && path.isAbsolute(opts.worktree)) opts.worktree = path.resolve(opts.worktree);
  return opts;
}

const USAGE = `Usage: node issue-gate.mjs <command> [options]
 controller (orchestrator session, base checkout):
  init           --issue N --worktree <abs> --branch <b> --base <base-branch> [--session <worker id>]
  adopt          --issue N --worktree <abs> --branch <b> --base <base-branch> [--classification c] [--through before] [--session <id>]
  bind           --issue N --session <worker claude session id>
  profile        --issue N --name <n> [--phases before,implement,after] [--dir <rel>] [--required] -- <argv...>
  approve-exec   --issue N --profile <n> --tree <tree hash>
  clear-quarantine --issue N --reason <text>
  reconcile      --issue N [--resolve failed]
  install-hook   --worktree <abs>
 worker (bound Claude session):
  status    --issue N
  begin     --issue N --phase classify|plan|before|implement|after
  complete  --issue N --phase <same> [--classification c]        (verified facts only)
  abort     --issue N --phase <same> [--reason text]
  exec      --issue N --profile <n>                              (approved argv + tree only)
  run       --issue N --phase intake|fetch|worktree|commit|publish-evidence|comment|sync-base|handback
            [--message <commit message>] [--approval <approvalId>]
  hook      (Claude PreToolUse hook; reads JSON on stdin)`;

export async function main(argv = process.argv.slice(2)) {
  const [command, ...rest] = argv;
  if (command === 'hook') return cmdHook();
  const commands = {
    init: cmdInit, adopt: cmdAdopt, bind: cmdBind, status: cmdStatus, begin: cmdBegin, complete: cmdComplete,
    abort: cmdAbort, run: cmdRun, exec: cmdExec, profile: cmdProfile, 'approve-exec': cmdApproveExec,
    'clear-quarantine': cmdClearQuarantine, reconcile: cmdReconcile, 'install-hook': cmdInstallHook,
  };
  try {
    if (!commands[command]) throw new GateError('USAGE', 2, USAGE);
    const result = await commands[command](parseOpts(rest));
    process.stdout.write(`${JSON.stringify({ ok: true, ...result }, null, 2)}\n`);
    return 0;
  } catch (error) {
    const code = error instanceof GateError ? error.code : 'INTERNAL_ERROR';
    const exitCode = error instanceof GateError ? error.exitCode : 1;
    process.stdout.write(`${JSON.stringify({ ok: false, code, message: error.message, ...(error.detail || {}) }, null, 2)}\n`);
    process.stderr.write(`${code}: ${error.message}\n`);
    return exitCode;
  }
}

function isMain() {
  try {
    return Boolean(process.argv[1]) && realpathSync(process.argv[1]) === SELF;
  } catch {
    return false;
  }
}

if (isMain()) process.exitCode = await main();
