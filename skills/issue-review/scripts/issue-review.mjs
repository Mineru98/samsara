#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const USAGE = `Usage:
  node issue-review.mjs target [--base <branch>]
  node issue-review.mjs record --issue <n> --input <findings.json> [--base <branch>]
  node issue-review.mjs skip   --issue <n> --reason <사유> [--base <branch>]
  node issue-review.mjs check  --issue <n>

target  리뷰 대상 커밋·기준 브랜치·ocr 설치 여부를 출력한다.
record  서브에이전트가 낸 findings JSON 을 .issue/<n>/evidence/review.json 으로 확정한다.
skip    리뷰를 건너뛴 사실과 사유를 같은 파일에 남긴다.
check   review.json 이 현재 코드에 대한 것인지 확인한다.
        exit 0 최신 / exit 3 없음 / exit 4 리뷰 뒤에 코드가 바뀜

exit 2 는 인자·입력 오류다.`;

const SEVERITIES = ['critical', 'high', 'medium', 'low'];
const BLOCKING = ['critical', 'high'];

function fail(message, code = 2) {
  console.error(`✗ ${message}`);
  process.exit(code);
}

function git(args, { allowFail = false } = {}) {
  try {
    return execFileSync('git', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  } catch (error) {
    if (allowFail) return '';
    fail(`git ${args.join(' ')} 실패: ${String(error.stderr || error.message).trim()}`);
  }
}

function parseArgs(argv) {
  const opts = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (!['--issue', '--input', '--reason', '--base'].includes(arg)) fail(`알 수 없는 인자: ${arg}\n\n${USAGE}`);
    const value = argv[i + 1];
    if (value === undefined) fail(`${arg} 에 값이 필요하다`);
    opts[arg.slice(2)] = value;
    i += 1;
  }
  return opts;
}

function recordedBase() {
  const commonDir = git(['rev-parse', '--path-format=absolute', '--git-common-dir']);
  const settings = path.join(path.dirname(commonDir), '.issue', 'settings.json');
  if (!existsSync(settings)) return '';
  try {
    return JSON.parse(readFileSync(settings, 'utf8'))?.git?.baseBranch ?? '';
  } catch {
    return '';
  }
}

function resolveBase(explicit) {
  const base = explicit
    || recordedBase()
    || git(['symbolic-ref', '--short', 'refs/remotes/origin/HEAD'], { allowFail: true }).replace(/^origin\//, '');
  if (!base) fail('기준 브랜치를 판별하지 못했다. --base <branch> 로 넘긴다');
  for (const ref of [`origin/${base}`, base]) {
    if (git(['rev-parse', '--verify', '--quiet', `${ref}^{commit}`], { allowFail: true })) return { base, ref };
  }
  fail(`기준 브랜치 ${base} 를 찾지 못했다`);
}

/** 증거 커밋은 .issue 만 건드리므로 리뷰 대상에서 뺀다. */
function reviewTarget() {
  return git(['rev-list', '-1', 'HEAD', '--', '.', ':(exclude).issue']);
}

function hasOcr() {
  try {
    execFileSync('ocr', ['--version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

function reviewFile(issue) {
  if (!/^\d+$/.test(String(issue ?? ''))) fail('--issue <번호> 가 필요하다');
  return path.join(git(['rev-parse', '--show-toplevel']), '.issue', String(issue), 'evidence', 'review.json');
}

function scope(explicitBase) {
  const { base, ref } = resolveBase(explicitBase);
  return { base, mergeBase: git(['merge-base', ref, 'HEAD']), reviewedSha: reviewTarget() };
}

function write(issue, body) {
  const file = reviewFile(issue);
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, `${JSON.stringify({ schemaVersion: 1, issue: Number(issue), mode: 'delegate', ...body }, null, 2)}\n`);
  return file;
}

function countBySeverity(findings) {
  return Object.fromEntries(SEVERITIES.map((s) => [s, findings.filter((f) => f.severity === s).length]));
}

function printSummary(review, file) {
  const counts = countBySeverity(review.findings ?? []);
  console.log(`REVIEW_FILE=${path.relative(process.cwd(), file) || file}`);
  console.log(`STATUS=${review.status}`);
  console.log(`REVIEWED_SHA=${review.reviewedSha}`);
  if (review.status === 'skipped') console.log(`SKIP_REASON=${review.skipReason}`);
  console.log(`FINDINGS=${SEVERITIES.map((s) => `${s}:${counts[s]}`).join(',')}`);
  console.log(`BLOCKING=${BLOCKING.reduce((sum, s) => sum + counts[s], 0)}`);
}

function cmdTarget(opts) {
  const { base, mergeBase, reviewedSha } = scope(opts.base);
  console.log(`BASE=${base}`);
  console.log(`MERGE_BASE=${mergeBase}`);
  console.log(`REVIEW_TARGET=${reviewedSha}`);
  console.log(`OCR=${hasOcr() ? 1 : 0}`);
}

function cmdRecord(opts) {
  if (!opts.input) fail('--input <findings.json> 이 필요하다');
  let input;
  try {
    input = JSON.parse(readFileSync(opts.input, 'utf8'));
  } catch (error) {
    fail(`findings JSON 을 읽지 못했다: ${error.message}`);
  }
  const coverage = input.coverage ?? {};
  const skippedFiles = coverage.skipped_files ?? [];
  if (!Number.isInteger(coverage.total_files) || !Number.isInteger(coverage.reviewed_files) || !Array.isArray(skippedFiles)) {
    fail('coverage.total_files · reviewed_files(정수)와 skipped_files(배열)가 필요하다');
  }
  if (coverage.reviewed_files + skippedFiles.length !== coverage.total_files) {
    fail(`커버리지가 맞지 않는다: reviewed ${coverage.reviewed_files} + skipped ${skippedFiles.length} ≠ total ${coverage.total_files}`);
  }
  if (skippedFiles.some((f) => !f?.path || !f?.reason)) fail('skipped_files 항목마다 path 와 reason 이 필요하다');
  const findings = input.findings ?? [];
  if (!Array.isArray(findings)) fail('findings 는 배열이어야 한다');
  for (const finding of findings) {
    if (!finding?.path || !finding?.content) fail('finding 마다 path 와 content 가 필요하다');
    if (!SEVERITIES.includes(finding.severity)) fail(`severity 는 ${SEVERITIES.join(' / ')} 중 하나여야 한다: ${finding.path}`);
  }
  const review = {
    status: 'reviewed',
    ...scope(opts.base),
    coverage: { total_files: coverage.total_files, reviewed_files: coverage.reviewed_files, skipped_files: skippedFiles },
    findings,
  };
  printSummary(review, write(opts.issue, review));
}

function cmdSkip(opts) {
  if (!opts.reason?.trim()) fail('--reason <사유> 가 필요하다');
  const review = { status: 'skipped', skipReason: opts.reason.trim(), ...scope(opts.base), findings: [] };
  printSummary(review, write(opts.issue, review));
}

function cmdCheck(opts) {
  const file = reviewFile(opts.issue);
  if (!existsSync(file)) {
    console.log('STATUS=missing');
    fail(`리뷰 결과가 없다: ${file}`, 3);
  }
  let review;
  try {
    review = JSON.parse(readFileSync(file, 'utf8'));
  } catch (error) {
    fail(`review.json 을 읽지 못했다: ${error.message}`, 3);
  }
  const current = reviewTarget();
  if (review.reviewedSha !== current) {
    console.log('STATUS=stale');
    console.log(`REVIEWED_SHA=${review.reviewedSha}`);
    console.log(`CURRENT_SHA=${current}`);
    fail('리뷰 뒤에 코드가 바뀌었다. issue-review 를 다시 돌린다', 4);
  }
  printSummary(review, file);
}

const [command, ...rest] = process.argv.slice(2);
const commands = { target: cmdTarget, record: cmdRecord, skip: cmdSkip, check: cmdCheck };
if (!commands[command]) {
  console.error(USAGE);
  process.exit(command ? 2 : 0);
}
commands[command](parseArgs(rest));
