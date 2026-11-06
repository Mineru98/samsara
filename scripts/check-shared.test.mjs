import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #56. check-shared.sh 는 사본 네 벌을 비교한다. 저장소 파일을 건드리지 않도록
 * 스크립트와 사본만 임시 폴더로 복사하고, 그 복사본을 변조해 검출 여부를 본다.
 */

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SKILLS = ['create', 'end', 'merge', 'start'];

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

test('현재 사본은 공유 블록 검사를 통과한다', (t) => {
  const r = check(fixture(t));
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /shared block: 4 copies match/);
});

test('공유 블록이 한 사본에서만 달라지면 drift 로 실패한다', (t) => {
  const root = fixture(t);
  edit(root, 'start', 'export function resolveStatus(raw) {', 'export function resolveStatus(raw) {\n  // drift');
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /shared block drift: skills\/issue-start/);
});

test('공유 블록 시작 표식이 없으면 not found 로 실패한다', (t) => {
  const root = fixture(t);
  edit(root, 'merge', 'export function resolveStatus(raw) {', 'export function resolveStatusRenamed(raw) {');
  const r = check(root);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /shared block not found: skills\/issue-merge/);
});
