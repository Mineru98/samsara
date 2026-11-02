import { memo, useEffect, useMemo, useState } from "react";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import type { Entities } from "../../store/projectStore";
import { displayStatus, milestoneLabel } from "../../status";
import { simpleLayout, type Positions } from "./elkGraph";
import { layoutInWorker } from "./layout";
import { buildGraph, type GraphMode } from "./model";

type IssueData = { title: string; key: string; status: string; reason: string; actions: number; version: string; selected: boolean };
type GateData = { open: boolean; met: number; total: number; phase: string; selected: boolean };
type GroupData = { label: string };

const IssueNode = memo(function IssueNode({ data }: NodeProps<Node<IssueData>>) {
  const s = displayStatus(data.status);
  return (
    <div className={`gnode tone-${s.tone}${data.selected ? " selected" : ""}`} data-version={data.version}>
      <Handle type="target" position={Position.Left} isConnectable={false} />
      <div className="gnode-top">
        <span className="mono small muted ell">{data.key}</span>
        <span className={`badge tone-${s.tone}`}>{s.label}</span>
      </div>
      <div className="gnode-title clamp2" title={data.title}>
        {data.title}
      </div>
      {data.reason && <div className="gnode-reason ell small">{data.reason}</div>}
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
});

const GateNode = memo(function GateNode({ data }: NodeProps<Node<GateData>>) {
  return (
    <div className={`gate ${data.open ? "open" : "closed"}${data.selected ? " selected" : ""}`}>
      <Handle type="target" position={Position.Left} isConnectable={false} />
      <span className="small">{data.phase === "INTEGRATION" ? "통합 게이트" : "실행 게이트"}</span>
      <strong>
        {data.met}/{data.total}
      </strong>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
});

function GroupNode({ data }: NodeProps<Node<GroupData>>) {
  return <div className="wtgroup-label mono small ell">{data.label}</div>;
}

const nodeTypes = { issue: IssueNode, gate: GateNode, group: GroupNode };

export function IssueGraph({ entities, runId, mode, selected, onSelect }: { entities: Entities; runId: string | null; mode: GraphMode; selected: string | null; onSelect: (id: string | null) => void }) {
  const graph = useMemo(() => buildGraph(entities, runId, mode), [entities, runId, mode]);
  // Layout depends only on structure, so status updates keep positions (02 §9.3).
  const [layout, setLayout] = useState<{ key: string; pos: Positions; failed: boolean }>(() => ({ key: graph.structureKey, pos: simpleLayout(graph), failed: false }));
  const structureKey = graph.structureKey;
  useEffect(() => {
    let live = true;
    setLayout((cur) => (cur.key === structureKey ? cur : { key: structureKey, pos: simpleLayout(graph), failed: false }));
    layoutInWorker(graph)
      .then((pos) => live && setLayout({ key: structureKey, pos, failed: false }))
      .catch(() => live && setLayout((cur) => (cur.key === structureKey ? { ...cur, failed: true } : cur)));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [structureKey]);

  const pos = layout.key === structureKey ? layout.pos : simpleLayout(graph);
  const nodes: Node[] = graph.nodes.map((n) => {
    const p = pos.pos[n.id] ?? { x: 0, y: 0 };
    if (n.kind === "group") {
      const sz = pos.size[n.id] ?? { width: 280, height: 120 };
      return { id: n.id, type: "group", position: p, data: { label: n.label ?? "" }, style: { width: sz.width, height: sz.height }, className: "wtgroup", selectable: false, draggable: false };
    }
    if (n.kind === "gate") {
      const g = entities.gates[n.gateId!];
      return {
        id: n.id,
        type: "gate",
        position: p,
        parentId: n.parent,
        draggable: false,
        data: { open: g.open, met: g.members.filter((m) => m.met).length, total: g.members.length, phase: g.downstream_phase, selected: selected === n.id },
        ariaLabel: `${g.downstream_phase === "INTEGRATION" ? "통합" : "실행"} 게이트, 조건 ${g.members.length}개 중 ${g.members.filter((m) => m.met).length}개 충족`,
      };
    }
    const i = entities.issues[n.issueId!];
    const s = displayStatus(i.display_status);
    return {
      id: n.id,
      type: "issue",
      position: p,
      parentId: n.parent,
      extent: n.parent ? ("parent" as const) : undefined,
      draggable: false,
      data: { title: i.title, key: i.client_key, status: i.display_status, reason: i.blocking_reasons[0]?.message ?? "", actions: i.available_actions.length, version: i.version, selected: selected === n.id },
      ariaLabel: `${i.client_key} ${i.title}, ${s.label}${i.blocking_reasons[0] ? `, ${i.blocking_reasons[0].message}` : ""}`,
    };
  });
  const edges: Edge[] = graph.edges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    label: e.milestone ? milestoneLabel(e.milestone) : undefined,
    className: e.met ? "edge-met" : "edge-unmet",
    animated: false,
    markerEnd: { type: MarkerType.ArrowClosed },
    focusable: false,
  }));

  return (
    <div className="graph" data-testid="issue-graph" aria-label="이슈 의존성 그래프 (왼쪽에서 오른쪽)">
      {layout.failed && <div className="graph-note small">자동 배치에 실패해 단순 배치로 표시합니다.</div>}
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        fitView
        minZoom={0.1}
        maxZoom={1.6}
        nodesConnectable={false}
        nodesDraggable={false}
        edgesFocusable={false}
        onlyRenderVisibleElements
        proOptions={{ hideAttribution: true }}
        onNodeClick={(_, n) => n.type !== "group" && onSelect(n.id)}
        onPaneClick={() => onSelect(null)}
        colorMode="system"
      >
        <Background gap={22} size={1} />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable ariaLabel="그래프 미니맵" />
      </ReactFlow>
    </div>
  );
}
