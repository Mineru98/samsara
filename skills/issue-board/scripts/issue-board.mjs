#!/usr/bin/env node
/**
 * issue-board.mjs — .issue/graph.json 을 그대로 칸반 HTML(board.html)로 렌더링해 바로 연다.
 *
 *   1) fetch: 그래프를 최신화한다. `.issue/graph.json` 이 없거나 `--sync` 면 issue-onboard sync 로 재생성한다.
 *   2) render: 렌더러 자산(assets/board.html)을 `.issue/board.html` 로 복사하고,
 *              데이터를 인라인한 단일 파일 `.issue/board.standalone.html` 도 함께 만든다.
 *              graph.json 을 `graph-data.js` 사이드카(window.__ISSUE_GRAPH__)로 떨어뜨린다.
 *   3) open: 기본 브라우저로 `.issue/board.html` 을 연다(file://, 서버·포트 없음).
 *
 * board.html 은 순수 렌더러다(외부 라이브러리·웹폰트 0). graph-data.js 를 <script> 로 읽어
 * file:// 더블클릭으로도 동작한다. `.issue/` 는 재생성 캐시라 board.html·graph-data.js 는 커밋되지 않는다.
 *
 *   node issue-board.mjs [--sync] [--no-open]
 *     --sync     : graph.json 이 있어도 GitHub 에서 새로 fetch(재생성)한다.
 *     --no-open  : 파일만 생성하고 브라우저를 열지 않는다(경로만 출력).
 */
import { spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, copyFileSync, existsSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));               // skills/issue-board/scripts
const ASSET = path.join(HERE, '..', 'assets', 'board.html');

function repoRoot() {
  const r = spawnSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error('git 저장소에서 실행해야 한다.');
  return r.stdout.trim();
}

/** 형제 스킬 스크립트를 찾는다(같은 skills/ 아래 또는 저장소 skills/). */
function siblingScript(root, skill, script) {
  const cands = [
    path.join(HERE, '..', '..', skill, 'scripts', script),
    path.join(root, 'skills', skill, 'scripts', script),
  ];
  return cands.find(existsSync) || null;
}

/** 플랫폼별 기본 브라우저로 파일을 연다. */
function openInBrowser(file) {
  const p = process.platform;
  const [cmd, args] = p === 'darwin' ? ['open', [file]]
    : p === 'win32' ? ['cmd', ['/c', 'start', '', file]]
    : ['xdg-open', [file]];
  try {
    const r = spawnSync(cmd, args, { stdio: 'ignore' });
    return r.status === 0;
  } catch { return false; }
}

function main() {
  const argv = process.argv.slice(2);
  const doSync = argv.includes('--sync');
  const noOpen = argv.includes('--no-open');

  const root = repoRoot();
  const outDir = path.join(root, '.issue');
  const graphPath = path.join(outDir, 'graph.json');

  // 1) fetch — 없거나 --sync 면 issue-onboard sync 로 그래프 재생성
  if (doSync || !existsSync(graphPath)) {
    const onboard = siblingScript(root, 'issue-onboard', 'issue-onboard.mjs');
    if (!onboard) throw new Error('issue-onboard 스킬을 찾지 못했다(그래프 fetch 불가). issue-sync/issue-onboard 설치를 확인하라.');
    console.log('그래프 fetch(sync) 중…');
    const r = spawnSync(process.execPath, [onboard, 'sync', '--state', 'all'], { cwd: root, encoding: 'utf8' });
    process.stdout.write(r.stdout || '');
    process.stderr.write(r.stderr || '');
    if (!String(r.stdout).includes('SNAPSHOT_STATUS=complete')) {
      console.error('✗ 그래프 sync 가 완전한 snapshot 을 만들지 못했다. 위 출력을 확인하라.');
      console.log('BOARD=failed');
      process.exit(2);
    }
  }
  if (!existsSync(graphPath)) {
    throw new Error('.issue/graph.json 이 없다. `--sync` 로 생성하거나 issue-sync 를 먼저 실행하라.');
  }

  // graph.json 파싱(손상 시 즉시 실패)
  let graph;
  try {
    graph = JSON.parse(readFileSync(graphPath, 'utf8'));
  } catch (e) {
    console.error('✗ .issue/graph.json 파싱 실패: ' + e.message);
    console.log('BOARD=failed');
    process.exit(1);
  }

  // 2) render — 사이드카 + 렌더러 복사
  const sidecar = `window.__ISSUE_GRAPH__=${JSON.stringify(graph)};\n`;
  writeFileSync(path.join(outDir, 'graph-data.js'), sidecar);
  const boardOut = path.join(outDir, 'board.html');
  copyFileSync(ASSET, boardOut);

  // 2-b) standalone — 데이터를 <script> 안에 인라인해 파일 하나로 완결시킨다.
  //      board.html 은 graph-data.js 를 상대 경로로 읽어서, 파일 하나만 다른 머신으로 옮기면
  //      사이드카를 못 찾고 렌더러 fallback 탓에 에러 없이 빈 보드가 뜬다. 그 경로를 막는다.
  const SIDECAR_TAG = '<script src="graph-data.js"></script>';
  const standaloneOut = path.join(outDir, 'board.standalone.html');
  let standaloneOk = false;
  try {
    const asset = readFileSync(ASSET, 'utf8');
    if (!asset.includes(SIDECAR_TAG)) throw new Error('사이드카 script 태그를 찾지 못했다');
    // 데이터에 </script> 가 섞여도 HTML 파서가 조기 종료하지 않게 한다(JSON 에서 \/ 는 / 로 읽힌다).
    const inlined = sidecar.replace(/<\/script/gi, '<\\/script');
    const inlineBlock = '<script>/* inlined graph-data.js */\n' + inlined + '</script>';
    // 치환자는 반드시 함수로 넘긴다. 문자열이면 데이터 안의 달러-앰퍼샌드 패턴이 해석돼 내용이 깨진다.
    writeFileSync(standaloneOut, asset.replace(SIDECAR_TAG, () => inlineBlock));
    standaloneOk = true;
  } catch (e) {
    // standalone 실패가 기존 2파일 산출물까지 죽이지 않게 한다.
    console.warn('  ! standalone 생성 생략 — ' + e.message);
  }

  const nodes = Object.keys(graph.nodes || {}).length;
  const edges = (graph.edges || []).length;
  console.log(`✓ 보드 생성 — 노드 ${nodes}개, 엣지 ${edges}개`);
  console.log(`  ${path.relative(root, boardOut)}  (+ graph-data.js 사이드카)`);
  if (standaloneOk) console.log(`  ${path.relative(root, standaloneOut)}  (데이터 인라인 · 파일 1개로 완결)`);
  console.log(`BOARD_HTML=${boardOut}`);
  if (standaloneOk) console.log(`BOARD_STANDALONE=${standaloneOut}`);
  console.log('BOARD=ok');

  // 3) open
  if (noOpen) {
    console.log('  --no-open: 브라우저를 열지 않았다. 위 파일을 직접 열어라.');
    return;
  }
  const opened = openInBrowser(boardOut);
  console.log(opened
    ? '  브라우저로 열었다.'
    : `  자동 열기 실패 — 브라우저로 file://${boardOut} 를 직접 열어라.`);
}

try {
  main();
} catch (e) {
  console.error('✗ ' + e.message);
  console.log('BOARD=failed');
  process.exit(1);
}
