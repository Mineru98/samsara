import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { affectedPaths } from './issue-start.mjs';

const SCRIPT = fileURLToPath(new URL('./issue-start.mjs', import.meta.url));
const STALE = 'dev/0.4.0-alpha';

function sh(cwd, cmd, args, env) {
  const r = spawnSync(cmd, args, { cwd, env, encoding: 'utf8' });
  return { code: r.status, out: r.stdout, err: r.stderr };
}

/** 원격에 main(skills/herdr-orch 있음)·dev/0.4.0-alpha(없음)가 있는 저장소. */
function fixture(t) {
  const dir = mkdtempSync(path.join(tmpdir(), 'issue-start-base-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const home = path.join(dir, 'home');
  mkdirSync(path.join(home, '.issue'), { recursive: true });
  writeFileSync(path.join(home, '.issue', 'settings.json'), '{"worktree":{"layout":"sibling"}}\n');
  const env = {
    ...process.env, HOME: home,
    GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@t', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@t',
  };
  const repo = path.join(dir, 'repo');
  const g = (...args) => assert.equal(sh(repo, 'git', args, env).code, 0, args.join(' '));
  sh(dir, 'git', ['init', '--bare', '-b', 'main', path.join(dir, 'origin.git')], env);
  sh(dir, 'git', ['init', '-b', 'main', repo], env);
  writeFileSync(path.join(repo, '.gitignore'), '.issue/**\n');
  mkdirSync(path.join(repo, 'skills/issue-start'), { recursive: true });
  writeFileSync(path.join(repo, 'skills/issue-start/a.md'), 'a\n');
  g('add', '-A'); g('commit', '-m', 'init');
  g('remote', 'add', 'origin', path.join(dir, 'origin.git'));
  g('push', 'origin', 'main'); g('push', 'origin', `main:${STALE}`);
  mkdirSync(path.join(repo, 'skills/herdr-orch'));
  writeFileSync(path.join(repo, 'skills/herdr-orch/SKILL.md'), 'x\n');
  g('add', '-A'); g('commit', '-m', 'herdr'); g('push', 'origin', 'main');
  g('fetch', 'origin'); g('remote', 'set-head', 'origin', 'main');

  mkdirSync(path.join(repo, '.issue/129'), { recursive: true });
  writeFileSync(path.join(repo, '.issue/129/issue.json'), JSON.stringify({
    number: 129,
    labels: [{ name: 'bug' }],
    body: '## 영향 범위 (추정)\n\n- `skills/herdr-orch/SKILL.md` — 탭 감시\n- `skills/herdr-orch/new.mjs` (신규)\n',
  }));
  const setBody = (n, body) => {
    mkdirSync(path.join(repo, `.issue/${n}`), { recursive: true });
    writeFileSync(path.join(repo, `.issue/${n}/issue.json`), JSON.stringify({ number: n, labels: [], body }));
  };
  const settings = path.join(repo, '.issue/settings.json');
  const setBase = (b) => writeFileSync(settings, JSON.stringify({ git: { baseBranch: b, detectedAt: '2026-09-28T00:00:00.000Z' } }));
  const run = (...args) => sh(repo, process.execPath, [SCRIPT, ...args], env);
  return { dir, repo, settings, setBase, setBody, run };
}

test('affectedPaths 는 영향 범위 섹션의 경로만 뽑는다', () => {
  const body = [
    '## 배경', '`skills/other/x.md` 는 섹션 밖', '',
    '## 영향 범위 (추정)',
    '- `skills/a/b.mjs:12` — 줄 번호', '- `./docs/c.md`', '- `skills/d/` 디렉터리',
    '- `base --set` 명령, `https://x.y/z`, `src/**/*.ts`, `noslash`',
    '### 하위 제목', '- `skills/e.mjs#L10`, `skills/f.mjs:10:5`, `skills/g.mjs::fn`',
    '- `skills/{a,b}/x.mjs`, `~/.issue/settings.json`, `/etc/hosts`, `--after/--before`',
    '## 참고', '- `skills/after/x.md`',
  ].join('\n');
  assert.deepEqual(affectedPaths(body), [
    'skills/a/b.mjs', 'docs/c.md', 'skills/d', 'skills/e.mjs', 'skills/f.mjs', 'skills/g.mjs',
  ]);
  assert.deepEqual(affectedPaths('## 영향범위\n- `a/b`'), ['a/b']);
  assert.deepEqual(affectedPaths('## 영향 범위\n```sh\n# 주석\n```\n- `src/b.ts`'), ['src/b.ts']);
  assert.deepEqual(affectedPaths('본문에 섹션 없음'), []);
});

test('낡은 기준 브랜치에 영향 경로가 없으면 워크트리를 만들기 전에 멈춘다', (t) => {
  const f = fixture(t);
  f.setBase(STALE);
  const r = f.run('worktree', '129', '--slug', 'herdr-tab', '--no-status');
  assert.equal(r.code, 5, r.err);
  assert.match(r.out, /BASE_PATHS_MISSING=skills\/herdr-orch\/SKILL\.md/);
  assert.match(r.out, /BASE_CANDIDATE=main/);
  assert.equal(existsSync(path.join(f.dir, 'repo-issue-129')), false);
  assert.notEqual(sh(f.repo, 'git', ['show-ref', '--verify', '--quiet', 'refs/heads/fix/129-herdr-tab']).code, 0);

  const allowed = f.run('worktree', '129', '--slug', 'herdr-tab', '--no-status', '--allow-missing-paths');
  assert.equal(allowed.code, 0, allowed.err);
});

test('origin/HEAD 가 없어도 main 으로 폴백해 낡은 기준을 잡는다', (t) => {
  const f = fixture(t);
  assert.equal(spawnSync('git', ['remote', 'set-head', 'origin', '-d'], { cwd: f.repo }).status, 0);
  f.setBase(STALE);
  const r = f.run('worktree', '129', '--slug', 'herdr-tab', '--no-status');
  assert.equal(r.code, 5, r.err);
  assert.match(r.out, /BASE_CANDIDATE=main/);
});

test('어느 브랜치에도 없는 경로(새 파일·상대 경로)만 빠지면 막지 않는다', (t) => {
  const f = fixture(t);
  f.setBase(STALE);
  f.setBody(7, '## 영향 범위\n- `scripts/new-check.mjs` (신규)\n- `references/worktree.md`\n- `skills/issue-start/a.md`\n');
  const stale = f.run('worktree', '7', '--slug', 'x', '--no-status');
  assert.equal(stale.code, 0, stale.err);

  f.setBase('main');
  f.setBody(8, '## 영향 범위\n- `skills/herdr-orch/SKILL.md`\n- `skills/herdr-orch/new.mjs` (신규)\n');
  const ok = f.run('worktree', '8', '--slug', 'y', '--no-status');
  assert.equal(ok.code, 0, ok.err);
});

test('base --set 으로 기준을 고치면 바로 워크트리가 만들어진다', (t) => {
  const f = fixture(t);
  f.setBase(STALE);
  const set = f.run('base', '--set', 'main');
  assert.equal(set.code, 0, set.err);
  assert.match(set.out, /^BASE=main$/m);
  assert.match(set.out, /^SOURCE=project$/m);
  assert.equal(JSON.parse(readFileSync(f.settings, 'utf8')).git.baseBranch, 'main');

  const r = f.run('worktree', '129', '--slug', 'herdr-tab', '--no-status');
  assert.equal(r.code, 0, r.err);
  assert.ok(existsSync(path.join(f.dir, 'repo-issue-129/skills/herdr-orch/SKILL.md')));

  const bad = f.run('base', '--set', 'no-such-branch');
  assert.notEqual(bad.code, 0);
  assert.notEqual(f.run('base', '--set').code, 0);
  assert.notEqual(f.run('base', '--set', '--upload-pack=touch pwned').code, 0);
  assert.notEqual(f.run('base', '--default').code, 0);
  assert.equal(JSON.parse(readFileSync(f.settings, 'utf8')).git.baseBranch, 'main');
});

test('base 는 기록이 없으면 원격에서 판별해 SOURCE=detected 로 남긴다', (t) => {
  const f = fixture(t);
  const r = f.run('base');
  assert.equal(r.code, 0, r.err);
  assert.match(r.out, /^BASE=main$/m);
  assert.match(r.out, /^SOURCE=detected$/m);
  assert.match(f.run('base').out, /^SOURCE=project$/m);
});

test('base 는 .issue 가 무시 대상이 아니어서 기록을 못 해도 SOURCE=detected 다', (t) => {
  const f = fixture(t);
  rmSync(path.join(f.repo, '.gitignore'));
  const r = f.run('base');
  assert.equal(r.code, 0, r.err);
  assert.match(r.out, /^SOURCE=detected$/m);
  assert.doesNotMatch(r.out, /DEFAULT_BASE_UNSET/);
});
