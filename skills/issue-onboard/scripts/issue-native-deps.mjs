// GitHub 네이티브 이슈 의존성(blocked-by / blocking) 리더 (#하이브리드 그래프).
//
// GitHub 은 2025-08 부터 이슈 의존성을 정식(GA) 지원한다. gh 2.9x 에는 전용
// 서브커맨드가 없어 GraphQL(`gh api graphql`)로만 접근된다. 이 모듈은 onboard 의
// sync 가 본문 마커(`depends on #N`)·결정 코멘트에 더해 GitHub 네이티브 의존성을
// 추가 정본으로 읽어들이기 위한 얇은 어댑터다.
//
// 왜 issue-tracker.mjs 가 아니라 여기 있나:
//   issue-tracker.mjs 는 vendored("DO NOT EDIT") 파일이고 이 저장소에는 그
//   canonical 원본(tools/issue-tracker.mjs)과 재동기화 스크립트가 존재하지 않는다.
//   그래서 잠긴 사본 5개를 손대는 대신, onboard 소유의 이 모듈에서만 gh 를 부른다.
//   실패(미지원 GitHub Enterprise·오프라인·권한)는 sync 를 partial 로 만들며
//   온보딩 추천을 막는다. 방향 규약은 onboard 와 동일하다:
//     issue X 가 Y 에 blocked-by  ⇒  Y 가 선수(predecessor)  ⇒  X --depends-on--> Y.
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { testFixtureCommandDir } from './issue-common.mjs';

const SYSTEM_PATH = [
  '/opt/homebrew/bin', '/opt/homebrew/sbin',
  '/usr/local/bin', '/usr/local/sbin',
  '/usr/bin', '/usr/sbin', '/bin', '/sbin',
  '/System/Cryptexes/App/usr/bin',
];
const COMMAND_ENV_KEYS = [
  'HOME', 'USER', 'LOGNAME', 'TMPDIR', 'TMP', 'TEMP',
  'LANG', 'LC_ALL', 'LC_CTYPE', 'TERM',
  'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME', 'GH_CONFIG_DIR',
  'GH_HOST', 'GH_TOKEN', 'GITHUB_TOKEN', 'GH_ENTERPRISE_TOKEN', 'GITHUB_ENTERPRISE_TOKEN',
];

function trustedPath() {
  const fixtureDir = testFixtureCommandDir();
  return [...(fixtureDir ? [fixtureDir] : []), ...SYSTEM_PATH].join(path.delimiter);
}

function trustedCommandEnv() {
  return Object.fromEntries([
    ...COMMAND_ENV_KEYS.filter((key) => typeof process.env[key] === 'string').map((key) => [key, process.env[key]]),
    ['PATH', trustedPath()],
  ]);
}

const DEP_QUERY = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){
    issue(number:$number){
      blockedBy(first:50){ nodes{ number } pageInfo { hasNextPage } }
    }
  }
}`;

/**
 * 한 이슈의 blocked-by(선수) 이슈 번호 목록을 GraphQL 로 읽는다.
 * 반환:
 *   { numbers: number[] }        조회 성공(빈 배열 포함)
 *   { unsupported: true }        네이티브 의존성 API/필드를 쓸 수 없음 → 호출부는 중단
 *   null                         일시적 실패(권한·네트워크·파싱) → 전체 snapshot을 partial로 처리
 */
export function fetchBlockedBy({ owner, repo, number, cwd, runner = spawnSync } = {}) {
  if (!owner || !repo || !Number.isInteger(number)) return null;
  const args = [
    'api', 'graphql',
    '-F', `owner=${owner}`,
    '-F', `repo=${repo}`,
    '-F', `number=${number}`,
    '-f', `query=${DEP_QUERY}`,
  ];
  let result;
  try {
    result = runner('gh', args, {
      cwd,
      encoding: 'utf8',
      maxBuffer: 16 * 1024 * 1024,
      timeout: 30000,
      env: trustedCommandEnv(),
    });
  } catch {
    return null;
  }
  const stderr = String(result?.stderr ?? '');
  // 스키마에 blockedBy 필드가 없는 오래된 GitHub Enterprise 등 → 재시도해도 무의미하므로 중단 신호.
  if (/blockedBy|Field '.*' doesn't exist|Unknown field/i.test(stderr) && (result?.status ?? 1) !== 0) {
    return { unsupported: true };
  }
  if (!result || result.status !== 0 || !result.stdout) return null;
  let json;
  try {
    json = JSON.parse(result.stdout);
  } catch {
    return null;
  }
  if (Array.isArray(json?.errors) && json.errors.length) {
    const message = json.errors.map((e) => e?.message ?? '').join(' ');
    if (/blockedBy|doesn't exist|Unknown field/i.test(message)) return { unsupported: true };
    return null;
  }
  const nodes = json?.data?.repository?.issue?.blockedBy?.nodes;
  if (!Array.isArray(nodes)) return null;
  if (json?.data?.repository?.issue?.blockedBy?.pageInfo?.hasNextPage !== false) return null;
  const numbers = nodes
    .map((n) => Number(n?.number))
    .filter((n) => Number.isInteger(n) && n > 0);
  return { numbers };
}

/** `owner/repo` 문자열을 {owner, repo} 로 쪼갠다. 형식이 아니면 null. */
export function splitSlug(slug) {
  if (typeof slug !== 'string') return null;
  const match = slug.trim().match(/^([^/\s]+)\/([^/\s]+)$/);
  return match ? { owner: match[1], repo: match[2] } : null;
}
