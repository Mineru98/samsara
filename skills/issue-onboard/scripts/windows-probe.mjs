#!/usr/bin/env node
/**
 * issue-onboard 실행 환경 프로브 (이슈 #55).
 *
 * 개발 머신에서 실행할 수 없는 Windows 동작을 실제 Windows 에서 한 번에 확인하기 위한
 * 읽기 전용 스크립트다. 저장소·이슈·파일을 바꾸지 않는다. 토큰 값은 출력하지 않는다.
 *
 * 사용:
 *   node skills/issue-onboard/scripts/windows-probe.mjs [--out <파일>] [--repo <owner/name>]
 *
 * 확인하는 것:
 *   1. 실행 환경 — 플랫폼, Node, 탐지에 쓰이는 환경변수 존재 여부
 *   2. 실제 설치 위치 — 제한 없는 PATH 로 찾은 git / gh (정책 후보와 비교용)
 *   3. 탐지 정책 — 플랫폼 정책의 후보 경로와 각 후보의 존재·채택 여부
 *   4. 저장소 인식 — issue-onboard 와 같은 git() 경로로 rev-parse
 *   5. gh 호출 — 트래커와 같은 제한 환경(PATH·환경변수 허용 목록)으로 gh 실행
 *   6. 네이티브 의존성 조회 — issue-native-deps 의 제한 환경으로 GraphQL 1회
 *
 * 각 항목은 PASS / FAIL / SKIP 으로 판정하고, 하나라도 FAIL 이면 exit 1 이다.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, realpathSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import {
  executablePolicy,
  git,
  gitFailureMessage,
  resolveTrustedExecutable,
  trustedCommandPath,
} from './issue-common.mjs';
import { fetchBlockedBy, splitSlug } from './issue-native-deps.mjs';
import { gitHost } from './issue-tracker.mjs';

const ENV_KEYS = [
  'SystemRoot', 'windir', 'ProgramFiles', 'ProgramFiles(x86)', 'ProgramW6432', 'LOCALAPPDATA',
  'APPDATA', 'USERPROFILE', 'HOME', 'PATHEXT', 'ComSpec', 'GH_TOKEN', 'GITHUB_TOKEN', 'GH_CONFIG_DIR',
];

function parseArgs(argv) {
  const opts = {};
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--out') opts.out = argv[++i];
    else if (argv[i] === '--repo') opts.repo = argv[++i];
    else if (argv[i] === '-h' || argv[i] === '--help') opts.help = true;
    else throw new Error(`알 수 없는 인자: ${argv[i]}`);
  }
  return opts;
}

/** 제한 없는 PATH 로 실제 설치 위치를 찾는다. 정책과 비교하려는 참고값이다. */
function locate(command) {
  const finder = process.platform === 'win32' ? 'where' : 'which';
  const r = spawnSync(finder, [command], { encoding: 'utf8' });
  if (r.error || r.status !== 0) return [];
  return String(r.stdout).split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

function firstLine(text) {
  return String(text ?? '').split(/\r?\n/).find((line) => line.trim()) ?? '';
}

export function runProbe({ repo } = {}) {
  const lines = [];
  const results = [];
  const out = (line = '') => lines.push(line);
  const verdict = (name, status, detail) => {
    results.push({ name, status });
    out(`[${status}] ${name}${detail ? ` — ${detail}` : ''}`);
  };

  out('# issue-onboard 환경 프로브');
  out();
  out('## 1. 실행 환경');
  out(`platform=${process.platform} arch=${process.arch} node=${process.version}`);
  out(`cwd=${process.cwd()}`);
  for (const key of ENV_KEYS) {
    const value = process.env[key];
    const secret = /TOKEN/.test(key);
    out(`  ${key}=${value === undefined ? '(없음)' : secret ? '(설정됨)' : value}`);
  }

  out();
  out('## 2. 실제 설치 위치 (제한 없는 PATH)');
  const located = {};
  for (const command of ['git', 'gh']) {
    located[command] = locate(command);
    out(`  ${command}: ${located[command].length ? located[command].join(' | ') : '(찾지 못함)'}`);
  }

  out();
  out('## 3. 탐지 정책');
  const policy = executablePolicy();
  out(`  checkMode=${policy.checkMode} caseInsensitive=${policy.caseInsensitive}`);
  out(`  trustedCommandPath=${trustedCommandPath()}`);
  const resolved = {};
  for (const command of ['git', 'gh']) {
    out(`  ${command} 후보:`);
    for (const candidate of policy.candidates[command] ?? []) {
      out(`    ${existsSync(candidate) ? '있음' : '없음'}  ${candidate}`);
    }
    resolved[command] = resolveTrustedExecutable(command);
    const hint = !resolved[command] && located[command].length
      ? `실제 위치 ${located[command][0]} 가 후보에 없음`
      : '';
    verdict(`${command} 탐지`, resolved[command] ? 'PASS' : 'FAIL', resolved[command] ?? (hint || '후보 전부 탈락'));
  }

  out();
  out('## 4. 저장소 인식 (issue-onboard 와 같은 git 경로)');
  const top = git(['rev-parse', '--show-toplevel']);
  if (top.code === 0) verdict('git rev-parse --show-toplevel', 'PASS', top.out);
  else verdict('git rev-parse --show-toplevel', 'FAIL', `${gitFailureMessage(top.err)} (${firstLine(top.err)})`);

  out();
  out('## 5. gh 호출 (트래커의 제한 환경)');
  if (!resolved.gh) {
    verdict('gh auth status', 'SKIP', 'gh 를 탐지하지 못해 건너뜀');
  } else {
    const auth = gitHost.auth();
    verdict('gh auth status', auth.ok ? 'PASS' : 'FAIL', auth.ok ? firstLine(auth.detail) : '인증 실패 또는 실행 실패');
  }

  out();
  out('## 6. 네이티브 의존성 조회 (issue-native-deps 의 제한 환경)');
  const slug = repo ?? (resolved.gh && top.code === 0 ? gitHost.repoInfo(top.out)?.nameWithOwner : null);
  const parts = slug ? splitSlug(slug) : null;
  if (!resolved.gh || !parts) {
    verdict('gh api graphql blockedBy', 'SKIP', resolved.gh ? '저장소 slug 를 알 수 없음 (--repo 로 지정)' : 'gh 미탐지');
  } else {
    const deps = fetchBlockedBy({ owner: parts.owner, repo: parts.repo, number: 1, cwd: top.code === 0 ? top.out : undefined });
    verdict('gh api graphql blockedBy', deps ? 'PASS' : 'FAIL', deps ? `${slug}#1 조회 성공` : `${slug}#1 조회 실패`);
  }

  const failed = results.filter((r) => r.status === 'FAIL').length;
  out();
  out(`## 요약: PASS ${results.filter((r) => r.status === 'PASS').length} / FAIL ${failed} / SKIP ${results.filter((r) => r.status === 'SKIP').length}`);
  return { lines, results, ok: failed === 0 };
}

function main() {
  let opts;
  try {
    opts = parseArgs(process.argv.slice(2));
  } catch (error) {
    console.error(`✗ ${error.message}`);
    process.exit(2);
  }
  if (opts.help) {
    console.log('사용: node skills/issue-onboard/scripts/windows-probe.mjs [--out <파일>] [--repo <owner/name>]');
    return;
  }
  const report = runProbe({ repo: opts.repo });
  const text = `${report.lines.join('\n')}\n`;
  process.stdout.write(text);
  if (opts.out) writeFileSync(opts.out, text);
  process.exitCode = report.ok ? 0 : 1;
}

/** 심볼릭 링크·Windows 경로에서도 진입점 판별이 어긋나지 않게 realpath 로 비교한다. */
function isMainModule() {
  if (!process.argv[1]) return false;
  try {
    return realpathSync(path.resolve(process.argv[1])) === realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return false;
  }
}

if (isMainModule()) main();
