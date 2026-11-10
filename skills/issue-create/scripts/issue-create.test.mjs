import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { detectCurrentVersion, natureLabels, parseSemver, versionLabelFor } from './issue-create.mjs';

const SCRIPT = fileURLToPath(new URL('./issue-create.mjs', import.meta.url));

function sh(cwd, cmd, args) {
  const r = spawnSync(cmd, args, { cwd, encoding: 'utf8' });
  return { code: r.status, out: r.stdout, err: r.stderr };
}

/** 커밋 하나짜리 저장소. files 는 { 경로: 내용 }, tags 는 그 커밋에 달 태그. */
function fixture(t, { files = {}, tags = [] } = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'issue-create-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  sh(dir, 'git', ['init', '-q']);
  writeFileSync(path.join(dir, 'README.md'), 'x\n');
  for (const [file, content] of Object.entries(files)) writeFileSync(path.join(dir, file), content);
  sh(dir, 'git', ['add', '-A']);
  sh(dir, 'git', ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-q', '-m', 'init']);
  for (const tag of tags) sh(dir, 'git', ['tag', tag]);
  return dir;
}

test('parseSemver 는 v 접두와 세 자리 숫자만 받는다', () => {
  assert.deepEqual(parseSemver('v0.3.8'), [0, 3, 8]);
  assert.deepEqual(parseSemver('1.20.3\n'), [1, 20, 3]);
  assert.equal(parseSemver('0.4.0-alpha'), null);
  assert.equal(parseSemver('latest'), null);
});

test('versionLabelFor 는 기본이 패치 올림이다', () => {
  assert.equal(versionLabelFor('0.3.8'), 'version:0.3.9');
  assert.equal(versionLabelFor('0.3.9', 'patch'), 'version:0.3.10');
  assert.equal(versionLabelFor('0.3.8', 'minor'), 'version:0.4.0');
  assert.equal(versionLabelFor('0.3.8', 'major'), 'version:1.0.0');
  assert.equal(versionLabelFor('0.3.8', 'huge'), null);
  assert.equal(versionLabelFor('not-a-version'), null);
});

test('natureLabels 는 status 와 version 라벨을 성격 라벨로 세지 않는다', () => {
  assert.deepEqual(natureLabels(['bug', 'status:open', 'version:0.3.9']), ['bug']);
  assert.deepEqual(natureLabels(['version:0.3.9']), []);
});

test('detectCurrentVersion 은 태그와 버전 파일 중 높은 값을 고른다', (t) => {
  const tagOnly = fixture(t, { tags: ['v0.3.7', 'v0.3.8', 'v0.4.0-alpha'] });
  assert.deepEqual(detectCurrentVersion(tagOnly), { version: '0.3.8', source: 'tag v0.3.8' });

  const fileAhead = fixture(t, { files: { VERSION: '0.3.9\n' }, tags: ['v0.3.8'] });
  assert.deepEqual(detectCurrentVersion(fileAhead), { version: '0.3.9', source: 'VERSION' });

  const pkg = fixture(t, { files: { 'package.json': '{"name":"x","version":"2.1.0"}\n' } });
  assert.deepEqual(detectCurrentVersion(pkg), { version: '2.1.0', source: 'package.json' });
});

test('버전 소스가 없는 저장소는 버전 라벨을 붙이지 않는다', (t) => {
  const repo = fixture(t);
  assert.equal(detectCurrentVersion(repo), null);
  const r = sh(repo, 'node', [SCRIPT, 'version-label']);
  assert.equal(r.code, 0);
  assert.match(r.out, /^VERSIONED=0$/m);
});

test('version-label 은 패치를 기본으로, 마이너를 후보로 출력한다', (t) => {
  const repo = fixture(t, { tags: ['v0.3.8'] });
  const r = sh(repo, 'node', [SCRIPT, 'version-label']);
  assert.match(r.out, /^CURRENT_VERSION=0\.3\.8$/m);
  assert.match(r.out, /^VERSION_LABEL=version:0\.3\.9$/m);
  assert.match(r.out, /^MINOR_LABEL=version:0\.4\.0$/m);
  assert.doesNotMatch(r.out, /1\.0\.0/);
  assert.match(sh(repo, 'node', [SCRIPT, 'version-label', '--bump', 'major']).out, /^VERSION_LABEL=version:1\.0\.0$/m);
  assert.equal(sh(repo, 'node', [SCRIPT, 'version-label', '--bump', 'huge']).code, 2);
});

test('create --dry-run 은 버전 라벨을 붙이고, 버전 라벨만으로는 성격 라벨 게이트를 못 넘는다', (t) => {
  const repo = fixture(t, { files: { 'body.md': 'b\n' }, tags: ['v0.3.8'] });
  const base = [SCRIPT, 'create', '--dry-run', '--title', 't', '--body-file', 'body.md', '--skip-duplicate-review'];

  const patch = sh(repo, 'node', [...base, '--label', 'bug']);
  assert.equal(patch.code, 0, patch.err);
  assert.match(patch.out, /--label bug --label version:0\.3\.9/);

  assert.match(sh(repo, 'node', [...base, '--label', 'bug', '--bump', 'minor']).out, /^VERSION_LABEL=version:0\.4\.0$/m);
  assert.match(sh(repo, 'node', [...base, '--label', 'bug', '--label', 'version:2.0.0']).out, /^VERSION_LABEL=version:2\.0\.0$/m);
  assert.match(sh(repo, 'node', [...base, '--label', 'bug', '--no-version']).out, /^VERSION_LABEL=$/m);

  assert.equal(sh(repo, 'node', [...base, '--label', 'version:0.3.9']).code, 2);
});
