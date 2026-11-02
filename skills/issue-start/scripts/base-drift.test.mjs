import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #90. 기록된 기준 브랜치가 실제 작업 흐름과 어긋나면 워크트리 생성·미러 push 전에 멈춘다.
 * 임시 원격·주 체크아웃을 만들고, PATH 앞에 가짜 gh 를 두어 최근 PR base 를 흉내 낸다.
 */

const here = path.dirname(fileURLToPath(import.meta.url));
const script = path.join(here, 'issue-start.mjs');

function fixture(t, { checkout, recorded, prBase }) {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'base-drift-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const home = path.join(dir, 'home');
  mkdirSync(path.join(home, '.issue'), { recursive: true });
  writeFileSync(path.join(home, '.issue', 'settings.json'), '{"worktree":{"layout":"sibling"}}');
  const bin = path.join(dir, 'bin');
  mkdirSync(bin);
  const gh = path.join(bin, 'gh');
  const prs = JSON.stringify([prBase, prBase, 'main'].map((b) => ({ baseRefName: b })));
  writeFileSync(gh, `#!/bin/sh\n[ "$1 $2" = "pr list" ] && { echo '${prs}'; exit 0; }\nexit 1\n`);
  chmodSync(gh, 0o755);
  const env = {
    ...process.env,
    HOME: home,
    PATH: `${bin}${path.delimiter}${process.env.PATH}`,
    GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@t', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@t',
  };
  const sh = (args, cwd) => {
    const r = spawnSync('git', args, { cwd, env, encoding: 'utf8' });
    assert.equal(r.status, 0, `git ${args.join(' ')}: ${r.stderr}`);
  };
  const remote = path.join(dir, 'remote.git');
  const repo = path.join(dir, 'repo');
  sh(['init', '--bare', '-b', 'main', remote], dir);
  sh(['clone', remote, repo], dir);
  writeFileSync(path.join(repo, '.gitignore'), '.issue/**\n!.issue/*/\n!.issue/*/evidence/\n!.issue/*/evidence/**\n');
  sh(['add', '.gitignore'], repo);
  sh(['commit', '-m', 'init'], repo);
  sh(['push', 'origin', 'main'], repo);
  sh(['checkout', '-b', 'dev/x'], repo);
  sh(['push', 'origin', 'dev/x'], repo);
  sh(['checkout', checkout], repo);
  sh(['remote', 'set-head', 'origin', 'main'], repo);
  mkdirSync(path.join(repo, '.issue'));
  writeFileSync(path.join(repo, '.issue', 'settings.json'), JSON.stringify({ git: { baseBranch: recorded } }));
  const run = (args, cwd = repo) => spawnSync(process.execPath, [script, ...args], { cwd, env, encoding: 'utf8' });
  const settings = () => JSON.parse(readFileSync(path.join(repo, '.issue', 'settings.json'), 'utf8'));
  return { dir, repo, env, sh, run, settings };
}

test('기록 main, 주 체크아웃·최근 PR base 가 dev/x 면 워크트리를 만들기 전에 exit 8 로 멈춘다', (t) => {
  const f = fixture(t, { checkout: 'dev/x', recorded: 'main', prBase: 'dev/x' });
  const r = f.run(['worktree', '1', '--slug', 't', '--no-status']);
  assert.equal(r.status, 8, r.stderr);
  assert.match(r.stdout, /BASE_DRIFT=1/);
  assert.match(r.stdout, /BASE_RECORDED=main/);
  assert.match(r.stdout, /BASE_CANDIDATE=dev\/x/);
  assert.equal(existsSync(path.join(f.dir, 'repo-issue-1')), false);
});

test('base --set 으로 고른 브랜치를 기록하면 이후 묻지 않고 그 브랜치 기준으로 만든다', (t) => {
  const f = fixture(t, { checkout: 'dev/x', recorded: 'main', prBase: 'dev/x' });
  const set = f.run(['base', '--set', 'dev/x']);
  assert.equal(set.status, 0, set.stderr);
  assert.match(set.stdout, /BASE=dev\/x/);
  assert.equal(f.settings().git.baseBranch, 'dev/x');
  const r = f.run(['worktree', '2', '--slug', 't', '--no-status']);
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /origin\/dev\/x 기준/);
});

test('기록값 유지를 고르면 같은 후보로는 다시 묻지 않는다', (t) => {
  const f = fixture(t, { checkout: 'dev/x', recorded: 'main', prBase: 'dev/x' });
  assert.equal(f.run(['base', '--set', 'main']).status, 0);
  assert.equal(f.settings().git.baseDriftAck, 'dev/x');
  const r = f.run(['worktree', '3', '--slug', 't', '--no-status']);
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /origin\/main 기준/);
});

test('--base 를 명시하면 검사하지 않는다', (t) => {
  const f = fixture(t, { checkout: 'dev/x', recorded: 'main', prBase: 'dev/x' });
  const r = f.run(['worktree', '4', '--slug', 't', '--no-status', '--base', 'main']);
  assert.equal(r.status, 0, r.stderr);
});

test('기록값과 신호가 일치하면 동작이 바뀌지 않는다', (t) => {
  const f = fixture(t, { checkout: 'main', recorded: 'main', prBase: 'main' });
  const r = f.run(['worktree', '5', '--slug', 't', '--no-status']);
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /origin\/main 기준/);
  assert.doesNotMatch(r.stdout, /BASE_DRIFT/);
});

test('신호가 하나만 다르면 멈추지 않는다', (t) => {
  const f = fixture(t, { checkout: 'dev/x', recorded: 'main', prBase: 'main' });
  const r = f.run(['worktree', '6', '--slug', 't', '--no-status']);
  assert.equal(r.status, 0, r.stderr);
});

test('링크된 워크트리 안에서도 주 체크아웃의 기록을 읽고, 워크트리에 따로 기록하지 않는다', async (t) => {
  const f = fixture(t, { checkout: 'dev/x', recorded: 'dev/x', prBase: 'dev/x' });
  const wt = path.join(f.dir, 'wt');
  f.sh(['worktree', 'add', '-b', 'fix/7-t', wt, 'origin/dev/x'], f.repo);
  const r = spawnSync(process.execPath, ['-e', `import(${JSON.stringify(path.join(here, 'issue-common.mjs'))})
    .then((m) => console.log(m.detectBase(process.cwd())))`], { cwd: wt, env: f.env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.stdout.trim(), 'dev/x');
  assert.equal(existsSync(path.join(wt, '.issue', 'settings.json')), false);
});

test('어긋난 상태에서 evidence-mirror --push 는 push 전에 멈춘다', (t) => {
  const f = fixture(t, { checkout: 'dev/x', recorded: 'main', prBase: 'dev/x' });
  const ev = path.join(f.repo, '.issue', '8', 'evidence', 'after');
  mkdirSync(ev, { recursive: true });
  writeFileSync(path.join(ev, 'x.txt'), 'x');
  const r = f.run(['evidence-mirror', '8', '--push']);
  assert.equal(r.status, 8, r.stderr);
  assert.match(r.stdout, /BASE_DRIFT=1/);
  const log = spawnSync('git', ['log', '--oneline', 'main'], { cwd: path.join(f.dir, 'remote.git'), encoding: 'utf8' });
  assert.equal(log.status, 0, log.stderr);
  assert.doesNotMatch(log.stdout, /증거 자료/);
});

test('예전 워크트리에 남은 main 기록 파일은 무시하고 주 체크아웃 기록을 쓴다 (#64·#89 사례)', (t) => {
  const f = fixture(t, { checkout: 'dev/x', recorded: 'dev/x', prBase: 'dev/x' });
  const wt = path.join(f.dir, 'wt-old');
  f.sh(['worktree', 'add', '-b', 'fix/9-t', wt, 'origin/dev/x'], f.repo);
  mkdirSync(path.join(wt, '.issue'), { recursive: true });
  writeFileSync(path.join(wt, '.issue', 'settings.json'), JSON.stringify({ git: { baseBranch: 'main' } }));
  const r = spawnSync(process.execPath, ['-e', `import(${JSON.stringify(path.join(here, 'issue-common.mjs'))})
    .then((m) => console.log(m.detectBase(process.cwd())))`], { cwd: wt, env: f.env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.stdout.trim(), 'dev/x');
});
