import type { Entities } from "../store/projectStore";
import { displayStatus } from "../status";
import { Badge, Empty, Mono } from "../components/ui";
import { issuesOf } from "./graph/model";

/** Keyboard/screen-reader alternative to the graph (02 §20). */
export function IssueTable({ entities, runId, selected, onSelect }: { entities: Entities; runId: string | null; selected: string | null; onSelect: (id: string) => void }) {
  const issues = issuesOf(entities, runId);
  if (issues.length === 0) return <Empty>이 Run에 이슈가 없습니다.</Empty>;
  const deps = Object.values(entities.dependencies);
  return (
    <div className="table-wrap" data-testid="issue-table">
      <table className="tbl">
        <thead>
          <tr>
            <th>키</th>
            <th>제목</th>
            <th>상태</th>
            <th>선행 이슈</th>
            <th>주요 차단 이유</th>
          </tr>
        </thead>
        <tbody>
          {issues.map((i) => {
            const s = displayStatus(i.display_status);
            const pre = deps.filter((d) => d.successor_issue_id === i.id).map((d) => entities.issues[d.predecessor_issue_id]?.client_key ?? d.predecessor_issue_id);
            return (
              <tr key={i.id} aria-selected={selected === `issue:${i.id}`}>
                <td>
                  <button type="button" className="linklike mono" onClick={() => onSelect(`issue:${i.id}`)}>
                    {i.client_key}
                  </button>
                </td>
                <td>{i.title}</td>
                <td>
                  <Badge tone={s.tone}>{s.label}</Badge>
                </td>
                <td>
                  <Mono>{pre.join(", ") || "-"}</Mono>
                </td>
                <td className="small">{i.blocking_reasons[0]?.message ?? ""}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
