import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #144. merge 는 검사한 head 로만 일어나야 하고, head 변경은 approval 이 아니라
 * head-changed 로 분류돼야 한다. gh 는 argv 를 기록하는 가짜로 바꿔 끼운다.
 */
const SCRIPT = path.join(path.dirname(fileURLToPath(import.meta.url)), 'issue-merge.mjs');
const INSPECTED = 'a'.repeat(40);

const FAKE_GH = `#!/bin/sh
echo "gh $*" >> "$FAKE_GH_LOG"
case "$1 $2" in
  "pr view") printf '{"headRefName":"fix/7-x","headRefOid":"%s"}\\n' "$FAKE_HEAD" ;;
  "pr merge") if [ -n "$FAKE_MERGE_FAIL" ]; then echo "$FAKE_MERGE_FAIL" >&2; exit 1; fi ;;
esac
exit 0
`;

function setup() {
  const tmp = mkdtempSync(path.join(tmpdir(), 'issue-merge-head-'));
  const bin = path.join(tmp, 'bin');
  const root = path.join(tmp, 'repo');
  mkdirSync(bin);
  writeFileSync(path.join(bin, 'gh'), FAKE_GH);
  chmodSync(path.join(bin, 'gh'), 0o755);
  mkdirSync(path.join(root, '.issue', '7', 'evidence'), { recursive: true });
  writeFileSync(path.join(root, '.issue', '7', 'evidence', 'comment.md'), '# report\n');
  spawnSync('git', ['init', '-q', '-b', 'main', root]);
  return { tmp, bin, root, log: path.join(tmp, 'gh.log'), cleanup: () => rmSync(tmp, { recursive: true, force: true }) };
}

function merge(ctx, { currentHead = INSPECTED, mergeFail = '', args = ['--head', INSPECTED] } = {}) {
  const r = spawnSync('node', [SCRIPT, 'merge', '--pr', '42', '--method', 'squash', ...args], {
    cwd: ctx.root,
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: `${ctx.bin}${path.delimiter}${process.env.PATH}`,
      FAKE_GH_LOG: ctx.log,
      FAKE_HEAD: currentHead,
      FAKE_MERGE_FAIL: mergeFail,
    },
  });
  const calls = existsSync(ctx.log) ? readFileSync(ctx.log, 'utf8').trim().split('\n') : [];
  let json = null;
  try { json = JSON.parse(r.stdout); } catch { /* fail() 는 stderr 로만 쓴다 */ }
  return { code: r.status, json, stderr: r.stderr, merges: calls.filter((c) => c.startsWith('gh pr merge')) };
}

test('merge 는 검사한 head 를 --match-head-commit 으로 gh 에 넘긴다', () => {
  const ctx = setup();
  try {
    const r = merge(ctx);
    assert.equal(r.code, 0, r.stderr);
    assert.equal(r.json.merged, true);
    assert.deepEqual(r.merges, [`gh pr merge 42 --squash --match-head-commit ${INSPECTED}`]);
  } finally {
    ctx.cleanup();
  }
});

test('PR head 가 검사한 head 와 다르면 gh merge 를 부르지 않고 head-changed 로 실패한다', () => {
  const ctx = setup();
  try {
    const r = merge(ctx, { currentHead: 'b'.repeat(40) });
    assert.equal(r.code, 1);
    assert.equal(r.json.merged, false);
    assert.equal(r.json.cause, 'head-changed');
    assert.equal(r.json.expectedHead, INSPECTED);
    assert.equal(r.json.currentHead, 'b'.repeat(40));
    assert.deepEqual(r.merges, []);
  } finally {
    ctx.cleanup();
  }
});

test('gh 의 Head branch was modified 는 approval 이 아니라 head-changed 다', () => {
  const ctx = setup();
  try {
    const r = merge(ctx, { mergeFail: 'Head branch was modified. Review and try the merge again.' });
    assert.equal(r.code, 1);
    assert.equal(r.json.cause, 'head-changed');
    assert.match(r.json.hint, /gh pr merge 를 직접 호출하지 마세요/);
  } finally {
    ctx.cleanup();
  }
});

test('리뷰 승인 부족은 여전히 approval 이다', () => {
  const ctx = setup();
  try {
    const r = merge(ctx, { mergeFail: 'At least 1 approving review is required by reviewers with write access.' });
    assert.equal(r.json.cause, 'approval');
  } finally {
    ctx.cleanup();
  }
});

test('--head 없이는 merge 하지 않는다', () => {
  const ctx = setup();
  try {
    const r = merge(ctx, { args: [] });
    assert.equal(r.code, 1);
    assert.match(r.stderr, /--head/);
    assert.deepEqual(r.merges, []);
  } finally {
    ctx.cleanup();
  }
});
