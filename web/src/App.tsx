import { useEffect, useState, useSyncExternalStore } from "react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import type { RuntimeInfo } from "./api/contract";
import { bootstrapFromFragment } from "./api/bootstrap";
import { auth, get } from "./api/http";
import { useStream } from "./hooks";
import { catalogSpec } from "./realtime/specs";
import { useRoute } from "./router";
import { ProjectNavigator, sortProjects } from "./features/projects/ProjectNavigator";
import { ProjectPage } from "./features/ProjectPage";
import { ConnectionBanner } from "./features/ConnectionBanner";
import { Mono } from "./components/ui";

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 5_000 } },
});

function useAuth() {
  return useSyncExternalStore(
    auth.subscribe,
    () => (auth.isExpired() ? "expired" : auth.get() ? "ok" : "none"),
    () => "none",
  );
}

export function App() {
  const [boot, setBoot] = useState<"pending" | "done" | "failed">("pending");
  useEffect(() => {
    void bootstrapFromFragment().then((r) => setBoot(r === "failed" ? "failed" : "done"));
  }, []);
  const a = useAuth();
  if (boot === "pending") return <div className="center muted">연결하는 중…</div>;
  if (a !== "ok") return <AuthGate reason={a === "expired" ? "expired" : boot === "failed" ? "failed" : "none"} />;
  return (
    <QueryClientProvider client={queryClient}>
      <Shell />
    </QueryClientProvider>
  );
}

function AuthGate({ reason }: { reason: "expired" | "failed" | "none" }) {
  const msg = {
    expired: "인증이 만료됐거나 폐기됐습니다.",
    failed: "일회용 접속 코드가 만료됐거나 이미 사용됐습니다(60초, 1회).",
    none: "이 페이지는 토큰을 저장하지 않으므로 새로 고치면 다시 연결해야 합니다.",
  }[reason];
  return (
    <div className="center" role="alert" data-testid="auth-gate">
      <h1>삼사라 대시보드에 연결하기</h1>
      <p>{msg}</p>
      <p>
        터미널에서 <Mono>samsara ui --open</Mono> 을 실행하면 새 접속 링크가 열립니다.
      </p>
      <p className="small muted">브라우저를 닫아도 런타임과 worker는 계속 동작합니다.</p>
    </div>
  );
}

function Shell() {
  const { projectId } = useRoute();
  const { view: catalog } = useStream("catalog", () => catalogSpec);
  const runtime = useQuery({ queryKey: ["runtime"], queryFn: ({ signal }) => get<RuntimeInfo>("/v1/runtime", signal), refetchInterval: 30_000 });
  const projects = catalog?.data ? Object.values(catalog.data.projects) : [];
  const live = catalog?.status === "live";
  return (
    <div className="app">
      <header className="top">
        <div className="brand">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
            <circle cx="12" cy="12" r="9" />
            <circle cx="12" cy="12" r="2.5" />
            <path d="M12 3v6.5M12 14.5V21M3 12h6.5M14.5 12H21" />
          </svg>
          <span className="brand-name">삼사라</span>
          <span className="mono small muted">
            runtime {runtime.data?.version ?? "?"} · {runtime.data ? (runtime.data.ready ? "준비됨" : "복구 중") : "확인 중"}
          </span>
        </div>
        <div role="status" className="pill" data-testid="runtime-status">
          <span className={`dot ${live ? "tone-ok" : "tone-wait"}`} aria-hidden="true" />
          {live ? "런타임 실시간 연결" : "런타임 연결 확인 중"}
        </div>
      </header>
      {catalog && catalog.status !== "live" && catalog.status !== "connecting" && (
        <ConnectionBanner status={catalog.status} lastAppliedAt={catalog.lastAppliedAt} reason={catalog.resyncReason} error={catalog.error?.message ?? null} />
      )}
      <div className="body">
        <ProjectNavigator projects={projects} currentId={projectId} />
        {projectId ? (
          <ProjectPage key={projectId} projectId={projectId} summary={catalog?.data?.projects[projectId]} />
        ) : (
          <Overview count={projects.length} first={sortProjects(projects)[0]?.display_name} />
        )}
      </div>
    </div>
  );
}

function Overview({ count, first }: { count: number; first?: string }) {
  return (
    <main className="center">
      <h1>프로젝트를 선택하세요</h1>
      <p className="muted">
        등록된 프로젝트 {count}개{first ? ` · 가장 최근 활동: ${first}` : ""}
      </p>
    </main>
  );
}
