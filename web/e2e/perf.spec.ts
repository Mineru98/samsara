// Performance budget of 02 §22 on seeded F-GRAPH data (fake runtime, production bundle).
// Writes perf.json / perf.md to PERF_OUT (default test-results/perf).
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { graphSnapshot } from "./fake/scenarios";
import { FakeRuntime } from "./fake/server";
import { expect, test } from "./fixtures";

/* eslint-disable @typescript-eslint/no-explicit-any */

const OUT = process.env.PERF_OUT ?? path.join("test-results", "perf");
const RUNS = Number(process.env.PERF_RUNS ?? 5);
const SEED = 109;
const BUDGET = { firstScreenMs: 2000, layoutP95Ms: 1000, updateP95Ms: 250 };
const results: any[] = [];

function pct(xs: number[], p: number): number {
  const s = [...xs].sort((a, b) => a - b);
  return s.length ? s[Math.min(s.length - 1, Math.ceil((p / 100) * s.length) - 1)] : NaN;
}
const stats = (xs: number[]) => ({ n: xs.length, p50: Math.round(pct(xs, 50)), p95: Math.round(pct(xs, 95)), p99: Math.round(pct(xs, 99)), max: Math.round(Math.max(...xs)) });

// Records, in page time, when the first issue node appears, when the ELK
// layout lands, and when each node's data-version changes.
const PROBE = `
  window.__perf = { firstNode: null, layout: null, layoutKind: null, versions: {} };
  new MutationObserver((ms) => {
    const p = window.__perf;
    if (p.firstNode === null && document.querySelector('.react-flow__node-issue')) p.firstNode = performance.now();
    const g = document.querySelector('[data-testid=issue-graph]');
    if (p.layout === null && g && (g.dataset.layout === 'elk' || g.dataset.layout === 'fallback')) { p.layout = performance.now(); p.layoutKind = g.dataset.layout; }
    for (const m of ms) if (m.type === 'attributes' && m.attributeName === 'data-version') {
      const n = m.target.closest('.react-flow__node'); if (n) p.versions[n.dataset.id + '@' + m.target.dataset.version] = Date.now();
    }
  }).observe(document, { subtree: true, childList: true, attributes: true, attributeFilter: ['data-layout', 'data-version'] });
`;

for (const size of [200, 1000]) {
  test(`F-GRAPH ${size}노드 성능 예산 (seed ${SEED})`, async ({ browser }) => {
    const first: number[] = [];
    const layout: number[] = [];
    const kinds: string[] = [];
    const updates: number[] = [];
    let missed = 0;
    let domNodes = 0;
    for (let run = 0; run < RUNS; run++) {
      const fake = await new FakeRuntime().start();
      const pid = fake.addProject(graphSnapshot(size, SEED));
      const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
      const page = await ctx.newPage();
      await page.addInitScript(PROBE);
      await page.goto(fake.bootstrapUrl(`/projects/${pid}`));
      await expect(page.locator(".react-flow__node-issue").first()).toBeVisible({ timeout: 30_000 });
      await page.waitForFunction(() => (window as any).__perf.layout !== null, null, { timeout: 30_000 });
      const p = await page.evaluate(() => (window as any).__perf);
      first.push(p.firstNode);
      layout.push(p.layout - p.firstNode);
      kinds.push(p.layoutKind);
      domNodes = await page.locator(".react-flow__node-issue").count();

      if (run === 0) {
        await page.waitForTimeout(500);
        await page.screenshot({ path: path.join(OUT, `graph-${size}.png`) });
        // 50 status events per second for 10 s on nodes that are in the DOM.
        const ids = (await page.locator(".react-flow__node-issue").evaluateAll((els) => els.map((e) => e.getAttribute("data-id")!.slice("issue:".length)))).slice(0, 50);
        const sent: Record<string, number> = {};
        const statuses = ["RUNNING", "VERIFYING", "BLOCKED", "READY"];
        for (let i = 0; i < 500; i++) {
          const id = ids[i % ids.length];
          const cur = fake.entity(pid, "issues", id);
          const version = String(BigInt(cur.version) + 1n);
          sent[`issue:${id}@${version}`] = Date.now();
          fake.change(pid, { upserts: { issues: [{ ...cur, display_status: statuses[i % statuses.length], version }] } });
          await new Promise((r) => setTimeout(r, 20));
        }
        await page.waitForTimeout(1500);
        const seen: Record<string, number> = await page.evaluate(() => (window as any).__perf.versions);
        for (const [k, t] of Object.entries(sent)) {
          if (seen[k] === undefined) missed++;
          else updates.push(seen[k] - t);
        }
        await expect(page.getByTestId("project-version")).toHaveText(`project_version ${fake.project(pid).snap.project_version}`);
      }
      await ctx.close();
      await fake.stop();
    }
    const r = { size, seed: SEED, runs: RUNS, domIssueNodes: domNodes, layoutKinds: [...new Set(kinds)], firstScreen: stats(first), layout: stats(layout), update: { ...stats(updates), sent: 500, missed, ratePerSec: 50 } };
    results.push(r);
    // The budget is enforced: a breach fails the test as well as the report.
    expect(r.firstScreen.p95, "최초 유용 화면 p95").toBeLessThanOrEqual(BUDGET.firstScreenMs);
    // ELK must land before the worker timeout (5 s) at every size; the 1 s
    // layout budget is defined for 200 nodes only (02 §22).
    expect(r.layoutKinds, "ELK 배치가 worker 제한 시간 안에 끝남").toEqual(["elk"]);
    if (size <= 200) expect(r.layout.p95, "그래프 배치 p95 (200노드 예산)").toBeLessThanOrEqual(BUDGET.layoutP95Ms);
    expect(missed, "이벤트 누락").toBe(0);
    expect(r.update.p95, "상태 반영 p95").toBeLessThanOrEqual(BUDGET.updateP95Ms);
  });
}

test.beforeAll(() => fs.mkdirSync(OUT, { recursive: true }));

test.afterAll(async ({ browser }) => {
  const env = { cpu: os.cpus()[0]?.model, cores: os.cpus().length, memGB: Math.round(os.totalmem() / 2 ** 30), os: `${os.type()} ${os.release()}`, node: process.version, browser: `chromium ${browser.version()}`, viewport: "1440x900", build: "vite production (runtime/internal/webui/static)", seed: SEED, runs: RUNS, budget: BUDGET };
  fs.writeFileSync(path.join(OUT, "perf.json"), JSON.stringify({ env, results }, null, 2));
  const row = (label: string, s: any, budget: string) => `| ${label} | ${s.p50} | ${s.p95} | ${s.p99} | ${s.max} | ${budget} |`;
  const md = [
    `# 대시보드 성능 측정 (02 §22)`,
    ``,
    `측정: \`pnpm -C web perf\` · fake runtime(계약 형태 seed 데이터) · 측정 시 load average ${os.loadavg().map((x) => x.toFixed(2)).join(" / ")}`,
    ``,
    `환경: ${env.cpu} · ${env.cores}코어 · ${env.memGB}GB · ${env.os} · Node ${env.node} · ${env.browser} · ${env.viewport} · ${env.build} · seed ${SEED} · 반복 ${RUNS}회`,
    ``,
    ...results.flatMap((r) => [
      `## ${r.size}노드 (DOM 이슈 노드 ${r.domIssueNodes}개, 배치 ${r.layoutKinds.join("/")})`,
      ``,
      `| 지표 (ms) | p50 | p95 | p99 | max | 예산 |`,
      `| --- | ---: | ---: | ---: | ---: | --- |`,
      row("최초 유용 화면 (탐색 시작→첫 노드)", r.firstScreen, `≤ ${BUDGET.firstScreenMs}`),
      row("그래프 배치 (첫 노드→ELK 좌표 반영)", r.layout, r.size <= 200 ? `p95 ≤ ${BUDGET.layoutP95Ms}` : "예산 없음 (200노드 기준), worker 제한 5000 안"),
      row(`상태 반영 (서버 기록→DOM, ${r.update.ratePerSec} ev/s · ${r.update.sent}건 · 누락 ${r.update.missed})`, r.update, `p95 ≤ ${BUDGET.updateP95Ms}`),
      ``,
    ]),
  ].join("\n");
  fs.writeFileSync(path.join(OUT, "perf.md"), md);
});
