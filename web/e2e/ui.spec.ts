// UI-T01~T24 browser scenarios (02 §25.2) against the in-process fake
// runtime. Fake-data results are reported separately from the real-runtime
// scenarios in runtime.spec.ts (02 §25). UI-T13 needs a real daemon and a
// real worker, so it lives in runtime.spec.ts.
import type { Page } from "@playwright/test";
import { ABC, abc, abcApprovalPending, DIGEST_2, find, otherProject, resultApproval } from "./fake/scenarios";
import { expect, expectVersion, node, open, openProject, test } from "./fixtures";

/* eslint-disable @typescript-eslint/no-explicit-any */

const snapshotCalls = (fake: { requests: { path: string }[] }, pid: string) => fake.requests.filter((r) => r.path === `/v1/projects/${pid}/snapshot`).length;

test("UI-T01 A 승인 대기·B는 A 의존·C 독립: 이유가 보이고 C 갱신은 계속된다", async ({ page, fake }) => {
  fake.addProject(abcApprovalPending());
  await openProject(page, fake, ABC.project);

  await expect(node(page, ABC.a)).toContainText("승인 대기");
  await expect(node(page, ABC.b)).toContainText("차단됨");
  await expect(node(page, ABC.b)).toContainText("선행 이슈의 결과 승인이 필요합니다.");
  await expect(page.getByTestId("pending-approvals")).toContainText("RESULT · 대기");

  // C keeps moving while A waits: the stream applies C's change, A/B stay.
  fake.patch(ABC.project, "issues", ABC.c, { display_status: "VERIFYING", execution_state: "VERIFYING" });
  await expect(node(page, ABC.c)).toContainText("검증 중");
  await expect(node(page, ABC.a)).toContainText("승인 대기");
  await expect(node(page, ABC.b)).toContainText("차단됨");
  await expectVersion(page, fake, ABC.project);

  await node(page, ABC.b).click();
  const insp = page.getByTestId("inspector");
  await expect(insp).toContainText("DEPENDENCY_APPROVAL_PENDING");
  await expect(insp).toContainText("선행 a 결과 승인 필요");
});

test("UI-T02 A 승인 후 B 조건 충족·Standalone: B를 자동 완료로 보이지 않고 수동 계속을 안내한다", async ({ page, fake }) => {
  fake.addProject(abcApprovalPending());
  await openProject(page, fake, ABC.project);
  const e = fake.project(ABC.project).snap.entities;
  const wt = { ...e.worktrees[0], id: "wt_b", attempt_id: "att_b", branch_ref: "refs/heads/samsara/b/att_b", path: "/fixture/wt/att_b", version: "1" };
  const att = { ...find(e, "attempts", ABC.attA), id: "att_b", issue_id: ABC.b, state: "PENDING", backend_kind: "standalone", resume_required: true, session_id: undefined, lease_state: undefined, lease_fence: undefined, worktree_id: "wt_b", version: "1" };
  fake.change(ABC.project, {
    upserts: {
      approvals: [{ ...find(e, "approvals", "apr_res_a"), state: "APPROVED", decided_at: "2026-10-28T01:06:00Z", decision_actor: "user:test", version: "2" }],
      dependencies: [{ ...find(e, "dependencies", ABC.dep), met: true, version: "2" }],
      issues: [
        { ...find(e, "issues", ABC.a), display_status: "VERIFIED", available_actions: [], version: "5" },
        { ...find(e, "issues", ABC.b), display_status: "READY", blocking_reasons: [], resume_required: true, current_attempt_id: "att_b", version: "3" },
      ],
      attempts: [att],
      worktrees: [wt],
    },
  });
  await expect(node(page, ABC.b)).toContainText("실행 가능");
  for (const done of ["실행 중", "검증 완료", "통합 완료"]) await expect(node(page, ABC.b)).not.toContainText(done);
  await node(page, ABC.b).click();
  const insp = page.getByTestId("inspector");
  await expect(insp).toContainText("수동 계속 필요");
  await expect(insp).toContainText(`samsara session attach --issue ${ABC.b}`);
  await expect(insp).toContainText("/fixture/wt/att_b");
  expect(fake.commands()).toHaveLength(0);
});

test("UI-T03 herdr 관찰이 blocked: 삼사라 승인 대기로 바꾸지 않는다", async ({ page, fake }) => {
  const s = abc();
  Object.assign(find(s.entities, "sessions", ABC.sesC), { backend: { binding_id: "bnd_h", generation: "g1", instance: "herdr-local", kind: "herdr", ref: { pane_id: "w1:p1" } }, backend_raw_state: "blocked", input_state: "NATIVE_PERMISSION" });
  Object.assign(find(s.entities, "attempts", ABC.attC), { backend_kind: "herdr" });
  fake.addProject(s);
  await openProject(page, fake, ABC.project);

  await expect(node(page, ABC.c)).toContainText("실행 중");
  await expect(node(page, ABC.c)).not.toContainText("승인 대기");
  await expect(page.getByTestId("metrics")).toContainText("승인 대기0");
  await expect(page.getByTestId("pending-approvals")).toHaveCount(0);
  await expect(page.getByTestId("activity-dock")).toContainText("NATIVE_PERMISSION");
  await node(page, ABC.c).click();
  await expect(page.getByTestId("inspector")).toContainText("입력 NATIVE_PERMISSION");
  await expect(page.getByTestId("approval-card")).toHaveCount(0);
});

test("UI-T04 herdr 연결 단절·워커 생존 미확인: 관찰 불가로 두고 tmux 이동·종료로 보이지 않는다", async ({ page, fake }) => {
  const s = abc();
  Object.assign(find(s.entities, "sessions", ABC.sesC), { backend: { binding_id: "bnd_h", generation: "g1", instance: "herdr-local", kind: "herdr", ref: { pane_id: "w1:p1" } } });
  Object.assign(find(s.entities, "attempts", ABC.attC), { backend_kind: "herdr" });
  fake.addProject(s);
  await openProject(page, fake, ABC.project);

  const ses = find(fake.project(ABC.project).snap.entities, "sessions", ABC.sesC);
  fake.change(ABC.project, { upserts: { sessions: [{ ...ses, liveness: "UNKNOWN", input_state: "UNKNOWN", version: "2" }] } });
  const dock = page.getByTestId("activity-dock");
  const row = dock.locator("tr", { hasText: "herdr" });
  await expect(row).toContainText("관찰 불가");
  await expect(row).not.toContainText("종료됨");
  // C's session is never shown as moved to tmux (A's own session is tmux).
  await expect(row.locator("td").nth(1)).toHaveText("herdr");
  await expect(dock.locator("tbody tr")).toHaveCount(2);
  await expect(node(page, ABC.c)).toContainText("실행 중");
  for (const t of ["실패", "완료", "취소됨"]) await expect(node(page, ABC.c)).not.toContainText(t);
  await node(page, ABC.c).click();
  await expect(page.getByTestId("inspector")).toContainText("관찰 불가");
  await expect(page.getByTestId("inspector")).toContainText("herdr");
  expect(fake.commands()).toHaveLength(0);
});

test("UI-T05 결과 제출 후 Queue 대기: 구현 결과 제출과 통합 대기를 함께 보인다", async ({ page, fake }) => {
  const s = abc();
  const a = find(s.entities, "issues", ABC.a);
  Object.assign(a, { display_status: "INTEGRATING", execution_state: "VERIFIED", integration_state: "WAITING", version: "6" });
  s.entities.merge_candidates.push({ approval_gate: null, attempt_id: ABC.attA, authority: "samsara_local", blocking_reasons: [], enqueued: false, id: "mc_a", issue_id: ABC.a, native_queue_state: "NONE", output_id: "out_a1", project_id: ABC.project, queue_position: "1", revision: "1", run_id: ABC.run, source_sha: "0000000000000000000000000000000000000a01", state: "WAITING", state_reason: "AWAITING_ENQUEUE", target_id: ABC.target, target_ref: "refs/heads/integration", version: "1" });
  fake.addProject(s);
  await openProject(page, fake, ABC.project);

  await expect(node(page, ABC.a)).toContainText("통합 대기");
  await expect(node(page, ABC.a)).not.toContainText("통합 완료");
  const queue = page.getByTestId("merge-queue");
  await expect(queue.locator(".qitem")).toContainText("대기");
  await expect(queue.getByRole("button", { name: "통합 요청" })).toBeEnabled();
  await node(page, ABC.a).click();
  const insp = page.getByTestId("inspector");
  await expect(insp.locator("dd").nth(0)).toHaveText("검증 완료");
  await expect(insp.locator("dd").nth(1)).toHaveText("대기");
  await expect(insp).toContainText("refs/heads/integration");

  // Opened from the Merge Queue: Escape returns focus to that queue item (02 §20.1).
  await page.keyboard.press("Escape");
  const item = page.getByTestId("merge-queue").locator(".qitem button.linklike");
  await item.focus();
  await page.keyboard.press("Enter");
  await expect(insp).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(insp).toHaveCount(0);
  await expect(page.getByTestId("merge-queue").locator(".qitem button.linklike")).toBeFocused();
});

test("UI-T06 Queue 검증 중 대상 Commit 변경: 이전 검증 기준과 재검증 필요를 보인다", async ({ page, fake }) => {
  const s = abc();
  const base1 = "b1".repeat(20);
  s.entities.merge_candidates.push({ approval_gate: null, attempt_id: ABC.attA, authority: "samsara_local", base_sha: base1, blocking_reasons: [], enqueued: true, id: "mc_a", issue_id: ABC.a, native_queue_state: "NONE", output_id: "out_a1", project_id: ABC.project, queue_position: "1", revision: "1", run_id: ABC.run, source_sha: "a0".repeat(20), state: "VALIDATING", target_id: ABC.target, target_ref: "refs/heads/integration", version: "3" });
  fake.addProject(s);
  await openProject(page, fake, ABC.project);
  const item = page.getByTestId("merge-queue").locator(".qitem");
  await expect(item).toContainText("검증 중");
  await expect(item).toContainText(`검증 기준 ${base1.slice(0, 9)}`);

  const c = find(fake.project(ABC.project).snap.entities, "merge_candidates", "mc_a");
  fake.change(ABC.project, { upserts: { merge_candidates: [{ ...c, state: "STALE_BASE", state_reason: "TARGET_MOVED", version: "4" }] } });
  await expect(item).toContainText("기준 변경됨");
  await expect(item).toContainText("대상 커밋이 바뀌어 재검증이 필요합니다.");
  await expect(item).toContainText(`검증 기준 ${base1.slice(0, 9)}`);
  await expect(item).not.toContainText("통합됨");
});

test("UI-T07 서로 다른 대상 브랜치 Queue: 하나가 대기해도 다른 Queue 진행을 보인다", async ({ page, fake }) => {
  const s = abc();
  s.entities.integration_targets.push({ authority: "samsara_local", id: "tgt_rel", policy_version: "1", repository_id: "repo_fx001", target_ref: "refs/heads/release", version: "1" });
  const cand = (id: string, issue: string, target: string, ref: string, state: string) => ({ approval_gate: null, attempt_id: "att_x", authority: "samsara_local", base_sha: "c0".repeat(20), blocking_reasons: state === "WAITING" ? [{ code: "APPROVAL_REQUIRED", message: "통합 승인이 필요합니다." }] : [], enqueued: state !== "WAITING", id, issue_id: issue, native_queue_state: "NONE", output_id: "out", project_id: ABC.project, queue_position: "1", revision: "1", run_id: ABC.run, source_sha: "d0".repeat(20), state, target_id: target, target_ref: ref, version: "1" });
  s.entities.merge_candidates.push(cand("mc_int", ABC.a, ABC.target, "refs/heads/integration", "WAITING"), cand("mc_rel", ABC.c, "tgt_rel", "refs/heads/release", "VALIDATING"));
  fake.addProject(s);
  await openProject(page, fake, ABC.project);
  const queue = page.getByTestId("merge-queue");
  const integ = queue.locator("section.queue", { hasText: "integration" });
  const rel = queue.locator("section.queue", { hasText: "release" });
  await expect(integ).toContainText("통합 승인이 필요합니다.");
  await expect(rel).toContainText("검증 중");

  const c = find(fake.project(ABC.project).snap.entities, "merge_candidates", "mc_rel");
  fake.change(ABC.project, { upserts: { merge_candidates: [{ ...c, state: "PUBLISHING", candidate_sha: "e0".repeat(20), version: "2" }] } });
  await expect(rel).toContainText("반영 중");
  await expect(integ.locator(".qitem")).toContainText("대기");
  await expect(integ).toContainText("통합 승인이 필요합니다.");
});

test("UI-T08 SSE 단절 후 중복 재생: 중복 이벤트·노드·카운터가 없다", async ({ page, fake }) => {
  fake.addProject(abc());
  await openProject(page, fake, ABC.project);
  fake.patch(ABC.project, "issues", ABC.c, { title: "C 첫 변경" });
  await expect(node(page, ABC.c)).toContainText("C 첫 변경");

  fake.dropStreams();
  fake.patch(ABC.project, "issues", ABC.b, { title: "B 단절 중 변경" });
  await expect(node(page, ABC.b)).toContainText("B 단절 중 변경");
  await expectVersion(page, fake, ABC.project);

  // The server replays everything again on the open stream.
  fake.replay(ABC.project);
  fake.patch(ABC.project, "issues", ABC.a, { title: "A 마지막 변경" });
  await expect(node(page, ABC.a)).toContainText("A 마지막 변경");
  await expectVersion(page, fake, ABC.project);
  await expect(page.locator(".react-flow__node-issue")).toHaveCount(3);
  await expect(node(page, ABC.b)).toContainText("B 단절 중 변경");
  await expect(page.getByTestId("metrics")).toContainText("실행 중2");
  await expect(page.getByTestId("metrics")).toContainText("차단됨1");
  await expect(page.getByTestId("project-nav").locator(".proj")).toHaveCount(1);
  expect(snapshotCalls(fake, ABC.project)).toBe(1);
});

test("UI-T09 cursor 보관 범위 만료: 전체 Snapshot으로 복구하고 잘못된 실시간 표시가 없다", async ({ page, fake }) => {
  fake.addProject(abc());
  await openProject(page, fake, ABC.project);
  fake.dropStreams(ABC.project);
  fake.patch(ABC.project, "issues", ABC.c, { title: "C 보관 범위 밖 변경" });
  fake.patch(ABC.project, "issues", ABC.c, { display_status: "VERIFYING" });
  fake.expireRetention(ABC.project);

  await expect.poll(() => snapshotCalls(fake, ABC.project)).toBe(2);
  await expect(node(page, ABC.c)).toContainText("C 보관 범위 밖 변경");
  await expect(node(page, ABC.c)).toContainText("검증 중");
  await expectVersion(page, fake, ABC.project);
  await expect(page.getByTestId("connection-banner")).toHaveCount(0);
});

test("UI-T10 프로젝트 빠르게 전환: 이전 이벤트·응답이 새 화면을 오염하지 않는다", async ({ page, fake }) => {
  fake.addProject(abc());
  fake.addProject(otherProject());
  await openProject(page, fake, "prj_other");
  fake.snapshotDelay.set(ABC.project, 800);
  const nav = page.getByTestId("project-nav");
  for (let i = 0; i < 3; i++) {
    await nav.getByRole("link", { name: /repo\s/ }).first().click();
    await nav.getByRole("link", { name: /other-repo/ }).click();
  }
  // Late snapshots of the abandoned project and its events arrive now.
  fake.patch(ABC.project, "issues", ABC.c, { title: "이전 프로젝트 이벤트" });
  await page.waitForTimeout(1500);
  await expect(page.locator("#project-title")).toHaveText("other-repo");
  await expectVersion(page, fake, "prj_other");
  await expect(page.getByText("이전 프로젝트 이벤트")).toHaveCount(0);
  await expect(page.locator(".react-flow__node-issue")).toHaveCount(3);
  await expect(node(page, "issue_fx001")).toBeVisible();
  // Only one project stream stays open: the current one.
  await expect.poll(() => fake.openStreams(ABC.project)).toBe(0);
  await expect.poll(() => fake.openStreams("prj_other")).toBe(1);
});

test("UI-T11 다른 탭에서 승인 먼저 처리: 처리된 결과를 보이고 이중 승인이 없다", async ({ page, fake, context }) => {
  fake.addProject(abcApprovalPending());
  fake.commandHandler = (_req, f) => {
    const apr = find(f.project(ABC.project).snap.entities, "approvals", "apr_res_a");
    if (apr.state !== "PENDING") return { status: 409, body: { error: { code: "CONFLICT", message: "approval already decided", retryable: false } } };
    f.patch(ABC.project, "approvals", "apr_res_a", { state: "APPROVED", decided_at: new Date().toISOString(), decision_actor: "user:ui" });
    return { status: 202, body: { approval_id: apr.id, state: "APPROVED" } };
  };
  const other = await context.newPage();
  await openProject(page, fake, ABC.project);
  await openProject(other, fake, ABC.project);

  await other.getByTestId("pending-approvals").getByRole("button", { name: "승인" }).click();
  await expect(other.getByTestId("pending-approvals")).toContainText("접수됨");

  const card = page.getByTestId("pending-approvals").getByTestId("approval-card");
  await expect(card).toContainText("RESULT · 승인됨");
  await expect(card.getByRole("button", { name: "승인" })).toBeDisabled();
  await expect(card).toContainText("이미 처리된 승인입니다.");
  await expect(page.getByTestId("pending-approvals")).toContainText("승인 대기 0");
  expect(fake.commands().filter((r) => r.path.includes("/decisions"))).toHaveLength(1);
});

test("UI-T12 승인 내용 revision 변경: 이전 내용 승인은 거부되고 최신 내용을 다시 검토한다", async ({ page, fake }) => {
  fake.addProject(abcApprovalPending());
  fake.commandHandler = (req, f) => {
    const apr = find(f.project(ABC.project).snap.entities, "approvals", "apr_res_a");
    if (req.body?.subject_digest !== apr.subject_digest || req.ifMatch !== `"${apr.id}:v${apr.version}"`) {
      return { status: 412, body: { error: { code: "APPROVAL_SUBJECT_CHANGED", message: "approval subject changed", retryable: false, details: { refresh_resource: `/v1/approvals/${apr.id}` } } } };
    }
    f.patch(ABC.project, "approvals", apr.id, { state: "APPROVED", decided_at: new Date().toISOString(), decision_actor: "user:ui" });
    return { status: 202, body: { approval_id: apr.id, state: "APPROVED" } };
  };
  await openProject(page, fake, ABC.project);
  const card = () => page.getByTestId("pending-approvals").getByTestId("approval-card");
  await expect(card()).toContainText(`digest ${resultApproval(ABC.a).subject_digest.slice(0, 12)}`);

  // The subject changes on the server; this tab has not seen it yet.
  const apr = find(fake.project(ABC.project).snap.entities, "approvals", "apr_res_a");
  const ev = fake.change(ABC.project, { upserts: { approvals: [{ ...apr, subject: { ...apr.subject, source_sha: "0000000000000000000000000000000000000a02" }, subject_digest: DIGEST_2, version: "2" }] } }, "approval.revised", { publish: false });
  await card().getByRole("button", { name: "승인" }).click();
  await expect(card()).toContainText("거절됨: APPROVAL_SUBJECT_CHANGED");
  expect(find(fake.project(ABC.project).snap.entities, "approvals", "apr_res_a").state).toBe("PENDING");

  // The latest content arrives; review shows the new digest and approving it succeeds.
  fake.replay(ABC.project, fake.project(ABC.project).events.findIndex((x) => x.ev === ev));
  await expect(card()).toContainText(`digest ${DIGEST_2.slice(0, 12)}`);
  await card().locator("summary").click();
  await expect(card().locator("pre.subject")).toContainText("0000000000000000000000000000000000000a02");
  await card().getByRole("button", { name: "승인" }).click();
  await expect(card()).toContainText("RESULT · 승인됨");
  const decisions = fake.commands().filter((r) => r.path.includes("/decisions"));
  expect(decisions.map((d) => d.body.subject_digest)).toEqual([resultApproval(ABC.a).subject_digest, DIGEST_2]);
  expect(decisions[0].idempotencyKey).not.toBe(decisions[1].idempotencyKey);
});

test("UI-T14 과거·비활성 프로젝트 선택: 자동 활성화 없이 기록을 표시한다", async ({ page, fake }) => {
  fake.addProject(abc());
  const past = otherProject("prj_past", "past-repo");
  past.project.activation = "DISABLED";
  past.project.registry_state = "MISSING";
  past.entities.runs[0].state = "COMPLETED";
  fake.addProject(past, { history: [{ action: "merge.enqueue", actor: "user:test", created_at: "2026-10-28T01:00:00Z", detail: {}, id: "aud_1", subject_id: "mc_fx001", subject_type: "merge_candidate" }] });
  await openProject(page, fake, ABC.project);

  const nav = page.getByTestId("project-nav");
  await nav.getByRole("button", { name: /보관·과거/ }).click();
  await nav.getByRole("link", { name: /past-repo/ }).click();
  await expect(page.locator("#project-title")).toHaveText("past-repo");
  await expect(page.locator(".summary")).toContainText("비활성");
  await expect(page.locator(".summary")).toContainText("경로 없음");
  await expect(page.locator(".summary select")).toContainText("완료");
  await expect(page.getByRole("button", { name: /일시 정지 요청|재개 요청/ })).toHaveCount(0);
  await page.getByRole("tab", { name: "이벤트" }).click();
  await expect(page.getByTestId("activity-dock")).toContainText("merge.enqueue");
  await page.waitForTimeout(500);
  expect(fake.commands()).toHaveLength(0);
});

test("UI-T15 GitHub 연결 끊김: 로컬 상태 갱신은 계속되고 외부 정보 신선도를 구분한다", async ({ page, fake }) => {
  const s = abc();
  s.freshness = { backend_observation: "LIVE", github: "UNAVAILABLE", runtime: "LIVE" };
  fake.addProject(s, { github: { error: "gh auth required", freshness: "UNAVAILABLE", items: [], last_attempt_at: "2026-10-28T01:10:00Z", last_synced_at: "2026-10-28T00:10:00Z", repository: "owner/repo", retry_at: null, sync: "AUTH_REQUIRED", truncated: false } });
  await openProject(page, fake, ABC.project);
  await expect(page.getByTestId("metrics")).toContainText("runtime LIVE · backend LIVE · GitHub UNAVAILABLE");
  await page.getByRole("tab", { name: "GitHub" }).click();
  const dock = page.getByTestId("activity-dock");
  await expect(dock).toContainText("사용 불가");
  await expect(dock).toContainText("AUTH_REQUIRED");
  await expect(dock).toContainText("gh auth required");

  fake.patch(ABC.project, "issues", ABC.c, { display_status: "VERIFYING" });
  await expect(node(page, ABC.c)).toContainText("검증 중");
  await expectVersion(page, fake, ABC.project);
  await expect(page.getByTestId("connection-banner")).toHaveCount(0);
});

test("UI-T16 긴 제목·다중 루트·교차 간선: 노드·간선 라벨이 겹치거나 잘리지 않는다", async ({ page, fake }) => {
  const s = abc();
  const long = "아주 긴 한국어 이슈 제목이 두 줄을 넘어가도 카드 밖으로 넘치지 않고 말줄임으로 정리되는지 확인하는 제목";
  const base = find(s.entities, "issues", ABC.c);
  const extra = ["d", "e", "f", "g", "h"].map((k, i) => ({ ...base, id: `issue_x${k}`, client_key: k, title: `${k.toUpperCase()} ${long}`, current_attempt_id: undefined, display_status: i % 2 ? "READY" : "PLANNED", version: "1" }));
  s.entities.issues.push(...extra);
  find(s.entities, "issues", ABC.a).title = `A ${long}`;
  const dep = (p: string, c: string) => ({ ...s.entities.dependencies[0], dependency_id: `dep_${p}_${c}`, predecessor_issue_id: p, successor_issue_id: c, met: false });
  s.entities.dependencies.push(dep(ABC.c, "issue_xe"), dep("issue_xd", "issue_xe"), dep(ABC.a, "issue_xf"), dep("issue_xd", "issue_xg"), dep(ABC.c, "issue_xg"), dep("issue_xh", "issue_xf"));
  s.entities.worktrees[0].branch_ref = "refs/heads/samsara/very-long-project-name/issue-with-a-very-long-branch-name/attempt-0123456789";
  fake.addProject(s);
  await openProject(page, fake, ABC.project);
  await expect(page.getByTestId("issue-graph")).toHaveAttribute("data-layout", "elk");
  await expect(page.locator(".react-flow__node-issue")).toHaveCount(8);

  const boxes = await page.locator(".react-flow__node-issue").evaluateAll((els) => els.map((el) => el.getBoundingClientRect().toJSON()));
  for (let i = 0; i < boxes.length; i++)
    for (let j = i + 1; j < boxes.length; j++) {
      const a = boxes[i], b = boxes[j];
      const overlap = a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1;
      expect(overlap, `node ${i} overlaps node ${j}`).toBe(false);
    }
  // Edge (milestone) labels do not sit on top of issue cards.
  const labels = await page.locator(".react-flow__edge-textwrapper").evaluateAll((els) => els.map((el) => el.getBoundingClientRect().toJSON()));
  expect(labels.length).toBeGreaterThan(0);
  for (const l of labels)
    for (const b of boxes) {
      const overlap = l.left < b.right - 1 && b.left < l.right - 1 && l.top < b.bottom - 1 && b.top < l.bottom - 1;
      expect(overlap, "edge label overlaps a node").toBe(false);
    }
  // Titles clamp inside the card and keep the full text available.
  const title = node(page, ABC.a).locator(".gnode-title");
  await expect(title).toHaveAttribute("title", `A ${long}`);
  const fits = await node(page, ABC.a).evaluate((el) => {
    const card = el.querySelector(".gnode")!.getBoundingClientRect();
    return [...el.querySelectorAll(".gnode-top, .gnode-title, .gnode-reason")].every((c) => {
      const r = c.getBoundingClientRect();
      return r.right <= card.right + 0.5 && r.bottom <= card.bottom + 0.5;
    });
  });
  expect(fits).toBe(true);
  // Worktree view keeps the long branch on one ellipsized line.
  await page.getByRole("button", { name: "Worktree 묶음" }).click();
  const label = page.locator(".wtgroup-label", { hasText: "very-long-project-name" });
  await expect(label).toBeVisible();
  const ell = await label.evaluate((el) => getComputedStyle(el).textOverflow === "ellipsis" && el.scrollHeight <= el.clientHeight + 1);
  expect(ell).toBe(true);
});

/** Press Tab until `target` has focus (keyboard-only navigation). */
async function tabTo(page: Page, target: ReturnType<Page["locator"]>, max = 80): Promise<void> {
  for (let i = 0; i < max; i++) {
    if (await target.evaluate((el) => el === document.activeElement).catch(() => false)) return;
    await page.keyboard.press("Tab");
  }
  throw new Error("target never received focus");
}

test("UI-T17 키보드만 사용: 프로젝트→차단 이유→승인 검토→복귀가 가능하다", async ({ page, fake }) => {
  fake.addProject(abcApprovalPending());
  await open(page, fake, "/");
  await tabTo(page, page.getByTestId("project-nav").getByRole("link").first());
  await page.keyboard.press("Enter");
  await expect(page.locator("#project-title")).toHaveText("repo");

  await tabTo(page, page.getByRole("button", { name: "목록" }));
  await page.keyboard.press("Enter");
  const table = page.getByTestId("issue-table");
  const bKey = table.getByRole("button", { name: "b", exact: true });
  await tabTo(page, bKey);
  await page.keyboard.press("Enter");
  const insp = page.getByTestId("inspector");
  await expect(insp).toContainText("선행 이슈의 결과 승인이 필요합니다.");
  await page.keyboard.press("Escape");
  await expect(insp).toHaveCount(0);
  await expect(bKey).toBeFocused();

  const aKey = table.getByRole("button", { name: "a", exact: true });
  await tabTo(page, aKey);
  await page.keyboard.press("Enter");
  await expect(insp.getByTestId("approval-card")).toBeVisible();
  await tabTo(page, insp.getByTestId("approval-card").locator("summary"));
  await page.keyboard.press("Enter");
  await expect(insp.locator("pre.subject")).toBeVisible();
  await tabTo(page, insp.getByRole("button", { name: "승인" }));
  await page.keyboard.press("Enter");
  await expect(insp).toContainText("접수됨");
  await page.keyboard.press("Escape");
  await expect(insp).toHaveCount(0);
  await expect(aKey).toBeFocused();
  expect(fake.commands().filter((r) => r.path.includes("/decisions"))).toHaveLength(1);
});

test("UI-T18 화면 축소·동작 줄이기: 목록 대안·포커스·비애니메이션이 정상이다", async ({ page, fake }) => {
  fake.addProject(abcApprovalPending());
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await openProject(page, fake, ABC.project);

  // Narrow screens open on the list; the graph stays a separate view (02 §19).
  const table = page.getByTestId("issue-table");
  await expect(table).toBeVisible();
  await expect(table).toContainText("선행 이슈의 결과 승인이 필요합니다.");
  await expect(table).toContainText("승인 대기");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);

  const bKey = table.getByRole("button", { name: "b", exact: true });
  await bKey.focus();
  await page.keyboard.press("Enter");
  const insp = page.getByTestId("inspector");
  await expect(insp).toBeInViewport();
  await page.keyboard.press("Escape");
  await expect(bKey).toBeFocused();

  await page.getByRole("button", { name: "이슈 DAG" }).click();
  await expect(page.getByTestId("issue-graph")).toBeVisible();
  const animations = await page.evaluate(() => document.getAnimations().filter((a) => a.playState === "running").length);
  expect(animations).toBe(0);
  const transitions = await page.evaluate(() => [...document.querySelectorAll(".react-flow__edge-path, .gnode")].some((el) => parseFloat(getComputedStyle(el).transitionDuration) > 0 || getComputedStyle(el).animationName !== "none"));
  expect(transitions).toBe(false);
});

test("UI-T19 SSE 청크가 JSON·UTF-8 중간에서 분리: 하나의 올바른 이벤트로 복원한다", async ({ page, fake }) => {
  fake.addProject(abc());
  await openProject(page, fake, ABC.project);
  fake.streamOptions.splitBytes = 7; // splits 3-byte Hangul and 4-byte emoji sequences
  const title = "분할된 청크 🚀 한글 제목 — 끝";
  fake.patch(ABC.project, "issues", ABC.c, { title });
  await expect(node(page, ABC.c).locator(".gnode-title")).toHaveText(title);
  await expectVersion(page, fake, ABC.project);
  expect(snapshotCalls(fake, ABC.project)).toBe(1);
});

test("UI-T20 프로젝트 버전 건너뜀·reset_required: 부분 적용 없이 Snapshot으로 다시 맞춘다", async ({ page, fake }) => {
  fake.addProject(abc());
  await openProject(page, fake, ABC.project);
  const snap = fake.project(ABC.project).snap;
  const c = find(snap.entities, "issues", ABC.c);
  // An event whose previous version skips one: it must not be applied.
  const v = BigInt(snap.project_version);
  const bad = { aggregate_id: c.id, aggregate_type: "issue", aggregate_version: "9", event_id: "evt_gap", occurred_at: new Date().toISOString(), payload: { changes: { deletes: {}, upserts: { issues: [{ ...c, title: "부분 적용 금지", version: "9" }] } } }, previous_project_version: String(v + 1n), project_id: ABC.project, project_version: String(v + 2n), recorded_at: new Date().toISOString(), schema_version: "1", seq: "9999", type: "issue.changed" };
  fake.sendRaw(ABC.project, `id: x\nevent: project.change\ndata: ${JSON.stringify(bad)}\n\n`);
  await expect.poll(() => snapshotCalls(fake, ABC.project)).toBe(2);
  await expectVersion(page, fake, ABC.project);
  await expect(page.getByText("부분 적용 금지")).toHaveCount(0);
  await expect(node(page, ABC.c)).toContainText(c.title);

  fake.resetStreams(ABC.project, "RESET_REQUIRED");
  await expect.poll(() => snapshotCalls(fake, ABC.project)).toBe(3);
  fake.patch(ABC.project, "issues", ABC.c, { title: "재동기화 후 이벤트" });
  await expect(node(page, ABC.c)).toContainText("재동기화 후 이벤트");
  await expectVersion(page, fake, ABC.project);
});

test("UI-T21 인증 만료·페이지 새로고침: 토큰을 저장하지 않고 재연결을 안내하며 무한 재시도하지 않는다", async ({ page, fake }) => {
  fake.addProject(abc());
  await openProject(page, fake, ABC.project);
  expect(page.url()).not.toContain("bootstrap");
  const stored = await page.evaluate(() => JSON.stringify({ ls: { ...localStorage }, ss: { ...sessionStorage }, c: document.cookie }));
  expect(stored).toBe(JSON.stringify({ ls: {}, ss: {}, c: "" }));

  fake.expireAuth();
  const gate = page.getByTestId("auth-gate");
  await expect(gate).toContainText("인증이 만료됐거나 폐기됐습니다.");
  await expect(gate).toContainText("samsara ui --open");
  const before = fake.requests.length;
  await page.waitForTimeout(3000);
  expect(fake.requests.length - before).toBeLessThanOrEqual(2);

  await page.reload();
  await expect(gate).toContainText("토큰을 저장하지 않으므로");
  const afterReload = fake.requests.length;
  await page.waitForTimeout(1500);
  expect(fake.requests.slice(afterReload).filter((r) => r.path.startsWith("/v1/"))).toHaveLength(0);
  expect(fake.requests.filter((r) => r.path === "/v1/ui/sessions")).toHaveLength(1);
});

test("UI-T22 offline 중 승인 클릭 후 연결 복구: 명령을 자동 대기열로 실행하지 않는다", async ({ page, fake, context }) => {
  fake.addProject(abcApprovalPending());
  await openProject(page, fake, ABC.project);
  const approve = page.getByTestId("pending-approvals").getByRole("button", { name: "승인" });
  await expect(approve).toBeEnabled();

  await context.setOffline(true);
  fake.dropStreams();
  await expect(page.getByTestId("connection-banner").first()).toContainText("실시간 연결 끊김");
  await expect(approve).toBeDisabled();
  await expect(page.getByTestId("pending-approvals")).toContainText("실시간 연결이 최신이 아니어서 명령을 막았습니다.");
  await approve.click({ force: true }).catch(() => undefined);

  await context.setOffline(false);
  await expect(page.getByTestId("runtime-status")).toHaveText("런타임 실시간 연결");
  await expect(page.getByTestId("connection-banner")).toHaveCount(0, { timeout: 20_000 });
  await expect(approve).toBeEnabled();
  await page.waitForTimeout(1000);
  expect(fake.commands()).toHaveLength(0);
  await expect(page.getByTestId("pending-approvals")).toContainText("RESULT · 대기");
});

test("UI-T23 브라우저는 열린 채 데몬만 재시작: 유효 token으로 재연결하고 새 epoch 상태를 복구한다", async ({ page, fake }) => {
  fake.addProject(abc());
  await openProject(page, fake, ABC.project);
  const token = fake.requests.find((r) => r.authorization)?.authorization;

  await fake.restart(1500, () => fake.patch(ABC.project, "issues", ABC.c, { display_status: "VERIFYING", title: "재시작 중 변경" }));
  await expect(node(page, ABC.c)).toContainText("재시작 중 변경");
  await expect(node(page, ABC.c)).toContainText("검증 중");
  await expectVersion(page, fake, ABC.project);
  await expect(page.getByTestId("auth-gate")).toHaveCount(0);
  expect(fake.requests.filter((r) => r.path === "/v1/ui/sessions")).toHaveLength(1);
  const after = fake.requests.filter((r) => r.authorization).slice(-3);
  expect(after.every((r) => r.authorization === token)).toBe(true);
  // A later event of the new epoch still streams in.
  fake.patch(ABC.project, "issues", ABC.c, { title: "새 epoch 이벤트" });
  await expect(node(page, ABC.c)).toContainText("새 epoch 이벤트");
});

test("UI-T24 명령이 202 접수 후 오래 진행: 접수를 완료로 표시하지 않고 결과는 이벤트로 반영한다", async ({ page, fake }) => {
  fake.addProject(abc());
  fake.commandHandler = (req, f) => {
    f.change(ABC.project, { upserts: { operations: [{ id: "op_pause", kind: "RUN_PAUSE", state: "RUNNING", command_id: req.idempotencyKey, project_id: ABC.project, created_at: new Date().toISOString(), updated_at: new Date().toISOString(), result: null }] } });
    // Same shape as the runtime's RunCommandResult; the run is not paused yet.
    const run = find(f.project(ABC.project).snap.entities, "runs", ABC.run);
    return { status: 202, body: { run_id: run.id, state: run.state, version: run.version } };
  };
  await openProject(page, fake, ABC.project);
  const select = page.locator(".summary select");
  await expect(select).toContainText("진행 중");
  await page.getByRole("button", { name: "일시 정지 요청" }).click();
  await expect(page.locator(".summary")).toContainText("접수됨 — 결과는 실시간 상태로 반영됩니다.");
  await page.waitForTimeout(1500);
  await expect(select).toContainText("진행 중");
  await expect(select).not.toContainText("일시 정지");
  await expect(page.getByRole("button", { name: "재개 요청" })).toHaveCount(0);

  const run = find(fake.project(ABC.project).snap.entities, "runs", ABC.run);
  const op = find(fake.project(ABC.project).snap.entities, "operations", "op_pause");
  fake.change(ABC.project, { upserts: { runs: [{ ...run, state: "PAUSED", version: String(BigInt(run.version) + 1n) }], operations: [{ ...op, state: "SUCCEEDED", updated_at: new Date().toISOString() }] } });
  await expect(select).toContainText("일시 정지");
  await expect(page.getByRole("button", { name: "재개 요청" })).toBeVisible();
  await expectVersion(page, fake, ABC.project);
  expect(fake.commands()).toHaveLength(1);
});
