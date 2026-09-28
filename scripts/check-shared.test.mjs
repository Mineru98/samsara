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

test('한 사본이 탐지 코드를 통째로 잃으면 그룹 불일치로 실패한다', (t) => {
  const root = fixture(t);
  // #40 을 되돌리는 잘못된 merge: onboard 사본이 탐지 코드 없는 옛 사본이 된다.
  cpSync(common(root, 'create'), common(root, 'onboard'));
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /git detection group mismatch/);
});

test('isPathWithin 처럼 모든 사본에 있는 이름도 탐지 그룹 안에서는 비교한다', (t) => {
  const root = fixture(t);
  edit(root, 'sync', "return relative === '' || (relative !== '..'", "return relative === '' || (relative !== '...'");
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /git detection drift: isPathWithin/);
});

test('이름이 접두어로 겹치는 선언은 부분 사본으로 오인하지 않는다', (t) => {
  const root = fixture(t);
  const file = common(root, 'create');
  writeFileSync(file, `export function executablePolicyNote() {\n  return 1;\n}\n${readFileSync(file, 'utf8')}`);
  const r = check(root);
  assert.equal(r.status, 0, r.stderr);
});

// 닫는 줄에 주석이 붙으면 추출이 다음 선언까지 이어진다. 사본마다 뒤따르는 코드가 달라 drift 로 실패한다.
// 파일 끝까지 닫는 줄이 없는 경우(not closed)는 safeGraphTarget 구간을 깨지 않고는 만들 수 없어 여기서 다루지 않는다.
test('닫는 줄 형식이 바뀌면 통과시키지 않고 실패한다', (t) => {
  const root = fixture(t);
  edit(root, 'sync', "export function trustedExecutable(command) {\n  return resolveTrustedExecutable(command);\n}",
    "export function trustedExecutable(command) {\n  return resolveTrustedExecutable(command);\n} // note");
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /git detection (drift|declaration not closed): trustedExecutable/);
});
