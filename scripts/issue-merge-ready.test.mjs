import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #133. issue-merge 다음 행동의 착수 후보(ready)가 본문 `## 선행 이슈` 가 모두 끝난 이슈만 고르고,
 * 선행이 남은 이슈는 waiting 으로 빼는지 가짜 gh 로 재현한다. 실제 GitHub 은 호출하지 않는다.
 */

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const script = path.join(repo, 'skills', 'issue-merge', 'scripts', 'issue-merge.mjs');

// issues.json 을 읽기만 하는 가짜 gh. issue view / issue list 만 지원한다.
const FAKE_GH = `#!/usr/bin/env node
const fs = require('node:fs');
const path = require('node:path');
const db = JSON.parse(fs.readFileSync(path.join(process.env.FAKE_GH_DIR, 'issues.json'), 'utf8'));
const [a, b, n] = process.argv.slice(2);
const out = (k) => ({ number: Number(k), title: 't' + k, state: db[k].state, body: db[k].body,
  labels: db[k].labels.map((name) => ({ name })), url: 'https://example.invalid/' + k });
if (a === 'issue' && b === 'view' && db[n]) process.stdout.write(JSON.stringify(out(n)));
else if (a === 'issue' && b === 'list') process.stdout.write(JSON.stringify(Object.keys(db).filter((k) => db[k].state === 'OPEN').map(out)));
else { process.stderr.write('unsupported'); process.exit(1); }
`;

const body = (...preds) => `## 배경\n\nx\n\n## 선행 이슈\n\n${preds.length ? preds.join('\n') : '없음'}\n\n## 후행 이슈\n\n없음\n`;

function runReady(issues) {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'merge-ready-'));
  mkdirSync(path.join(dir, 'bin'));
  writeFileSync(path.join(dir, 'bin', 'gh'), FAKE_GH);
  chmodSync(path.join(dir, 'bin', 'gh'), 0o755);
  writeFileSync(path.join(dir, 'issues.json'), JSON.stringify(issues));
  const work = path.join(dir, 'repo');
  mkdirSync(work);
  spawnSync('git', ['init', '-q'], { cwd: work });
  const r = spawnSync(process.execPath, [script, 'ready'], {
    cwd: work,
    encoding: 'utf8',
    env: { ...process.env, HOME: dir, PATH: `${path.join(dir, 'bin')}${path.delimiter}${process.env.PATH}`, FAKE_GH_DIR: dir },
  });
  assert.equal(r.status, 0, r.stderr);
  return JSON.parse(r.stdout);
}

test('선행이 열린 이슈는 착수 목록(ready)에 없고 waiting 에 남은 선행과 함께 나온다', () => {
  const out = runReady({
    5: { state: 'OPEN', labels: ['status:in-process'], body: body() },
    7: { state: 'OPEN', labels: [], body: body('- [ ] #5') },
    // 본문 체크가 [x] 여도 실제로 열려 있으면 대기다. 조회에 실패한 선행(#404)도 미완료로 센다.
    9: { state: 'OPEN', labels: [], body: body('- [x] #5', '- [ ] #404') },
  });
  assert.deepEqual(out.ready.map((r) => r.number), [5]);
  assert.deepEqual(out.waiting.map((w) => [w.number, w.pending]), [[7, [5]], [9, [5, 404]]]);
});

test('선행이 모두 끝난 이슈(CLOSED 또는 status:close)와 선행 없는 이슈는 ready 에 그대로 나온다', () => {
  const out = runReady({
    1: { state: 'OPEN', labels: [], body: body() },
    3: { state: 'OPEN', labels: ['status:close'], body: body() },
    4: { state: 'CLOSED', labels: ['status:close'], body: body() },
    8: { state: 'OPEN', labels: [], body: body('- [ ] #4', '- [ ] #3') },
  });
  assert.deepEqual(out.ready.map((r) => r.number), [1, 3, 8]);
  assert.deepEqual(out.waiting, []);
});
