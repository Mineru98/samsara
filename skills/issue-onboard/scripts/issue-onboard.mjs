#!/usr/bin/env node
/**
 * issue-onboard.mjs — 이슈 그래프 기반 온보딩을 위한 보조 스크립트.
 *
 * 기존 파이프라인(issue-create → issue-start → issue-end → issue-merge)은 이슈를
 * 독립 단위로만 다루고 이슈 사이의 의존/순서를 저장하지 않는다. 이 스크립트는
 * `.issue/graph.json` 에 노드(이슈) + 타입 엣지(의존)를 두고, ready-frontier 와
 * 우선순위를 반영한 todo 를 산출한다.
 *
 * 서브커맨드:
 *   sync                트래커에서 열린·닫힌 이슈를 노드로 갱신하고, 본문의
 *                       "depends on #N" 류 참조를 엣지로 자동 감지한다.
 *   link <from> <to>    from --<type>--> to 엣지를 근거와 함께 추가한다.
 *   unlink <from> <to>  일치하는 엣지를 제거한다.
 *   plan (todo)         위상정렬 + ready/blocked/in-progress/done 분류로 todo 를 낸다.
 *   next                의존·우선순위를 반영해 다음 착수 이슈 1건을 추천한다.
 *   validate            사이클·dangling 엣지·close 불일치를 점검한다.
 *
 * 엣지 방향 규약: `from --depends-on--> to` = "from 은 to 가 close 전엔 착수 불가".
 * blocks 는 depends-on 의 역방향으로 취급한다(A blocks B == B depends-on A).
 * relates-on/parent-of/duplicate-of 는 순서 제약이 아니라 정보성 엣지다.
 *
 * 이슈 백엔드는 ~/.issue/settings.json 의 provider 설정이 정한다(github 기본 | jira).
 * 트래커 호출은 전부 issue-tracker.mjs 를 거친다.
 *
 * 요구사항: git, Node 18+, (github 면 gh 로그인 / jira 면 baseUrl·projectKey·토큰)
 */
import { mkdirSync, writeFileSync, readFileSync, writeSync, existsSync, lstatSync, realpathSync, renameSync, unlinkSync, linkSync, readdirSync, mkdtempSync, rmSync, openSync, closeSync, fstatSync, constants } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { repoRoot, WORKSPACE_DIR, GRAPH_FILE_NAME, isStatusLabel, typeLabels, parseIssueNumber, readIssueSettings, resolveSkillScript } from './issue-common.mjs';
import { createTracker, gitHost } from './issue-tracker.mjs';
import { GRAPH_VERSION as V2_GRAPH_VERSION, EDGE_TYPES as V2_EDGE_TYPES, ORDERING_TYPES as V2_ORDERING_TYPES, CONTEXT_FIELDS, EDGE_CONTEXT_VERSION, DECISION_MARKER, digest, normalizeEdge, edgeKey, parseDecisionComments, decisionEdge, resolveDecisions, auditGraph, migrateGraphV1, kindOfType, extractQuote, sharedConcepts, carryStaleEdges } from './issue-graph-v2.mjs';
import { detectLlmCommand, enrichEdges } from './issue-llm.mjs';
import { fetchBlockedBy, splitSlug } from './issue-native-deps.mjs';

export const GRAPH_VERSION = V2_GRAPH_VERSION;
export const GRAPH_FILE = GRAPH_FILE_NAME;

/** 저장 가능한 엣지 타입. 앞 둘만 순서 제약이다. */
export const EDGE_TYPES = V2_EDGE_TYPES;
export const ORDERING_TYPES = V2_ORDERING_TYPES;

/** 진행 상태를 세 부류로. done 만 "끝난 것"으로 본다. */
const DONE = 'close';
const IN_PROGRESS = new Set(['plan', 'in-process', 'review']);
const DEFAULT_ISSUE_LIST_LIMIT = 200;
const MAX_ISSUE_LIST_LIMIT = 10000;
const BOOTSTRAP_TIMEOUT_MS = 120000;
const TRUSTED_PATH_DIRS = [
  '/usr/bin', '/usr/sbin', '/bin', '/sbin',
  '/System/Cryptexes/App/usr/bin',
];
const TRUSTED_PATH = TRUSTED_PATH_DIRS.join(path.delimiter);
const BOOTSTRAP_ENV_KEYS = [
  'PATH', 'HOME', 'USER', 'LOGNAME', 'TMPDIR', 'TMP', 'TEMP',
  'LANG', 'LC_ALL', 'LC_CTYPE', 'TERM',
  'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME',
  'ISSUE_PROVIDER', 'ISSUE_ONTOLOGY_ROOT', 'GH_HOST',
  'GH_TOKEN', 'GITHUB_TOKEN', 'GH_ENTERPRISE_TOKEN', 'GITHUB_ENTERPRISE_TOKEN',
  'JIRA_API_TOKEN',
];
const BOOTSTRAP_CREDENTIAL_KEYS = new Set([
  'GH_TOKEN', 'GITHUB_TOKEN', 'GH_ENTERPRISE_TOKEN', 'GITHUB_ENTERPRISE_TOKEN',
  'JIRA_API_TOKEN',
]);
const ENV_KEY_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;

function configuredJiraTokenEnv() {
  try {
    const tokenEnv = readIssueSettings()?.provider?.jira?.tokenEnv;
    return ENV_KEY_PATTERN.test(String(tokenEnv ?? '')) ? tokenEnv : null;
  } catch {
    return null;
  }
}
function unknownField(reason, source) { return { value: 'unknown', reason, source }; }

function isPathWithin(parent, target) {
  const relative = path.relative(parent, target);
  return relative === '' || (relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative));
}

function optionalLstat(file) {
  try {
    return lstatSync(file);
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
}

function trustedInstallationRoot(metaUrl = import.meta.url) {
  try {
    return path.resolve(path.dirname(fileURLToPath(metaUrl)), '..', '..', '..');
  } catch {
    return null;
  }
}

function trustedRegularFile(file, root) {
  try {
    const stat = lstatSync(file);
    if (!stat.isFile() || stat.isSymbolicLink()) return false;
    const rootReal = realpathSync(root);
    const fileReal = realpathSync(file);
    return isPathWithin(rootReal, fileReal);
  } catch {
    return false;
  }
}

function ontologyEntry() {
  const installationRoot = trustedInstallationRoot();
  if (!installationRoot) return null;
  const configuredRoot = process.env.ISSUE_ONTOLOGY_ROOT
    ? path.resolve(process.env.ISSUE_ONTOLOGY_ROOT)
    : path.join(installationRoot, 'tools', 'issue-ontology');
  const candidate = path.join(configuredRoot, 'validate.mjs');
  try {
    const realInstallationRoot = realpathSync(installationRoot);
    const realConfiguredRoot = realpathSync(configuredRoot);
    if (!isPathWithin(realInstallationRoot, realConfiguredRoot)) return null;
    return trustedRegularFile(candidate, realInstallationRoot) ? candidate : null;
  } catch {
    return null;
  }
}

const ONTOLOGY_ENTRY = ontologyEntry();
let ontologyModule = null;
let ontologyLoadError = null;
if (ONTOLOGY_ENTRY) {
  try {
    ontologyModule = await import(pathToFileURL(ONTOLOGY_ENTRY).href);
  } catch (error) {
    ontologyLoadError = error;
  }
}

/* --------------------------------------------------------------- graph I/O */

export function graphPath(root) {
  return path.join(root, WORKSPACE_DIR, GRAPH_FILE);
}

export function issueSnapshotDigest(issues = []) {
  return digest([...issues].map((issue) => ({
    number: issue.number,
    updatedAt: issue.updatedAt ?? null,
    body: issue.body ?? '',
    comments: issue.comments ?? [],
  })).sort((a, b) => Number(a.number) - Number(b.number)));
}

export function graphDocumentDigest(graph) {
  const snapshot = { ...(graph.snapshot ?? {}) };
  delete snapshot.graphDigest;
  const nodes = {};
  for (const key of Object.keys(graph.nodes ?? {}).sort((a, b) => Number(a) - Number(b))) {
    nodes[key] = graph.nodes[key];
  }
  const edges = [...(graph.edges ?? [])].map(normalizeEdge).sort((a, b) =>
    a.from - b.from || a.to - b.to || String(a.type).localeCompare(String(b.type)));
  return digest({ ...graph, snapshot, nodes, edges });
}

function safeGraphTarget(root) {
  const resolvedRoot = path.resolve(root);
  let rootStat;
  try {
    rootStat = lstatSync(resolvedRoot);
  } catch (error) {
    throw new Error(`저장소 루트를 확인할 수 없다: ${error.message}`);
  }
  if (rootStat.isSymbolicLink() || !rootStat.isDirectory()) {
    throw new Error('저장소 루트는 심볼릭 링크가 아닌 실제 디렉터리여야 한다.');
  }
  let realRoot;
  try {
    realRoot = realpathSync(resolvedRoot);
  } catch (error) {
    throw new Error(`저장소 루트를 확인할 수 없다: ${error.message}`);
  }

  const issueDir = path.join(resolvedRoot, WORKSPACE_DIR);
  let realIssueDir = issueDir;
  const issueStat = optionalLstat(issueDir);
  if (issueStat) {
    if (issueStat.isSymbolicLink() || !issueStat.isDirectory()) {
      throw new Error(`${WORKSPACE_DIR} 디렉터리는 심볼릭 링크가 아닌 실제 디렉터리여야 한다.`);
    }
    realIssueDir = realpathSync(issueDir);
    if (!isPathWithin(realRoot, realIssueDir)) {
      throw new Error(`${WORKSPACE_DIR} 디렉터리가 저장소 바깥을 가리킨다.`);
    }
    const realIssueStat = lstatSync(realIssueDir);
    if (!sameFile(issueStat, realIssueStat)) {
      throw new Error(`${WORKSPACE_DIR} 디렉터리가 검증 중 변경되었다.`);
    }
  }

  const file = path.join(realIssueDir, GRAPH_FILE);
  const fileStat = optionalLstat(file);
  if (fileStat) {
    if (fileStat.isSymbolicLink() || !fileStat.isFile()) {
      throw new Error(`${WORKSPACE_DIR}/${GRAPH_FILE}은 심볼릭 링크가 아닌 일반 파일이어야 한다.`);
    }
    if (fileStat.nlink !== 1) {
      throw new Error(`${WORKSPACE_DIR}/${GRAPH_FILE}은 다른 경로와 하드링크되지 않은 파일이어야 한다.`);
    }
    const realFile = realpathSync(file);
    if (!isPathWithin(realRoot, realFile)) {
      throw new Error(`${WORKSPACE_DIR}/${GRAPH_FILE}이 저장소 바깥을 가리킨다.`);
    }
  }
  const parentStat = issueStat ? lstatSync(realIssueDir) : null;
  if (issueStat && !sameFile(issueStat, parentStat)) {
    throw new Error(`${WORKSPACE_DIR} 디렉터리가 검증 중 변경되었다.`);
  }
  return { file: path.join(realIssueDir, GRAPH_FILE), parentStat, fileStat };
}

function safeGraphFile(root) {
  return safeGraphTarget(root).file;
}

export function emptyGraph(provider = 'github') {
  return { version: GRAPH_VERSION, provider, repository: null, updatedAt: null, snapshot: { status: 'missing' }, nodes: {}, edges: [] };
}

export function loadGraph(root, provider = 'github', { tolerateParseError = false } = {}) {
  const target = safeGraphTarget(root);
  const content = readStableFile(target);
  if (content === null) return emptyGraph(provider);
  try {
    const g = JSON.parse(content);
    return { ...emptyGraph(provider), ...g, nodes: g.nodes ?? {}, edges: g.edges ?? [] };
  } catch (e) {
    console.error(`✗ ${WORKSPACE_DIR}/${GRAPH_FILE} 파싱 실패: ${e.message}`);
    if (tolerateParseError) return { ...emptyGraph(provider), snapshot: { status: 'invalid', reason: 'graph parse failed' } };
    process.exit(1);
  }
}

function graphCacheFingerprint(root) {
  try {
    const first = readStableFile(safeGraphTarget(root));
    if (first === null) return null;
    const second = readStableFile(safeGraphTarget(root));
    return first === second
      ? digest(first)
      : `unstable:${digest(first)}:${second === null ? 'missing' : digest(second)}`;
  } catch (error) {
    if (error instanceof Error && error.message === '그래프 캐시 파일이 읽기 중 변경되었다.') {
      throw new Error('추천 직전 그래프 캐시가 변경되어 추천하지 않는다.');
    }
    throw error;
  }
}

function noFollowFlags(flags) {
  if (!Number.isInteger(constants.O_NOFOLLOW) || constants.O_NOFOLLOW <= 0) {
    throw new Error('심볼릭 링크를 안전하게 차단하는 파일 플래그를 사용할 수 없다.');
  }
  return flags | constants.O_NOFOLLOW;
}

function sameFile(first, second) {
  return first.dev === second.dev && first.ino === second.ino;
}

function sameFileVersion(first, second) {
  return sameFile(first, second)
    && first.size === second.size
    && first.mtimeMs === second.mtimeMs
    && first.ctimeMs === second.ctimeMs;
}

function withStableRootDirectory(root, action) {
  const resolvedRoot = path.resolve(root);
  const before = lstatSync(resolvedRoot);
  if (before.isSymbolicLink() || !before.isDirectory()) {
    throw new Error('저장소 루트는 심볼릭 링크가 아닌 실제 디렉터리여야 한다.');
  }
  const previousCwd = process.cwd();
  const alreadyInRoot = path.resolve(previousCwd) === resolvedRoot;
  let changedCwd = false;
  try {
    if (!alreadyInRoot) {
      process.chdir(resolvedRoot);
      changedCwd = true;
    }
    if (!sameFile(before, lstatSync('.'))) throw new Error('저장소 루트가 열기 중 변경되었다.');
    const result = action();
    if (!sameFile(before, lstatSync('.'))) throw new Error('저장소 루트가 작업 중 변경되었다.');
    const after = lstatSync(resolvedRoot);
    if (after.isSymbolicLink() || !after.isDirectory() || !sameFile(before, after)) {
      throw new Error('저장소 루트 경로가 작업 중 변경되었다.');
    }
    return result;
  } finally {
    if (changedCwd) process.chdir(previousCwd);
  }
}

function sameGraphTarget(first, second) {
  const sameFileState = first.fileStat && second.fileStat
    ? sameFileVersion(first.fileStat, second.fileStat)
    : !first.fileStat && !second.fileStat;
  return Boolean(first.file === second.file
    && first.parentStat && second.parentStat
    && sameFile(first.parentStat, second.parentStat)
    && sameFileState);
}

// Node 18에는 openat/renameat API가 없다. 모든 graph writer는 동기식이므로
// 검증한 부모 디렉터리를 cwd로 고정한 뒤 상대 경로 syscall을 수행한다.
function withStableParentDirectory(file, action, expectedParent = null, onError = null) {
  const parent = path.dirname(file);
  const before = expectedParent ?? lstatSync(parent);
  if (before.isSymbolicLink() || !before.isDirectory()) {
    throw new Error(`${WORKSPACE_DIR} 상위 디렉터리가 안전한 일반 디렉터리가 아니다.`);
  }
  const previousCwd = process.cwd();
  const alreadyInParent = path.resolve(previousCwd) === path.resolve(parent);
  let changedCwd = false;
  let parentVerified = false;
  try {
    if (!alreadyInParent) {
      process.chdir(parent);
      changedCwd = true;
    }
    if (!sameFile(before, lstatSync('.'))) throw new Error('그래프 캐시 상위 디렉터리가 열기 중 변경되었다.');
    parentVerified = true;
    const result = action(path.basename(file));
    if (!sameFile(before, lstatSync('.'))) throw new Error('그래프 캐시 상위 디렉터리가 작업 중 변경되었다.');
    const after = lstatSync(parent);
    if (after.isSymbolicLink() || !sameFile(before, after)) throw new Error('그래프 캐시 상위 디렉터리 경로가 작업 중 변경되었다.');
    return result;
  } catch (error) {
    if (onError && parentVerified) {
      try { onError(path.basename(file)); } catch { /* 원래 오류를 보존한다 */ }
    }
    throw error;
  } finally {
    if (changedCwd) process.chdir(previousCwd);
  }
}

function readStableFile(target) {
  if (!target.fileStat) {
    if (!target.parentStat) return null;
    return withStableParentDirectory(target.file, (relativeFile) => {
      try {
        lstatSync(relativeFile);
      } catch (error) {
        if (error.code === 'ENOENT') return null;
        throw error;
      }
      throw new Error('그래프 캐시 파일이 읽기 중 변경되었다.');
    }, target.parentStat);
  }

  let fd;
  let verificationFd;
  try {
    return withStableParentDirectory(target.file, (relativeFile) => {
      fd = openSync(relativeFile, noFollowFlags(constants.O_RDONLY));
      const openedFile = fstatSync(fd);
      if (!sameFileVersion(openedFile, target.fileStat) || openedFile.nlink !== 1) {
        throw new Error('그래프 캐시 파일이 열기 중 변경되었다.');
      }
      const content = readFileSync(fd, 'utf8');
      const readFileStat = fstatSync(fd);
      verificationFd = openSync(relativeFile, noFollowFlags(constants.O_RDONLY));
      const verificationFile = fstatSync(verificationFd);
      const verificationContent = readFileSync(verificationFd, 'utf8');
      const verificationStat = fstatSync(verificationFd);
      const pathStat = lstatSync(relativeFile);
      if (!sameFileVersion(readFileStat, openedFile) || readFileStat.nlink !== 1
        || !sameFileVersion(verificationFile, openedFile)
        || !sameFileVersion(verificationStat, verificationFile) || verificationStat.nlink !== 1
        || content !== verificationContent
        || pathStat.isSymbolicLink() || !sameFileVersion(pathStat, target.fileStat) || pathStat.nlink !== 1) {
        throw new Error('그래프 캐시 파일이 읽기 중 변경되었다.');
      }
      return content;
    }, target.parentStat);
  } finally {
    if (verificationFd !== undefined) closeSync(verificationFd);
    if (fd !== undefined) closeSync(fd);
  }
}

function writeAll(fd, content) {
  const bytes = Buffer.from(content);
  let offset = 0;
  while (offset < bytes.length) offset += writeSync(fd, bytes, offset);
}

function removeOwnedFile(relativeFile, expectedFile) {
  try {
    const currentStat = lstatSync(relativeFile);
    if (!currentStat.isSymbolicLink() && currentStat.isFile() && currentStat.nlink === 1
      && sameFileVersion(currentStat, expectedFile)) {
      unlinkSync(relativeFile);
      return true;
    }
  } catch { /* 원래 오류를 보존한다 */ }
  return false;
}

function writeExclusiveFile(file, content, expectedParent = null) {
  let fd;
  let created = false;
  let createdFile;
  try {
    withStableParentDirectory(file, (relativeFile) => {
      fd = openSync(relativeFile, noFollowFlags(constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL), 0o600);
      created = true;
      createdFile = fstatSync(fd);
      try {
        writeAll(fd, content);
      } finally {
        try { createdFile = fstatSync(fd); } catch { /* 원래 오류를 보존한다 */ }
        closeSync(fd);
        fd = undefined;
      }
    }, expectedParent, (relativeFile) => {
      if (created && createdFile && removeOwnedFile(relativeFile, createdFile)) created = false;
    });
  } catch (error) {
    if (fd !== undefined) {
      try { closeSync(fd); } catch { /* 원래 오류를 보존한다 */ }
    }
    if (created && createdFile) {
      try {
        withStableParentDirectory(file, (relativeFile) => {
          if (removeOwnedFile(relativeFile, createdFile)) created = false;
        }, expectedParent);
      } catch { /* 원래 오류를 보존한다 */ }
    }
    throw error;
  }
  return createdFile;
}

function unlinkOwnedWithStableParent(file, expectedFile, expectedParent = null) {
  return withStableParentDirectory(file, (relativeFile) => removeOwnedFile(relativeFile, expectedFile), expectedParent);
}

function renameWithStableParent(source, destination, expectedFile = null, expectedParent = null, expectedSource = null) {
  if (path.resolve(path.dirname(source)) !== path.resolve(path.dirname(destination))) {
    throw new Error('그래프 캐시 임시 파일과 대상 파일의 부모 디렉터리가 다르다.');
  }
  let sourceStat;
  return withStableParentDirectory(source, (relativeSource) => {
    const relativeDestination = path.basename(destination);
    sourceStat = lstatSync(relativeSource);
    if (sourceStat.isSymbolicLink() || !sourceStat.isFile() || sourceStat.nlink !== 1
      || (expectedSource && !sameFileVersion(sourceStat, expectedSource))) {
      throw new Error('그래프 캐시 임시 파일이 안전한 일반 파일이 아니다.');
    }
    let destinationStat;
    try {
      destinationStat = lstatSync(relativeDestination);
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    if (expectedFile) {
      if (!destinationStat || destinationStat.isSymbolicLink() || !destinationStat.isFile()
        || destinationStat.nlink !== 1 || !sameFileVersion(destinationStat, expectedFile)) {
        throw new Error('그래프 캐시 대상 파일이 교체 중 변경되었다.');
      }
    } else if (destinationStat) {
      throw new Error('그래프 캐시 대상 파일이 예기치 않게 생성되었다.');
    }
    renameSync(relativeSource, relativeDestination);
    const replacedStat = lstatSync(relativeDestination);
    if (replacedStat.isSymbolicLink() || !replacedStat.isFile() || replacedStat.nlink !== 1
      || !sameFile(replacedStat, sourceStat)
      || replacedStat.size !== sourceStat.size || replacedStat.mtimeMs !== sourceStat.mtimeMs) {
      throw new Error('그래프 캐시 대상 파일이 교체 중 변경되었다.');
    }
  }, expectedParent, (relativeSource) => {
    if (!sourceStat) return;
    try {
      const currentStat = lstatSync(relativeSource);
      if (!currentStat.isSymbolicLink() && currentStat.isFile() && currentStat.nlink === 1
        && sameFileVersion(currentStat, expectedSource ?? sourceStat)) unlinkSync(relativeSource);
    } catch { /* 원래 오류를 보존한다 */ }
  });
}

function graphCacheOwnerPid(content) {
  const match = /^(\d+):\d+:\d+\n$/.exec(content);
  if (!match) return null;
  const pid = Number(match[1]);
  return Number.isSafeInteger(pid) && pid > 0 ? pid : null;
}

function graphCacheOwnerAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return error.code !== 'ESRCH';
  }
}

function reclaimStaleGraphCacheLock(file, expectedParent) {
  return withStableParentDirectory(file, (relativeFile) => {
    let lockStat;
    try {
      lockStat = lstatSync(relativeFile);
    } catch (error) {
      if (error.code === 'ENOENT') return false;
      throw error;
    }
    if (lockStat.isSymbolicLink() || !lockStat.isFile() || lockStat.nlink !== 1) return false;
    const target = { file, parentStat: expectedParent, fileStat: lockStat };
    const owner = readStableFile(target);
    const pid = graphCacheOwnerPid(owner);
    if (pid === null || graphCacheOwnerAlive(pid)) return false;
    return removeOwnedGraphCacheLock(relativeFile, owner.slice(0, -1));
  }, expectedParent);
}

function createLockQuarantine(relativeFile) {
  const base = path.basename(relativeFile);
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const quarantine = `.${base}.release-${process.pid}-${process.hrtime.bigint()}-${attempt}`;
    let fd;
    try {
      fd = openSync(quarantine, noFollowFlags(constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL), 0o600);
      closeSync(fd);
      return quarantine;
    } catch (error) {
      if (fd !== undefined) {
        try { closeSync(fd); } catch { /* 원래 오류를 보존한다 */ }
      }
      if (error.code !== 'EEXIST') throw error;
    }
  }
  throw new Error('그래프 캐시 lock 임시 경로를 확보하지 못했다.');
}

function restoreLockQuarantine(relativeFile, quarantine) {
  try {
    const quarantined = lstatSync(quarantine);
    if (quarantined.isSymbolicLink() || !quarantined.isFile() || quarantined.nlink !== 1) return false;
    try {
      lstatSync(relativeFile);
      return false;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    linkSync(quarantine, relativeFile);
    const restored = lstatSync(relativeFile);
    const linked = lstatSync(quarantine);
    if (restored.isSymbolicLink() || !restored.isFile()
      || !sameFile(restored, linked) || restored.nlink !== 2 || linked.nlink !== 2) return false;
    unlinkSync(quarantine);
    return true;
  } catch {
    return false;
  }
}

function removeOwnedGraphCacheLock(relativeFile, owner) {
  let fd;
  try {
    fd = openSync(relativeFile, noFollowFlags(constants.O_RDONLY));
    const openedStat = fstatSync(fd);
    if (!openedStat.isFile() || openedStat.nlink !== 1) return false;
    const content = readFileSync(fd, 'utf8');
    const currentStat = fstatSync(fd);
    if (!sameFile(openedStat, currentStat) || currentStat.nlink !== 1 || content !== `${owner}\n`) return false;
    closeSync(fd);
    fd = undefined;
    const quarantine = createLockQuarantine(relativeFile);
    try {
      renameSync(relativeFile, quarantine);
    } catch (error) {
      try { unlinkSync(quarantine); } catch { /* 원래 오류를 보존한다 */ }
      if (error.code === 'ENOENT') return false;
      throw error;
    }
    let detachedFd;
    let detachedOwned = false;
    try {
      detachedFd = openSync(quarantine, noFollowFlags(constants.O_RDONLY));
      const detachedStat = fstatSync(detachedFd);
      const detachedContent = readFileSync(detachedFd, 'utf8');
      const latestDetachedStat = fstatSync(detachedFd);
      detachedOwned = latestDetachedStat.nlink === 1
        && sameFile(detachedStat, openedStat)
        && sameFile(latestDetachedStat, openedStat)
        && detachedContent === `${owner}\n`;
    } catch {
      detachedOwned = false;
    } finally {
      if (detachedFd !== undefined) closeSync(detachedFd);
    }
    if (detachedOwned) {
      try { unlinkSync(quarantine); } catch { /* 경쟁자가 바꾼 quarantine을 보존한다 */ }
      return true;
    }
    // quarantine이 경쟁 중 바뀌었으면 canonical 경로를 원자적으로 복구한다.
    // 복구가 불확실하면 quarantine을 남겨 다음 writer도 fail closed 한다.
    restoreLockQuarantine(relativeFile, quarantine);
    return false;
  } catch (error) {
    if (error.code === 'ENOENT') return false;
    throw error;
  } finally {
    if (fd !== undefined) closeSync(fd);
  }
}

function lockQuarantineExists(file, expectedParent) {
  return withStableParentDirectory(file, (relativeFile) => {
    const prefix = `.${path.basename(relativeFile)}.release-`;
    return readdirSync('.').some((name) => name.startsWith(prefix));
  }, expectedParent);
}

function acquireGraphCacheLock(root) {
  const initialTarget = safeGraphTarget(root);
  const lockFile = `${initialTarget.file}.lock`;
  if (!initialTarget.parentStat) {
    withStableRootDirectory(root, () => mkdirSync(WORKSPACE_DIR, { recursive: true }));
  }
  const lockTarget = safeGraphTarget(root);
  if (initialTarget.parentStat && (!lockTarget.parentStat || !sameFile(initialTarget.parentStat, lockTarget.parentStat))) {
    throw new Error('그래프 캐시 상위 디렉터리가 검증 중 변경되었다.');
  }
  const parentStat = lockTarget.parentStat ?? lstatSync(path.dirname(lockFile));
  const owner = `${process.pid}:${Date.now()}:${process.hrtime.bigint()}`;
  if (lockQuarantineExists(lockFile, parentStat)) {
    throw new Error('그래프 캐시가 다른 작업에서 변경 중이라 갱신하지 않는다.');
  }
  try {
    writeExclusiveFile(lockFile, `${owner}\n`, parentStat);
  } catch (error) {
    if (error.code !== 'EEXIST') throw error;
    if (!reclaimStaleGraphCacheLock(lockFile, parentStat)) {
      throw new Error('그래프 캐시가 다른 작업에서 변경 중이라 추천하지 않는다.');
    }
    try {
      writeExclusiveFile(lockFile, `${owner}\n`, parentStat);
    } catch (retryError) {
      if (retryError.code === 'EEXIST') throw new Error('그래프 캐시가 다른 작업에서 변경 중이라 추천하지 않는다.');
      throw retryError;
    }
  }
  let released = false;
  const release = (inVerifiedParent = false) => {
    if (released) return;
    released = true;
    if (inVerifiedParent) {
      removeOwnedGraphCacheLock(path.basename(lockFile), owner);
      return;
    }
    try {
      withStableParentDirectory(lockFile, (relativeFile) => {
        removeOwnedGraphCacheLock(relativeFile, owner);
      }, parentStat, (relativeFile) => {
        removeOwnedGraphCacheLock(relativeFile, owner);
      });
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  };
  release.file = lockFile;
  release.parentStat = parentStat;
  return release;
}

function withGraphCacheLock(root, action) {
  const release = acquireGraphCacheLock(root);
  try {
    return withStableParentDirectory(release.file, () => action(), release.parentStat, () => release(true));
  } finally {
    release();
  }
}

function assertGraphCacheStable(root, expected) {
  if (graphCacheFingerprint(root) !== expected) {
    throw new Error('추천 직전 그래프 캐시가 변경되어 추천하지 않는다.');
  }
}

function emitStableOutput(root, expected, output) {
  if (graphCacheFingerprint(root) !== expected) {
    throw new Error('추천 직전 그래프 캐시가 변경되어 추천하지 않는다.');
  }
  const bytes = Buffer.from(`${output}\n`);
  let offset = 0;
  while (offset < bytes.length) offset += writeSync(1, bytes, offset);
  assertGraphCacheStable(root, expected);
}

/** 결정적 순서로 저장한다 — diff 가 안정되도록 노드는 번호순, 엣지는 (from,to,type) 순. */
export function saveGraph(root, graph, { now } = {}) {
  return withGraphCacheLock(root, () => {
    const nodes = {};
    for (const k of Object.keys(graph.nodes).sort((a, b) => Number(a) - Number(b))) nodes[k] = graph.nodes[k];
    const edges = [...graph.edges].map(normalizeEdge).sort((a, b) =>
      a.from - b.from || a.to - b.to || String(a.type).localeCompare(String(b.type)));
    const out = { ...graph, version: GRAPH_VERSION, updatedAt: now ?? graph.updatedAt, nodes, edges };
    const target = safeGraphTarget(root);
    const file = target.file;
    const temporary = `${file}.tmp-${process.pid}`;
    let temporaryCreated = false;
    let temporaryFile;
    try {
      withStableParentDirectory(temporary, () => {
        temporaryFile = writeExclusiveFile(temporary, `${JSON.stringify(out, null, 2)}\n`, target.parentStat);
        temporaryCreated = true;
        const finalTarget = safeGraphTarget(root);
        if (!sameGraphTarget(target, finalTarget)) {
          throw new Error('그래프 캐시 대상 파일이 교체 중 변경되었다.');
        }
        renameWithStableParent(temporary, file, target.fileStat, target.parentStat, temporaryFile);
        temporaryCreated = false;
      }, target.parentStat, (relativeTemporary) => {
        if (temporaryCreated && temporaryFile && removeOwnedFile(relativeTemporary, temporaryFile)) {
          temporaryCreated = false;
        }
      });
    } catch (error) {
      if (temporaryCreated && temporaryFile) {
        try {
          withStableParentDirectory(temporary, (relativeTemporary) => {
            if (removeOwnedFile(relativeTemporary, temporaryFile)) temporaryCreated = false;
          }, target.parentStat);
        } catch { /* 원래 오류를 보존한다 */ }
      }
      throw error;
    }
    return file;
  });
}

/* --------------------------------------------------------------- 상태 파생 */

/** 트래커의 종료 state를 먼저 반영하고, 열린 이슈만 status:* 라벨로 세부 상태를 정한다. */
export function deriveStatus(labels = [], state) {
  if (['CLOSED', 'MERGED'].includes(String(state ?? '').toUpperCase())) return 'close';
  const status = labels.map((l) => (typeof l === 'string' ? l : l.name)).find(isStatusLabel);
  if (status) return status.slice('status:'.length);
  return 'open';
}

/** 노드에서 우선순위 랭크를 뽑는다. P0=0 … 라벨 없으면 뒤로. */
export function priorityRank(node) {
  if (typeof node.priority === 'number') return node.priority;
  for (const name of node.labels ?? []) {
    const m = /^p([0-3])$/i.exec(name);
    if (m) return Number(m[1]);
  }
  return 9;
}

/* --------------------------------------------------------- 의존 그래프 계산 */

/**
 * 순서 제약 엣지를 "prereq(선행) 맵" 으로 정규화한다.
 * depends-on {from,to} → from 의 선행에 to.
 * blocks     {from,to} → to 의 선행에 from.
 * 반환: Map<number, Set<number>> — 노드 → 선행 노드 집합.
 */
export function prereqMap(graph) {
  const map = new Map();
  const add = (node, dep) => {
    if (!map.has(node)) map.set(node, new Set());
    map.get(node).add(dep);
  };
  for (const num of Object.keys(graph.nodes)) map.set(Number(num), map.get(Number(num)) ?? new Set());
  for (const e of graph.edges) {
    if (e.type === 'depends-on') add(e.from, e.to);
  }
  return map;
}

/** 순서 엣지에서 사이클을 찾는다. 있으면 노드 배열(사이클 경로), 없으면 null. */
export function findCycle(graph) {
  const prereq = prereqMap(graph);
  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map();
  const stack = [];
  let cycle = null;
  const visit = (n) => {
    if (cycle) return;
    color.set(n, GRAY);
    stack.push(n);
    for (const dep of prereq.get(n) ?? []) {
      if (!prereq.has(dep)) continue; // 알 수 없는 노드는 dangling — validate 가 따로 잡는다
      const c = color.get(dep) ?? WHITE;
      if (c === GRAY) { cycle = [...stack.slice(stack.indexOf(dep)), dep]; return; }
      if (c === WHITE) visit(dep);
      if (cycle) return;
    }
    stack.pop();
    color.set(n, BLACK);
  };
  for (const n of prereq.keys()) if ((color.get(n) ?? WHITE) === WHITE) visit(n);
  return cycle;
}

/**
 * 각 노드를 ready / blocked / in-progress / done 으로 분류한다.
 * blocked: 선행 중 close 가 아닌 것이 있음.
 * in-progress: plan/in-process/review.
 * ready: open + 선행이 전부 close.
 */
export function classify(graph) {
  const prereq = prereqMap(graph);
  const statusOf = (num) => graph.nodes[String(num)]?.status ?? 'open';
  const out = { ready: [], blocked: [], inProgress: [], done: [] };
  for (const num of Object.keys(graph.nodes).map(Number)) {
    const st = statusOf(num);
    if (st === DONE) { out.done.push(num); continue; }
    // 그래프에 없는 선행(다른 저장소 참조·오타·fetch 창 밖)은 blocker 로 치지 않는다.
    // 안 그러면 그 이슈가 영원히 blocked 로 남아 ready/next 에서 사라진다. dangling 은 validate 가 계속 경고한다.
    const blockers = [...(prereq.get(num) ?? [])].filter((d) => graph.nodes[String(d)] && statusOf(d) !== DONE);
    if (blockers.length) { out.blocked.push({ num, blockers }); continue; }
    if (IN_PROGRESS.has(st)) { out.inProgress.push(num); continue; }
    out.ready.push(num);
  }
  const byPrio = (a, b) => priorityRank(graph.nodes[String(a)]) - priorityRank(graph.nodes[String(b)]) || a - b;
  out.ready.sort(byPrio);
  out.inProgress.sort(byPrio);
  out.blocked.sort((a, b) => byPrio(a.num, b.num));
  out.done.sort((a, b) => a - b);
  return out;
}

/* --------------------------------------------------------------- sync 파싱 */

/** 본문에서 의존 참조를 뽑는다. 반환: [{ type, to, index, matched }]. NFC 정규화된 본문 기준 offset. */
export function parseDependencies(body = '') {
  const refs = [];
  const text = String(body).normalize('NFC');
  const grab = (re, type) => {
    let m;
    while ((m = re.exec(text)) !== null) refs.push({ type, to: Number(m[1]), index: m.index, matched: m[0] });
  };
  // "depends on #N", "depends-on #N", "blocked by #N", "needs #N" → depends-on
  grab(/\bdepends[\s-]?on\s+#(\d{1,6})/gi, 'depends-on');
  grab(/\bblocked[\s-]?by\s+#(\d{1,6})/gi, 'depends-on');
  grab(/\bneeds\s+#(\d{1,6})/gi, 'depends-on');
  let m;
  const blocks = /\bblocks\s+#(\d{1,6})/gi;
  while ((m = blocks.exec(text)) !== null) refs.push({ type: 'depends-on', from: Number(m[1]), reverse: true, index: m.index, matched: m[0] });
  return refs;
}

/**
 * GitHub 네이티브 의존성(blocked-by)을 depends-on 후보로 모은다.
 * X 가 Y 에 blocked-by 면 Y 가 선수 → { from: X, to: Y }.
 * seen 에 이미 있는 키(본문 마커가 만든 엣지)는 건너뛰어 본문 근거를 우선한다.
 * 어떤 실패든 전체 snapshot을 partial로 만든다: API 미지원이거나 한 노드라도 실패하면 중단한다.
 */
export function collectNativeDependencies({ list = [], seen = new Set(), owner, repo, root, fetch = fetchBlockedBy } = {}) {
  const stats = { queried: 0, edges: 0, skipped: null };
  const candidates = [];
  if (!owner || !repo) { stats.skipped = 'repo-unknown'; return { candidates, stats }; }
  for (const it of list) {
    const res = fetch({ owner, repo, number: it.number, cwd: root });
    if (res && res.unsupported) { stats.skipped = 'api-unsupported'; break; }
    if (!res || !Array.isArray(res.numbers)) {
      stats.skipped = 'unavailable';
      break;
    }
    stats.queried += 1;
    for (const dep of res.numbers) {
      const from = it.number;
      const to = dep;
      if (to === from) continue;
      const key = `${from}|${to}|depends-on`;
      if (seen.has(key)) continue;
      seen.add(key);
      candidates.push({ from, to });
      stats.edges += 1;
    }
  }
  return { candidates, stats };
}

/** 네이티브 의존성 후보를 그래프 엣지(depends-on)로 만든다. graph-v2 스키마와 일치해야 한다. */
export function buildNativeEdge({ from, to, toClosed = false, url = null, now }) {
  return {
    from, to, type: 'depends-on', kind: 'blocked-by',
    rationale: `#${from} 은(는) GitHub 네이티브 의존성에서 #${to} 에 blocked-by`,
    context: { summary: `#${from} 이(가) #${to} 에 blocked-by (GitHub 네이티브 의존성)`, label: `blocked-by #${to}`, keywords: [], sharedConcepts: [], generatedBy: 'github-native', confidence: 'high', generatedAt: now },
    evidence: [],
    status: toClosed ? 'resolved' : 'active',
    schemaVersion: EDGE_CONTEXT_VERSION,
    createdBy: 'github-native', createdAt: now,
    provenance: { url, digest: digest({ nativeDependency: true, from, to }) },
  };
}

/** 라이브 이슈 본문·승인 코멘트에서 재현할 수 있는 관계 키를 만든다. */
export function issueSourceEdgeKeys(issues = []) {
  const keys = new Set();
  const decisions = [];
  for (const issue of issues) {
    for (const ref of parseDependencies(issue.body ?? '')) {
      const from = ref.reverse ? ref.from : issue.number;
      const to = ref.reverse ? issue.number : ref.to;
      if (to !== from) keys.add(edgeKey({ from, to, type: ref.type }));
    }
    decisions.push(...parseDecisionComments(issue.comments ?? []));
  }
  for (const edge of resolveDecisions(decisions).map(decisionEdge).filter(Boolean)) {
    keys.add(edgeKey(edge));
  }
  return keys;
}

/** 라이브 이슈에서 관계의 출처·근거를 재구성한다. 키만 비교하면 캐시가
 * self-digest 를 다시 계산해 rationale/createdBy/provenance 를 위조할 수 있다. */
function issueSourceEdgeDescriptors(issues = []) {
  const sources = new Map();
  for (const issue of issues) {
    for (const ref of parseDependencies(issue.body ?? '')) {
      const from = ref.reverse ? ref.from : issue.number;
      const to = ref.reverse ? issue.number : ref.to;
      if (to === from) continue;
      const key = edgeKey({ from, to, type: ref.type });
      if (sources.has(key)) continue;
      const extracted = extractQuote(issue.body ?? '', ref.index, ref.matched.length);
      const summary = `#${issue.number} 본문이 "${ref.matched}" 로 #${ref.reverse ? from : to} 을(를) 참조`;
      sources.set(key, {
        createdBy: 'sync',
        rationale: summary,
        provenance: { url: issue.url ?? null, digest: digest(issue.body ?? '') },
        evidence: extracted ? [{
          issue: issue.number,
          field: 'body',
          commentId: null,
          author: issue.author?.login ?? issue.author ?? null,
          authoredAt: issue.createdAt ?? null,
          quote: extracted.quote,
          start: extracted.start,
          end: extracted.end,
          url: issue.url,
          digest: digest(issue.body ?? ''),
        }] : [],
      });
    }
  }
  const decisions = issues.flatMap((issue) => parseDecisionComments(issue.comments ?? []));
  for (const decision of resolveDecisions(decisions)) {
    const edge = decisionEdge(decision);
    if (!edge) continue;
    const key = edgeKey(edge);
    if (!sources.has(key)) sources.set(key, {
      createdBy: edge.createdBy,
      rationale: edge.rationale ?? '',
      decisionId: edge.decisionId,
      provenance: {
        url: edge.provenance?.url ?? null,
        digest: edge.provenance?.digest ?? null,
        graphRevision: edge.provenance?.graphRevision ?? null,
        evidence: edge.provenance?.evidence ?? null,
      },
    });
  }
  return sources;
}

function issueListLimit(value) {
  const limit = Number(value);
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > MAX_ISSUE_LIST_LIMIT) {
    throw new Error(`이슈 목록 limit은 1-${MAX_ISSUE_LIST_LIMIT} 사이의 정수여야 한다.`);
  }
  return limit;
}

export function fetchCompleteIssueList(tracker, options = {}) {
  let limit = options.initialLimit ?? DEFAULT_ISSUE_LIST_LIMIT;
  const request = { ...options };
  delete request.initialLimit;
  if (tracker.provider === 'jira' && typeof tracker.issueListPage === 'function') {
    let startAt = 0;
    const items = [];
    for (;;) {
      const page = tracker.issueListPage({ ...request, limit, startAt });
      if (!page || !Array.isArray(page.items)) return { items: null, complete: false };
      items.push(...page.items);
      if (page.complete === true) return { items, complete: true };
      if (!Number.isSafeInteger(page.total) || page.total < 0) return { items, complete: false };
      const pageStart = Number.isSafeInteger(page.startAt) ? page.startAt : startAt;
      if (pageStart !== startAt || page.items.length > limit) return { items, complete: false };
      const nextStart = pageStart + page.items.length;
      if (!page.items.length || nextStart <= startAt || nextStart > MAX_ISSUE_LIST_LIMIT) {
        return { items, complete: false };
      }
      if (nextStart >= page.total) return { items, complete: false };
      startAt = nextStart;
    }
  }
  for (;;) {
    const list = tracker.issueList({ ...request, limit });
    if (list === null) return { items: null, complete: false };
    if (list.length < limit) return { items: list, complete: true };
    if (limit >= MAX_ISSUE_LIST_LIMIT) return { items: list, complete: false };
    limit = Math.min(limit * 2, MAX_ISSUE_LIST_LIMIT);
  }
}

function issueIsOpen(issue) {
  return !['CLOSED', 'MERGED'].includes(String(issue.state ?? '').toUpperCase());
}

/* ------------------------------------------------------------------- 명령 */

function cmdSync(root, tracker, opts) {
  const state = opts.state ?? 'all';
  const limit = issueListLimit(opts.limit ?? DEFAULT_ISSUE_LIST_LIMIT);
  let listResult;
  if (opts.limit == null) {
    listResult = fetchCompleteIssueList(tracker, {
      state,
      initialLimit: limit,
      fields: 'number,title,labels,url,state,body,comments,updatedAt,author,createdAt',
    });
  } else if (tracker.provider === 'jira' && typeof tracker.issueListPage === 'function') {
    const page = tracker.issueListPage({
      state,
      limit,
      fields: 'number,title,labels,url,state,body,comments,updatedAt,author,createdAt',
    });
    listResult = { items: page?.items ?? null, complete: page?.complete === true };
  } else {
    const items = tracker.issueList({
      state,
      limit,
      fields: 'number,title,labels,url,state,body,comments,updatedAt,author,createdAt',
    });
    listResult = { items, complete: items !== null && items.length < limit };
  }
  const list = listResult.items;
  if (list === null) {
    console.log('SYNCED=0');
    console.log('SYNC_FAILED=1');
    return;
  }
  const graph = loadGraph(root, tracker.provider, { tolerateParseError: true });
  graph.version = GRAPH_VERSION;
  graph.provider = tracker.provider;
  graph.repository = opts.repo
    ?? (tracker.provider === 'github' ? tracker.repository ?? gitHost.repoInfo(root)?.nameWithOwner : null);
  graph.nodes = {};

  const now = opts.now ?? new Date().toISOString();
  // 노드 갱신 (제목·상태·라벨·url 은 트래커가 정본).
  for (const it of list) {
    const labels = (it.labels ?? []).map((l) => l.name);
    const source = { url: it.url, revision: it.updatedAt ?? 'unknown', observedAt: now };
    graph.nodes[String(it.number)] = {
      id: `github:${graph.repository ?? 'unknown'}#${it.number}`,
      number: it.number,
      title: it.title,
      status: deriveStatus(it.labels ?? [], it.state),
      labels: typeLabels(labels),
      url: it.url,
      context: Object.fromEntries(CONTEXT_FIELDS.map((field) => [field, unknownField(`GitHub 본문에 구조화된 ${field} 필드가 없음`, source)])),
      provenance: source,
    };
  }

  // V2 캐시는 GitHub에서 다시 만들 수 있는 auto/decision 엣지만 보관한다.
  const previousEdges = [...(graph.edges ?? []), ...(graph.staleEdges ?? [])];
  const candidates = [];
  const decisions = [];
  const seen = new Set();
  const itemByNumber = new Map(list.map((it) => [it.number, it]));
  for (const it of list) {
    for (const ref of parseDependencies(it.body ?? '')) {
      const from = ref.reverse ? ref.from : it.number;
      const to = ref.reverse ? it.number : ref.to;
      if (to === from) continue;
      const key = `${from}|${to}|${ref.type}`;
      if (seen.has(key)) continue;
      seen.add(key);
      candidates.push({ from, to, type: ref.type, ref, it });
    }
    decisions.push(...parseDecisionComments(it.comments ?? []));
  }
  // 하이브리드: GitHub 네이티브 의존성(blocked-by)도 depends-on 후보로 읽는다. 본문 마커가
  // 이미 만든 엣지는 seen 으로 걸러 우선하고, 조회 실패는 snapshot을 partial로 만든다.
  const nativeSlug = splitSlug(opts.repo ?? graph.repository ?? '');
  const native = tracker.provider !== 'github'
    ? { candidates: [], stats: { queried: 0, edges: 0, skipped: null } }
    : opts.noNative
    ? { candidates: [], stats: { queried: 0, edges: 0, skipped: 'disabled-by-flag' } }
    : collectNativeDependencies({ list, seen, owner: nativeSlug?.owner, repo: nativeSlug?.repo, root });
  const referenced = [...new Set([...candidates, ...native.candidates].flatMap((c) => [c.from, c.to]))].filter((number) => !graph.nodes[String(number)]);
  const unresolved = [];
  for (const number of referenced) {
    const item = tracker.issueView(number);
    if (!item) { unresolved.push(number); continue; }
    itemByNumber.set(number, item);
    const labels = (item.labels ?? []).map((label) => label.name);
    const source = { url: item.url, revision: item.updatedAt ?? 'unknown', observedAt: now, kind: 'referenced' };
    graph.nodes[String(number)] = { id: `github:${graph.repository ?? 'unknown'}#${number}`, number, title: item.title, status: deriveStatus(item.labels ?? [], item.state), labels: typeLabels(labels), url: item.url, context: Object.fromEntries(CONTEXT_FIELDS.map((field) => [field, unknownField(`참조된 GitHub 항목의 구조화된 ${field} 필드가 없음`, source)])), provenance: source };
  }
  // 결정론 근거 조립: #N 참조 주변 문장을 verbatim 발췌하고 공유 개념을 추출한다 (#93).
  const auto = candidates.map(({ from, to, type, ref, it }) => {
    const other = itemByNumber.get(ref.reverse ? from : to);
    const extracted = extractQuote(it.body ?? '', ref.index, ref.matched.length);
    const shared = sharedConcepts(
      { body: it.body, title: it.title, labels: (it.labels ?? []).map((label) => label.name ?? label) },
      other ? { body: other.body, title: other.title, labels: (other.labels ?? []).map((label) => label.name ?? label) } : {},
    );
    const summary = `#${it.number} 본문이 "${ref.matched}" 로 #${ref.reverse ? from : to} 을(를) 참조`;
    const toClosed = graph.nodes[String(to)]?.status === 'close';
    return {
      from, to, type,
      kind: kindOfType(type),
      rationale: summary,
      context: { summary, label: ref.matched.slice(0, 20), keywords: shared.slice(0, 4), sharedConcepts: shared, generatedBy: 'deterministic', confidence: extracted ? 'high' : 'low', generatedAt: now },
      evidence: extracted ? [{ issue: it.number, field: 'body', commentId: null, author: it.author?.login ?? it.author ?? null, authoredAt: it.createdAt ?? null, quote: extracted.quote, start: extracted.start, end: extracted.end, url: it.url, digest: digest(it.body ?? '') }] : [],
      status: type === 'depends-on' && toClosed ? 'resolved' : 'active',
      schemaVersion: EDGE_CONTEXT_VERSION,
      createdBy: 'sync', createdAt: now,
      provenance: { url: it.url, digest: digest(it.body ?? '') },
    };
  });
  const nativeEdges = native.candidates.map(({ from, to }) => buildNativeEdge({
    from, to,
    toClosed: graph.nodes[String(to)]?.status === 'close',
    url: itemByNumber.get(from)?.url ?? graph.nodes[String(from)]?.url ?? null,
    now,
  }));
  const approved = resolveDecisions(decisions).map(decisionEdge).filter(Boolean).filter((edge) => { const key = edgeKey(edge); if (seen.has(key)) return false; seen.add(key); return true; });
  graph.edges = [...auto, ...nativeEdges, ...approved];
  // 재감지되지 않은 sync 엣지는 삭제하지 않고 stale 로 이관한다 — 소비 코드는 graph.edges 만 읽으므로 스케줄링에 영향 없음 (#93).
  graph.staleEdges = carryStaleEdges(previousEdges, new Set(graph.edges.map(edgeKey)), now);
  // LLM 보강 패스 (#94) — 실패·부재 시 결정론 산출물 유지, sync 는 정상 진행.
  let llmStats = { enriched: 0, cached: 0, discarded: 0, skipped: opts.noLlm ? 'disabled-by-flag' : null };
  if (!opts.noLlm) {
    const command = detectLlmCommand();
    const result = enrichEdges(graph.edges, { itemByNumber, previousEdges, command });
    graph.edges = result.edges;
    llmStats = result.stats;
  }
  const nativeComplete = opts.noNative || !native.stats.skipped;
  const complete = state === 'all' && listResult.complete && unresolved.length === 0 && nativeComplete;
  graph.updatedAt = now;
  graph.snapshot = {
    status: complete ? 'complete' : 'partial',
    fetchedAt: now,
    digest: issueSnapshotDigest(list),
    reason: complete ? null : native.stats.skipped
      ? `GitHub 네이티브 의존성 조회 실패: ${native.stats.skipped}`
      : unresolved.length
      ? `참조 GitHub 항목을 조회할 수 없음: ${unresolved.map((number) => `#${number}`).join(', ')}`
      : 'state filter, limit, 또는 전체 목록 증명 실패',
  };
  graph.snapshot.graphDigest = graphDocumentDigest(graph);

  const file = saveGraph(root, graph, { now });
  const cycle = findCycle(graph);

  console.log(`✓ sync 완료 — 노드 ${Object.keys(graph.nodes).length}개, 엣지 ${graph.edges.length}개 (자동 ${auto.length}, 네이티브 ${nativeEdges.length}, 승인 ${approved.length})`);
  console.log(`  저장: ${path.relative(root, file)}`);
  if (cycle) console.log(`  ! 경고: 순환 의존 감지 ${cycle.join(' → ')} (validate 로 확인)`);
  console.log('');
  console.log(`SYNCED=${Object.keys(graph.nodes).length}`);
  console.log(`EDGES=${graph.edges.length}`);
  console.log(`AUTO_EDGES=${auto.length}`);
  console.log(`NATIVE_EDGES=${nativeEdges.length}`);
  console.log(`NATIVE_QUERIED=${native.stats.queried}`);
  console.log(`NATIVE_SKIPPED=${native.stats.skipped ?? ''}`);
  console.log(`DECISION_EDGES=${approved.length}`);
  console.log(`RESOLVED_REFERENCES=${referenced.length - unresolved.length}`);
  console.log(`UNRESOLVED_REFERENCES=${unresolved.join(' ')}`);
  console.log(`SNAPSHOT_STATUS=${graph.snapshot.status}`);
  console.log(`LLM_ENRICHED=${llmStats.enriched}`);
  console.log(`LLM_CACHED=${llmStats.cached}`);
  console.log(`LLM_DISCARDED=${llmStats.discarded}`);
  console.log(`LLM_SKIPPED=${llmStats.skipped ?? ''}`);
  console.log(`CYCLE=${cycle ? cycle.join('>') : ''}`);
}

/** 결정 코멘트 본문을 만든다. sync 의 parseDecisionComments 가 그대로 파싱한다. */
export function decisionCommentBody(payload, human) {
  return `<!-- ${DECISION_MARKER}\n${JSON.stringify(payload)}\n-->\n\n${human}`;
}

/** 결정 코멘트를 대상 이슈에 게시한다. 임시 파일을 거쳐 tracker.issueComment 로 올린다. */
function postDecision(tracker, issueNumber, payload, human) {
  const directory = mkdtempSync(path.join(tmpdir(), 'issue-relation-'));
  const file = path.join(directory, 'body.md');
  try {
    writeFileSync(file, decisionCommentBody(payload, human), { encoding: 'utf8', flag: 'wx', mode: 0o600 });
    return tracker.issueComment(issueNumber, file);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

// V2 는 GitHub 을 정본으로 두고 graph.json 은 재생성 캐시다. 그래서 link 는 로컬 엣지를
// 쓰는 대신, sync 가 읽는 구조화된 승인 결정 코멘트를 대상(from) 이슈에 남기고 다시
// sync 해 엣지를 실체화한다. 순서 엣지(depends-on)는 순환이 되면 게시 전에 거부한다.
function cmdLink(root, tracker, from, to, opts) {
  if (from == null || to == null) { console.error('✗ from 과 to 이슈 번호가 필요하다 (예: link 61 60)'); process.exit(1); }
  const type = opts.type ?? 'depends-on';
  if (!EDGE_TYPES.includes(type)) {
    console.error(`✗ 알 수 없는 엣지 타입: ${type} — ${EDGE_TYPES.join(' | ')} 중 하나`);
    console.log('LINKED=0');
    process.exit(1);
  }
  if (from === to) { console.error('✗ 자기 자신을 가리키는 엣지는 만들 수 없다.'); console.log('LINKED=0'); process.exit(1); }

  const graph = loadGraph(root, tracker.provider);
  // 순서 엣지는 순환을 만들면 게시하지 않는다 (dag-ops 의 계약).
  if (ORDERING_TYPES.has(type)) {
    const cycle = findCycle({ nodes: graph.nodes ?? {}, edges: [...(graph.edges ?? []), { from, to, type }] });
    if (cycle) { console.error(`✗ 순환이 되어 거부: ${cycle.join(' → ')}`); console.log('LINKED=0'); process.exit(2); }
  }

  const fromView = tracker.issueView(from);
  const toView = tracker.issueView(to);
  if (!fromView) { console.error(`✗ #${from} 이슈를 조회할 수 없다.`); console.log('LINKED=0'); process.exit(1); }
  if (!toView) { console.error(`✗ #${to} 이슈를 조회할 수 없다.`); console.log('LINKED=0'); process.exit(1); }

  const evidence = [fromView.url, toView.url].filter(Boolean);
  if (!evidence.length) evidence.push(`#${from} → #${to}`);
  const graphRevision = graph.snapshot?.digest ?? digest(graph);
  const id = `relation-${from}-${to}-${type}`;
  const payload = {
    version: 1, id, action: 'relation', decision: 'approved',
    type, from, to, graphRevision, rationale: opts.why ?? '', evidence,
  };
  const human = `관계 승인: #${from} --${type}--> #${to}${opts.why ? `\n\n근거: ${opts.why}` : ''}`;
  const posted = postDecision(tracker, from, payload, human);
  if (!posted.ok) { console.error(`✗ 결정 코멘트 게시 실패: ${posted.err ?? ''}`); console.log('LINKED=0'); process.exit(1); }

  console.log(`✓ #${from} 에 관계 승인 코멘트 게시 (${id})`);
  console.log('  그래프 재동기화 중...');
  console.log('');
  cmdSync(root, tracker, opts);
  console.log('');
  console.log('LINKED=1');
  console.log(`LINK=${from}|${to}|${type}`);
  console.log(`DECISION_ID=${id}`);
}

// unlink 는 같은 id 의 revoked 결정 코멘트를 남긴다. resolveDecisions 가 최신 결정을
// 골라 revoked 를 걸러내므로 다음 sync 에서 결정 엣지가 사라진다. 단, 본문 마커
// (`depends on #N`)나 GitHub 네이티브 의존성으로 생긴 엣지는 이 방식으로 지워지지
// 않는다 — 본문을 고치거나 GitHub 에서 의존성 링크를 직접 해제해야 한다.
function cmdUnlink(root, tracker, from, to, opts) {
  if (from == null || to == null) { console.error('✗ from 과 to 이슈 번호가 필요하다.'); console.log('UNLINKED=0'); process.exit(1); }
  const type = opts.type ?? 'depends-on';
  if (!EDGE_TYPES.includes(type)) {
    console.error(`✗ 알 수 없는 엣지 타입: ${type} — ${EDGE_TYPES.join(' | ')} 중 하나`);
    console.log('UNLINKED=0');
    process.exit(1);
  }
  const id = `relation-${from}-${to}-${type}`;
  const payload = { version: 1, id, action: 'relation', decision: 'revoked', type, from, to };
  const human = `관계 철회: #${from} --${type}--> #${to}`;
  const posted = postDecision(tracker, from, payload, human);
  if (!posted.ok) { console.error(`✗ 철회 코멘트 게시 실패: ${posted.err ?? ''}`); console.log('UNLINKED=0'); process.exit(1); }

  console.log(`✓ #${from} 에 관계 철회 코멘트 게시 (${id})`);
  console.log('  본문 마커·GitHub 네이티브 의존성으로 생긴 엣지는 이 방식으로 지워지지 않는다.');
  console.log('  그래프 재동기화 중...');
  console.log('');
  cmdSync(root, tracker, opts);
  console.log('');
  console.log('UNLINKED=1');
  console.log(`UNLINK=${from}|${to}|${type}`);
  console.log(`DECISION_ID=${id}`);
}

function label(graph, num) {
  const n = graph.nodes[String(num)];
  return n ? `#${num} ${n.title}` : `#${num} (그래프에 없음)`;
}

function cmdPlan(root, tracker, opts) {
  return withValidatedOnboardGraph(root, tracker, ({ graph, groups: c, cacheFingerprint }) => {
    const prio = (num) => { const r = priorityRank(graph.nodes[String(num)]); return r < 9 ? ` [P${r}]` : ''; };

    if (opts.json) {
      emitStableOutput(root, cacheFingerprint, JSON.stringify({
        ready: c.ready, blocked: c.blocked, inProgress: c.inProgress, done: c.done,
      }, null, 2));
      return;
    }

    const output = [
      '# 이슈 DAG todo',
      '',
      `## ▶ 착수 가능 (ready) — ${c.ready.length}개`,
      ...(c.ready.length ? c.ready.map((n) => `  - ${label(graph, n)}${prio(n)}`) : ['  (없음)']),
      '',
      `## ⏳ 진행 중 (in-progress) — ${c.inProgress.length}개`,
      ...(c.inProgress.length ? c.inProgress.map((n) => `  - ${label(graph, n)} (${graph.nodes[String(n)].status})`) : ['  (없음)']),
      '',
      `## ⛔ 막힘 (blocked) — ${c.blocked.length}개`,
      ...(c.blocked.length ? c.blocked.map((b) => `  - ${label(graph, b.num)}  ← 대기: ${b.blockers.map((x) => `#${x}`).join(', ')}`) : ['  (없음)']),
      '',
      `## ✔ 완료 (done) — ${c.done.length}개`,
      ...(c.done.length ? [`  ${c.done.map((x) => `#${x}`).join(', ')}`] : ['  (없음)']),
      '',
      `READY_NUMBERS=${c.ready.join(' ')}`,
      `BLOCKED_NUMBERS=${c.blocked.map((b) => b.num).join(' ')}`,
      `IN_PROGRESS_NUMBERS=${c.inProgress.join(' ')}`,
      `DONE_NUMBERS=${c.done.join(' ')}`,
    ];
    emitStableOutput(root, cacheFingerprint, output.join('\n'));
  });
}

function cmdNext(root, tracker) {
  return withValidatedOnboardGraph(root, tracker, ({ graph, groups: c, cacheFingerprint }) => {
    if (!c.ready.length) {
      const message = c.inProgress.length
        ? `착수 가능한 이슈가 없다. 진행 중: ${c.inProgress.map((n) => `#${n}`).join(', ')}`
        : '착수 가능한 이슈가 없다. `sync` 로 그래프를 갱신하거나 막힌 이슈의 선행을 끝내라.';
      emitStableOutput(root, cacheFingerprint, `${message}\nNEXT_ISSUE=`);
      return;
    }
    const n = c.ready[0];
    const output = [
      `다음 착수 추천: ${label(graph, n)}`,
      '',
      `NEXT_ISSUE=${n}`,
      `NEXT=/issue-start #${n}`,
    ];
    emitStableOutput(root, cacheFingerprint, output.join('\n'));
  });
}

function graphFromFile(root, file) {
  const resolved = path.resolve(root, file);
  if (!existsSync(resolved)) {
    console.error('✗ 그래프 파일이 없습니다: ' + resolved);
    process.exit(1);
  }
  try {
    return JSON.parse(readFileSync(resolved, 'utf8'));
  } catch (error) {
    console.error('✗ 그래프 JSON 파싱 실패: ' + error.message);
    process.exit(1);
  }
}

function ontologyProblems(graph, { required = false } = {}) {
  if (!ontologyModule || ontologyModule.ontologyAvailable === false) {
    if (required) {
      console.error('✗ Ajv 온톨로지를 사용할 수 없습니다. tools/issue-ontology에서 npm install을 실행하세요.');
      if (ontologyLoadError) console.error('  ' + ontologyLoadError.message);
      process.exit(2);
    }
    return [];
  }
  try {
    const result = ontologyModule.validateGraphDocument(graph);
    return result.valid ? [] : result.errors.map((error) =>
      (error.instancePath || '/') + ' ' + error.message);
  } catch (error) {
    if (required) {
      console.error('✗ Ajv 온톨로지 검증 실패: ' + error.message);
      process.exit(2);
    }
    throw error;
  }
}

function validatedOntologyProblems(graph) {
  if (!ontologyModule || ontologyModule.ontologyAvailable === false) {
    const detail = ontologyLoadError ? `: ${ontologyLoadError.message}` : '';
    throw new Error(`Ajv 온톨로지를 사용할 수 없다${detail}`);
  }
  try {
    return ontologyProblems(graph);
  } catch (error) {
    throw new Error(`Ajv 온톨로지 검증 실패: ${error.message}`);
  }
}

function ontologyBootstrapReason(graph) {
  if (!ontologyModule || ontologyModule.ontologyAvailable === false) return 'ontology-unavailable';
  try {
    return ontologyProblems(graph).length ? 'invalid' : null;
  } catch {
    return 'ontology-unavailable';
  }
}

function cmdValidate(root, tracker, opts = {}) {
  const graph = opts.graph ? graphFromFile(root, opts.graph) : loadGraph(root, tracker.provider);
  const shapeProblems = ontologyProblems(graph, { required: true });
  if (shapeProblems.length) {
    console.log('AJV_VALID=0');
    for (const problem of shapeProblems) console.log('  - ' + problem);
    console.log('VALID=0');
    console.log('PROBLEMS=' + shapeProblems.length);
    process.exit(1);
  }
  console.log('AJV_VALID=1');
  const problems = auditGraph(graph);

  const cycle = findCycle(graph);
  if (cycle) problems.push(`순환 의존: ${cycle.join(' → ')}`);

  for (const e of graph.edges) {
    if (!graph.nodes[String(e.from)]) problems.push(`dangling 엣지: from #${e.from} 노드 없음 (${e.from}→${e.to})`);
    if (!graph.nodes[String(e.to)]) problems.push(`dangling 엣지: to #${e.to} 노드 없음 (${e.from}→${e.to})`);
    if (!EDGE_TYPES.includes(e.type)) problems.push(`알 수 없는 엣지 타입: ${e.type} (${e.from}→${e.to})`);
  }

  // close 불일치: done 인 노드가 아직 done 이 아닌 선행에 의존.
  const prereq = prereqMap(graph);
  for (const num of Object.keys(graph.nodes).map(Number)) {
    if (graph.nodes[String(num)].status !== DONE) continue;
    for (const dep of prereq.get(num) ?? []) {
      const d = graph.nodes[String(dep)];
      if (d && d.status !== DONE) problems.push(`close 불일치: #${num} 는 done 인데 선행 #${dep} 가 미완`);
    }
  }

  if (!problems.length) {
    console.log('✓ 문제 없음 — V2 snapshot·관계·DAG 정상');
    console.log('');
    console.log('VALID=1');
    console.log('PROBLEMS=0');
    return;
  }
  console.log('✗ 문제 발견:');
  for (const p of problems) console.log(`  - ${p}`);
  console.log('');
  console.log('VALID=0');
  console.log(`PROBLEMS=${problems.length}`);
  process.exit(cycle ? 2 : 1);
}

function cmdMigrate(root, tracker, opts) {
  const graph = loadGraph(root, tracker.provider);
  if (graph.version === GRAPH_VERSION) { console.log('MIGRATED=0'); console.log('MIGRATION_STATUS=already-v2'); return; }
  if (graph.version !== 1) { console.error(`✗ 지원하지 않는 마이그레이션 원본: V${graph.version}`); console.log('MIGRATED=0'); process.exit(2); }
  const migrated = migrateGraphV1(graph, { now: opts.now });
  const file = saveGraph(root, migrated, { now: migrated.updatedAt });
  console.log(`✓ V1 → V2 마이그레이션 완료: ${path.relative(root, file)}`);
  console.log('  이 캐시는 migrating 상태다. GitHub sync 전에는 plan/next를 실행하지 않는다.');
  console.log('MIGRATED=1'); console.log('MIGRATION_STATUS=migrating');
}

function cmdAudit(root, tracker) {
  const problems = auditGraph(loadGraph(root, tracker.provider));
  if (problems.length) { console.log('AUDIT=0'); console.log(`PROBLEMS=${problems.length}`); for (const problem of problems) console.log(`  - ${problem}`); process.exit(2); }
  console.log('AUDIT=1'); console.log('PROBLEMS=0');
}

/**
 * 자동 부트스트랩은 기존 스킬 탐색 순서를 따르되, 확인된 설치 루트의
 * 일반 파일만 실행한다. bare repository `skills/`는 현재 onboard가 그
 * 묶음에서 실행될 때만 trustedInstallationRoot를 통해 허용된다.
 */
function siblingSkill(root, skill, script) {
  const trustedRoots = [];
  const addRoot = (candidate) => {
    if (!candidate) return;
    const resolved = path.resolve(candidate);
    if (!trustedRoots.includes(resolved)) trustedRoots.push(resolved);
  };

  addRoot(trustedInstallationRoot());
  if (root) {
    addRoot(path.join(root, '.claude'));
    addRoot(path.join(root, '.codex'));
  }
  const home = homedir();
  addRoot(path.join(home, '.claude'));
  addRoot(path.join(home, '.codex'));

  return resolveSkillScript(import.meta.url, skill, script, {
    root,
    accept: (candidate) => trustedRoots.some((trustedRoot) => trustedRegularFile(candidate, trustedRoot)),
  });
}

/** 못 찾았을 때 어디를 확인해야 하는지 알려 주는 메시지. */
function missingSkill(skill) {
  return `${skill} 스킬을 찾지 못했다. 플러그인의 skills/ 또는 사용자 스킬 디렉터리에 설치돼 있는지 확인하라.`;
}

function edgeSourceDescriptorMatches(edge, source) {
  if (!source || edge.createdBy !== source.createdBy) return false;
  if ((edge.rationale ?? '') !== (source.rationale ?? '')) return false;
  if ((edge.provenance?.url ?? null) !== (source.provenance?.url ?? null)
    || (edge.provenance?.digest ?? null) !== (source.provenance?.digest ?? null)) return false;
  if (source.decisionId !== undefined && edge.decisionId !== source.decisionId) return false;
  if (source.provenance?.graphRevision !== undefined
    && edge.provenance?.graphRevision !== source.provenance.graphRevision) return false;
  if (source.provenance?.evidence !== undefined
    && JSON.stringify(edge.provenance?.evidence ?? null) !== JSON.stringify(source.provenance.evidence)) return false;
  if (source.status !== undefined && edge.status !== source.status) return false;
  if (source.evidence !== undefined
    && JSON.stringify(edge.evidence ?? []) !== JSON.stringify(source.evidence)) return false;
  return true;
}

function edgeSourceReason(graph, issues, edgeSource = null) {
  const expected = issueSourceEdgeDescriptors(issues);
  if (edgeSource) {
    if (!edgeSource.complete) return edgeSource.reason ?? 'native-dependencies-unverified';
    for (const [key, source] of edgeSource.sources ?? []) expected.set(key, source);
    for (const key of edgeSource.edgeKeys ?? []) if (!expected.has(key)) expected.set(key, null);
  } else if ([...(graph.edges ?? [])].some((edge) => edge.createdBy === 'github-native')) {
    return 'native-dependencies-unverified';
  }
  const actual = new Map((graph.edges ?? []).map((edge) => [edgeKey(edge), edge]));
  if (actual.size !== expected.size) return 'edge-source-changed';
  for (const [key, edge] of actual) {
    if (!expected.has(key) || !edgeSourceDescriptorMatches(edge, expected.get(key))) return 'edge-source-changed';
  }
  return null;
}

function canonicalIssueReason(graph, issues, { edgeSource = null } = {}) {
  if (graph.snapshot?.digest !== issueSnapshotDigest(issues)) return 'snapshot-changed';
  if (graph.snapshot?.graphDigest !== graphDocumentDigest(graph)) return 'cache-integrity-failed';

  const live = new Map(issues.map((issue) => [String(issue.number), issue]));
  for (const issue of issues) {
    const node = graph.nodes[String(issue.number)];
    if (!node) return 'issue-missing';
    const labels = typeLabels((issue.labels ?? []).map((label) => typeof label === 'string' ? label : label.name));
    const nodeLabels = [...(node.labels ?? [])].map(String).sort();
    const liveLabels = [...labels].map(String).sort();
    const revision = issue.updatedAt ?? 'unknown';
    if (node.number !== issue.number
      || node.title !== issue.title
      || node.status !== deriveStatus(issue.labels ?? [], issue.state)
      || node.url !== issue.url
      || node.provenance?.revision !== revision
      || JSON.stringify(nodeLabels) !== JSON.stringify(liveLabels)) {
      return issueIsOpen(issue) ? 'open-issue-changed' : 'issue-changed';
    }
  }
  for (const node of Object.values(graph.nodes)) {
    if (!live.has(String(node.number)) && node.provenance?.kind !== 'referenced') return 'issue-removed';
  }
  return edgeSourceReason(graph, issues, edgeSource);
}

/**
 * 온보딩 전에 그래프 캐시를 다시 만들어야 하는 이유를 반환한다.
 * null 이면 완전한 캐시가 현재 열린 이슈 목록과 일치한다.
 */
export function graphBootstrapReason(graph, {
  fileExists = true,
  openIssues = [],
  allIssues = null,
  allIssuesComplete = true,
  edgeSource = null,
  repository = null,
} = {}) {
  if (!fileExists) return 'missing';
  if (graph.snapshot?.status === 'invalid') return 'invalid';
  if (graph.snapshot?.status !== 'complete') return 'snapshot-incomplete';
  if (allIssues && !allIssuesComplete) return 'issue-list-incomplete';

  const ontologyReason = ontologyBootstrapReason(graph);
  if (ontologyReason) return ontologyReason;

  if (repository && String(graph.repository ?? '').toLowerCase() !== String(repository).toLowerCase()) {
    return 'repository-changed';
  }

  const problems = auditGraph(graph);
  if (problems.length) return 'invalid';

  if (allIssues) {
    const canonicalReason = canonicalIssueReason(graph, allIssues, { edgeSource });
    if (canonicalReason) return canonicalReason;
  }

  const nodeCount = Object.keys(graph.nodes ?? {}).length;
  if (!nodeCount) return openIssues.length ? 'empty' : null;

  for (const issue of openIssues) {
    const node = graph.nodes[String(issue.number)];
    if (!node) return 'open-issue-missing';
    if (node.status === DONE) return 'open-issue-reopened';
    if (issue.title && node.title !== issue.title) return 'open-issue-changed';
    if (issue.updatedAt && node.provenance?.revision !== issue.updatedAt) return 'open-issue-changed';
  }

  const openNumbers = new Set(openIssues.map((issue) => String(issue.number)));
  for (const node of Object.values(graph.nodes)) {
    if (node.status !== DONE && !openNumbers.has(String(node.number))) return 'open-issue-closed';
  }
  return null;
}

export function bootstrapEnvironment(env = process.env, { includeCredentials = false } = {}) {
  const keys = new Set(BOOTSTRAP_ENV_KEYS);
  if (!includeCredentials) {
    for (const key of BOOTSTRAP_CREDENTIAL_KEYS) keys.delete(key);
  }
  const tokenEnv = configuredJiraTokenEnv();
  if (includeCredentials && tokenEnv) keys.add(tokenEnv);
  return Object.fromEntries([...keys]
    .filter((key) => key === 'PATH' || typeof env[key] === 'string')
    .map((key) => [key, key === 'PATH' ? TRUSTED_PATH : env[key]]));
}

function isTrustedBootstrapScript(file) {
  const installationRoot = trustedInstallationRoot();
  const skillRoot = installationRoot ? path.join(installationRoot, 'skills') : null;
  return Boolean(skillRoot && trustedRegularFile(file, skillRoot));
}

export function runSyncBootstrap(root, {
  resolve = siblingSkill,
  spawn = spawnSync,
  env = process.env,
} = {}) {
  const sync = resolve(root, 'issue-sync', 'issue-sync.mjs');
  if (!sync) throw new Error(missingSkill('issue-sync'));
  const trusted = isTrustedBootstrapScript(sync);
  const childEnv = bootstrapEnvironment(env, { includeCredentials: trusted });
  let isolatedState = null;
  try {
    if (!trusted) {
      isolatedState = mkdtempSync(path.join(tmpdir(), 'issue-bootstrap-state-'));
      const xdgDirectories = {
        XDG_CONFIG_HOME: path.join(isolatedState, 'config'),
        XDG_DATA_HOME: path.join(isolatedState, 'data'),
        XDG_CACHE_HOME: path.join(isolatedState, 'cache'),
      };
      mkdirSync(xdgDirectories.XDG_CONFIG_HOME, { mode: 0o700 });
      mkdirSync(xdgDirectories.XDG_DATA_HOME, { mode: 0o700 });
      mkdirSync(xdgDirectories.XDG_CACHE_HOME, { mode: 0o700 });
      Object.assign(childEnv, { HOME: isolatedState, ...xdgDirectories });
    }
    return spawn(process.execPath, [sync], {
      cwd: root,
      encoding: 'utf8',
      env: childEnv,
      timeout: BOOTSTRAP_TIMEOUT_MS,
      maxBuffer: 16 * 1024 * 1024,
    });
  } finally {
    if (isolatedState) rmSync(isolatedState, { recursive: true, force: true });
  }
}

export function syncBootstrapComplete(result) {
  const stdout = String(result.stdout ?? '');
  const markers = new Set(stdout.split(/\r?\n/).map((line) => line.trim()));
  return result.status === 0
    && markers.has('SNAPSHOT_STATUS=complete')
    && markers.has('GRAPH_SYNC=ok');
}

function bootstrapWithSync(root, reason) {
  console.log('GRAPH_BOOTSTRAP_REASON=' + reason);
  const result = runSyncBootstrap(root);
  process.stdout.write(result.stdout ?? '');
  process.stderr.write(result.stderr ?? '');
  if (result.status !== 0) process.exit(result.status ?? 1);
  if (!syncBootstrapComplete(result)) {
    console.error('✗ issue-sync가 complete snapshot과 GRAPH_SYNC=ok를 확인하지 못했다.');
    process.exit(1);
  }
  console.log('GRAPH_BOOTSTRAP=issue-sync');
}

function liveIssueSnapshot(tracker) {
  return fetchCompleteIssueList(tracker, {
    state: 'all',
    fields: 'number,title,labels,url,state,body,comments,updatedAt,author,createdAt',
  });
}

/** 라이브 원본에서 재생성한 엣지 집합과 캐시를 비교한다. */
function liveEdgeSource(root, graph, issues, repository = null) {
  const sources = issueSourceEdgeDescriptors(issues);
  const edgeKeys = new Set(sources.keys());
  if (graph.provider !== 'github') return { complete: true, edgeKeys, sources };
  const currentRepository = repository ?? gitHost.repoInfo(root)?.nameWithOwner;
  const slug = splitSlug(currentRepository ?? '');
  if (!slug) return { complete: false, reason: 'native-dependencies-repo-unknown', edgeKeys, sources };
  if (String(graph.repository ?? '').toLowerCase() !== currentRepository.toLowerCase()) {
    return { complete: false, reason: 'repository-changed', edgeKeys, sources };
  }
  const native = collectNativeDependencies({
    list: issues,
    seen: new Set(edgeKeys),
    owner: slug.owner,
    repo: slug.repo,
    root,
  });
  if (native.stats.skipped) {
    return { complete: false, reason: `native-dependencies-${native.stats.skipped}`, edgeKeys, sources };
  }
  for (const candidate of native.candidates) {
    const edge = { ...candidate, type: 'depends-on' };
    const key = edgeKey(edge);
    edgeKeys.add(key);
    const sourceIssue = issues.find((issue) => Number(issue.number) === Number(candidate.from));
    const toClosed = graph.nodes[String(candidate.to)]?.status === DONE;
    sources.set(key, {
      createdBy: 'github-native',
      rationale: `#${candidate.from} 은(는) GitHub 네이티브 의존성에서 #${candidate.to} 에 blocked-by`,
      status: toClosed ? 'resolved' : 'active',
      provenance: {
        url: sourceIssue?.url ?? graph.nodes[String(candidate.from)]?.url ?? null,
        digest: digest({ nativeDependency: true, from: candidate.from, to: candidate.to }),
      },
    });
  }
  return { complete: true, edgeKeys, sources };
}

function snapshotBootstrapReason(root, graph, live, tracker) {
  const repository = tracker.provider === 'github'
    ? tracker.repository ?? gitHost.repoInfo(root)?.nameWithOwner
    : null;
  const base = {
    fileExists: graph.snapshot?.status !== 'missing',
    openIssues: live.items.filter(issueIsOpen),
    allIssues: live.items,
    allIssuesComplete: live.complete,
    repository,
  };
  let reason = graphBootstrapReason(graph, base);
  if (graph.snapshot?.status === 'complete'
    && (reason === null || reason === 'native-dependencies-unverified')) {
    reason = graphBootstrapReason(graph, {
      ...base,
      edgeSource: liveEdgeSource(root, graph, live.items, repository),
    });
  }
  return reason;
}

function ensureValidatedOnboardGraph(root, tracker) {
  let graph = loadGraph(root, tracker.provider, { tolerateParseError: true });
  let live = liveIssueSnapshot(tracker);
  if (live.items === null) throw new Error('전체 이슈 목록을 조회하지 못했다.');
  if (!live.complete) throw new Error('전체 이슈 목록이 limit 안에서 끝났다는 것을 증명하지 못했다.');
  // live snapshot을 읽는 동안 cache가 바뀔 수 있으므로, 검증 대상은 반드시
  // 같은 시점의 디스크 상태에서 다시 읽는다. 그렇지 않으면 오래된 메모리
  // 객체로 깨진 graph.json을 가리고 추천을 계속할 수 있다.
  graph = loadGraph(root, tracker.provider, { tolerateParseError: true });
  const reason = snapshotBootstrapReason(root, graph, live, tracker);
  if (reason === 'ontology-unavailable') {
    ontologyProblems(graph, { required: true });
    throw new Error('온톨로지 검증을 사용할 수 없어 그래프를 안전하게 사용할 수 없다.');
  }
  if (reason) {
    bootstrapWithSync(root, reason);
    graph = loadGraph(root, tracker.provider);
    live = liveIssueSnapshot(tracker);
    if (live.items === null) throw new Error('동기화 후 전체 이슈 목록을 조회하지 못했다.');
    if (!live.complete) throw new Error('동기화 후 전체 이슈 목록이 완전하다는 것을 증명하지 못했다.');
    graph = loadGraph(root, tracker.provider, { tolerateParseError: true });
    const postSyncReason = snapshotBootstrapReason(root, graph, live, tracker);
    if (postSyncReason === 'ontology-unavailable') {
      ontologyProblems(graph, { required: true });
      throw new Error('동기화 후 온톨로지 검증을 사용할 수 없어 그래프를 안전하게 사용할 수 없다.');
    }
    if (postSyncReason) throw new Error(`동기화 후 그래프가 최신 열린 이슈와 일치하지 않는다: ${postSyncReason}`);
  }
  // 최종 graph reload·검증·분류·출력을 하나의 프로토콜 구간으로 묶는다.
  // saveGraph/patchGraphNode 도 같은 sidecar lock을 사용하므로, 지원되는 모든
  // graph writer는 이 구간에서 캐시를 교체할 수 없다.
  const releaseGraphCacheLock = acquireGraphCacheLock(root);
  try {
    const beforeRecommendation = graphCacheFingerprint(root);
    graph = loadGraph(root, tracker.provider, { tolerateParseError: true });
    const afterRecommendationLoad = graphCacheFingerprint(root);
    if (beforeRecommendation !== afterRecommendationLoad) {
      throw new Error('추천 직전 그래프 캐시가 변경되어 추천하지 않는다.');
    }
    const finalReason = snapshotBootstrapReason(root, graph, live, tracker);
    if (finalReason === 'ontology-unavailable') {
      throw new Error('추천 직전 온톨로지 검증을 사용할 수 없어 그래프를 안전하게 사용할 수 없다.');
    }
    if (finalReason) throw new Error(`추천 직전 그래프가 최신 열린 이슈와 일치하지 않는다: ${finalReason}`);

    const problems = auditGraph(graph);
    problems.push(...validatedOntologyProblems(graph));
    const cycle = findCycle(graph);
    if (cycle) problems.push(`순환 의존: ${cycle.join(' → ')}`);
    if (problems.length) throw new Error(`안전하지 않은 그래프: ${problems.join(' / ')}`);
    const groups = classify(graph);
    assertGraphCacheStable(root, afterRecommendationLoad);
    const openIssues = live.items.filter(issueIsOpen);
    return {
      graph,
      openIssues,
      groups,
      cacheFingerprint: afterRecommendationLoad,
      releaseGraphCacheLock,
    };
  } catch (error) {
    releaseGraphCacheLock();
    throw error;
  }
}

function withValidatedOnboardGraph(root, tracker, callback) {
  const validated = ensureValidatedOnboardGraph(root, tracker);
  try {
    return callback(validated);
  } finally {
    validated.releaseGraphCacheLock();
  }
}

function cmdOnboard(root, tracker, opts) {
  return withValidatedOnboardGraph(root, tracker, ({ graph, openIssues, groups, cacheFingerprint }) => {
    const openNumbers = new Set(openIssues.map((issue) => issue.number));
    const ordered = [...groups.ready, ...groups.inProgress, ...groups.blocked.map((item) => item.num)]
      .filter((number) => openNumbers.has(number));
    const visible = opts.all ? ordered : ordered.slice(0, 6);
    const output = [
      `OPEN_ISSUES=${openIssues.length}`,
      `ONBOARD_COUNT=${visible.length}`,
      ...visible.map((number) => `PRIORITY=#${number}\t${graph.nodes[String(number)].title}`),
      `MORE_AVAILABLE=${ordered.length > visible.length ? 1 : 0}`,
      'NEXT_ACTIONS=issue-start,issue-merge,issue-create',
    ];
    emitStableOutput(root, cacheFingerprint, output.join('\n'));
  });
}

/* ------------------------------------------------------------------- usage */

function usage(exitCode = 1) {
  console.error(`Usage:
  node issue-onboard.mjs [--all]
  node issue-onboard.mjs sync [--state open|closed|all] [--limit <n>] [--no-llm] [--no-native]
  node issue-onboard.mjs link <from> <to> [--type ${EDGE_TYPES.join('|')}] [--why "<근거>"]
  node issue-onboard.mjs unlink <from> <to> [--type <type>]
  node issue-onboard.mjs plan [--json]
  node issue-onboard.mjs next
  node issue-onboard.mjs validate [--graph <path>]
  node issue-onboard.mjs audit
  node issue-onboard.mjs migrate

엣지 방향: from --depends-on--> to = "from 은 to 가 close 전엔 착수 불가" (to 가 선수).
link/unlink 는 대상(from) 이슈에 구조화된 승인/철회 결정 코멘트를 남기고 재-sync 한다.
sync 는 본문 마커(depends on #N)·결정 코멘트·GitHub 네이티브 의존성(blocked-by)을 함께 읽는다.
그래프: ${WORKSPACE_DIR}/${GRAPH_FILE} (GitHub 정본에서 재생성하는 로컬 캐시).
이슈 백엔드는 ~/.issue/settings.json 의 provider.type 이 정한다 (github 기본 | jira).
`);
  process.exit(exitCode);
}

/* ------------------------------------------------------------------- main */

function main() {
  const argv = process.argv.slice(2);
  if (argv.includes('-h') || argv.includes('--help')) usage(0);

  const optionFirst = argv[0]?.startsWith('-');
  let mode = optionFirst ? 'onboard' : argv[0] ?? 'onboard';
  const MODES = ['onboard', 'sync', 'link', 'unlink', 'plan', 'next', 'validate', 'audit', 'migrate'];
  if (!MODES.includes(mode)) { console.error(`✗ 알 수 없는 모드: ${argv[0]}`); usage(); }

  const opts = {};
  const positionals = [];
  for (let i = optionFirst ? 0 : 1; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--json') opts.json = true;
    else if (arg === '--type') opts.type = argv[++i];
    else if (arg === '--why') opts.why = argv[++i];
    else if (arg === '--state') opts.state = argv[++i];
    else if (arg === '--limit') opts.limit = argv[++i];
    else if (arg === '--repo') opts.repo = argv[++i];
    else if (arg === '--all') opts.all = true;
    else if (arg === '--no-llm') opts.noLlm = true;
    else if (arg === '--no-native') opts.noNative = true;
    else if (arg === '--graph') opts.graph = argv[++i];
    else if (arg.startsWith('-')) { console.error(`✗ 알 수 없는 옵션: ${arg}`); usage(); }
    else positionals.push(arg);
  }

  const root = repoRoot();
  const tracker = createTracker(root, { repo: opts.repo });

  if (mode === 'onboard') cmdOnboard(root, tracker, opts);
  else if (mode === 'sync') cmdSync(root, tracker, opts);
  else if (mode === 'link') cmdLink(root, tracker, parseIssueNumber(positionals[0]), parseIssueNumber(positionals[1]), opts);
  else if (mode === 'unlink') cmdUnlink(root, tracker, parseIssueNumber(positionals[0]), parseIssueNumber(positionals[1]), opts);
  else if (mode === 'plan') cmdPlan(root, tracker, opts);
  else if (mode === 'next') cmdNext(root, tracker);
  else if (mode === 'validate') cmdValidate(root, tracker, opts);
  else if (mode === 'audit') cmdAudit(root, tracker);
  else if (mode === 'migrate') cmdMigrate(root, tracker, opts);
}

/** 심볼릭 링크로 설치돼도 진입점 판별이 어긋나지 않게 realpath 로 비교한다. */
function isMainModule(metaUrl) {
  const entry = process.argv[1];
  if (!entry) return false;
  const here = fileURLToPath(metaUrl);
  const resolved = path.resolve(entry);
  if (here === resolved) return true;
  try { return realpathSync(here) === realpathSync(resolved); } catch { return false; }
}

if (isMainModule(import.meta.url)) main();
