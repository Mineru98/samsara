import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';

import {
  executablePolicy,
  resolveTrustedExecutable,
  trustedCommandPath,
  trustedExecutable,
} from './issue-common.mjs';

/*
 * 이슈 #40 회귀.
 *
 * Windows 바이너리를 macOS/Linux 에서 실행할 수는 없다. 그래서 실행 자체가 아니라
 * "탐색 정책이 그 플랫폼의 입력에 대해 어떻게 판정하는가" 를 검증한다.
 * platform·env·파일시스템 접근자를 주입해 Windows 를 흉내 낸다.
 */

const WIN_ENV = {
  SystemRoot: 'C:\\Windows',
  ProgramFiles: 'C:\\Program Files',
  'ProgramFiles(x86)': 'C:\\Program Files (x86)',
  LOCALAPPDATA: 'C:\\Users\\dev\\AppData\\Local',
};

const WIN_GIT = 'C:\\Program Files\\Git\\cmd\\git.exe';

/** Windows 가 실행 파일에 돌려주는 전형적인 stat. 실행 비트가 없다. */
function winStat(overrides = {}) {
  return { isFile: () => true, mode: 0o100666, uid: undefined, ...overrides };
}

/** 주어진 경로 집합만 존재하는 가짜 파일시스템. */
function fakeFs(existing, statFor = () => winStat()) {
  const set = new Set(existing);
  return {
    realpathSync(p) {
      if (!set.has(p)) throw Object.assign(new Error(`ENOENT: ${p}`), { code: 'ENOENT' });
      return p;
    },
    statSync(p) {
      if (!set.has(p)) throw Object.assign(new Error(`ENOENT: ${p}`), { code: 'ENOENT' });
      return statFor(p);
    },
  };
}

function resolveOnWindows(existing, extra = {}) {
  const fs = fakeFs(existing, extra.statFor);
  return resolveTrustedExecutable(extra.command ?? 'git', {
    platform: 'win32',
    env: extra.env ?? WIN_ENV,
    realpathSync: fs.realpathSync,
    statSync: fs.statSync,
    getuid: () => undefined,
    ...(extra.options ?? {}),
  });
}

/* ------------------------------------------------- 완료 기준 1: 플랫폼 분기 */

test('플랫폼에 따라 후보와 검증 정책이 갈린다', () => {
  const win = executablePolicy('win32', WIN_ENV);
  const unix = executablePolicy('darwin', {});

  assert.equal(win.pathApi, path.win32);
  assert.equal(unix.pathApi, path.posix);
  assert.equal(win.caseInsensitive, true);
  assert.equal(unix.caseInsensitive, false);
  assert.equal(win.checkMode, false, 'Windows 는 POSIX 모드 비트를 검사하지 않는다');
  assert.equal(unix.checkMode, true, 'Unix 계열은 모드 비트 검사를 유지한다');

  assert.ok(win.candidates.git.every((c) => c.endsWith('.exe')));
  assert.ok(unix.candidates.git.every((c) => c.startsWith('/')));
});

test('linux 는 darwin 과 같은 Unix 정책을 쓴다', () => {
  assert.deepEqual(executablePolicy('linux', {}), executablePolicy('darwin', {}));
});

/* ------------------------------ 완료 기준 2: 표준 설치 경로가 신뢰를 통과한다 */

test('Git for Windows 표준 설치 경로의 git.exe 를 찾는다', () => {
  assert.equal(resolveOnWindows([WIN_GIT]), WIN_GIT);
});

test('bin\\git.exe 만 있어도 찾는다', () => {
  const binGit = 'C:\\Program Files\\Git\\bin\\git.exe';
  assert.equal(resolveOnWindows([binGit]), binGit);
});

test('사용자 단위 설치(%LOCALAPPDATA%)도 찾는다', () => {
  const userGit = 'C:\\Users\\dev\\AppData\\Local\\Programs\\Git\\cmd\\git.exe';
  assert.equal(resolveOnWindows([userGit]), userGit);
});

test('32비트 설치 경로도 찾는다', () => {
  const x86Git = 'C:\\Program Files (x86)\\Git\\cmd\\git.exe';
  assert.equal(resolveOnWindows([x86Git]), x86Git);
});

test('시스템 드라이브가 C: 가 아니어도 환경변수를 따라간다', () => {
  const env = { ...WIN_ENV, ProgramFiles: 'D:\\Program Files', SystemRoot: 'D:\\Windows' };
  const dGit = 'D:\\Program Files\\Git\\cmd\\git.exe';
  assert.equal(resolveOnWindows([dGit], { env }), dGit);
});

test('실행 비트가 없어도 거부하지 않는다 (이슈 #40 의 핵심)', () => {
  // Windows 의 Node 는 실행 파일에도 0o100666 을 돌려준다.
  // 모드 비트를 검사하면 모든 후보가 탈락한다.
  const stat = winStat({ mode: 0o100666 });
  assert.equal((stat.mode & 0o111), 0, '전제: 실행 비트가 없다');
  assert.notEqual((stat.mode & 0o022), 0, '전제: 쓰기 비트가 켜져 있다');
  assert.equal(resolveOnWindows([WIN_GIT], { statFor: () => stat }), WIN_GIT);
});

test('gh 와 curl 도 Windows 경로로 찾는다', () => {
  const gh = 'C:\\Program Files\\GitHub CLI\\bin\\gh.exe';
  const curl = 'C:\\Windows\\System32\\curl.exe';
  assert.equal(resolveOnWindows([gh], { command: 'gh' }), gh);
  assert.equal(resolveOnWindows([curl], { command: 'curl' }), curl);
});

/* ------------------------------- 완료 기준 5: 신뢰할 수 없는 것은 계속 거부 */

test('신뢰 루트 밖의 실행 파일은 거부한다', () => {
  const rogue = 'C:\\Users\\dev\\Downloads\\git.exe';
  assert.equal(resolveOnWindows([rogue]), null);
});

test('임시 폴더에 심어 둔 git 도 거부한다', () => {
  const rogue = 'C:\\Temp\\Git\\cmd\\git.exe';
  assert.equal(resolveOnWindows([rogue]), null);
});

test('후보 경로가 심볼릭 링크로 신뢰 루트 밖을 가리키면 거부한다', () => {
  // realpath 가 신뢰 루트 밖으로 해소되는 상황
  const outside = 'C:\\Users\\dev\\evil\\git.exe';
  const resolved = resolveTrustedExecutable('git', {
    platform: 'win32',
    env: WIN_ENV,
    realpathSync: (p) => (p === WIN_GIT ? outside : (() => { throw new Error('ENOENT'); })()),
    statSync: () => winStat(),
    getuid: () => undefined,
  });
  assert.equal(resolved, null);
});

test('디렉터리는 실행 파일로 받아들이지 않는다', () => {
  const stat = winStat({ isFile: () => false });
  assert.equal(resolveOnWindows([WIN_GIT], { statFor: () => stat }), null);
});

test('아무 후보도 없으면 null 을 돌려준다', () => {
  assert.equal(resolveOnWindows([]), null);
});

test('알 수 없는 명령은 null 을 돌려준다', () => {
  assert.equal(resolveOnWindows([WIN_GIT], { command: 'rm' }), null);
});

/* ------------------------------------- 완료 기준 4: Unix 회귀가 깨지지 않는다 */

test('Unix 는 모드 비트 검사를 그대로 유지한다', () => {
  const unixGit = '/usr/bin/git';
  const fs = fakeFs([unixGit], () => ({ isFile: () => true, mode: 0o100666, uid: 0 }));
  // 실행 비트가 없으므로 Unix 에서는 거부되어야 한다
  const resolved = resolveTrustedExecutable('git', {
    platform: 'linux',
    env: {},
    realpathSync: fs.realpathSync,
    statSync: fs.statSync,
    getuid: () => 501,
  });
  assert.equal(resolved, null, 'Unix 에서 실행 비트 없는 파일은 계속 거부된다');
});

test('Unix 는 그룹·기타 쓰기 가능 파일을 계속 거부한다', () => {
  const unixGit = '/usr/bin/git';
  const fs = fakeFs([unixGit], () => ({ isFile: () => true, mode: 0o100777, uid: 0 }));
  const resolved = resolveTrustedExecutable('git', {
    platform: 'linux',
    env: {},
    realpathSync: fs.realpathSync,
    statSync: fs.statSync,
    getuid: () => 501,
  });
  assert.equal(resolved, null, '0o777 은 쓰기 비트가 열려 있어 거부된다');
});

test('Unix 의 정상 git 은 통과한다', () => {
  const unixGit = '/usr/bin/git';
  const fs = fakeFs([unixGit], () => ({ isFile: () => true, mode: 0o100755, uid: 0 }));
  const resolved = resolveTrustedExecutable('git', {
    platform: 'linux',
    env: {},
    realpathSync: fs.realpathSync,
    statSync: fs.statSync,
    getuid: () => 501,
  });
  assert.equal(resolved, unixGit);
});

test('Unix 는 신뢰 루트 밖을 계속 거부한다', () => {
  const rogue = '/tmp/git';
  const fs = fakeFs([rogue], () => ({ isFile: () => true, mode: 0o100755, uid: 0 }));
  const resolved = resolveTrustedExecutable('git', {
    platform: 'linux',
    env: {},
    realpathSync: () => rogue,
    statSync: fs.statSync,
    getuid: () => 501,
  });
  assert.equal(resolved, null);
});

/* --------------------------------------------------------- PATH 구성 */

test('자식 프로세스 PATH 가 플랫폼별로 갈린다', () => {
  const win = trustedCommandPath('win32', WIN_ENV);
  assert.ok(win.includes('C:\\Windows\\System32'));
  assert.ok(win.includes(';'), 'Windows 는 세미콜론으로 구분한다');
  assert.ok(!win.includes('/usr/bin'), 'Unix 경로가 섞이지 않는다');

  const unix = trustedCommandPath('darwin', {});
  assert.equal(unix, '/usr/bin:/usr/sbin:/bin:/sbin:/System/Cryptexes/App/usr/bin');
});

/* ------------------------------------------- 실제 환경에서의 기존 동작 유지 */

test('주입 없이 부르면 현재 플랫폼의 실제 git 을 찾는다', () => {
  const resolved = trustedExecutable('git');
  assert.ok(typeof resolved === 'string' && resolved.length > 0, 'git 을 찾아야 한다');
  assert.equal(resolved, resolveTrustedExecutable('git'), '래퍼와 본체가 같은 값을 준다');
});

/* ------------------------ 완료 기준 4: 미탐지를 "저장소 아님" 으로 오인하지 않는다 */

test('실행 파일 미탐지와 저장소 아님을 구분한다', async () => {
  const { executableMissing, gitFailureMessage } = await import('./issue-common.mjs');

  assert.equal(executableMissing('trusted executable not found: git'), true);
  assert.equal(executableMissing('fatal: not a git repository'), false);
  assert.equal(executableMissing(''), false);
  assert.equal(executableMissing(undefined), false);

  const missing = gitFailureMessage('trusted executable not found: git', 'win32');
  assert.match(missing, /git 실행 파일을 찾지 못했습니다/);
  assert.match(missing, /win32/);
  assert.doesNotMatch(missing, /저장소가 아닙니다/, '미탐지를 저장소 문제로 보고하지 않는다');

  const notRepo = gitFailureMessage('fatal: not a git repository', 'win32');
  assert.match(notRepo, /git 저장소가 아닙니다/);
});
