import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #154. 도움말 요청이나 모르는 옵션에는 merge·close·cleanup·resolve 가
 * 아무 동작도 하지 않는지, gh 를 가짜로 바꿔 호출 기록으로 확인한다.
 */
const SCRIPT = path.join(path.dirname(fileURLToPath(import.meta.url)), 'issue-merge.mjs');

function sh(cwd, cmd, ...args) {
  const r = spawnSync(cmd, args, { cwd, encoding: 'utf8' });
  assert.equal(r.status, 0, `${cmd} ${args.join(' ')}: ${r.stderr}`);
}

function setup() {
  const tmp = mkdtempSync(path.join(tmpdir(), 'issue-merge-args-'));
  const bin = path.join(tmp, 'bin');
  const repo = path.join(tmp, 'repo');
  const log = path.join(tmp, 'gh.log');
  mkdirSync(bin);
  mkdirSync(repo);
  // 상태 변경 호출까지 성공으로 응답한다. 검사가 없으면 실제로 merge 가 "성공"하는 상황을 만든다.
  writeFileSync(path.join(bin, 'gh'), `#!/bin/sh
echo "$*" >> "$GH_LOG"
case "$1 $2" in
  "pr view") echo '{"headRefName":"fix/151-x"}';;
esac
exit 0
`);
  chmodSync(path.join(bin, 'gh'), 0o755);

  sh(repo, 'git', 'init', '-q', '-b', 'main');
  mkdirSync(path.join(repo, '.issue/151/evidence'), { recursive: true });
  writeFileSync(path.join(repo, '.issue/151/evidence/comment.md'), 'report\n');
  sh(repo, 'git', 'add', '-f', '.issue');
  sh(repo, 'git', '-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', 'base');

  const env = { ...process.env, HOME: tmp, GH_LOG: log, PATH: `${bin}${path.delimiter}${process.env.PATH}` };
  const runCli = (...args) => {
    rmSync(log, { force: true });
    const r = spawnSync('node', [SCRIPT, ...args], { cwd: repo, env, encoding: 'utf8' });
    const calls = existsSync(log) ? readFileSync(log, 'utf8').split('\n').filter(Boolean) : [];
    return { code: r.status, stdout: r.stdout, stderr: r.stderr, calls };
  };
  return { tmp, repo, runCli, cleanup: () => rmSync(tmp, { recursive: true, force: true }) };
}

test('도움말 요청은 사용법만 출력하고 gh 를 부르지 않는다', () => {
  const t = setup();
  try {
    for (const args of [
      ['merge', '--pr', '151', '--help'],
      ['merge', '--pr', '151', '-h'],
      ['merge', '--help', '--pr', '151'],
      ['close', '--issue', '1', '--help'],
      ['cleanup', '--worktree', t.repo, '--help'],
      ['resolve', '--worktree', t.repo, '--continue', '--push', '--help'],
    ]) {
      const r = t.runCli(...args);
      assert.equal(r.code, 0, args.join(' '));
      assert.match(r.stdout, /^Usage: node issue-merge\.mjs/, args.join(' '));
      assert.deepEqual(r.calls, [], args.join(' '));
    }
  } finally {
    t.cleanup();
  }
});

test('모르는 옵션은 exit 1 로 끝나고 gh 를 부르지 않는다', () => {
  const t = setup();
  try {
    for (const args of [
      ['merge', '--pr', '151', '--bogus', 'x'],
      ['merge', '--pr', '151', '--dry-run'],
      ['close', '--issue', '1', '--bogus'],
      ['cleanup', '--worktree', t.repo, '--bogus', 'x'],
      ['resolve', '--worktree', t.repo, '--continue', '--push', '--bogus'],
    ]) {
      const r = t.runCli(...args);
      assert.equal(r.code, 1, args.join(' '));
      assert.match(r.stderr, /받지 않는 옵션입니다: --(bogus|dry-run)/, args.join(' '));
      assert.deepEqual(r.calls, [], args.join(' '));
    }
    // cleanup 이 실행되지 않았으므로 저장소가 그대로 남아 있다.
    assert.ok(existsSync(t.repo));
  } finally {
    t.cleanup();
  }
});

test('허용된 옵션만 쓰면 merge 는 그대로 실행된다', () => {
  const t = setup();
  try {
    const r = t.runCli('merge', '--pr', '151', '--method', 'squash');
    assert.equal(r.code, 0, r.stderr);
    assert.equal(JSON.parse(r.stdout).merged, true);
    assert.ok(r.calls.includes('pr merge 151 --squash'));
  } finally {
    t.cleanup();
  }
});
