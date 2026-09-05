#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

// 버전이 박혀 있는 파일 전부. 하나라도 빠지면 태그와 매니페스트가 어긋난다.
// path 의 '*' 는 배열 전체를 뜻한다 (marketplace 의 plugins[] 처럼 항목이 늘 수 있다).
export const VERSION_SOURCES = [
  { file: 'VERSION', kind: 'text' },
  { file: 'marketplace.json', kind: 'json', path: ['plugins', '*', 'version'] },
  { file: '.claude-plugin/plugin.json', kind: 'json', path: ['version'] },
  { file: '.claude-plugin/marketplace.json', kind: 'json', path: ['plugins', '*', 'version'] },
  { file: '.codex-plugin/plugin.json', kind: 'json', path: ['version'] },
  { file: '.grok-plugin/plugin.json', kind: 'json', path: ['version'] },
  { file: '.zcode-plugin/plugin.json', kind: 'json', path: ['version'] },
  { file: 'tools/issue-ontology/package.json', kind: 'json', path: ['version'] },
];

export const LEVELS = ['major', 'minor', 'patch'];
export const FIRST_VERSION = '0.1.0';

// conventional commit type -> 릴리즈 노트 섹션. 순서가 곧 노트의 순서다.
export const TYPE_SECTIONS = [
  ['feat', '✨ 기능'],
  ['fix', '🐛 버그 수정'],
  ['perf', '⚡ 성능'],
  ['refactor', '♻️ 리팩터링'],
  ['docs', '📝 문서'],
  ['test', '✅ 테스트'],
  ['build', '📦 빌드'],
  ['ci', '🤖 CI'],
  ['style', '💄 스타일'],
  ['revert', '⏪ 되돌림'],
  ['chore', '🧹 유지보수'],
];
export const OTHER_SECTION = '📌 기타';
export const BREAKING_SECTION = '💥 호환성이 깨지는 변경';

// ── semver ──────────────────────────────────────────────────────────────────

export function parseSemver(input) {
  const match = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(String(input ?? '').trim());
  if (!match) return null;
  return { major: Number(match[1]), minor: Number(match[2]), patch: Number(match[3]) };
}

export function formatVersion(version) {
  return `${version.major}.${version.minor}.${version.patch}`;
}

export function compareSemver(a, b) {
  return a.major - b.major || a.minor - b.minor || a.patch - b.patch;
}

export function bumpVersion(version, level) {
  if (!LEVELS.includes(level)) throw new Error(`알 수 없는 단계: ${level}`);
  if (level === 'major') return { major: version.major + 1, minor: 0, patch: 0 };
  if (level === 'minor') return { major: version.major, minor: version.minor + 1, patch: 0 };
  return { major: version.major, minor: version.minor, patch: version.patch + 1 };
}

export function pickLatestTag(tags) {
  const parsed = tags
    .map((tag) => ({ tag, version: parseSemver(tag) }))
    .filter((entry) => entry.version);
  if (!parsed.length) return null;
  parsed.sort((a, b) => compareSemver(a.version, b.version));
  return parsed[parsed.length - 1];
}

// ── git ─────────────────────────────────────────────────────────────────────

function git(args, cwd) {
  return spawnSync('git', args, { cwd, encoding: 'utf8' });
}

function gitOut(args, cwd) {
  const result = git(args, cwd);
  return result.status === 0 ? result.stdout.trim() : '';
}

export function repoRoot(cwd = process.cwd()) {
  const result = git(['rev-parse', '--show-toplevel'], cwd);
  if (result.status !== 0) fail('git 저장소가 아니다.', 3);
  return result.stdout.trim();
}

// issue-create/issue-start 와 같은 순서로 기본 브랜치를 정한다.
export function baseBranch(root) {
  const settingsFile = path.join(root, '.issue', 'settings.json');
  if (existsSync(settingsFile)) {
    try {
      const configured = JSON.parse(readFileSync(settingsFile, 'utf8'))?.git?.baseBranch;
      if (configured) return String(configured);
    } catch { /* 설정이 깨져 있으면 아래 판별로 넘어간다 */ }
  }
  const head = gitOut(['symbolic-ref', '--quiet', '--short', 'refs/remotes/origin/HEAD'], root);
  if (head) return head.replace(/^origin\//, '');
  for (const candidate of ['main', 'master']) {
    if (git(['rev-parse', '--verify', '--quiet', candidate], root).status === 0) return candidate;
  }
  return 'main';
}

// 기본 브랜치에서 도달 가능한 태그만 센다. 다른 브랜치에만 달린 태그는 현재 버전이 아니다.
export function latestTag(root, base) {
  for (const ref of [base, `origin/${base}`]) {
    const result = git(['tag', '--list', '--merged', ref], root);
    if (result.status !== 0) continue;
    const picked = pickLatestTag(result.stdout.split('\n').map((line) => line.trim()).filter(Boolean));
    if (picked) return picked;
  }
  const all = gitOut(['tag', '--list'], root).split('\n').map((line) => line.trim()).filter(Boolean);
  return pickLatestTag(all);
}

export function remoteSlug(root) {
  const url = gitOut(['remote', 'get-url', 'origin'], root);
  const match = /github\.com[:/]([^/]+\/[^/]+?)(?:\.git)?$/.exec(url);
  return match ? match[1] : null;
}

// ── 버전 소스 파일 ───────────────────────────────────────────────────────────

function walk(node, segments) {
  if (!segments.length) return [node];
  const [head, ...rest] = segments;
  if (node == null) return [];
  if (head === '*') return Array.isArray(node) ? node.flatMap((item) => walk(item, rest)) : [];
  return walk(node[head], rest);
}

function assign(node, segments, value) {
  if (node == null) return 0;
  const [head, ...rest] = segments;
  if (head === '*') {
    return Array.isArray(node) ? node.reduce((count, item) => count + assign(item, rest, value), 0) : 0;
  }
  if (!rest.length) {
    if (!Object.prototype.hasOwnProperty.call(node, head)) return 0;
    node[head] = value;
    return 1;
  }
  return assign(node[head], rest, value);
}

export function readSourceVersion(root, source) {
  const absolute = path.join(root, source.file);
  if (!existsSync(absolute)) return { file: source.file, missing: true, versions: [] };
  const raw = readFileSync(absolute, 'utf8');
  if (source.kind === 'text') {
    return { file: source.file, missing: false, versions: [raw.trim()], raw };
  }
  let data;
  try {
    data = JSON.parse(raw);
  } catch (error) {
    return { file: source.file, missing: false, versions: [], parseError: error.message, raw };
  }
  return {
    file: source.file,
    missing: false,
    versions: walk(data, source.path).filter((value) => typeof value === 'string'),
    raw,
  };
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// JSON 재직렬화가 원본 포맷을 보존하지 못할 때만 쓰는 폴백.
// "version" 필드의 값만 바꾸므로 본문의 다른 곳에 같은 숫자가 있어도 오염되지 않는다.
export function replaceVersionText(raw, previousValues, next) {
  let output = raw;
  let replaced = 0;
  for (const previous of new Set(previousValues)) {
    const pattern = new RegExp(`("version"\\s*:\\s*")${escapeRegExp(previous)}(")`, 'g');
    output = output.replace(pattern, (_match, head, tail) => {
      replaced += 1;
      return `${head}${next}${tail}`;
    });
  }
  if (!replaced) throw new Error('version 문자열을 찾지 못했다.');
  return output;
}

export function renderSourceVersion(source, raw, next) {
  if (source.kind === 'text') {
    return next + (raw.endsWith('\n') ? '\n' : '');
  }
  const data = JSON.parse(raw);
  const previousValues = walk(data, source.path).filter((value) => typeof value === 'string');
  const changed = assign(data, source.path, next);
  if (!changed) throw new Error(`${source.file}: version 경로를 찾지 못했다.`);
  const trailing = raw.endsWith('\n') ? '\n' : '';
  // 원본을 그대로 다시 찍었을 때 바이트가 같아야 이 파일이 2-space 정규 포맷이라고 볼 수 있다.
  const control = JSON.stringify(JSON.parse(raw), null, 2) + trailing;
  if (control !== raw) return replaceVersionText(raw, previousValues, next);
  return JSON.stringify(data, null, 2) + trailing;
}

export function writeSourceVersion(root, source, next) {
  const absolute = path.join(root, source.file);
  const raw = readFileSync(absolute, 'utf8');
  const rendered = renderSourceVersion(source, raw, next);
  if (rendered === raw) return false;
  writeFileSync(absolute, rendered);
  return true;
}

export function collectVersionState(root) {
  const sources = VERSION_SOURCES.map((source) => ({ source, ...readSourceVersion(root, source) }));
  const values = new Set();
  const problems = [];
  for (const entry of sources) {
    if (entry.missing) { problems.push(`${entry.file}: 파일이 없다`); continue; }
    if (entry.parseError) { problems.push(`${entry.file}: JSON 파싱 실패 — ${entry.parseError}`); continue; }
    if (!entry.versions.length) { problems.push(`${entry.file}: version 값을 찾지 못했다`); continue; }
    for (const value of entry.versions) values.add(value);
  }
  return { sources, values: [...values], problems };
}

// ── 릴리즈 노트 ─────────────────────────────────────────────────────────────

// squash merge 가 제목 끝에 붙인 "(#39)" 는 설명이 아니라 PR 번호다.
// 떼어내지 않으면 참조 목록에서 한 번 더 찍혀 "(#39) (#39)" 가 된다.
export function stripTrailingReference(description) {
  return description.replace(/\s*\(#\d+\)\s*$/, '').trim() || description.trim();
}

export function parseCommitSubject(subject) {
  const match = /^(\w+)(?:\(([^)]*)\))?(!)?:\s*(.+)$/.exec(subject);
  if (!match) {
    return { type: null, scope: null, breaking: false, description: stripTrailingReference(subject) };
  }
  return {
    type: match[1].toLowerCase(),
    scope: match[2] ?? null,
    breaking: Boolean(match[3]),
    description: stripTrailingReference(match[4]),
  };
}

export function extractReferences(text) {
  return [...new Set((text.match(/#\d+/g) ?? []))];
}

export function groupCommits(commits) {
  const sectionOrder = [BREAKING_SECTION, ...TYPE_SECTIONS.map(([, label]) => label), OTHER_SECTION];
  const typeLabels = new Map(TYPE_SECTIONS);
  const groups = new Map();
  for (const commit of commits) {
    const parsed = parseCommitSubject(commit.subject);
    const breaking = parsed.breaking || /^BREAKING[ -]CHANGE:/m.test(commit.body ?? '');
    const label = breaking
      ? BREAKING_SECTION
      : typeLabels.get(parsed.type) ?? OTHER_SECTION;
    const entry = {
      scope: parsed.scope,
      description: parsed.description,
      references: extractReferences(`${commit.subject}\n${commit.body ?? ''}`),
      shortHash: (commit.hash ?? '').slice(0, 7),
    };
    if (!groups.has(label)) groups.set(label, []);
    groups.get(label).push(entry);
  }
  return sectionOrder
    .filter((label) => groups.has(label))
    .map((label) => ({ label, entries: groups.get(label) }));
}

export function renderNotes({ version, previousTag, commits, slug }) {
  const groups = groupCommits(commits);
  const lines = [];
  const previousLabel = previousTag ?? '최초 릴리즈';
  lines.push(`## ${version}`);
  lines.push('');
  lines.push(previousTag
    ? `${previousTag} 이후 커밋 ${commits.length}건.`
    : `최초 릴리즈. 커밋 ${commits.length}건.`);
  lines.push('');
  if (!groups.length) {
    lines.push('기록할 변경이 없다.');
    lines.push('');
  }
  for (const group of groups) {
    lines.push(`### ${group.label}`);
    lines.push('');
    for (const entry of group.entries) {
      const scope = entry.scope ? `**${entry.scope}**: ` : '';
      const references = entry.references.length ? ` (${entry.references.join(', ')})` : '';
      lines.push(`- ${scope}${entry.description}${references}`);
    }
    lines.push('');
  }
  if (slug && previousTag) {
    lines.push(`**전체 변경**: https://github.com/${slug}/compare/${previousTag}...${version}`);
    lines.push('');
  }
  void previousLabel;
  return lines.join('\n');
}

export function readCommits(root, from, to) {
  const range = from ? `${from}..${to}` : to;
  const result = git(['log', '--no-merges', '--pretty=format:%H%x1f%s%x1f%b%x1e', range], root);
  if (result.status !== 0) fail(`커밋을 읽지 못했다: ${result.stderr.trim()}`, 3);
  return result.stdout
    .split('\x1e')
    .map((record) => record.replace(/^\n/, ''))
    .filter((record) => record.trim())
    .map((record) => {
      const [hash, subject, body] = record.split('\x1f');
      return { hash, subject: subject ?? '', body: body ?? '' };
    });
}

// ── 출력 ────────────────────────────────────────────────────────────────────

function fail(message, code = 2) {
  console.error(`✗ ${message}`);
  process.exit(code);
}

function usage() {
  console.log(`사용법: issue-version.mjs <command> [options]

  current                          현재 버전 상태를 진단한다 (태그 · 8개 파일)
  plan <major|minor|patch>         다음 버전을 계산만 한다 (파일을 건드리지 않는다)
  bump <major|minor|patch> [opts]  버전 소스 파일을 새 버전으로 갱신한다
      --dry-run                    갱신 없이 대상과 전후 값만 출력
      --branch                     release/vX.Y.Z 브랜치를 만들고 그 위에서 갱신
      --force                      태그·파일 버전이 어긋나도 진행
  notes [opts]                     릴리즈 노트를 만든다
      --from <ref>                 시작 지점 (기본: 최신 태그)
      --to <ref>                   끝 지점 (기본: HEAD)
      --version <vX.Y.Z>           노트 제목에 쓸 버전
      --out <file>                 파일로 저장
  pr <vX.Y.Z> [--dry-run]          bump 결과를 커밋·push 하고 PR 을 만든다
  release <vX.Y.Z> [opts]          태그를 달고 GitHub 릴리즈를 낸다
      --notes-file <file>          쓸 노트 파일 (없으면 그 자리에서 만든다)
      --dry-run                    실제로 태그·릴리즈를 만들지 않는다
`);
}

function resolveCurrent(root) {
  const base = baseBranch(root);
  const tag = latestTag(root, base);
  const state = collectVersionState(root);
  const tagVersion = tag ? formatVersion(tag.version) : null;
  const fileVersions = state.values;
  const drift = Boolean(tagVersion) && (fileVersions.length !== 1 || fileVersions[0] !== tagVersion);
  return { base, tag, tagVersion, state, fileVersions, drift };
}

function printCurrent(context) {
  const { base, tag, tagVersion, state, fileVersions, drift } = context;
  console.log(`기본 브랜치 : ${base}`);
  console.log(`최신 태그   : ${tag ? tag.tag : '(없음)'}`);
  for (const entry of state.sources) {
    const shown = entry.missing
      ? '(파일 없음)'
      : entry.parseError
        ? `(JSON 오류) ${entry.parseError}`
        : entry.versions.join(', ') || '(version 없음)';
    console.log(`  ${entry.file.padEnd(36)} ${shown}`);
  }
  for (const problem of state.problems) console.log(`  ! ${problem}`);
  if (drift) {
    console.log('');
    console.log(`! 태그(${tagVersion})와 파일 버전(${fileVersions.join(', ') || '없음'})이 어긋난다.`);
  }
  console.log('');
  console.log(`BASE_BRANCH=${base}`);
  console.log(`LATEST_TAG=${tag ? tag.tag : ''}`);
  console.log(`TAG_VERSION=${tagVersion ?? ''}`);
  console.log(`FILE_VERSIONS=${fileVersions.join(',')}`);
  console.log(`SOURCE_PROBLEMS=${state.problems.length}`);
  console.log(`VERSION_DRIFT=${drift ? 1 : 0}`);
}

function cmdCurrent(root) {
  const context = resolveCurrent(root);
  printCurrent(context);
  return context;
}

function computeNext(context, level) {
  // 태그도 파일 버전도 없으면 첫 릴리즈다. 이때는 단계와 무관하게 v0.1.0 에서 시작한다.
  const baseVersion = context.tag
    ? context.tag.version
    : parseSemver(context.fileVersions.length === 1 ? context.fileVersions[0] : '');
  if (!baseVersion) {
    return { first: true, current: null, next: parseSemver(FIRST_VERSION) };
  }
  return { first: false, current: baseVersion, next: bumpVersion(baseVersion, level) };
}

function cmdPlan(root, level) {
  if (!LEVELS.includes(level)) fail(`단계는 ${LEVELS.join(' | ')} 중 하나여야 한다.`);
  const context = resolveCurrent(root);
  const { first, current, next } = computeNext(context, level);
  const nextVersion = formatVersion(next);
  if (first) {
    console.log('태그도 파일 버전도 없다. 첫 릴리즈이므로 단계와 무관하게 v0.1.0 에서 시작한다.');
  }
  console.log(`현재 : ${current ? `v${formatVersion(current)}` : '(없음)'}`);
  console.log(`다음 : v${nextVersion}  (${level})`);
  if (context.drift) console.log(`! 태그와 파일 버전이 어긋난다 — bump 전에 확인이 필요하다.`);
  console.log('');
  console.log(`LEVEL=${level}`);
  console.log(`CURRENT_VERSION=${current ? formatVersion(current) : ''}`);
  console.log(`NEXT_VERSION=${nextVersion}`);
  console.log(`NEXT_TAG=v${nextVersion}`);
  console.log(`FIRST_RELEASE=${first ? 1 : 0}`);
  console.log(`VERSION_DRIFT=${context.drift ? 1 : 0}`);
  return { context, next: nextVersion, first };
}

function cmdBump(root, level, options) {
  if (!LEVELS.includes(level)) fail(`단계는 ${LEVELS.join(' | ')} 중 하나여야 한다.`);
  const context = resolveCurrent(root);
  if (context.state.problems.length) {
    for (const problem of context.state.problems) console.error(`✗ ${problem}`);
    fail('버전 소스 파일이 온전하지 않다. 먼저 고쳐야 한다.', 3);
  }
  if (context.drift && !options.force && !options.dryRun) {
    printCurrent(context);
    fail('태그와 파일 버전이 어긋난다. 확인 후 --force 로 진행한다.', 3);
  }
  const { first, current, next } = computeNext(context, level);
  const nextVersion = formatVersion(next);

  if (options.branch && !options.dryRun) {
    const branch = `release/v${nextVersion}`;
    if (git(['rev-parse', '--verify', '--quiet', branch], root).status === 0) {
      fail(`브랜치 ${branch} 가 이미 있다.`, 3);
    }
    const created = git(['switch', '-c', branch], root);
    if (created.status !== 0) fail(`브랜치를 만들지 못했다: ${created.stderr.trim()}`, 3);
    console.log(`브랜치 생성 : ${branch}`);
  }

  const changed = [];
  for (const source of VERSION_SOURCES) {
    const before = readSourceVersion(root, source).versions.join(', ');
    if (options.dryRun) {
      console.log(`  ${source.file.padEnd(36)} ${before} -> ${nextVersion}`);
      continue;
    }
    if (writeSourceVersion(root, source, nextVersion)) changed.push(source.file);
  }

  if (options.dryRun) {
    console.log('');
    console.log('(--dry-run — 파일을 건드리지 않았다)');
  } else {
    for (const file of changed) console.log(`  갱신 ${file}`);
  }
  console.log('');
  console.log(`LEVEL=${level}`);
  console.log(`CURRENT_VERSION=${current ? formatVersion(current) : ''}`);
  console.log(`NEXT_VERSION=${nextVersion}`);
  console.log(`NEXT_TAG=v${nextVersion}`);
  console.log(`FIRST_RELEASE=${first ? 1 : 0}`);
  console.log(`CHANGED_FILES=${options.dryRun ? '' : changed.join(',')}`);
  console.log(`DRY_RUN=${options.dryRun ? 1 : 0}`);
}

function buildNotes(root, options) {
  const context = resolveCurrent(root);
  const from = options.from ?? (context.tag ? context.tag.tag : null);
  const to = options.to ?? 'HEAD';
  const commits = readCommits(root, from, to);
  const version = options.version
    ? (options.version.startsWith('v') ? options.version : `v${options.version}`)
    : (context.fileVersions.length === 1 ? `v${context.fileVersions[0]}` : to);
  return {
    from,
    to,
    commits,
    body: renderNotes({ version, previousTag: from, commits, slug: remoteSlug(root) }),
  };
}

function cmdNotes(root, options) {
  const { from, to, commits, body } = buildNotes(root, options);
  if (options.out) {
    writeFileSync(path.resolve(root, options.out), body.endsWith('\n') ? body : `${body}\n`);
  } else {
    console.log(body);
  }
  console.log(`NOTES_FROM=${from ?? ''}`);
  console.log(`NOTES_TO=${to}`);
  console.log(`NOTES_COMMITS=${commits.length}`);
  console.log(`NOTES_FILE=${options.out ?? ''}`);
}

function requireGh() {
  const result = spawnSync('gh', ['auth', 'status'], { encoding: 'utf8' });
  if (result.status !== 0) fail('gh 인증이 필요하다. gh-setup 스킬로 먼저 끝낸다.', 4);
}

function requireClean(root) {
  const status = gitOut(['status', '--porcelain'], root);
  if (status) {
    console.error(status);
    fail('커밋되지 않은 변경이 남아 있다.', 3);
  }
}

function cmdPr(root, versionArg, options) {
  const version = parseSemver(versionArg);
  if (!version) fail('버전은 vX.Y.Z 형식이어야 한다.');
  const versionText = formatVersion(version);
  const base = baseBranch(root);
  const branch = gitOut(['branch', '--show-current'], root);
  if (!branch) fail('브랜치가 없는 상태(detached HEAD)에서는 PR 을 만들지 않는다.', 3);
  if (branch === base) fail(`기본 브랜치(${base})에서 직접 커밋하지 않는다. release/v${versionText} 브랜치를 쓴다.`, 3);

  const state = collectVersionState(root);
  if (state.values.length !== 1 || state.values[0] !== versionText) {
    fail(`버전 소스 파일이 ${versionText} 로 맞춰져 있지 않다 (현재: ${state.values.join(', ') || '없음'}). bump 를 먼저 돌린다.`, 3);
  }

  const title = `chore(release): bump version to v${versionText}`;
  if (options.dryRun) {
    console.log(`(--dry-run) ${branch} -> ${base} PR 제목: ${title}`);
    console.log(`PR_DRY_RUN=1`);
    return;
  }
  requireGh();

  const staged = git(['add', '--', ...VERSION_SOURCES.map((source) => source.file)], root);
  if (staged.status !== 0) fail(`파일을 스테이지하지 못했다: ${staged.stderr.trim()}`, 3);
  if (gitOut(['diff', '--cached', '--name-only'], root)) {
    const committed = git(['commit', '-m', title], root);
    if (committed.status !== 0) fail(`커밋하지 못했다: ${committed.stderr.trim()}`, 3);
  }
  const pushed = git(['push', '-u', 'origin', branch], root);
  if (pushed.status !== 0) fail(`push 하지 못했다: ${pushed.stderr.trim()}`, 3);

  const notes = buildNotes(root, { version: `v${versionText}` });
  const body = `버전을 v${versionText} 로 올린다.\n\n${notes.body}\n\n---\n\n버전 소스 ${VERSION_SOURCES.length}개 파일을 함께 갱신했다.\n`;
  const created = spawnSync('gh', ['pr', 'create', '--base', base, '--head', branch, '--title', title, '--body', body], {
    cwd: root, encoding: 'utf8',
  });
  if (created.status !== 0) fail(`PR 을 만들지 못했다: ${created.stderr.trim()}`, 3);
  const url = created.stdout.trim().split('\n').filter(Boolean).pop() ?? '';
  console.log(`✓ PR 생성 — ${url}`);
  console.log(`PR_URL=${url}`);
  console.log(`PR_BRANCH=${branch}`);
  console.log(`PR_BASE=${base}`);
  console.log('NEXT=PR 이 merge 된 뒤 release 로 이어간다');
}

function cmdRelease(root, versionArg, options) {
  const version = parseSemver(versionArg);
  if (!version) fail('버전은 vX.Y.Z 형식이어야 한다.');
  const versionText = formatVersion(version);
  const tag = `v${versionText}`;
  const base = baseBranch(root);

  if (git(['rev-parse', '--verify', '--quiet', `refs/tags/${tag}`], root).status === 0) {
    fail(`태그 ${tag} 가 이미 있다.`, 3);
  }
  const branch = gitOut(['branch', '--show-current'], root);
  if (branch !== base) fail(`태그는 기본 브랜치(${base})에서 단다. 현재 브랜치: ${branch || '(없음)'}`, 3);
  requireClean(root);

  // bump PR 이 merge 됐는지는 파일 버전으로 확인한다. 안 맞으면 아직 merge 전이다.
  const state = collectVersionState(root);
  if (state.values.length !== 1 || state.values[0] !== versionText) {
    fail(`${base} 의 버전 소스가 ${versionText} 가 아니다 (현재: ${state.values.join(', ') || '없음'}). bump PR 이 merge 됐는지 확인한다.`, 3);
  }

  const previous = latestTag(root, base);
  const notesPath = options.notesFile ? path.resolve(root, options.notesFile) : null;
  const body = notesPath && existsSync(notesPath)
    ? readFileSync(notesPath, 'utf8')
    : renderNotes({
        version: tag,
        previousTag: previous ? previous.tag : null,
        commits: readCommits(root, previous ? previous.tag : null, 'HEAD'),
        slug: remoteSlug(root),
      });

  if (options.dryRun) {
    console.log(body);
    console.log('');
    console.log(`(--dry-run) 태그 ${tag} 와 릴리즈를 만들지 않았다.`);
    console.log(`RELEASE_DRY_RUN=1`);
    console.log(`RELEASE_TAG=${tag}`);
    return;
  }
  requireGh();

  const tagged = git(['tag', '-a', tag, '-m', tag], root);
  if (tagged.status !== 0) fail(`태그를 만들지 못했다: ${tagged.stderr.trim()}`, 3);
  const pushed = git(['push', 'origin', tag], root);
  if (pushed.status !== 0) {
    git(['tag', '-d', tag], root);
    fail(`태그를 push 하지 못했다: ${pushed.stderr.trim()}`, 3);
  }

  const notesFile = path.join(root, '.git', `issue-version-notes-${tag}.md`);
  writeFileSync(notesFile, body.endsWith('\n') ? body : `${body}\n`);
  const released = spawnSync('gh', ['release', 'create', tag, '--title', tag, '--notes-file', notesFile], {
    cwd: root, encoding: 'utf8',
  });
  if (released.status !== 0) fail(`릴리즈를 만들지 못했다: ${released.stderr.trim()}`, 3);
  const url = released.stdout.trim().split('\n').filter(Boolean).pop() ?? '';
  console.log(`✓ 릴리즈 발행 — ${tag}`);
  console.log(`RELEASE_TAG=${tag}`);
  console.log(`RELEASE_URL=${url}`);
  console.log(`PREVIOUS_TAG=${previous ? previous.tag : ''}`);
}

function parseArgs(argv) {
  const options = { positional: [] };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--dry-run') options.dryRun = true;
    else if (arg === '--branch') options.branch = true;
    else if (arg === '--force') options.force = true;
    else if (arg === '--from') options.from = argv[++index];
    else if (arg === '--to') options.to = argv[++index];
    else if (arg === '--version') options.version = argv[++index];
    else if (arg === '--out') options.out = argv[++index];
    else if (arg === '--notes-file') options.notesFile = argv[++index];
    else if (arg === '--help' || arg === '-h') options.help = true;
    else if (arg.startsWith('--')) fail(`알 수 없는 옵션: ${arg}`);
    else options.positional.push(arg);
  }
  return options;
}

function main(argv) {
  const options = parseArgs(argv);
  const [command, ...rest] = options.positional;
  if (!command || options.help) { usage(); process.exit(command ? 0 : 2); }
  const root = repoRoot();
  if (command === 'current') return cmdCurrent(root);
  if (command === 'plan') return cmdPlan(root, rest[0]);
  if (command === 'bump') return cmdBump(root, rest[0], options);
  if (command === 'notes') return cmdNotes(root, options);
  if (command === 'pr') return cmdPr(root, rest[0], options);
  if (command === 'release') return cmdRelease(root, rest[0], options);
  usage();
  process.exit(2);
}

if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(fileURLToPath(import.meta.url))) {
  main(process.argv.slice(2));
}
