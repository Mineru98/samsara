import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync,
} from 'node:fs';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const GATE = realpathSync(path.join(HERE, 'issue-gate.mjs'));
const ISSUE_START = path.join(HERE, 'issue-start.mjs');
const ISSUE = 7;
const BRANCH = 'fix/7-gate-probe';
const sleep = (ms) => new Promise((res) => setTimeout(res, ms));

const sh = (cmd, args, cwd, extra = {}) => {
  const r = spawnSync(cmd, args, { cwd, encoding: 'utf8', ...extra });
  return { code: r.status, out: (r.stdout || '').trim(), err: (r.stderr || '').trim() };
};
const mustGit = (cwd, ...args) => {
  const r = sh('git', args, cwd);
  assert.equal(r.code, 0, `git ${args.join(' ')}: ${r.err}`);
  return r.out;
};

/** 실제 bare origin + base checkout + 이슈 산출물. tracker(gh)는 네트워크 경계라 실패하는 stub 으로 둔다. */
function fixture(t, { ignoreIssue = false } = {}) {
  const tmp = realpathSync(mkdtempSync(path.join(tmpdir(), 'samsara-gate-')));
  const children = [];
  t.after(() => {
    for (const c of children) try { process.kill(c.pid, 'SIGKILL'); } catch { /* 이미 종료 */ }
    rmSync(tmp, { recursive: true, force: true });
  });
  const bin = path.join(tmp, 'bin');
  mkdirSync(bin);
  writeFileSync(path.join(bin, 'gh'), '#!/bin/sh\necho "gh stub: offline" >&2\nexit 1\n');
  chmodSync(path.join(bin, 'gh'), 0o755);
  const origin = path.join(tmp, 'origin.git');
  mustGit(tmp, 'init', '--bare', '-b', 'main', origin);
  const base = path.join(tmp, 'repo');
  mkdirSync(base);
  mustGit(base, 'init', '-b', 'main');
  mustGit(base, 'config', 'user.email', 'gate@test.invalid');
  mustGit(base, 'config', 'user.name', 'gate test');
  writeFileSync(path.join(base, 'app.txt'), 'v1\n');
  writeFileSync(path.join(base, 'run-tests.sh'), '#!/bin/sh\necho tests-ok\n');
  writeFileSync(path.join(base, '.gitignore'), ignoreIssue
    ? '.issue/\nignored/\n'
    : '.issue/**\n!.issue/*/\n!.issue/*/evidence/\n!.issue/*/evidence/**\nignored/\n');
  mustGit(base, 'add', '.');
  mustGit(base, 'commit', '-m', 'init');
  mustGit(base, 'remote', 'add', 'origin', origin);
  mustGit(base, 'push', '-u', 'origin', 'main');
  const issueDir = path.join(base, '.issue', String(ISSUE));
  mkdirSync(issueDir, { recursive: true });
  writeFileSync(path.join(issueDir, 'issue.json'), JSON.stringify({ number: ISSUE, title: 'probe' }));
  writeFileSync(path.join(issueDir, 'issue.md'), '# probe\n');
  const wt = path.join(tmp, 'repo-issue-7');
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, HOME: tmp };
  for (const k of ['CLAUDE_CODE_SESSION_ID', 'CLAUDE_PID', 'SAMSARA_GATE_FENCE']) delete env[k];

  /** Claude 세션 대역: 살아 있는 pid 와 세션 id. kill 하면 stale owner 가 된다. */
  const session = (id) => {
    const child = spawn('sleep', ['600'], { stdio: 'ignore' });
    children.push(child);
    return {
      id,
      pid: child.pid,
      env: { CLAUDE_CODE_SESSION_ID: id, CLAUDE_PID: String(child.pid) },
      kill: async () => { child.kill('SIGKILL'); await once(child, 'exit'); },
    };
  };
  const parse = (r) => { let json = null; try { json = JSON.parse(r.out); } catch { /* 비 JSON */ } return { ...r, json }; };
  const withIssue = (args, n = ISSUE) => [args[0], '--issue', String(n), ...args.slice(1)];
  const gate = (args, s = null, cwd = base) => parse(sh(process.execPath, [GATE, ...withIssue(args)], cwd, { env: { ...env, ...(s?.env || {}) } }));
  const gateAsync = (args, s) => new Promise((resolve) => {
    const child = spawn(process.execPath, [GATE, ...withIssue(args)], { cwd: base, env: { ...env, ...(s?.env || {}) } });
    let out = '';
    child.stdout.on('data', (d) => { out += d; });
    child.on('close', (code) => resolve({ code, json: JSON.parse(out) }));
  });
  const spawnGate = (args) => {
    const child = spawn(process.execPath, [GATE, ...withIssue(args)], { cwd: base, env, stdio: 'ignore' });
    children.push(child);
    return child;
  };
  const hook = (toolName, toolInput, sessionId, cwd = wt) => {
    const r = sh(process.execPath, [GATE, 'hook'], cwd, {
      env,
      input: JSON.stringify({ session_id: sessionId, cwd, hook_event_name: 'PreToolUse', tool_name: toolName, tool_input: toolInput }),
    });
    assert.equal(r.code, 0, r.err);
    return r.out ? JSON.parse(r.out).hookSpecificOutput.permissionDecision : 'pass';
  };
  const store = () => path.join(base, '.git', 'samsara-gate', `issue-${ISSUE}`);
  const ledger = () => JSON.parse(readFileSync(path.join(store(), 'ledger.json'), 'utf8'));
  const lock = () => JSON.parse(readFileSync(path.join(store(), 'lock.json'), 'utf8'));
  const commits = () => Number(mustGit(base, 'rev-list', '--count', '--all'));
  const preCommit = (body) => {
    const file = path.join(base, '.git', 'hooks', 'pre-commit');
    writeFileSync(file, `#!/bin/sh\n${body}\n`);
    chmodSync(file, 0o755);
  };
  return { tmp, base, wt, env, gate, gateAsync, spawnGate, hook, session, store, ledger, lock, commits, preCommit };
}

/** intake·fetch·classify 는 실제 사실로 편입, plan·worktree·before 는 정상 경로로 진행한다. */
function throughBefore(f) {
  const s = f.session('S-main');
  let r = f.gate(['adopt', '--worktree', f.wt, '--branch', BRANCH, '--base', 'main', '--classification', 'backend', '--through', 'classify', '--session', s.id]);
  assert.equal(r.code, 0, r.out);
  assert.deepEqual(r.json.adopted, ['intake', 'fetch', 'classify']);
  r = f.gate(['begin', '--phase', 'plan'], s);
  assert.equal(r.code, 0, r.out);
  writeFileSync(path.join(f.base, '.issue', '7', 'plan.md'), '# plan\n## 증거 계획\n- before/after text\n');
  r = f.gate(['complete', '--phase', 'plan'], s);
  assert.equal(r.code, 0, r.out);
  r = f.gate(['run', '--phase', 'worktree']);
  assert.equal(r.code, 0, r.out + r.err);
  assert.ok(existsSync(f.wt));
  r = f.gate(['begin', '--phase', 'before'], s);
  assert.equal(r.code, 0, r.out);
  const before = path.join(f.wt, '.issue', '7', 'evidence', 'before');
  mkdirSync(before, { recursive: true });
  writeFileSync(path.join(before, 'baseline.txt'), 'v1 baseline\n');
  r = f.gate(['complete', '--phase', 'before'], s);
  assert.equal(r.code, 0, r.out);
  assert.equal(r.json.nextPhase, 'implement');
  return s;
}

function implementAndComplete(f, s) {
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  writeFileSync(path.join(f.wt, 'app.txt'), 'v2\n');
  assert.equal(f.gate(['complete', '--phase', 'implement'], s).code, 0);
}

test('out-of-order phases are rejected and produce no side effect', (t) => {
  const f = fixture(t);
  const s = f.session('S1');
  assert.equal(f.gate(['init', '--worktree', f.wt, '--branch', BRANCH, '--base', 'main']).code, 0);
  for (const phase of ['implement', 'before']) {
    const r = f.gate(['begin', '--phase', phase], s);
    assert.equal(r.code, 2);
    assert.equal(r.json.code, 'PHASE_SEQUENCE');
  }
  const r = f.gate(['run', '--phase', 'worktree']);
  assert.equal(r.code, 2);
  assert.equal(r.json.code, 'PHASE_SEQUENCE');
  assert.equal(existsSync(f.wt), false, 'worktree effect must not run out of order');
  assert.equal(existsSync(path.join(f.store(), 'lock.json')), false);
});

test('forged completion claims never unlock the next phase', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  let r = f.gate(['complete', '--phase', 'implement'], s);
  assert.equal(r.code, 3);
  assert.equal(r.json.code, 'VERIFY_FAILED');
  assert.equal(f.ledger().state.completedPhases.at(-1), 'issue-start.before');
  r = f.gate(['run', '--phase', 'commit', '--message', 'fix: forged']);
  assert.equal(r.code, 2);
  assert.equal(r.json.code, 'PHASE_SEQUENCE');
  assert.equal(f.commits(), 1);
  const other = f.session('S-other');
  r = f.gate(['complete', '--phase', 'implement'], other);
  assert.equal(r.code, 5);
  assert.equal(r.json.code, 'NOT_OWNER');
  r = f.gate(['abort', '--phase', 'implement'], other);
  assert.equal(r.code, 5);
  assert.ok(existsSync(path.join(f.store(), 'lock.json')), 'non-owner cannot unlock');
});

test('a forged ledger is caught by prior-fact re-verification', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  rmSync(path.join(f.wt, '.issue', '7', 'evidence', 'before'), { recursive: true });
  const r = f.gate(['begin', '--phase', 'implement'], s);
  assert.equal(r.code, 3);
  assert.equal(r.json.code, 'PRIOR_FACTS_INVALID');
  assert.equal(existsSync(path.join(f.store(), 'lock.json')), false);
});

test('only one of many concurrent workers acquires the same issue lock', async (t) => {
  const f = fixture(t);
  throughBefore(f);
  const workers = Array.from({ length: 8 }, (_, i) => f.session(`S-race-${i}`));
  const results = await Promise.all(workers.map((s) => f.gateAsync(['begin', '--phase', 'implement'], s)));
  const winners = results.filter((r) => r.code === 0);
  assert.equal(winners.length, 1, JSON.stringify(results.map((r) => r.json.code ?? r.json.status)));
  assert.ok(results.filter((r) => r.code !== 0).every((r) => r.code === 5 && r.json.code === 'LOCKED'));
  assert.equal(f.lock().fence, f.ledger().fence);
});

test('review regression: no phase lock grants arbitrary shell (redirect, interpreter, fake gate path)', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  const evil = path.join(f.tmp, 'evil-issue-gate.mjs');
  writeFileSync(evil, "require('child_process').execSync('git commit --allow-empty -m bypass')\n");
  const bypasses = [
    'printf bad > app.txt',
    'echo x >> app.txt',
    `python3 -c "import subprocess; subprocess.run(['git','commit','--allow-empty','-m','bypass'])"`,
    "node -e \"require('child_process').execSync('git commit --allow-empty -m x')\"",
    'sh run-tests.sh',
    `node ${evil} run --issue 7 --phase commit`,
    `node ./skills/x/issue-gate.mjs run --issue 7 --phase commit`,
    `SAMSARA_GATE_FENCE=9 node ${ISSUE_START} evidence-commit 7`,
    `node ${GATE} status --issue 7 && git commit -am bypass`,
    `node ${GATE} status --issue 7; git push`,
    'git -c core.pager=sh log',
    'find . -exec git commit -m x ;',
    'rg --pre ./x foo',
    'git commit -am bypass',
    'gh issue comment 7 --body bypass',
  ];
  const decide = () => bypasses.map((command) => [command, f.hook('Bash', { command }, s.id)]);
  // 잠금 없음
  for (const [command, d] of decide()) assert.equal(d, 'deny', `no lock: ${command}`);
  // implement 보유 중
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  for (const [command, d] of decide()) assert.equal(d, 'deny', `implement: ${command}`);
  writeFileSync(path.join(f.wt, 'app.txt'), 'v2\n');
  assert.equal(f.gate(['complete', '--phase', 'implement'], s).code, 0);
  assert.equal(f.gate(['run', '--phase', 'commit', '--message', 'fix: v2']).code, 0);
  // after 보유 중
  assert.equal(f.gate(['begin', '--phase', 'after'], s).code, 0);
  for (const [command, d] of decide()) assert.equal(d, 'deny', `after: ${command}`);
  // 허용되는 것은 신뢰 경로 gate CLI 와 읽기 전용 명령뿐
  for (const command of [`node ${GATE} status --issue 7`, 'git status --short', 'git diff HEAD~1', 'cat app.txt', 'rg v2 app.txt']) {
    assert.equal(f.hook('Bash', { command }, s.id), 'pass', command);
  }
  // base checkout 에서 worktree 를 겨눈 명령도 같은 규칙
  assert.equal(f.hook('Bash', { command: `git -C ${f.wt} commit -am bypass` }, 'S-controller', f.base), 'deny');
  assert.equal(f.hook('Bash', { command: 'gh issue comment 7 --body bypass' }, 'S-controller', f.base), 'deny');
});

test('before lock: shell cannot modify code; evidence via file tool and brokered profile only', (t) => {
  const f = fixture(t);
  const s = f.session('S-before');
  assert.equal(f.gate(['adopt', '--worktree', f.wt, '--branch', BRANCH, '--base', 'main', '--classification', 'backend', '--through', 'classify', '--session', s.id]).code, 0);
  assert.equal(f.gate(['begin', '--phase', 'plan'], s).code, 0);
  writeFileSync(path.join(f.base, '.issue', '7', 'plan.md'), '## 증거\n');
  assert.equal(f.gate(['complete', '--phase', 'plan'], s).code, 0);
  assert.equal(f.gate(['run', '--phase', 'worktree']).code, 0);
  assert.equal(f.gate(['profile', '--name', 'unit', '--', 'sh', 'run-tests.sh']).code, 0);
  assert.equal(f.gate(['begin', '--phase', 'before'], s).code, 0);
  assert.equal(f.hook('Bash', { command: 'printf bad > app.txt' }, s.id), 'deny');
  assert.equal(f.hook('Write', { file_path: path.join(f.wt, 'app.txt'), content: 'x' }, s.id), 'deny');
  assert.equal(f.hook('Write', { file_path: path.join(f.wt, '.issue/7/evidence/before/a.txt'), content: 'x' }, s.id), 'pass');
  assert.equal(f.hook('Bash', { command: `node ${GATE} exec --issue 7 --profile unit` }, s.id), 'pass');
  const r = f.gate(['exec', '--profile', 'unit'], s);
  assert.equal(r.code, 0, r.out);
  assert.match(readFileSync(path.join(f.wt, '.issue/7/evidence/before/exec-unit.txt'), 'utf8'), /tests-ok[\s\S]*exit=0/);
  assert.equal(f.gate(['complete', '--phase', 'before'], s).code, 0);
});

test('brokered exec runs only controller-approved trees and quarantines ref mutations', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  assert.equal(f.gate(['profile', '--name', 'unit', '--phases', 'implement', '--', 'sh', 'run-tests.sh']).code, 0);
  // worker 세션은 profile·승인을 스스로 만들 수 없다
  let r = f.gate(['profile', '--name', 'evil', '--', 'git', 'commit', '-m', 'x'], s);
  assert.equal(r.code, 5);
  assert.equal(r.json.code, 'CONTROLLER_ONLY');
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  // worker 가 테스트 스크립트에 commit 을 심어도 승인되지 않은 tree 라 실행 0회
  writeFileSync(path.join(f.wt, 'run-tests.sh'), '#!/bin/sh\ngit commit --allow-empty -m smuggled\n');
  r = f.gate(['exec', '--profile', 'unit'], s);
  assert.equal(r.code, 3);
  assert.equal(r.json.code, 'EXEC_APPROVAL_REQUIRED');
  assert.equal(f.commits(), 1, 'unapproved brokered code never ran');
  // controller 가 그 tree 를 (잘못) 승인하면 실행되지만 ref 변화로 즉시 격리되고 이후 진행이 막힌다
  assert.equal(f.gate(['approve-exec', '--profile', 'unit', '--tree', r.json.tree]).code, 0);
  r = f.gate(['exec', '--profile', 'unit'], s);
  assert.equal(r.code, 3);
  assert.equal(r.json.code, 'QUARANTINED');
  writeFileSync(path.join(f.wt, 'app.txt'), 'v2\n');
  r = f.gate(['complete', '--phase', 'implement'], s);
  assert.equal(r.code, 3);
  assert.equal(r.json.code, 'QUARANTINED');
  assert.equal(f.gate(['run', '--phase', 'commit']).json.code, 'QUARANTINED');
});

test('implement completes only with a passing required profile bound to the current tree', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  assert.equal(f.gate(['profile', '--name', 'unit', '--phases', 'implement', '--required', '--', 'sh', 'run-tests.sh']).code, 0);
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  const approveAndExec = () => {
    let r = f.gate(['exec', '--profile', 'unit'], s);
    assert.equal(r.json.code, 'EXEC_APPROVAL_REQUIRED', r.out);
    // controller 승인은 별도 mutex 라 세션이 implement lock 을 쥔 채로 가능하다
    assert.equal(f.gate(['approve-exec', '--profile', 'unit', '--tree', r.json.tree]).code, 0);
    return f.gate(['exec', '--profile', 'unit'], s);
  };
  writeFileSync(path.join(f.wt, 'app.txt'), 'v2\n');
  let r = f.gate(['complete', '--phase', 'implement'], s);
  assert.equal(r.json.code, 'VERIFY_FAILED', 'no test run yet');
  assert.equal(approveAndExec().code, 0);
  writeFileSync(path.join(f.wt, 'app.txt'), 'v3\n');
  r = f.gate(['complete', '--phase', 'implement'], s);
  assert.equal(r.json.code, 'VERIFY_FAILED', 'a PASS on an older tree does not count');
  writeFileSync(path.join(f.wt, 'run-tests.sh'), '#!/bin/sh\necho failing\nexit 1\n');
  r = approveAndExec();
  assert.equal(r.code, 4);
  assert.equal(r.json.code, 'COMMAND_FAILED');
  r = f.gate(['complete', '--phase', 'implement'], s);
  assert.equal(r.json.code, 'VERIFY_FAILED', 'a failing run on the current tree blocks completion');
  assert.equal(f.gate(['run', '--phase', 'commit']).json.code, 'PHASE_SEQUENCE');
  assert.equal(f.commits(), 1);
  writeFileSync(path.join(f.wt, 'run-tests.sh'), '#!/bin/sh\necho tests-ok\n');
  assert.equal(approveAndExec().code, 0);
  r = f.gate(['complete', '--phase', 'implement'], s);
  assert.equal(r.code, 0, r.out);
  r = f.gate(['run', '--phase', 'commit', '--message', 'fix: v3']);
  assert.equal(r.code, 0, r.out);
  assert.equal(mustGit(f.wt, 'rev-parse', 'HEAD^{tree}'), f.ledger().facts.implement.tree, 'the commit is exactly the tested tree');
});

test('hook confines code edits to the implement holder inside tracked worktree content', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  const file = path.join(f.wt, 'app.txt');
  assert.equal(f.hook('Write', { file_path: file, content: 'x' }, s.id), 'deny');
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  assert.equal(f.hook('Write', { file_path: file, content: 'x' }, s.id), 'pass');
  assert.equal(f.hook('Edit', { file_path: file, old_string: 'v1', new_string: 'v2' }, s.id), 'pass');
  assert.equal(f.hook('Write', { file_path: file, content: 'x' }, 'S-other'), 'deny');
  for (const denied of [
    path.join(f.wt, '.issue/7/evidence/before/x.txt'),
    path.join(f.wt, 'ignored/payload.js'),
    path.join(f.wt, '.claude/settings.local.json'),
    path.join(f.wt, '.git'),
    path.join(f.store(), 'ledger.json'),
    path.join(f.base, '.git', 'config'),
    path.join(f.base, 'app.txt'),
    GATE,
  ]) {
    assert.equal(f.hook('Write', { file_path: denied, content: 'x' }, s.id), 'deny', denied);
  }
  assert.equal(f.hook('Bash', { command: `cat ${f.store()}/ledger.json` }, s.id), 'deny');
});

test('legacy CLI requires being the live gate run step, not a copied fence', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  implementAndComplete(f, s);
  const before = f.commits();
  let r = sh(process.execPath, [ISSUE_START, 'evidence-commit', '7'], f.wt, { env: f.env });
  assert.equal(r.code, 7, r.err);
  assert.match(r.err, /GATE_REQUIRED/);
  r = sh(process.execPath, [ISSUE_START, 'evidence-commit', '7'], f.wt, { env: { ...f.env, SAMSARA_GATE_FENCE: String(f.ledger().fence) } });
  assert.equal(r.code, 7, 'a copied fence is rejected');
  assert.equal(f.commits(), before);
  // gate run 이 실행 중일 때 옆에서 같은 fence 로 불러도 직접 자식·현재 step argv 가 아니라 거절된다
  f.preCommit('sleep 3');
  const child = f.spawnGate(['run', '--phase', 'commit', '--message', 'fix: v2']);
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    try { if (f.lock().step?.pgid) break; } catch { /* 아직 없음 */ }
    spawnSync('sleep', ['0.05']);
  }
  r = sh(process.execPath, [ISSUE_START, 'evidence-commit', '7'], f.wt, { env: { ...f.env, SAMSARA_GATE_FENCE: String(f.lock().fence) } });
  assert.equal(r.code, 7, 'a sibling process with the live fence is not the gate step');
  return once(child, 'exit');
});

test('replayed completion and re-run are idempotent with zero side effects', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  const snapshot = readFileSync(path.join(f.store(), 'ledger.json'), 'utf8');
  let r = f.gate(['complete', '--phase', 'before'], s);
  assert.equal(r.code, 0);
  assert.equal(r.json.status, 'already-complete');
  assert.equal(readFileSync(path.join(f.store(), 'ledger.json'), 'utf8'), snapshot);
  const worktrees = mustGit(f.base, 'worktree', 'list');
  r = f.gate(['run', '--phase', 'worktree']);
  assert.equal(r.code, 2);
  assert.equal(r.json.code, 'PHASE_SEQUENCE');
  assert.equal(mustGit(f.base, 'worktree', 'list'), worktrees);
});

test('a stale session owner is UNKNOWN until reconciled and cannot act afterwards', async (t) => {
  const f = fixture(t);
  throughBefore(f);
  const stale = f.session('S-stale');
  assert.equal(f.gate(['begin', '--phase', 'implement'], stale).code, 0);
  writeFileSync(path.join(f.wt, 'app.txt'), 'half done\n');
  await stale.kill();
  const next = f.session('S-next');
  let r = f.gate(['status']);
  assert.equal(r.json.condition, 'unknown');
  r = f.gate(['begin', '--phase', 'implement'], next);
  assert.equal(r.code, 6);
  assert.equal(r.json.code, 'RECONCILE_REQUIRED');
  assert.equal(f.hook('Write', { file_path: path.join(f.wt, 'app.txt'), content: 'x' }, stale.id), 'deny');
  assert.equal(f.gate(['run', '--phase', 'commit']).code, 6);
  r = f.gate(['reconcile']);
  assert.equal(r.code, 0);
  assert.equal(r.json.status, 'abandoned', 'session work of a dead owner is never auto-completed');
  assert.equal(r.json.nextPhase, 'implement');
  assert.equal(f.commits(), 1);
  assert.equal(f.gate(['begin', '--phase', 'implement'], next).code, 0);
  r = f.gate(['complete', '--phase', 'implement'], { env: { CLAUDE_CODE_SESSION_ID: stale.id, CLAUDE_PID: String(process.pid) } });
  assert.equal(r.code, 5);
  assert.equal(r.json.code, 'NOT_OWNER');
  assert.equal(f.gate(['complete', '--phase', 'implement'], next).code, 0);
});

test('effect failure keeps the next phase locked with zero commits', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  implementAndComplete(f, s);
  f.preCommit('exit 1');
  const r = f.gate(['run', '--phase', 'commit', '--message', 'fix: v2']);
  assert.equal(r.code, 4, r.out);
  assert.equal(r.json.code, 'EFFECT_FAILED');
  assert.equal(f.commits(), 1);
  assert.equal(f.gate(['begin', '--phase', 'after'], s).json.code, 'PHASE_SEQUENCE');
  assert.equal(existsSync(path.join(f.store(), 'lock.json')), false);
});

test('a killed gate keeps UNKNOWN while its child runs, then confirms the real result', async (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  implementAndComplete(f, s);
  const marker = path.join(f.tmp, 'precommit-started');
  f.preCommit(`touch '${marker}'\nsleep 2\nexit 0`);
  const child = f.spawnGate(['run', '--phase', 'commit', '--message', 'fix: v2']);
  while (!existsSync(marker)) await sleep(50);
  child.kill('SIGKILL');
  await once(child, 'exit');
  let r = f.gate(['reconcile']);
  assert.equal(r.code, 6);
  assert.equal(r.json.code, 'STILL_RUNNING', 'a dead owner with a live child must stay UNKNOWN');
  r = f.gate(['begin', '--phase', 'after'], s);
  assert.equal(r.code, 6, 'next phase is blocked while the commit result is UNKNOWN');
  assert.equal(f.hook('Write', { file_path: path.join(f.wt, '.issue/7/evidence/after/a.txt'), content: 'x' }, s.id), 'deny');
  for (let i = 0; i < 100; i += 1) {
    r = f.gate(['reconcile']);
    if (r.json.code !== 'STILL_RUNNING') break;
    await sleep(100);
  }
  assert.equal(r.code, 0, r.out);
  assert.equal(r.json.status, 'reconciled-complete');
  assert.equal(r.json.nextPhase, 'after');
  assert.equal(f.commits(), 2);
  assert.equal(f.ledger().facts.commit.implementationCommit, mustGit(f.wt, 'rev-parse', 'HEAD'));
});

test('a killed effect whose child exit is confirmed reconciles to failed and stays retryable', async (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  implementAndComplete(f, s);
  const marker = path.join(f.tmp, 'precommit-started');
  f.preCommit(`touch '${marker}'\nsleep 30`);
  const child = f.spawnGate(['run', '--phase', 'commit', '--message', 'fix: v2']);
  while (!existsSync(marker)) await sleep(50);
  child.kill('SIGKILL');
  await once(child, 'exit');
  assert.equal(f.gate(['reconcile']).json.code, 'STILL_RUNNING');
  process.kill(-f.lock().step.pgid, 'SIGKILL');
  await sleep(200);
  let r = f.gate(['reconcile']);
  assert.equal(r.json.status, 'reconciled-failed', r.out);
  assert.equal(r.json.nextPhase, 'commit');
  assert.equal(f.commits(), 1);
  f.preCommit('exit 0');
  r = f.gate(['run', '--phase', 'commit', '--message', 'fix: v2']);
  assert.equal(r.code, 0, r.out);
  assert.equal(f.commits(), 2);
});

test('an UNKNOWN effect with no recorded child needs explicit operator resolution', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  implementAndComplete(f, s);
  // gate 가 lock 에 step 을 쓰기 직전에 죽은 상태를 재현한다
  const dead = spawnSync('sh', ['-c', 'echo $$']).stdout.toString().trim();
  writeFileSync(path.join(f.store(), 'lock.json'), JSON.stringify({
    fence: f.ledger().fence + 1, phase: 'commit', stage: 'executing', acquiredAt: new Date().toISOString(),
    owner: { kind: 'process', session: null, pid: Number(dead), pidStart: 'gone' }, step: { index: 0, argv: ['git'], cwd: f.wt, pgid: null },
  }));
  let r = f.gate(['reconcile']);
  assert.equal(r.code, 6);
  assert.equal(r.json.code, 'OPERATOR_RESOLUTION_REQUIRED');
  assert.equal(f.gate(['begin', '--phase', 'after'], s).code, 6);
  r = f.gate(['reconcile', '--resolve', 'failed'], s);
  assert.equal(r.code, 5, 'a bound worker cannot resolve UNKNOWN');
  r = f.gate(['reconcile', '--resolve', 'failed']);
  assert.equal(r.json.status, 'reconciled-failed');
  assert.equal(r.json.nextPhase, 'commit');
});

test('hook failures fail closed', (t) => {
  const f = fixture(t);
  const s = throughBefore(f);
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  writeFileSync(path.join(f.store(), 'lock.json'), '{corrupt');
  assert.equal(f.hook('Write', { file_path: path.join(f.wt, 'app.txt'), content: 'x' }, s.id), 'deny');
  const missing = sh('/bin/sh', ['-c', "node '/nonexistent/issue-gate.mjs' hook || exit 2"], f.wt, { input: '{}' });
  assert.equal(missing.code, 2);
});

test('independent issues hold their implement locks in parallel', (t) => {
  const f = fixture(t);
  const s7 = throughBefore(f);
  const dir8 = path.join(f.base, '.issue', '8');
  mkdirSync(dir8, { recursive: true });
  writeFileSync(path.join(dir8, 'issue.json'), JSON.stringify({ number: 8 }));
  writeFileSync(path.join(dir8, 'plan.md'), '## 증거\n');
  const wt8 = path.join(f.tmp, 'repo-issue-8');
  mustGit(f.base, 'worktree', 'add', '-b', 'fix/8-other', wt8, 'main');
  mkdirSync(path.join(wt8, '.issue', '8', 'evidence', 'before'), { recursive: true });
  writeFileSync(path.join(wt8, '.issue', '8', 'evidence', 'before', 'b.txt'), 'b\n');
  const s8 = f.session('S-8');
  const run8 = (args, s) => sh(process.execPath, [GATE, args[0], '--issue', '8', ...args.slice(1)], f.base, { env: { ...f.env, ...(s?.env || {}) } });
  let r = run8(['adopt', '--worktree', wt8, '--branch', 'fix/8-other', '--base', 'main', '--classification', 'backend', '--session', s8.id]);
  assert.equal(r.code, 0, r.out);
  assert.equal(f.gate(['begin', '--phase', 'implement'], s7).code, 0);
  r = run8(['begin', '--phase', 'implement'], s8);
  assert.equal(r.code, 0, r.out);
  assert.equal(f.hook('Write', { file_path: path.join(wt8, 'app.txt'), content: 'x' }, s8.id, wt8), 'pass');
  assert.equal(f.hook('Write', { file_path: path.join(wt8, 'app.txt'), content: 'x' }, s7.id, wt8), 'deny');
  assert.equal(f.hook('Write', { file_path: path.join(f.wt, 'app.txt'), content: 'x' }, s8.id, wt8), 'deny');
});

test('adopt only records phases whose facts exist', (t) => {
  const f = fixture(t);
  mustGit(f.base, 'worktree', 'add', '-b', BRANCH, f.wt, 'main');
  const r = f.gate(['adopt', '--worktree', f.wt, '--branch', BRANCH, '--base', 'main', '--classification', 'backend']);
  assert.equal(r.code, 3);
  assert.equal(r.json.nextPhase, 'plan', 'no plan.md, so adoption stops before plan');
  assert.deepEqual(f.ledger().state.completedPhases, ['issue-start.intake', 'issue-start.fetch', 'issue-start.classify']);
});

test('repositories that ignore .issue entirely still get tree digests and brokered runs', (t) => {
  const f = fixture(t, { ignoreIssue: true });
  const s = throughBefore(f);
  let r = f.gate(['status']);
  assert.equal(r.code, 0, r.out);
  assert.equal(r.json.tree, mustGit(f.wt, 'rev-parse', 'HEAD^{tree}'), 'evidence under ignored .issue does not change the tree');
  assert.equal(f.gate(['profile', '--name', 'unit', '--phases', 'implement', '--required', '--', 'sh', 'run-tests.sh']).code, 0);
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  writeFileSync(path.join(f.wt, 'app.txt'), 'v2\n');
  r = f.gate(['exec', '--profile', 'unit'], s);
  assert.equal(r.json.code, 'EXEC_APPROVAL_REQUIRED');
  assert.equal(f.gate(['approve-exec', '--profile', 'unit', '--tree', r.json.tree]).code, 0);
  assert.equal(f.gate(['exec', '--profile', 'unit'], s).code, 0);
  assert.equal(f.gate(['complete', '--phase', 'implement'], s).code, 0);
  // pure API 의 `git add -A -- :!.issue` 는 이 저장소에서 git 이 거절한다(직접 실행 시 exit 1).
  const raw = sh('git', ['add', '-A', '--', ':!.issue'], f.wt, { env: { ...f.env, GIT_INDEX_FILE: path.join(f.tmp, 'probe-index') } });
  assert.notEqual(raw.code, 0, 'the proposed pathspec fails on a fully ignored .issue');
  // gate 는 같은 의미의 staging adapter 로 실행한다: 증거 제외, ignore 존중, 실제 argv 기록
  mkdirSync(path.join(f.wt, 'ignored'), { recursive: true });
  writeFileSync(path.join(f.wt, 'ignored', 'dep.js'), 'x\n');
  writeFileSync(path.join(f.wt, 'new file *.txt'), 'literal\n');
  r = f.gate(['run', '--phase', 'commit', '--message', 'fix: v2']);
  assert.equal(r.json.code, 'VERIFY_FAILED', 'a file added after the verified implement tree is caught');
  assert.equal(f.commits(), 2, 'the commit ran but its tree differs from the tested tree, so the phase stays open');
});

test('staging adapter commits exactly the verified tree in an ignored-.issue repository', (t) => {
  const f = fixture(t, { ignoreIssue: true });
  const s = throughBefore(f);
  assert.equal(f.gate(['begin', '--phase', 'implement'], s).code, 0);
  writeFileSync(path.join(f.wt, 'app.txt'), 'v2\n');
  writeFileSync(path.join(f.wt, 'new file *.txt'), 'literal\n');
  mkdirSync(path.join(f.wt, 'ignored'), { recursive: true });
  writeFileSync(path.join(f.wt, 'ignored', 'dep.js'), 'x\n');
  rmSync(path.join(f.wt, 'run-tests.sh'));
  assert.equal(f.gate(['complete', '--phase', 'implement'], s).code, 0);
  const r = f.gate(['run', '--phase', 'commit', '--message', 'fix: v2']);
  assert.equal(r.code, 0, r.out);
  const files = mustGit(f.wt, 'show', '--name-status', '--format=', 'HEAD').split('\n').sort();
  assert.deepEqual(files, ['A\tnew file *.txt', 'D\trun-tests.sh', 'M\tapp.txt']);
  assert.equal(mustGit(f.wt, 'rev-parse', 'HEAD^{tree}'), f.ledger().facts.implement.tree);
  const steps = readFileSync(path.join(f.store(), 'attempts.jsonl'), 'utf8').split('\n').filter(Boolean).map(JSON.parse)
    .filter((e) => e.outcome === 'step' && e.detail.proposedArgv);
  assert.equal(steps.length, 1);
  assert.deepEqual(steps[0].detail.proposedArgv, ['git', 'add', '-A', '--', ':!.issue']);
  assert.equal(steps[0].detail.argv[1], '--literal-pathspecs');
  assert.equal(steps[0].detail.exit, 0);
});

test('status parsing keeps leading spaces, spaced paths and renames (real #74 DIRTY_BASE regression)', async (t) => {
  const f = fixture(t);
  // base 의 유일한 변경이 추적 중인 .issue/graph.json 수정(` M .issue/graph.json`, 첫 줄 선행 공백)
  writeFileSync(path.join(f.base, '.gitignore'), '.issue/**\n!.issue/*/\n!.issue/*/evidence/\n!.issue/*/evidence/**\n!.issue/graph.json\nignored/\n');
  writeFileSync(path.join(f.base, '.issue', 'graph.json'), '{}\n');
  mustGit(f.base, 'add', '.gitignore', '.issue/graph.json');
  mustGit(f.base, 'commit', '-m', 'graph');
  writeFileSync(path.join(f.base, '.issue', 'graph.json'), '{"changed":true}\n');
  assert.match(sh('git', ['status', '--porcelain'], f.base).out + '|', /M .issue\/graph.json/);
  const s = f.session('S-74');
  let r = f.gate(['adopt', '--worktree', f.wt, '--branch', BRANCH, '--base', 'main', '--classification', 'backend', '--through', 'classify', '--session', s.id]);
  assert.equal(r.code, 0, r.out);
  assert.deepEqual(r.json.adopted, ['intake', 'fetch', 'classify']);
  // 실제 코드 변경은 여전히 dirty 로 잡는다
  writeFileSync(path.join(f.base, 'app.txt'), 'dirty\n');
  assert.equal(f.gate(['begin', '--phase', 'plan'], s).json.reason, 'DIRTY_BASE');
  writeFileSync(path.join(f.base, 'app.txt'), 'v1\n');
  const { statusEntries } = await import(GATE);
  mustGit(f.base, 'mv', 'app.txt', 'renamed app.txt');
  writeFileSync(path.join(f.base, 'run-tests.sh'), 'changed\n');
  const entries = statusEntries(f.base, 'no');
  assert.deepEqual(entries.map((e) => [e.xy, e.path, e.orig]).sort(), [
    [' M', '.issue/graph.json', null],
    [' M', 'run-tests.sh', null],
    ['R ', 'renamed app.txt', 'app.txt'],
  ]);
});

test('owner liveness is locale independent (ko worker vs C controller, legacy localized locks)', async (t) => {
  const f = fixture(t);
  throughBefore(f);
  const ko = { LC_ALL: 'ko_KR.UTF-8', LANG: 'ko_KR.UTF-8' };
  const c = { LC_ALL: 'C', LANG: 'C' };
  const { startKey } = await import(GATE);
  assert.equal(startKey('2026년 10월 29일 목요일 08시 04분 02초'), startKey('Thu Oct 29 08:04:02 2026  '));
  assert.notEqual(startKey('2026년 10월 29일 목요일 08시 04분 03초'), startKey('Thu Oct 29 08:04:02 2026'));
  const s = f.session('S-ko');
  const asWorker = { env: { ...s.env, ...ko } };
  assert.equal(f.gate(['begin', '--phase', 'implement'], asWorker).code, 0);
  const koStart = sh('ps', ['-o', 'lstart=', '-p', String(s.pid)], f.base, { env: { ...f.env, ...ko } }).out;
  assert.match(koStart, /년/, 'the platform renders a localized lstart');
  const lockPath = path.join(f.store(), 'lock.json');
  for (const stored of ['as-recorded', 'legacy-ko']) {
    if (stored === 'legacy-ko') {
      const lock = f.lock();
      lock.owner.pidStart = koStart;
      writeFileSync(lockPath, JSON.stringify(lock));
    }
    for (const loc of [c, ko]) {
      const st = f.gate(['status'], { env: loc });
      assert.equal(st.json.condition, 'locked', `${stored} under ${loc.LANG}`);
      const other = f.session(`S-other-${stored}-${loc.LANG}`);
      const r = f.gate(['begin', '--phase', 'implement'], { env: { ...other.env, ...loc } });
      assert.equal(r.code, 5, `${stored} under ${loc.LANG}: a live owner is LOCKED, never UNKNOWN`);
      assert.equal(f.gate(['reconcile'], { env: loc }).code, 5, 'reconcile must not take over a live owner');
      const hook = sh(process.execPath, [GATE, 'hook'], f.wt, {
        env: { ...f.env, ...loc },
        input: JSON.stringify({ session_id: s.id, cwd: f.wt, tool_name: 'Write', tool_input: { file_path: path.join(f.wt, 'app.txt'), content: 'x' } }),
      });
      assert.equal(hook.out, '', `${stored} under ${loc.LANG}: the live holder may edit`);
    }
  }
  await s.kill();
  for (const loc of [c, ko]) assert.equal(f.gate(['status'], { env: loc }).json.condition, 'unknown');
});
