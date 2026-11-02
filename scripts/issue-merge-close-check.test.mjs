import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #97. issue-merge 회차 끝의 close-check 가 merge 된 이슈를 status:close + CLOSED 로
 * 맞추는지, 보류 이슈는 건드리지 않는지 가짜 gh 로 재현한다. 실제 GitHub 은 호출하지 않는다.
 */

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const script = path.join(repo, 'skills', 'issue-merge', 'scripts', 'issue-merge.mjs');

// 상태 파일(issues.json)을 읽고 쓰는 가짜 gh. 호출 기록은 calls.log 에 남긴다.
const FAKE_GH = `#!/usr/bin/env node
const fs = require('node:fs');
const path = require('node:path');
const dir = process.env.FAKE_GH_DIR;
const file = path.join(dir, 'issues.json');
const db = JSON.parse(fs.readFileSync(file, 'utf8'));
const argv = process.argv.slice(2);
fs.appendFileSync(path.join(dir, 'calls.log'), argv.join(' ') + '\\n');
const save = () => fs.writeFileSync(file, JSON.stringify(db));
const [a, b, n] = argv;
const it = db[n];
if (a === 'label' && b === 'create') process.exit(0);
if (a !== 'issue' || !it) { process.stderr.write('unsupported'); process.exit(1); }
if (b === 'view') {
  process.stdout.write(JSON.stringify({ number: Number(n), title: 't' + n, state: it.state, body: '',
    labels: it.labels.map((name) => ({ name })), assignees: [], milestone: null, comments: [],
    url: 'https://example.invalid/' + n, createdAt: '', updatedAt: '' }));
} else if (b === 'edit') {
  for (let i = 3; i < argv.length; i += 2) {
    if (argv[i] === '--add-label') it.labels.push(argv[i + 1]);
    if (argv[i] === '--remove-label') it.labels = it.labels.filter((l) => l !== argv[i + 1]);
  }
  save();
} else if (b === 'comment') {
  it.comments = (it.comments ?? 0) + 1; save();
} else if (b === 'close') {
  it.state = 'CLOSED'; save();
}
`;

function setup(issues) {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'close-check-'));
  const bin = path.join(dir, 'bin');
  mkdirSync(bin);
  writeFileSync(path.join(bin, 'gh'), FAKE_GH);
  chmodSync(path.join(bin, 'gh'), 0o755);
  writeFileSync(path.join(dir, 'issues.json'), JSON.stringify(issues));
  writeFileSync(path.join(dir, 'calls.log'), '');
  const work = path.join(dir, 'repo');
  mkdirSync(work);
  spawnSync('git', ['init', '-q'], { cwd: work });
  return { dir, work };
}

function runCheck({ dir, work }, args) {
  const r = spawnSync(process.execPath, [script, 'close-check', ...args], {
    cwd: work,
    encoding: 'utf8',
    env: { ...process.env, HOME: dir, PATH: `${path.join(dir, 'bin')}${path.delimiter}${process.env.PATH}`, FAKE_GH_DIR: dir },
  });
  return { code: r.status, json: JSON.parse(r.stdout), stderr: r.stderr };
}

const state = (dir) => JSON.parse(readFileSync(path.join(dir, 'issues.json'), 'utf8'));
const writes = (dir) => readFileSync(path.join(dir, 'calls.log'), 'utf8')
  .split('\n').filter((l) => /^issue (edit|comment|close) /.test(l));

test('status:review + OPEN 이슈는 --fix 로 status:close + CLOSED 가 되고 결과 코멘트가 남는다', () => {
  const env = setup({
    21: { state: 'OPEN', labels: ['enhancement', 'status:review'] },
    53: { state: 'CLOSED', labels: ['status:close'] },
    16: { state: 'CLOSED', labels: ['status:review'] },
  });
  try {
    const comments = path.join(env.dir, 'verify');
    mkdirSync(comments);
    writeFileSync(path.join(comments, '21-result.md'), '## 통합 완료');
    const r = runCheck(env, ['21', '53', '16', '--comment-dir', comments, '--fix']);
    assert.equal(r.code, 0, r.stderr);
    assert.equal(r.json.summary, '확인 1건 / 보정 2건 / 보류 0건');
    const db = state(env.dir);
    assert.equal(db[21].state, 'CLOSED');
    assert.deepEqual(db[21].labels, ['enhancement', 'status:close']);
    assert.equal(db[21].comments, 1);
    assert.deepEqual(db[16].labels, ['status:close']);
    assert.deepEqual(r.json.fixed.find((f) => f.issue === 21).actions, ['status:close', 'close', 'comment']);
    assert.deepEqual(r.json.fixed.find((f) => f.issue === 16).actions, ['status:close']);
    assert.ok(!writes(env.dir).some((l) => / 53( |$)/.test(l)), '이미 정상인 #53 에는 쓰기 호출이 없어야 한다');
  } finally {
    rmSync(env.dir, { recursive: true, force: true });
  }
});

test('--hold 로 넘긴 보류 이슈는 상태가 어긋나 있어도 바꾸지 않는다', () => {
  const env = setup({
    21: { state: 'OPEN', labels: ['status:review'] },
    64: { state: 'OPEN', labels: ['status:review'] },
  });
  try {
    const r = runCheck(env, ['21', '64', '--hold', '64', '--fix']);
    assert.equal(r.code, 0, r.stderr);
    assert.equal(r.json.summary, '확인 0건 / 보정 1건 / 보류 1건');
    const db = state(env.dir);
    assert.deepEqual(db[64], { state: 'OPEN', labels: ['status:review'] });
    assert.ok(!writes(env.dir).some((l) => / 64( |$)/.test(l)));
  } finally {
    rmSync(env.dir, { recursive: true, force: true });
  }
});

test('--fix 없이는 조회만 하고 보정 필요를 exit 1 로 알린다', () => {
  const env = setup({ 21: { state: 'OPEN', labels: ['status:review'] } });
  try {
    const r = runCheck(env, ['21']);
    assert.equal(r.code, 1);
    assert.deepEqual(r.json.needsFix, [{ issue: 21, state: 'OPEN', status: ['status:review'], label: true, close: true }]);
    assert.deepEqual(writes(env.dir), []);
  } finally {
    rmSync(env.dir, { recursive: true, force: true });
  }
});

test('보류 이슈는 위치 인자에 없어도 보고되고, 이미 닫혀 있으면 경고가 붙는다', () => {
  const env = setup({
    21: { state: 'CLOSED', labels: ['status:close'] },
    64: { state: 'CLOSED', labels: ['status:review'] },
  });
  try {
    const r = runCheck(env, ['21', '--hold', '64', '--fix']);
    assert.equal(r.code, 0, r.stderr);
    assert.equal(r.json.summary, '확인 1건 / 보정 0건 / 보류 1건');
    assert.equal(r.json.held[0].issue, 64);
    assert.match(r.json.held[0].warning, /재오픈/);
    assert.deepEqual(writes(env.dir), []);
  } finally {
    rmSync(env.dir, { recursive: true, force: true });
  }
});

test('status:close 와 다른 status 라벨이 함께 있으면 다른 status 만 떼어 낸다', () => {
  const env = setup({ 30: { state: 'CLOSED', labels: ['status:close', 'status:review'] } });
  try {
    const r = runCheck(env, ['30', '--fix']);
    assert.equal(r.code, 0, r.stderr);
    assert.deepEqual(state(env.dir)[30].labels, ['status:close']);
    assert.deepEqual(r.json.fixed[0].actions, ['status:close']);
  } finally {
    rmSync(env.dir, { recursive: true, force: true });
  }
});
