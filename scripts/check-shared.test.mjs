import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #56. check-shared.sh 는 사본 여섯 벌을 비교한다. 저장소 파일을 건드리지 않도록
 * 스크립트와 사본만 임시 폴더로 복사하고, 그 복사본을 변조해 검출 여부를 본다.
 */

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SKILLS = ['create', 'end', 'merge', 'onboard', 'start', 'sync'];

function fixture(t) {
  const root = mkdtempSync(path.join(os.tmpdir(), 'check-shared-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(path.join(root, 'scripts'));
  cpSync(path.join(repo, 'scripts', 'check-shared.sh'), path.join(root, 'scripts', 'check-shared.sh'));
  for (const skill of SKILLS) {
    const rel = path.join('skills', `issue-${skill}`, 'scripts', 'issue-common.mjs');
    mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    cpSync(path.join(repo, rel), path.join(root, rel));
  }
  return root;
}

function common(root, skill) {
  return path.join(root, 'skills', `issue-${skill}`, 'scripts', 'issue-common.mjs');
}

function edit(root, skill, from, to) {
  const file = common(root, skill);
  const before = readFileSync(file, 'utf8');
  assert.ok(before.includes(from), `변조할 문자열이 ${skill} 사본에 있어야 한다: ${from}`);
  writeFileSync(file, before.replace(from, to));
}

function check(root) {
  return spawnSync('sh', [path.join(root, 'scripts', 'check-shared.sh')], { encoding: 'utf8' });
}

test('현재 사본은 두 검사를 모두 통과한다', (t) => {
  const r = check(fixture(t));
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /shared cache-safety block: 6 copies match/);
  assert.match(r.stdout, /git detection code: 2 copies match \(12 declarations\)/);
});

test('한쪽 사본의 Git 후보 경로만 바꾸면 어긋난 선언과 파일을 알린다', (t) => {
  const root = fixture(t);
  edit(root, 'sync', "git: ['/usr/bin/git', ", 'git: [');
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /git detection drift: UNIX_TRUSTED_EXECUTABLE_CANDIDATES/);
  assert.match(r.stderr, /skills\/issue-sync\/scripts\/issue-common\.mjs/);
});

test('Windows 정책 함수 본문이 달라도 잡는다', (t) => {
  const root = fixture(t);
  edit(root, 'onboard', "candidates.git.push(win.join(root, 'bin', 'git.exe'));", '');
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /git detection drift: windowsExecutablePolicy/);
});

test('탐지 그룹 사본에서 선언이 빠지면 missing 으로 실패한다', (t) => {
  const root = fixture(t);
  edit(root, 'sync', 'export function executableMissing(err) {', 'export function executableMissingRenamed(err) {');
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /git detection declaration missing: executableMissing in skills\/issue-sync/);
});

test('executablePolicy 없이 탐지 선언만 들어간 부분 사본을 거부한다', (t) => {
  const root = fixture(t);
  const file = common(root, 'create');
  // safeGraphTarget 구간(파일 끝까지)을 건드리지 않도록 파일 앞쪽에 넣는다.
  writeFileSync(file, `export function trustedExecutable(command) {\n  return command;\n}\n${readFileSync(file, 'utf8')}`);
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /git detection partial copy.*skills\/issue-create/);
});

test('기존 safeGraphTarget 구간 검출은 그대로 동작한다', (t) => {
  const root = fixture(t);
  edit(root, 'start', 'function safeGraphTarget(root) {', 'function safeGraphTarget(root) {\n  // drift');
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /shared cache-safety block drift: skills\/issue-start/);
});
