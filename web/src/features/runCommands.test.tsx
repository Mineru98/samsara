import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { RunView } from "../api/contract";
import { RunControls } from "./RunControls";
import { RevisePlanPanel } from "./RevisePlanPanel";

function wrap(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

function runView(state: string, actions?: string[]): RunView {
  return {
    id: "run_1",
    project_id: "prj_1",
    version: "4",
    state,
    run_generation: "1",
    graph_version: "1",
    plan_digest: "sha256:old",
    alias_mapping: null,
    created_at: "2026-11-03T00:00:00Z",
    ...(actions ? { available_actions: actions.map((action) => ({ action, enabled: true })) } : {}),
  };
}

function json(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("RunControls", () => {
  it("renders only the actions the server lists", () => {
    const noop = () => undefined;
    const { rerender } = wrap(<RunControls run={runView("PLANNED")} canCommand offlineReason="" revising={false} onRevise={noop} />);
    expect(screen.queryAllByRole("button")).toHaveLength(0);
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <RunControls run={runView("PLANNED", ["START_RUN"])} canCommand offlineReason="" revising={false} onRevise={noop} />
      </QueryClientProvider>,
    );
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["실행 시작"]);
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <RunControls run={runView("PAUSED", ["RESUME_RUN", "REVISE_PLAN"])} canCommand offlineReason="" revising={false} onRevise={noop} />
      </QueryClientProvider>,
    );
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["재개 요청", "계획 수정"]);
  });

  it("starts with Idempotency-Key and If-Match and shows the refusal like the CLI", async () => {
    const fetch = vi.fn(async () => json(409, { error: { code: "APPROVAL_REQUIRED", message: "PLAN approval required before start", retryable: false, details: { approval_id: "apr_9" } } }));
    vi.stubGlobal("fetch", fetch);
    wrap(<RunControls run={runView("PLANNED", ["START_RUN"])} canCommand offlineReason="" revising={false} onRevise={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "실행 시작" }));
    await screen.findByText(/거절됨: APPROVAL_REQUIRED — PLAN approval required before start/);
    expect(screen.getByText(/"approval_id": "apr_9"/)).toBeTruthy();
    const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    const h = init.headers as Record<string, string>;
    expect(path).toBe("/v1/runs/run_1/start");
    expect(h["If-Match"]).toBe('"run_1:v4"');
    expect(h["Idempotency-Key"]).toMatch(/^ui-/);
  });
});

describe("RevisePlanPanel", () => {
  const planView = {
    run_id: "run_1",
    version: "5",
    state: "PAUSED",
    run_generation: "1",
    graph_version: "1",
    plan_digest: "sha256:old",
    plan: { spec_version: "1", project_id: "prj_1", expected_graph_version: "1", issues: [{ client_key: "a", title: "A", acceptance_criteria: ["x"] }], dependencies: [] },
  };

  it("sends the loaded generation and shows a stale-generation refusal", async () => {
    const fetch = vi.fn(async (path: string) =>
      path.endsWith("/plan")
        ? json(200, planView)
        : json(412, { error: { code: "VERSION_MISMATCH", message: "run generation changed", retryable: false, details: { expected_run_generation: "1", current_run_generation: "2" } } }),
    );
    vi.stubGlobal("fetch", fetch);
    wrap(<RevisePlanPanel runId="run_1" canCommand offlineReason="" onClose={() => undefined} />);
    const editor = (await screen.findByLabelText("계획 JSON")) as HTMLTextAreaElement;
    expect(JSON.parse(editor.value)).toEqual(planView.plan);
    fireEvent.click(screen.getByRole("button", { name: "수정 제출" }));
    await screen.findByText(/오래된 세대/);
    expect(screen.getByText(/거절됨: VERSION_MISMATCH — run generation changed/)).toBeTruthy();
    expect(screen.getByText(/"current_run_generation": "2"/)).toBeTruthy();
    const [path, init] = fetch.mock.calls[1] as unknown as [string, RequestInit];
    expect(path).toBe("/v1/runs/run_1/revise");
    expect((init.headers as Record<string, string>)["If-Match"]).toBe('"run_1:v5"');
    expect(JSON.parse(init.body as string)).toEqual({ expected_run_generation: "1", plan: planView.plan });
  });

  it("shows the revise result digest and rejects malformed JSON locally", async () => {
    const result = {
      run_id: "run_1",
      run_state: "PAUSED",
      run_generation: "2",
      graph_version: "2",
      plan_digest: "sha256:new",
      issue_mapping: {},
      change_kind: "EXECUTION",
      affected_issue_ids: ["issue_c"],
      superseded_output_ids: [],
      invalidated_approval_ids: [],
      plan_approval_id: "apr_2",
    };
    const fetch = vi.fn(async (path: string) => (path.endsWith("/plan") ? json(200, planView) : json(202, result)));
    vi.stubGlobal("fetch", fetch);
    wrap(<RevisePlanPanel runId="run_1" canCommand offlineReason="" onClose={() => undefined} />);
    const editor = await screen.findByLabelText("계획 JSON");
    fireEvent.change(editor, { target: { value: "{" } });
    fireEvent.click(screen.getByRole("button", { name: "수정 제출" }));
    await screen.findByText(/JSON 형식 오류/);
    expect(fetch).toHaveBeenCalledTimes(1);
    fireEvent.change(editor, { target: { value: JSON.stringify(planView.plan) } });
    fireEvent.click(screen.getByRole("button", { name: "수정 제출" }));
    const out = await screen.findByTestId("revise-result");
    expect(out.textContent).toContain("sha256:new");
    expect(out.textContent).toContain("EXECUTION · 세대 2");
    expect(out.textContent).toContain("apr_2");
  });
  it("reload restores the editor even when the server plan is unchanged", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => json(200, planView)));
    wrap(<RevisePlanPanel runId="run_1" canCommand offlineReason="" onClose={() => undefined} />);
    const editor = (await screen.findByLabelText("계획 JSON")) as HTMLTextAreaElement;
    fireEvent.change(editor, { target: { value: "{ broken" } });
    fireEvent.click(screen.getByRole("button", { name: "다시 불러오기" }));
    await waitFor(() => expect(JSON.parse(editor.value)).toEqual(planView.plan));
  });
});
