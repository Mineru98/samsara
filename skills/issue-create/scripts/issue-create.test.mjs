import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const CLI = path.join(HERE, 'issue-create.mjs');

/** 사용자 설정(~/.issue)에 흔들리지 않게 HOME 을 비운 채 --dry-run 으로만 돌린다. */
function fixture(t) {
  const tmp = mkdtempSync(path.join(tmpdir(), 'samsara-create-'));
  t.after(() => rmSync(tmp, { recursive: true, force: true }));
  const body = path.join(tmp, 'body.md');
  writeFileSync(body, '# body\n');
  const run = (...args) => {
    const r = spawnSync(process.execPath, [CLI, ...args, '--dry-run'], {
      cwd: HERE, encoding: 'utf8', env: { ...process.env, HOME: tmp },
    });
    return { code: r.status, out: r.stdout, err: r.stderr };
  };
  const create = (...labels) => run(
    'create', '--title', 't', '--body-file', body, '--skip-duplicate-review',
    ...labels.flatMap((label) => ['--label', label]),
  );
  return { run, create };
}

test('#136 빈 라벨·공백 라벨만 넘어오면 create 가 exit 2 로 막는다', (t) => {
  const { create } = fixture(t);
  for (const labels of [[''], ['  '], ['', ' ']]) {
    const r = create(...labels);
    assert.equal(r.code, 2, `labels=${JSON.stringify(labels)}: ${r.out}${r.err}`);
    assert.match(r.err, /성격 라벨\(--label\)이 하나 이상 필요하다/);
    assert.doesNotMatch(r.out, /gh issue create/);
  }
});

test('#136 정상 라벨과 빈 라벨이 섞이면 빈 라벨만 버리고 앞뒤 공백을 다듬는다', (t) => {
  const { create } = fixture(t);
  const r = create('', ' bug ', '  ');
  assert.equal(r.code, 0, r.err);
  const line = r.out.split('\n').find((l) => l.startsWith('(dry-run) gh issue create'));
  assert.ok(line, r.out);
  assert.match(line, / --label bug$/);
  assert.equal(line.match(/--label/g).length, 1);
});

test('#136 label 명령도 빈 라벨만으로는 붙이지 않는다', (t) => {
  const { run } = fixture(t);
  const r = run('label', '1', '--label', '');
  assert.notEqual(r.code, 0);
  assert.match(r.err, /--label 또는 --remove-label 이 하나 이상 필요하다/);
  assert.doesNotMatch(r.out, /\(dry-run\)/);
});
