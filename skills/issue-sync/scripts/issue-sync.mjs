#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { readIssueSettings, resolveSkillScript, trustedExecutable, trustedRegularFile } from './issue-common.mjs';

const TRUSTED_PATH = [
  '/usr/bin', '/usr/sbin', '/bin', '/sbin',
  '/System/Cryptexes/App/usr/bin',
].join(path.delimiter);
const CHILD_ENV_KEYS = [
  'HOME', 'USER', 'LOGNAME', 'TMPDIR', 'TMP', 'TEMP',
  'LANG', 'LC_ALL', 'LC_CTYPE', 'TERM',
  'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME',
  'ISSUE_PROVIDER', 'ISSUE_ONTOLOGY_ROOT', 'GH_HOST',
  'GH_TOKEN', 'GITHUB_TOKEN', 'GH_ENTERPRISE_TOKEN', 'GITHUB_ENTERPRISE_TOKEN',
  'JIRA_API_TOKEN',
];
const ENV_KEY_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;

function configuredJiraTokenEnv() {
  try {
    const tokenEnv = readIssueSettings()?.provider?.jira?.tokenEnv;
    return ENV_KEY_PATTERN.test(String(tokenEnv ?? '')) ? tokenEnv : null;
  } catch {
    return null;
  }
}

function childEnvironment(env = process.env) {
  const keys = new Set(CHILD_ENV_KEYS);
  const tokenEnv = configuredJiraTokenEnv();
  if (tokenEnv) keys.add(tokenEnv);
  return Object.fromEntries([
    ...[...keys].filter((key) => typeof env[key] === 'string').map((key) => [key, env[key]]),
    ['PATH', TRUSTED_PATH],
  ]);
}

function repoRoot() {
  const git = trustedExecutable('git');
  if (!git) throw new Error('신뢰할 수 있는 git 실행 파일을 찾지 못했다.');
  const result = spawnSync(git, ['rev-parse', '--show-toplevel'], {
    encoding: 'utf8',
    env: childEnvironment(),
    timeout: 30000,
  });
  if (result.status !== 0) throw new Error('git 저장소에서 실행해야 한다.');
  return result.stdout.trim();
}

function hasOutputMarker(output, marker) {
  return String(output ?? '').split(/\r?\n/).some((line) => line.trim() === marker);
}

function trustedInstallationRoot(metaUrl = import.meta.url) {
  try {
    return path.resolve(path.dirname(fileURLToPath(metaUrl)), '..', '..', '..');
  } catch {
    return null;
  }
}

try {
  const root = repoRoot();
  // 설치 위치(프로젝트 로컬 / 홈 전역 / 저장소를 링크한 개발 설치)를 가리지 않고 형제 스킬을 찾는다.
  const installationRoot = trustedInstallationRoot();
  const script = resolveSkillScript(import.meta.url, 'issue-onboard', 'issue-onboard.mjs', {
    root,
    accept: (candidate) => Boolean(installationRoot && trustedRegularFile(candidate, installationRoot)),
  });
  if (!script) {
    throw new Error(
      'issue-onboard 스킬을 찾지 못했다. 플러그인의 skills/ 또는 사용자 스킬 디렉터리에 설치돼 있는지 확인하라.',
    );
  }
  const result = spawnSync(process.execPath, [script, 'sync', '--state', 'all'], {
    cwd: root,
    encoding: 'utf8',
    env: childEnvironment(),
    timeout: 120000,
    maxBuffer: 16 * 1024 * 1024,
  });
  process.stdout.write(result.stdout);
  process.stderr.write(result.stderr);
  if (result.status !== 0 || !hasOutputMarker(result.stdout, 'SNAPSHOT_STATUS=complete')) {
    console.log('GRAPH_SYNC=failed');
    process.exit(result.status || 2);
  }
  console.log('GRAPH_SYNC=ok');
} catch (error) {
  console.error(`✗ ${error.message}`);
  console.log('GRAPH_SYNC=failed');
  process.exit(1);
}
