import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

/*
 * 이슈 #55. 프로브는 실제 Windows 에서 돌리는 도구라 여기서는 판정 로직이 아니라
 * "어느 플랫폼에서든 읽기 전용으로 끝까지 돌고 약속한 형식을 낸다" 를 확인한다.
 */

const PROBE = path.join(path.dirname(fileURLToPath(import.meta.url)), 'windows-probe.mjs');

test('프로브는 여섯 절과 요약을 출력하고 --out 파일에도 같은 내용을 쓴다', () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'probe-'));
  try {
    const out = path.join(dir, 'report.txt');
    const r = spawnSync(process.execPath, [PROBE, '--out', out], { encoding: 'utf8', timeout: 60000 });
    assert.ok([0, 1].includes(r.status), `exit 0 또는 1 이어야 한다: ${r.status} ${r.stderr}`);
    for (const heading of ['## 1. 실행 환경', '## 2. 실제 설치 위치', '## 3. 탐지 정책', '## 4. 저장소 인식', '## 5. gh 호출', '## 6. 네이티브 의존성 조회']) {
      assert.ok(r.stdout.includes(heading), `${heading} 절이 있어야 한다`);
    }
    assert.match(r.stdout, /## 요약: PASS \d+ \/ FAIL \d+ \/ SKIP \d+/);
    assert.match(r.stdout, /\[(PASS|FAIL)\] git 탐지/);
    assert.equal(readFileSync(out, 'utf8'), r.stdout);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('프로브는 토큰 값을 출력하지 않는다', () => {
  const secret = 'ghp_probe_secret_value_should_not_leak';
  const r = spawnSync(process.execPath, [PROBE], {
    encoding: 'utf8',
    timeout: 60000,
    env: { ...process.env, GH_TOKEN: secret, GITHUB_TOKEN: secret },
  });
  assert.ok(!r.stdout.includes(secret), '토큰 값이 출력에 나오면 안 된다');
  assert.match(r.stdout, /GH_TOKEN=\(설정됨\)/);
});

test('모르는 인자는 exit 2 로 거부한다', () => {
  const r = spawnSync(process.execPath, [PROBE, '--nope'], { encoding: 'utf8' });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /알 수 없는 인자/);
});
