import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #150. resolve 가 충돌 종류·공통 조상·반대편 커밋을 알려 주고,
 * 삭제가 섞인 충돌을 사람 결정 없이 커밋하지 않는지 임시 저장소로 확인한다.
 */
const SCRIPT = path.join(path.dirname(fileURLToPath(import.meta.url)), 'issue-merge.mjs');

function sh(cwd, cmd, ...args) {
  const r = spawnSync(cmd, args, { cwd, encoding: 'utf8' });
  assert.equal(r.status, 0, `${cmd} ${args.join(' ')}: ${r.stderr}`);
  return r.stdout.trim();
}

function commitAll(cwd, msg) {
  sh(cwd, 'git', 'add', '-A');
  sh(cwd, 'git', '-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', msg);
}

function write(dir, file, text) {
  writeFileSync(path.join(dir, file), text);
}

function resolve(root, ...args) {
  const r = spawnSync('node', [SCRIPT, 'resolve', ...args], { cwd: root, encoding: 'utf8' });
  return { code: r.status, json: JSON.parse(r.stdout), stderr: r.stderr };
}

function setup() {
  const tmp = mkdtempSync(path.join(tmpdir(), 'issue-merge-resolve-'));
  const origin = path.join(tmp, 'origin.git');
  const root = path.join(tmp, 'repo');
  const wt = path.join(tmp, 'repo-issue-1');
  sh(tmp, 'git', 'init', '-q', '--bare', '-b', 'main', origin);
  sh(tmp, 'git', 'clone', '-q', origin, root);
  sh(root, 'git', 'checkout', '-q', '-b', 'main');

  write(root, 'api.js', 'export function fetchUser(id) {\n  return http.get(`/users/${id}`);\n}\n');
  write(root, 'lib.js', 'export const a = 1;\n');
  write(root, 'util.js', 'export const u = 1;\n');
  write(root, 'yarn.lock', 'lock 1\n');
  write(root, 'use.js', 'import { fetchUser } from "./api.js";\nfetchUser(1);\n');
  commitAll(root, 'base');
  sh(root, 'git', 'push', '-q', '-u', 'origin', 'main');
  sh(root, 'git', 'remote', 'set-head', 'origin', 'main');

  sh(root, 'git', 'worktree', 'add', '-q', '-b', 'feat/1-x', wt);
  write(wt, 'api.js', 'export function fetchUser(id, opts) {\n  return http.get(`/users/${id}`, opts);\n}\n');
  rmSync(path.join(wt, 'lib.js'));
  write(wt, 'util.js', 'export const u = 2;\n');
  write(wt, 'new.js', 'export const n = "branch";\n');
  write(wt, 'yarn.lock', 'lock branch\n');
  commitAll(wt, 'feat: opts');

  write(root, 'api.js', 'export function fetchUser(id) {\n  return http.get(`/v2/users/${id}`);\n}\n');
  write(root, 'lib.js', 'export const a = 2;\n');
  rmSync(path.join(root, 'util.js'));
  write(root, 'new.js', 'export const n = "main";\n');
  write(root, 'yarn.lock', 'lock main\n');
  commitAll(root, 'feat(#2): v2 경로');
  sh(root, 'git', 'push', '-q', 'origin', 'main');

  return { tmp, root, wt, cleanup: () => rmSync(tmp, { recursive: true, force: true }) };
}

test('resolve 는 충돌 종류를 stage 로 구분하고 공통 조상과 반대편 커밋을 보여 준다', () => {
  const { root, wt, cleanup } = setup();
  try {
    const { code, json } = resolve(root, '--worktree', wt);
    assert.equal(code, 1);
    const kinds = Object.fromEntries(json.conflicts.map((c) => [c.path, c.kind]));
    assert.deepEqual(kinds, {
      'api.js': 'content',
      'lib.js': 'deleted-by-us',
      'new.js': 'both-added',
      'util.js': 'deleted-by-them',
      'yarn.lock': 'generated',
    });
    assert.match(readFileSync(path.join(wt, 'api.js'), 'utf8'), /^\|\|\|\|\|\|\| /m, '공통 조상 구간이 있다');
    const api = json.conflicts.find((c) => c.path === 'api.js');
    assert.equal(api.hunks.length, 1);
    assert.equal(api.incoming.length, 1);
    assert.match(api.incoming[0], /feat\(#2\): v2 경로$/);
  } finally {
    cleanup();
  }
});

test('--continue 는 삭제형 충돌이 남아 있으면 커밋하지 않는다', () => {
  const { root, wt, cleanup } = setup();
  try {
    resolve(root, '--worktree', wt);
    write(wt, 'api.js', 'export function fetchUser(id, opts) {\n  return http.get(`/v2/users/${id}`, opts);\n}\n');
    write(wt, 'new.js', 'export const n = "both";\n');
    write(wt, 'yarn.lock', 'lock regenerated\n');

    const { code, json } = resolve(root, '--worktree', wt, '--continue');
    assert.equal(code, 1);
    assert.equal(json.committed, false);
    assert.deepEqual(json.remaining.map((f) => f.path).sort(), ['lib.js', 'util.js']);
    assert.ok(existsSync(path.join(wt, '.git')), '워크트리는 그대로다');
  } finally {
    cleanup();
  }
});

test('--continue 는 추적 중인 파일의 수정을 담고 검사 산출물은 커밋하지 않는다', () => {
  const { root, wt, cleanup } = setup();
  try {
    resolve(root, '--worktree', wt);
    write(wt, 'api.js', 'export function fetchUser(id, opts) {\n  return http.get(`/v2/users/${id}`, opts);\n}\n');
    write(wt, 'new.js', 'export const n = "both";\n');
    write(wt, 'yarn.lock', 'lock regenerated\n');
    sh(wt, 'git', 'rm', '-q', 'lib.js');
    sh(wt, 'git', 'add', 'util.js');
    write(wt, 'use.js', 'import { fetchUser } from "./api.js";\nfetchUser(1, {});\n');
    write(wt, 'tsc-output.log', 'scratch from a check run\n');

    const { code, json } = resolve(root, '--worktree', wt, '--continue');
    assert.equal(code, 0, JSON.stringify(json));
    assert.equal(json.committed, true);
    const tracked = sh(wt, 'git', 'ls-files').split('\n');
    assert.ok(!tracked.includes('tsc-output.log'), '검사 산출물은 커밋되지 않는다');
    assert.ok(!tracked.includes('lib.js'), 'git rm 한 결정이 반영된다');
    assert.match(readFileSync(path.join(wt, 'api.js'), 'utf8'), /\/v2\/users\/\$\{id\}`, opts/);
    assert.match(sh(wt, 'git', 'show', 'HEAD:use.js'), /fetchUser\(1, \{\}\)/, '충돌 없는 파일의 의미 충돌 수정도 커밋된다');
    assert.equal(sh(wt, 'git', 'status', '--porcelain'), '?? tsc-output.log');

    const again = resolve(root, '--worktree', wt);
    assert.equal(again.json.started, true, '검사 산출물이 남아 있어도 다시 시작할 수 있다');
  } finally {
    cleanup();
  }
});

test('이름 변경 충돌은 한쪽에만 있는 경로로 분류하고 커밋하지 않는다', () => {
  const tmp = mkdtempSync(path.join(tmpdir(), 'issue-merge-rename-'));
  try {
    const origin = path.join(tmp, 'origin.git');
    const root = path.join(tmp, 'repo');
    const wt = path.join(tmp, 'repo-issue-3');
    sh(tmp, 'git', 'init', '-q', '--bare', '-b', 'main', origin);
    sh(tmp, 'git', 'clone', '-q', origin, root);
    sh(root, 'git', 'checkout', '-q', '-b', 'main');
    write(root, 'old.txt', 'one\ntwo\nthree\n');
    commitAll(root, 'base');
    sh(root, 'git', 'push', '-q', '-u', 'origin', 'main');
    sh(root, 'git', 'remote', 'set-head', 'origin', 'main');
    sh(root, 'git', 'worktree', 'add', '-q', '-b', 'feat/3-x', wt);
    sh(wt, 'git', 'mv', 'old.txt', 'ours.txt');
    commitAll(wt, 'rename ours');
    sh(root, 'git', 'mv', 'old.txt', 'theirs.txt');
    commitAll(root, 'rename theirs');
    sh(root, 'git', 'push', '-q', 'origin', 'main');

    const { json } = resolve(root, '--worktree', wt);
    const kinds = Object.fromEntries(json.conflicts.map((c) => [c.path, c.kind]));
    assert.deepEqual(kinds, { 'old.txt': 'both-deleted', 'ours.txt': 'added-by-us', 'theirs.txt': 'added-by-them' });

    const cont = resolve(root, '--worktree', wt, '--continue');
    assert.equal(cont.code, 1);
    assert.equal(cont.json.committed, false);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});
