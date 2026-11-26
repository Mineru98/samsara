import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const SCRIPT = fileURLToPath(new URL('./issue-review.mjs', import.meta.url));

function fixture(t) {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'issue-review-'));
  const inputs = mkdtempSync(path.join(os.tmpdir(), 'issue-review-input-'));
  t.after(() => [dir, inputs].forEach((d) => rmSync(d, { recursive: true, force: true })));
  const git = (...args) => execFileSync('git', args, { cwd: dir, encoding: 'utf8' }).trim();
  const commit = (file, content, message) => {
    mkdirSync(path.dirname(path.join(dir, file)), { recursive: true });
    writeFileSync(path.join(dir, file), content);
    git('add', '-A', '-f');
    git('-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-q', '-m', message);
    return git('rev-parse', 'HEAD');
  };
  git('init', '-q', '-b', 'main');
  commit('a.js', '1\n', 'base');
  git('checkout', '-q', '-b', 'feat/7-x');
  const head = commit('a.js', '2\n', 'impl');
  const run = (args, { env = {}, cwd = dir } = {}) => spawnSync(process.execPath, [SCRIPT, ...args], { cwd, encoding: 'utf8', env: { ...process.env, ...env } });
  const record = (list, target = head) => run(['record', '--issue', '7', '--target', target, '--input', findings(list), '--base', 'main']);
  const findings = (list) => {
    const file = path.join(inputs, 'findings.json');
    writeFileSync(file, JSON.stringify({ coverage: { total_files: 1, reviewed_files: 1, skipped_files: [] }, findings: list }));
    return file;
  };
  return { dir, inputs, commit, run, record, head };
}

test('record 뒤 check 는 통과하고 blocking 개수를 알린다', (t) => {
  const f = fixture(t);
  const record = f.record([
    { path: 'a.js', content: 'null 역참조', severity: 'high', start_line: 1, end_line: 1 },
    { path: 'a.js', content: '이름', severity: 'low' },
  ]);
  assert.equal(record.status, 0, record.stderr);
  const saved = JSON.parse(readFileSync(path.join(f.dir, '.issue/7/evidence/review.json'), 'utf8'));
  assert.equal(saved.reviewedSha, f.head);
  assert.equal(saved.mode, 'delegate');
  const check = f.run(['check', '--issue', '7']);
  assert.equal(check.status, 0, check.stderr);
  assert.match(check.stdout, /STATUS=reviewed/);
  assert.match(check.stdout, /BLOCKING=1/);
});

test('증거 커밋(.issue 만 변경)은 리뷰를 낡게 만들지 않는다', (t) => {
  const f = fixture(t);
  assert.equal(f.record([]).status, 0);
  f.commit('.issue/7/evidence/comment.md', 'report\n', 'docs: evidence');
  assert.equal(f.run(['check', '--issue', '7']).status, 0);
});

test('하위 디렉터리에서 실행해도 그 밖의 코드 커밋을 잡는다', (t) => {
  const f = fixture(t);
  assert.equal(f.record([]).status, 0);
  f.commit('sub/b.js', '1\n', 'sub');
  assert.equal(f.record([], f.head).status, 4);
  f.commit('a.js', '3\n', 'root fix');
  assert.equal(f.run(['check', '--issue', '7'], { cwd: path.join(f.dir, 'sub') }).status, 4);
});

test('target 뒤에 커밋이 끼면 record 가 exit 4 로 거부한다', (t) => {
  const f = fixture(t);
  f.commit('a.js', '3\n', 'late');
  const record = f.record([], f.head);
  assert.equal(record.status, 4);
  assert.match(record.stderr, /다시 리뷰/);
});

test('커밋되지 않은 코드 변경이 있으면 check 가 exit 4 다', (t) => {
  const f = fixture(t);
  assert.equal(f.record([]).status, 0);
  writeFileSync(path.join(f.dir, 'a.js'), '9\n');
  const check = f.run(['check', '--issue', '7']);
  assert.equal(check.status, 4);
  assert.match(check.stdout, /UNCOMMITTED=1/);
});

test('형식이 맞지 않는 review.json 은 check 가 exit 3 이다', (t) => {
  const f = fixture(t);
  mkdirSync(path.join(f.dir, '.issue/7/evidence'), { recursive: true });
  for (const body of ['null', '{"status":"garbage","reviewedSha":"x"}']) {
    writeFileSync(path.join(f.dir, '.issue/7/evidence/review.json'), body);
    assert.equal(f.run(['check', '--issue', '7']).status, 3, body);
  }
});

test('리뷰 뒤 코드 커밋이 쌓이면 check 가 exit 4 로 막는다', (t) => {
  const f = fixture(t);
  assert.equal(f.record([]).status, 0);
  f.commit('a.js', '3\n', 'fix');
  const check = f.run(['check', '--issue', '7']);
  assert.equal(check.status, 4);
  assert.match(check.stdout, /STATUS=stale/);
});

test('리뷰 결과가 없으면 check 가 exit 3 이다', (t) => {
  const f = fixture(t);
  const check = f.run(['check', '--issue', '7']);
  assert.equal(check.status, 3);
  assert.match(check.stdout, /STATUS=missing/);
});

test('ocr 이 없으면 target 이 OCR=0 을 내고 skip 기록은 check 를 통과한다', (t) => {
  const f = fixture(t);
  const bin = path.join(f.inputs, 'bin');
  mkdirSync(bin);
  symlinkSync(execFileSync('which', ['git'], { encoding: 'utf8' }).trim(), path.join(bin, 'git'));
  const target = f.run(['target', '--base', 'main'], { env: { PATH: bin } });
  assert.equal(target.status, 0, target.stderr);
  assert.match(target.stdout, /OCR=0/);
  assert.match(target.stdout, new RegExp(`REVIEW_TARGET=${f.head}`));
  assert.equal(f.run(['skip', '--issue', '7', '--reason', 'ocr 미설치', '--base', 'main']).status, 0);
  const check = f.run(['check', '--issue', '7']);
  assert.equal(check.status, 0);
  assert.match(check.stdout, /STATUS=skipped/);
  assert.match(check.stdout, /SKIP_REASON=ocr 미설치/);
});

test('커버리지 합이 맞지 않는 입력은 record 가 거부한다', (t) => {
  const f = fixture(t);
  const file = path.join(f.inputs, 'bad.json');
  writeFileSync(file, JSON.stringify({ coverage: { total_files: 3, reviewed_files: 1, skipped_files: [] }, findings: [] }));
  const record = f.run(['record', '--issue', '7', '--target', f.head, '--input', file, '--base', 'main']);
  assert.equal(record.status, 2);
  assert.match(record.stderr, /커버리지/);
});
