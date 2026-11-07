import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { appendFileSync, cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #145. 능력 번들은 플러그인 루트 기준 skills/<name>/… 파일을 가리켜야 한다.
 * 저장소를 건드리지 않도록 생성기와 skills/ 를 임시 폴더로 복사해 그 사본에서 검사한다.
 */

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const GENERATOR = path.join('scripts', 'build-phase-capability-bundle.mjs');
const BUNDLE = (skill) => path.join('skills', skill, 'contracts', 'issue-phase-capability-bundle-v1.json');

function fixture(t) {
  const root = mkdtempSync(path.join(os.tmpdir(), 'capability-bundle-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  cpSync(path.join(repo, GENERATOR), path.join(root, GENERATOR));
  for (const skill of ['issue-start', 'issue-end', 'issue-merge']) {
    cpSync(path.join(repo, 'skills', skill), path.join(root, 'skills', skill), { recursive: true });
  }
  return root;
}

function run(root, ...args) {
  return spawnSync(process.execPath, [path.join(root, GENERATOR), ...args], { encoding: 'utf8' });
}

test('저장소의 번들 세 사본은 실제 파일과 일치한다', () => {
  const r = run(repo, '--check');
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /closure: (\d+)\/\1 match/);
  assert.match(r.stdout, /capability bundle: 3 copies match/);
});

test('closure 경로는 모두 플러그인 루트의 skills/ 또는 생성기다', () => {
  const bundle = JSON.parse(readFileSync(path.join(repo, BUNDLE('issue-merge')), 'utf8'));
  for (const { path: rel } of bundle.closure.entries) {
    assert.ok(rel.startsWith('skills/issue-') || rel === 'scripts/build-phase-capability-bundle.mjs', rel);
  }
  assert.deepEqual(bundle.mirrors.map((mirror) => mirror.root), ['.', '.']);
});

test('다시 생성해도 같은 바이트가 나온다', (t) => {
  const root = fixture(t);
  const before = readFileSync(path.join(root, BUNDLE('issue-merge')));
  const r = run(root);
  assert.equal(r.status, 0, r.stderr);
  for (const skill of ['issue-start', 'issue-end', 'issue-merge']) {
    assert.ok(readFileSync(path.join(root, BUNDLE(skill))).equals(before), skill);
  }
});

test('스킬 파일만 바꾸고 번들을 안 바꾸면 검사가 실패한다', (t) => {
  const root = fixture(t);
  appendFileSync(path.join(root, 'skills', 'issue-merge', 'SKILL.md'), '\n<!-- drift -->\n');
  const r = run(root, '--check');
  assert.equal(r.status, 1);
  assert.match(r.stderr, /closure drift: skills\/issue-merge\/SKILL\.md/);
});

test('scripts/ 하위 폴더에 스크립트를 추가해도 검사가 실패한다', (t) => {
  const root = fixture(t);
  mkdirSync(path.join(root, 'skills', 'issue-start', 'scripts', 'lib'));
  writeFileSync(path.join(root, 'skills', 'issue-start', 'scripts', 'lib', 'helper.mjs'), 'export {};\n');
  const r = run(root, '--check');
  assert.equal(r.status, 1);
  assert.match(r.stderr, /closure missing: skills\/issue-start\/scripts\/lib\/helper\.mjs/);
});

test('번들 사본 하나만 달라도 검사가 실패한다', (t) => {
  const root = fixture(t);
  const file = path.join(root, BUNDLE('issue-end'));
  writeFileSync(file, `${readFileSync(file, 'utf8')}\n`);
  const r = run(root, '--check');
  assert.equal(r.status, 1);
  assert.match(r.stderr, /stale bundle: skills\/issue-end\//);
});

test('closure 대상 파일이 없으면 검사가 실패한다', (t) => {
  const root = fixture(t);
  rmSync(path.join(root, 'skills', 'issue-start', 'contracts', 'issue-start-phase-api-v1.json'));
  const r = run(root, '--check');
  assert.equal(r.status, 1);
  assert.match(r.stderr, /closure files missing: skills\/issue-start\/contracts\/issue-start-phase-api-v1\.json/);
});
