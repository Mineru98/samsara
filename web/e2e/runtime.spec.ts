// Real-runtime scenarios: the dashboard against a real `samsara` daemon in an
// isolated SAMSARA_HOME (tmux backend, sleep workers). Checks that what the
// browser shows is the server projection (F-ABC, F-JOIN, F-STREAM) and that
// closing the browser leaves the runtime and its workers running (UI-T13).
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test as base, expect, type Page } from "@playwright/test";
import { buildRuntime, RealRuntime, waitFor } from "./runtime/harness";

/* eslint-disable @typescript-eslint/no-explicit-any */

/** Every attempt a test saw, so teardown can kill its tmux sessions even if the daemon died. */
const seenAttempts = new Set<string>();

const test = base.extend<object, { rt: RealRuntime }>({
  rt: [
    async ({}, use) => {
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), "samsara-e2e-"));
      const rt = await new RealRuntime(buildRuntime(dir), dir).start();
      try {
        await use(rt);
      } finally {
        const attempts: string[] = [];
        try {
          for (const p of rt.cli("status").projects ?? []) {
            const s = await rt.snapshot(p.id).catch(() => null);
            for (const a of s?.entities.attempts ?? []) attempts.push(a.id);
          }
        } catch {
          /* daemon gone: fall back to the attempts the tests recorded */
        }
        await rt.stop([...new Set([...attempts, ...seenAttempts])]);
        fs.rmSync(dir, { recursive: true, force: true });
      }
    },
    { scope: "worker", timeout: 120_000 },
  ],
});

async function openProject(page: Page, rt: RealRuntime, projectId: string): Promise<void> {
  await page.goto(rt.bootstrapUrl(`/projects/${encodeURIComponent(projectId)}`));
  await expect(page.getByTestId("runtime-status")).toHaveText("런타임 실시간 연결");
  await expect(page.getByTestId("issue-graph")).toBeVisible();
}

/** Screen project_version and every issue node version equal the server snapshot. */
async function expectProjection(page: Page, rt: RealRuntime, projectId: string): Promise<any> {
  let snap: any;
  await expect
    .poll(
      async () => {
        snap = await rt.snapshot(projectId);
        const screen = await page.getByTestId("project-version").textContent();
        const nodes = await page.locator(".react-flow__node-issue").evaluateAll((els) => Object.fromEntries(els.map((el) => [el.getAttribute("data-id"), el.querySelector(".gnode")?.getAttribute("data-version")])));
        const server = Object.fromEntries(snap.entities.issues.map((i: any) => [`issue:${i.id}`, i.version]));
        return JSON.stringify({ v: screen, nodes }) === JSON.stringify({ v: `project_version ${snap.project_version}`, nodes: sortKeys(nodes, server) });
      },
      { message: "screen versions equal the server projection", timeout: 15_000 },
    )
    .toBe(true);
  return snap;
}

/** Server versions in the screen's key order so JSON comparison is exact. */
function sortKeys(order: Record<string, unknown>, server: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const k of Object.keys(order)) out[k] = server[k];
  for (const k of Object.keys(server)) if (!(k in out)) out[k] = server[k];
  return out;
}

const ABC_ISSUES = [{ key: "a" }, { key: "b" }, { key: "c" }];
const ABC_DEPS = [{ from: "a", to: "b", milestone: "RESULT_APPROVED" as const }];

test("F-ABC 실제 runtime: 화면 version 이 서버 projection 과 같다", async ({ page, rt }) => {
  const p = rt.createProject("f-abc");
  const plan = rt.applyPlan(p.id, ABC_ISSUES, ABC_DEPS);
  await openProject(page, rt, p.id);
  await expectProjection(page, rt, p.id);

  rt.cli("run", "start", plan.run_id);
  const snap = await expectProjection(page, rt, p.id);
  expect(snap.entities.runs[0].state).toBe("ACTIVE");
  const b = snap.entities.issues.find((i: any) => i.client_key === "b");
  await expect(page.locator(`.react-flow__node[data-id="issue:${b.id}"]`)).toContainText("차단됨");
  const s2 = await waitFor(() => rt.snapshot(p.id), (s) => s.entities.attempts.length === 2, "A and C attempts");
  s2.entities.attempts.forEach((a: any) => seenAttempts.add(a.id));
  await expectProjection(page, rt, p.id);
});

test("F-JOIN 실제 runtime: 합류 게이트와 version 이 서버 projection 과 같다", async ({ page, rt }) => {
  const p = rt.createProject("f-join");
  const plan = rt.applyPlan(
    p.id,
    [{ key: "a" }, { key: "b" }, { key: "c" }, { key: "d" }],
    [
      { from: "a", to: "b", milestone: "IMPLEMENTED" },
      { from: "a", to: "c", milestone: "IMPLEMENTED" },
      { from: "b", to: "d", milestone: "INTEGRATED" },
      { from: "c", to: "d", milestone: "INTEGRATED" },
    ],
  );
  await openProject(page, rt, p.id);
  const snap = await expectProjection(page, rt, p.id);
  // D waits for B and C together: one AND gate with two members.
  const gates = snap.entities.gates.filter((g: any) => g.members.length >= 2);
  expect(gates).toHaveLength(1);
  await expect(page.locator(".react-flow__node-gate")).toHaveCount(1);
  await expect(page.locator(".react-flow__node-gate")).toContainText(`0/2`);

  rt.cli("run", "start", plan.run_id);
  await waitFor(() => rt.snapshot(p.id), (s) => s.entities.attempts.length === 1, "A attempt");
  await expectProjection(page, rt, p.id);
});

test("F-STREAM 실제 runtime: 두 프로젝트 이벤트가 교차해도 화면은 자기 projection 만 따른다", async ({ page, rt }) => {
  const p1 = rt.createProject("f-stream-1");
  const p2 = rt.createProject("f-stream-2");
  const plan1 = rt.applyPlan(p1.id, ABC_ISSUES, ABC_DEPS);
  await openProject(page, rt, p1.id);
  const plan2 = rt.applyPlan(p2.id, ABC_ISSUES, ABC_DEPS);
  rt.cli("run", "start", plan1.run_id);
  rt.cli("run", "start", plan2.run_id);
  await waitFor(async () => [(await rt.snapshot(p1.id)).entities.attempts.length, (await rt.snapshot(p2.id)).entities.attempts.length], (n) => n[0] === 2 && n[1] === 2, "attempts in both projects");
  rt.cli("run", "pause", plan2.run_id);
  rt.cli("run", "pause", plan1.run_id);

  const s1 = await expectProjection(page, rt, p1.id);
  const s2 = await rt.snapshot(p2.id);
  // Events interleave by construction: p1 start, p2 start, p2 pause, p1 pause
  // each commit in their own project on the one global seq (F-STREAM). The
  // screen above matched p1's projection after p2's events in between.
  expect(s1.entities.runs[0].state).toBe("PAUSED");
  expect(s2.entities.runs[0].state).toBe("PAUSED");
  await expect(page.locator(".react-flow__node-issue")).toHaveCount(3);
  for (const i of s2.entities.issues) await expect(page.locator(`.react-flow__node[data-id="issue:${i.id}"]`)).toHaveCount(0);
  // The sidebar row of the other project follows its catalog summary.
  const summary = rt.cli("status").projects.find((x: any) => x.id === p2.id);
  const row = page.getByTestId("project-nav").locator(".proj", { hasText: "f-stream-2" });
  for (const [label, n] of [["실행", summary.running], ["승인", summary.approval_pending], ["차단", summary.blocked], ["통합", summary.integration_pending]] as const) {
    if (n > 0) await expect(row).toContainText(`${label} ${n}`);
    else await expect(row).not.toContainText(`${label} `);
  }
  await page.getByTestId("project-nav").getByRole("link", { name: /f-stream-2/ }).click();
  await expectProjection(page, rt, p2.id);
});

test("UI-T13 브라우저를 닫고 재접속: runtime·worker 는 계속 동작하고 최신 상태를 복구한다", async ({ browser, rt }) => {
  const p = rt.createProject("ui-t13");
  const plan = rt.applyPlan(p.id, ABC_ISSUES, ABC_DEPS);
  rt.cli("run", "start", plan.run_id);
  const started = await waitFor(() => rt.snapshot(p.id), (s) => s.entities.attempts.length === 2, "A and C attempts");
  const ids: string[] = started.entities.attempts.map((a: any) => a.id);
  ids.forEach((id) => seenAttempts.add(id));
  // Exactly this test's two workers, found through their own tmux sessions.
  const workers = await waitFor(() => rt.workerPids(ids), (pids) => pids.length === 2, "this test's two worker processes");

  const first = await browser.newContext();
  const page1 = await first.newPage();
  await openProject(page1, rt, p.id);
  await expectProjection(page1, rt, p.id);
  const daemonPid = rt.daemonPid();
  await first.close(); // the browser goes away

  await new Promise((r) => setTimeout(r, 6000)); // > one launcher heartbeat
  expect(rt.daemonAlive()).toBe(true);
  expect(rt.daemonPid()).toBe(daemonPid);
  expect(rt.workerPids(ids).sort()).toEqual([...workers].sort());
  for (const pid of workers) expect(() => process.kill(pid, 0)).not.toThrow();
  const during = await rt.snapshot(p.id);
  expect(during.entities.runs[0].state).toBe("ACTIVE");
  expect(during.entities.attempts.every((a: any) => !["CANCELLED", "CANCEL_REQUESTED", "FAILED", "LOST"].includes(a.state))).toBe(true);

  // Work continues without a browser: the user pauses the run from the CLI.
  rt.cli("run", "pause", plan.run_id);
  await waitFor(() => rt.snapshot(p.id), (s) => s.entities.runs[0].state === "PAUSED", "run paused");

  const second = await browser.newContext();
  const page2 = await second.newPage();
  await openProject(page2, rt, p.id);
  await expectProjection(page2, rt, p.id);
  await expect(page2.locator(".summary select")).toContainText("일시 정지");
  await expect(page2.getByRole("button", { name: "재개 요청" })).toBeVisible();
  await second.close();
});
