import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, rmSync, unlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  VERSION_SOURCES,
  stripTrailingReference,
  bumpVersion,
  collectVersionState,
  driftDirection,
  extractReferences,
  formatVersion,
  groupCommits,
  parseCommitSubject,
  parseSemver,
  pickLatestTag,
  renderNotes,
  renderSourceVersion,
  replaceVersionText,
  writeSourceVersion,
} from './issue-version.mjs';

const SCRIPT = fileURLToPath(new URL('./issue-version.mjs', import.meta.url));

function run(cwd, args, env) {
  return spawnSync(process.execPath, [SCRIPT, ...args], {
    cwd,
    encoding: 'utf8',
    env: env ? { ...process.env, ...env } : process.env,
  });
}

function field(stdout, key) {
  const match = new RegExp(`^${key}=(.*)$`, 'm').exec(stdout);
  return match ? match[1] : null;
}

// gh 를 흉내 내는 임시 실행 파일. auth 는 통과하고 지정한 하위 명령만 실패시킨다.
function fakeGh(failCommand) {
  const dir = mkdtempSync(path.join(tmpdir(), 'fake-gh-'));
  writeFileSync(path.join(dir, 'gh'), [
    '#!/bin/sh',
    `if [ "$1" = "${failCommand.split(' ')[0]}" ] && [ "$2" = "${failCommand.split(' ')[1]}" ]; then`,
    '  echo "gh: forced failure" >&2',
    '  exit 1',
    'fi',
    'exit 0',
  ].join('\n'));
  chmodSync(path.join(dir, 'gh'), 0o755);
  return dir;
}

function seedRepo({ withTags, withOrigin }) {
  const root = mkdtempSync(path.join(tmpdir(), 'issue-version-'));
  const git = (...args) => spawnSync('git', args, { cwd: root, encoding: 'utf8' });
  git('init', '-q', '-b', 'main');
  git('config', 'user.email', 'test@example.com');
  git('config', 'user.name', 'test');
  git('config', 'commit.gpgsign', 'false');

  writeFileSync(path.join(root, 'VERSION'), '0.3.2\n');
  writeFileSync(path.join(root, 'marketplace.json'),
    JSON.stringify({ name: 'x', plugins: [{ name: 'x', version: '0.3.2' }] }, null, 2) + '\n');
  for (const dir of ['.claude-plugin', '.codex-plugin', '.grok-plugin', '.zcode-plugin']) {
    mkdirSync(path.join(root, dir), { recursive: true });
    writeFileSync(path.join(root, dir, 'plugin.json'),
      JSON.stringify({ name: 'x', version: '0.3.2', description: '0.3.2 는 본문에도 나온다' }, null, 2) + '\n');
  }
  writeFileSync(path.join(root, '.claude-plugin', 'marketplace.json'),
    JSON.stringify({ name: 'x', plugins: [{ name: 'x', version: '0.3.2' }] }, null, 2) + '\n');
  mkdirSync(path.join(root, 'tools', 'issue-ontology'), { recursive: true });
  writeFileSync(path.join(root, 'tools', 'issue-ontology', 'package.json'),
    JSON.stringify({ name: 'x', version: '0.3.2', private: true }, null, 2) + '\n');

  git('add', '-A');
  git('commit', '-q', '-m', 'chore: seed');
  if (withTags) {
    git('tag', '-a', 'v0.2.0', '-m', 'v0.2.0');
    git('tag', '-a', 'v0.3.2', '-m', 'v0.3.2');
    writeFileSync(path.join(root, 'note.txt'), 'after tag\n');
    git('add', '-A');
    git('commit', '-q', '-m', 'feat(version): 릴리즈 자동화 추가 (#41)');
    writeFileSync(path.join(root, 'note2.txt'), 'more\n');
    git('add', '-A');
    git('commit', '-q', '-m', 'fix(graph): 캐시 갱신 실패 수정');
  }
  if (withOrigin) {
    const origin = mkdtempSync(path.join(tmpdir(), 'issue-version-origin-'));
    spawnSync('git', ['init', '-q', '--bare', '-b', 'main', origin], { encoding: 'utf8' });
    git('remote', 'add', 'origin', origin);
    git('push', '-q', 'origin', 'main');
    if (withTags) git('push', '-q', 'origin', '--tags');
    git('fetch', '-q', 'origin');
    git('branch', '--set-upstream-to=origin/main', 'main');
  }
  return root;
}

test('semver 를 파싱하고 v 접두사를 허용한다', () => {
  assert.deepEqual(parseSemver('v0.3.2'), { major: 0, minor: 3, patch: 2 });
  assert.deepEqual(parseSemver('0.3.2'), { major: 0, minor: 3, patch: 2 });
  assert.equal(parseSemver('0.3'), null);
  assert.equal(parseSemver('v1.2.3-rc1'), null);
});

test('단계별로 한 자리만 올리고 아래 자리는 0 으로 내린다', () => {
  const base = parseSemver('0.3.2');
  assert.equal(formatVersion(bumpVersion(base, 'patch')), '0.3.3');
  assert.equal(formatVersion(bumpVersion(base, 'minor')), '0.4.0');
  assert.equal(formatVersion(bumpVersion(base, 'major')), '1.0.0');
});

test('태그는 문자열 순서가 아니라 숫자 순서로 고른다', () => {
  const picked = pickLatestTag(['v0.9.0', 'v0.10.0', 'v0.2.1', 'nightly']);
  assert.equal(picked.tag, 'v0.10.0');
  assert.equal(pickLatestTag(['nightly', 'latest']), null);
});

test('conventional commit 제목을 type·scope·설명으로 나눈다', () => {
  assert.deepEqual(parseCommitSubject('feat(version): 릴리즈 자동화 (#41)'), {
    type: 'feat', scope: 'version', breaking: false, description: '릴리즈 자동화',
  });
  assert.equal(parseCommitSubject('feat!: 큰 변경').breaking, true);
  assert.equal(parseCommitSubject('그냥 커밋').type, null);
});

test('제목 끝의 squash PR 번호는 설명에서 떼어내 참조로만 남긴다', () => {
  assert.equal(stripTrailingReference('ZCode 카탈로그 추가 (#32)'), 'ZCode 카탈로그 추가');
  assert.equal(stripTrailingReference('이슈 #41 을 다룬다'), '이슈 #41 을 다룬다');
  assert.equal(stripTrailingReference('(#12)'), '(#12)');
  const entry = groupCommits([{ hash: 'a', subject: 'feat(x): 카탈로그 추가 (#32)', body: 'Closes #29' }])[0].entries[0];
  assert.equal(entry.description, '카탈로그 추가');
  assert.deepEqual(entry.references, ['#32', '#29']);
});

test('이슈·PR 번호를 중복 없이 뽑는다', () => {
  assert.deepEqual(extractReferences('feat: x (#41)\n\nCloses #41, refs #12'), ['#41', '#12']);
});

test('커밋을 type 별 섹션으로 묶고 호환성 파괴를 맨 위로 올린다', () => {
  const groups = groupCommits([
    { hash: 'a'.repeat(40), subject: 'chore: 정리', body: '' },
    { hash: 'b'.repeat(40), subject: 'feat(version): 자동화 (#41)', body: '' },
    { hash: 'c'.repeat(40), subject: 'refactor: 구조 변경', body: 'BREAKING CHANGE: API 변경' },
    { hash: 'd'.repeat(40), subject: '형식 없는 커밋', body: '' },
  ]);
  assert.deepEqual(groups.map((group) => group.label),
    ['💥 호환성이 깨지는 변경', '✨ 기능', '♻️ 리팩터링', '🧹 유지보수', '📌 기타']
      .filter((label) => groups.some((group) => group.label === label)));
  assert.equal(groups[0].label, '💥 호환성이 깨지는 변경');
  assert.deepEqual(groups.find((group) => group.label === '✨ 기능').entries[0].references, ['#41']);
});

test('릴리즈 노트에 이전 태그·커밋 수·compare 링크가 들어간다', () => {
  const body = renderNotes({
    version: 'v0.3.3',
    previousTag: 'v0.3.2',
    commits: [{ hash: 'a'.repeat(40), subject: 'feat(version): 자동화 (#41)', body: '' }],
    slug: 'Mineru98/samsara',
  });
  assert.match(body, /^## v0\.3\.3/);
  assert.match(body, /v0\.3\.2 이후 커밋 1건\./);
  assert.match(body, /### ✨ 기능/);
  assert.match(body, /- \*\*version\*\*: 자동화 \(#41\)$/m);
  assert.match(body, /compare\/v0\.3\.2\.\.\.v0\.3\.3/);
});

test('첫 릴리즈 노트에는 compare 링크가 없다', () => {
  const body = renderNotes({ version: 'v0.1.0', previousTag: null, commits: [], slug: 'o/r' });
  assert.match(body, /최초 릴리즈\. 커밋 0건\./);
  assert.doesNotMatch(body, /compare/);
});

test('JSON 은 version 필드만 바꾸고 다른 같은 숫자는 남긴다', () => {
  const raw = JSON.stringify({ version: '0.3.2', description: '0.3.2 는 본문에도 나온다' }, null, 2) + '\n';
  const rendered = renderSourceVersion({ file: 'x.json', kind: 'json', path: ['version'] }, raw, '0.3.3');
  const parsed = JSON.parse(rendered);
  assert.equal(parsed.version, '0.3.3');
  assert.equal(parsed.description, '0.3.2 는 본문에도 나온다');
  assert.ok(rendered.endsWith('\n'));
});

test('배열 안의 모든 plugins[].version 을 갱신한다', () => {
  const raw = JSON.stringify({ plugins: [{ version: '0.3.2' }, { version: '0.3.2' }] }, null, 2) + '\n';
  const rendered = renderSourceVersion({ file: 'm.json', kind: 'json', path: ['plugins', '*', 'version'] }, raw, '1.0.0');
  assert.deepEqual(JSON.parse(rendered).plugins.map((p) => p.version), ['1.0.0', '1.0.0']);
});

test('포맷이 특이한 JSON 은 텍스트 폴백으로 version 만 바꾼다', () => {
  const raw = '{\n\t"version": "0.3.2",\n\t"note": "0.3.2"\n}\n';
  const rendered = renderSourceVersion({ file: 'odd.json', kind: 'json', path: ['version'] }, raw, '0.4.0');
  assert.equal(rendered, '{\n\t"version": "0.4.0",\n\t"note": "0.3.2"\n}\n');
  assert.throws(() => replaceVersionText('{}', ['9.9.9'], '1.0.0'), /찾지 못했다/);
});

test('VERSION 텍스트 파일은 개행을 보존한다', () => {
  assert.equal(renderSourceVersion({ file: 'VERSION', kind: 'text' }, '0.3.2\n', '0.3.3'), '0.3.3\n');
  assert.equal(renderSourceVersion({ file: 'VERSION', kind: 'text' }, '0.3.2', '0.3.3'), '0.3.3');
});


test('drift 는 방향으로 구분한다 — 파일이 앞서면 사고가 아니라 릴리즈할 상태다', () => {
  assert.equal(driftDirection('0.3.2', ['0.3.2']), 'none');
  assert.equal(driftDirection('0.3.2', ['0.3.3']), 'files-ahead');
  assert.equal(driftDirection('0.3.3', ['0.3.2']), 'tag-ahead');
  assert.equal(driftDirection('0.3.2', ['0.3.2', '0.3.3']), 'files-inconsistent');
  assert.equal(driftDirection(null, ['0.3.2']), 'no-tag');
  assert.equal(driftDirection('0.3.2', []), 'files-missing');
});

test('파일이 태그보다 앞서면 RELEASE_READY=1 이 나온다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  assert.equal(field(run(root, ['current']).stdout, 'RELEASE_READY'), '0');
  run(root, ['bump', 'patch']);
  const after = run(root, ['current']);
  assert.equal(field(after.stdout, 'DRIFT_DIRECTION'), 'files-ahead');
  assert.equal(field(after.stdout, 'RELEASE_READY'), '1');
});

test('파일이 앞선 상태의 bump 는 release 로 가라고 알려 준다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  run(root, ['bump', 'patch']);
  const blocked = run(root, ['bump', 'patch']);
  assert.equal(blocked.status, 3);
  assert.match(blocked.stderr, /release 로 이어갈 상태/);
});

test('버전 소스 파일이 사라지면 drift 가 0 이어도 release 를 막는다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  run(root, ['bump', 'patch']);
  spawnSync('git', ['commit', '-qam', 'chore(release): bump'], { cwd: root, encoding: 'utf8' });
  unlinkSync(path.join(root, '.grok-plugin', 'plugin.json'));
  spawnSync('git', ['commit', '-qam', 'chore: drop manifest'], { cwd: root, encoding: 'utf8' });

  const current = run(root, ['current']);
  assert.equal(field(current.stdout, 'VERSION_DRIFT'), '1');
  assert.notEqual(field(current.stdout, 'SOURCE_PROBLEMS'), '0');
  assert.equal(field(current.stdout, 'RELEASE_READY'), '0');

  const blocked = run(root, ['release', 'v0.3.3', '--dry-run']);
  assert.equal(blocked.status, 3);
  assert.match(blocked.stderr, /온전하지 않다/);
});

test('로컬 기본 브랜치가 origin 보다 앞서 있으면 태그를 달지 않는다', (t) => {
  const root = seedRepo({ withTags: true, withOrigin: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  run(root, ['bump', 'patch']);
  spawnSync('git', ['commit', '-qam', 'chore(release): bump version to v0.3.3'], { cwd: root, encoding: 'utf8' });
  // 커밋만 하고 push 하지 않은 상태 = origin 에 없는 커밋에 태그가 달릴 뻔한 상황
  const blocked = run(root, ['release', 'v0.3.3'], { PATH: `${fakeGh('release create')}:${process.env.PATH}` });
  assert.equal(blocked.status, 3);
  assert.match(blocked.stderr, /origin\/main 와 다르다/);
  assert.equal(spawnSync('git', ['tag', '--list', 'v0.3.3'], { cwd: root, encoding: 'utf8' }).stdout.trim(), '');
});

test('릴리즈 생성이 실패하면 원격·로컬 태그를 되돌린다', (t) => {
  const root = seedRepo({ withTags: true, withOrigin: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  run(root, ['bump', 'patch']);
  spawnSync('git', ['commit', '-qam', 'chore(release): bump version to v0.3.3'], { cwd: root, encoding: 'utf8' });
  spawnSync('git', ['push', '-q', 'origin', 'main'], { cwd: root, encoding: 'utf8' });

  const failed = run(root, ['release', 'v0.3.3'], { PATH: `${fakeGh('release create')}:${process.env.PATH}` });
  assert.equal(failed.status, 3);
  assert.equal(field(failed.stdout, 'RELEASE_ROLLED_BACK'), '1');
  // 로컬에도 원격에도 태그가 남지 않아야 다시 시도할 수 있다
  assert.equal(spawnSync('git', ['tag', '--list', 'v0.3.3'], { cwd: root, encoding: 'utf8' }).stdout.trim(), '');
  const remoteTags = spawnSync('git', ['ls-remote', '--tags', 'origin', 'v0.3.3'], { cwd: root, encoding: 'utf8' });
  assert.equal(remoteTags.stdout.trim(), '');
});

test('되돌린 뒤 같은 버전으로 다시 릴리즈할 수 있다', (t) => {
  const root = seedRepo({ withTags: true, withOrigin: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  run(root, ['bump', 'patch']);
  spawnSync('git', ['commit', '-qam', 'chore(release): bump version to v0.3.3'], { cwd: root, encoding: 'utf8' });
  spawnSync('git', ['push', '-q', 'origin', 'main'], { cwd: root, encoding: 'utf8' });
  run(root, ['release', 'v0.3.3'], { PATH: `${fakeGh('release create')}:${process.env.PATH}` });

  const retried = run(root, ['release', 'v0.3.3'], { PATH: `${fakeGh('nothing fails')}:${process.env.PATH}` });
  assert.equal(retried.status, 0, retried.stderr);
  assert.equal(field(retried.stdout, 'RELEASE_TAG'), 'v0.3.3');
  assert.equal(
    spawnSync('git', ['tag', '--list', 'v0.3.3'], { cwd: root, encoding: 'utf8' }).stdout.trim(),
    'v0.3.3',
  );
});

test('폴백 치환 개수가 기대와 다르면 파일을 쓰지 않고 던진다', () => {
  // 경로 밖에 같은 값의 "version" 이 하나 더 있는 특수 포맷 파일
  const raw = '{\n\t"version": "0.3.2",\n\t"nested": { "version": "0.3.2" }\n}\n';
  assert.throws(
    () => renderSourceVersion({ file: 'odd.json', kind: 'json', path: ['version'] }, raw, '0.4.0'),
    /치환 개수가 다르다: 기대 1건, 실제 2건/,
  );
});

test('current 는 태그와 8개 파일 버전을 함께 보고한다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const result = run(root, ['current']);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(field(result.stdout, 'LATEST_TAG'), 'v0.3.2');
  assert.equal(field(result.stdout, 'FILE_VERSIONS'), '0.3.2');
  assert.equal(field(result.stdout, 'VERSION_DRIFT'), '0');
  assert.equal(field(result.stdout, 'SOURCE_PROBLEMS'), '0');
});

test('plan 은 파일을 건드리지 않고 세 단계를 계산한다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const [level, expected] of [['patch', '0.3.3'], ['minor', '0.4.0'], ['major', '1.0.0']]) {
    const result = run(root, ['plan', level]);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(field(result.stdout, 'NEXT_VERSION'), expected);
    assert.equal(field(result.stdout, 'NEXT_TAG'), `v${expected}`);
  }
  assert.equal(readFileSync(path.join(root, 'VERSION'), 'utf8'), '0.3.2\n');
});

test('태그가 하나도 없으면 단계와 무관하게 v0.1.0 에서 시작한다', (t) => {
  const root = seedRepo({ withTags: false });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(path.join(root, 'VERSION'), '\n');
  for (const level of ['patch', 'minor', 'major']) {
    const result = run(root, ['plan', level]);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(field(result.stdout, 'NEXT_VERSION'), '0.1.0');
    assert.equal(field(result.stdout, 'FIRST_RELEASE'), '1');
  }
});

test('bump --dry-run 은 아무 파일도 바꾸지 않는다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const result = run(root, ['bump', 'patch', '--dry-run']);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(field(result.stdout, 'DRY_RUN'), '1');
  assert.equal(field(result.stdout, 'CHANGED_FILES'), '');
  const state = collectVersionState(root);
  assert.deepEqual(state.values, ['0.3.2']);
});

test('bump 는 8개 파일을 모두 새 버전으로 맞춘다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const result = run(root, ['bump', 'minor']);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(field(result.stdout, 'NEXT_VERSION'), '0.4.0');
  const state = collectVersionState(root);
  assert.deepEqual(state.values, ['0.4.0']);
  assert.equal(state.problems.length, 0);
  assert.equal(state.sources.length, VERSION_SOURCES.length);
  for (const source of VERSION_SOURCES.filter((entry) => entry.kind === 'json')) {
    JSON.parse(readFileSync(path.join(root, source.file), 'utf8'));
  }
});

test('태그와 파일 버전이 어긋나면 bump 를 막고 --force 로만 통과시킨다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeSourceVersion(root, VERSION_SOURCES[0], '9.9.9');
  const blocked = run(root, ['bump', 'patch']);
  assert.equal(blocked.status, 3);
  assert.match(blocked.stderr, /어긋난다/);
  const forced = run(root, ['bump', 'patch', '--force']);
  assert.equal(forced.status, 0, forced.stderr);
  assert.equal(field(forced.stdout, 'NEXT_VERSION'), '0.3.3');
});

test('알 수 없는 단계는 거부한다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  assert.equal(run(root, ['plan', 'huge']).status, 2);
  assert.equal(run(root, ['bump', 'huge']).status, 2);
});

test('notes 는 직전 태그 이후 커밋만 type 별로 묶는다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const result = run(root, ['notes', '--version', 'v0.3.3']);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(field(result.stdout, 'NOTES_FROM'), 'v0.3.2');
  assert.equal(field(result.stdout, 'NOTES_COMMITS'), '2');
  assert.match(result.stdout, /### ✨ 기능/);
  assert.match(result.stdout, /### 🐛 버그 수정/);
  assert.match(result.stdout, /\(#41\)/);
  assert.doesNotMatch(result.stdout, /chore: seed/);
});

test('notes --out 은 파일로 저장한다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const result = run(root, ['notes', '--version', 'v0.3.3', '--out', 'NOTES.md']);
  assert.equal(result.status, 0, result.stderr);
  const saved = readFileSync(path.join(root, 'NOTES.md'), 'utf8');
  assert.match(saved, /^## v0\.3\.3/);
  assert.ok(saved.endsWith('\n'));
});

test('release 는 기본 브랜치의 버전이 안 맞으면 태그를 만들지 않는다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const result = run(root, ['release', 'v0.3.3', '--dry-run']);
  assert.equal(result.status, 3);
  assert.match(result.stderr, /merge 됐는지 확인/);
  assert.equal(spawnSync('git', ['tag', '--list', 'v0.3.3'], { cwd: root, encoding: 'utf8' }).stdout.trim(), '');
});

test('release --dry-run 은 노트만 출력하고 태그를 남기지 않는다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  run(root, ['bump', 'patch']);
  spawnSync('git', ['commit', '-qam', 'chore(release): bump version to v0.3.3'], { cwd: root, encoding: 'utf8' });
  const result = run(root, ['release', 'v0.3.3', '--dry-run']);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(field(result.stdout, 'RELEASE_DRY_RUN'), '1');
  assert.equal(spawnSync('git', ['tag', '--list', 'v0.3.3'], { cwd: root, encoding: 'utf8' }).stdout.trim(), '');
});

test('이미 있는 태그로는 릴리즈하지 않는다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const result = run(root, ['release', 'v0.3.2']);
  assert.equal(result.status, 3);
  assert.match(result.stderr, /이미 있다/);
});

test('pr 은 기본 브랜치에서 실행되면 거부한다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  run(root, ['bump', 'patch']);
  const result = run(root, ['pr', 'v0.3.3', '--dry-run']);
  assert.equal(result.status, 3);
  assert.match(result.stderr, /기본 브랜치/);
});

test('bump --branch 는 release 브랜치를 만들고 pr --dry-run 이 통과한다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bumped = run(root, ['bump', 'patch', '--branch']);
  assert.equal(bumped.status, 0, bumped.stderr);
  assert.equal(
    spawnSync('git', ['branch', '--show-current'], { cwd: root, encoding: 'utf8' }).stdout.trim(),
    'release/v0.3.3',
  );
  const result = run(root, ['pr', 'v0.3.3', '--dry-run']);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(field(result.stdout, 'PR_DRY_RUN'), '1');
});

test('pr 은 버전 소스가 목표 버전과 다르면 거부한다', (t) => {
  const root = seedRepo({ withTags: true });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bumped = run(root, ['bump', 'patch', '--branch']);
  assert.equal(bumped.status, 0, bumped.stderr);
  const result = run(root, ['pr', 'v0.9.0', '--dry-run']);
  assert.equal(result.status, 3);
  assert.match(result.stderr, /bump 를 먼저 돌린다/);
});
