import assert from 'node:assert/strict';
import test from 'node:test';

import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { findClosingKeywords } from './issue-start.mjs';
import { validateEvidenceReport } from './issue-media.mjs';
import { isImageEvidence, listEvidence } from './issue-common.mjs';

/*
 * 이슈 #147. 구현 커밋의 자동 종료 문구와, 증거 텍스트 파일 링크를 이미지로 오판하던 리포트 검사.
 */

const BLOB = 'https://github.com/o/r/blob/main/.issue/1/evidence/after';
const RAW = 'https://raw.githubusercontent.com/o/r/main/.issue/1/evidence/after';

test('자동 종료 문구를 찾는다', () => {
  for (const message of [
    'fix(x): y\n\nCloses #147',
    'fix: y (fixes #3)',
    'feat: y\n\nResolved: #12',
    'chore: y\n\nclose o/r#9',
    'fix: y\n\nFixes https://github.com/o/r/issues/5',
  ]) {
    assert.equal(findClosingKeywords(message).length, 1, message);
  }
});

test('관련 이슈 언급은 자동 종료 문구가 아니다', () => {
  for (const message of [
    'fix(issue-147): 리포트 검사 수정',
    'docs: #147 관련 문서',
    'fix: prefix closes 처리',
    'refactor: closest #3 검색',
  ]) {
    assert.deepEqual(findClosingKeywords(message), [], message);
  }
});

test('증거 텍스트 파일 링크만 있는 리포트는 통과한다', () => {
  const report = `원본: [guard.txt](${BLOB}/guard.txt)\n원본: ${RAW}/bench.txt`;
  const result = validateEvidenceReport(report);
  assert.equal(result.ok, true, result.errors.join('\n'));
  assert.deepEqual(result.references, []);
});

test('깨진 이미지 링크는 여전히 실패한다', () => {
  for (const report of [
    `![after](${BLOB}/a.webp)`,
    `[after](${RAW}/a.webp)`,
    `${RAW}/a.webp`,
    `![](${RAW}/a.webp)`,
  ]) {
    assert.equal(validateEvidenceReport(report).ok, false, report);
  }
});

test('정상 이미지와 user-attachments 는 통과한다', () => {
  assert.equal(validateEvidenceReport(`![after](${RAW}/a.webp)`).ok, true);
  assert.equal(validateEvidenceReport('![after](https://github.com/user-attachments/assets/abc-123)').ok, true);
});

/*
 * 이슈 #173. 증거 폴더의 세션 파일이 업로드·커밋 대상에 섞이던 문제.
 */

test('증거 목록은 숨김 파일과 세션 파일을 제외하고 이미지·텍스트만 돌려준다', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'issue-173-'));
  try {
    const dir = path.join(root, '.issue/999/evidence');
    for (const f of [
      '.DS_Store', '.auth.json', 'storage-state.json', 'cookies.json',
      'comment.md', 'after/home.webp', 'before/home.webp', 'before/bench.txt', 'before/.hidden.md',
    ]) {
      mkdirSync(path.dirname(path.join(dir, f)), { recursive: true });
      writeFileSync(path.join(dir, f), 'x');
    }
    assert.deepEqual(listEvidence(root, '999'), [
      '.issue/999/evidence/after/home.webp',
      '.issue/999/evidence/before/bench.txt',
      '.issue/999/evidence/before/home.webp',
      '.issue/999/evidence/comment.md',
    ]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('이미지 증거 판별은 이미지 확장자만 true 다', () => {
  for (const f of ['a.webp', 'a.PNG', 'a.jpg', 'a.jpeg', 'a.gif']) assert.equal(isImageEvidence(f), true, f);
  for (const f of ['comment.md', 'bench.txt', '.auth.json', 'storage-state.json']) assert.equal(isImageEvidence(f), false, f);
});
