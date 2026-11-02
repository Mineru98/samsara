import type { GraphModel } from "./model";

export interface Positions {
  /** Top-left position relative to the parent (xyflow convention). */
  pos: Record<string, { x: number; y: number }>;
  /** Computed size of group nodes. */
  size: Record<string, { width: number; height: number }>;
}

export interface ElkNodeIn {
  id: string;
  width?: number;
  height?: number;
  layoutOptions?: Record<string, string>;
  children?: ElkNodeIn[];
  labels?: { text: string }[];
}

export interface ElkNodeOut extends ElkNodeIn {
  x?: number;
  y?: number;
  children?: ElkNodeOut[];
}

const ROOT_OPTIONS: Record<string, string> = {
  "elk.algorithm": "layered",
  "elk.direction": "RIGHT",
  "elk.hierarchyHandling": "INCLUDE_CHILDREN",
  "elk.layered.spacing.nodeNodeBetweenLayers": "72",
  "elk.spacing.nodeNode": "28",
  "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
  "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
  "elk.edgeRouting": "SPLINES",
};

/** Build the ELK input. Group nodes contain their issue nodes. */
export function toElk(g: GraphModel): { id: string; layoutOptions: Record<string, string>; children: ElkNodeIn[]; edges: { id: string; sources: string[]; targets: string[] }[] } {
  const byParent = new Map<string, ElkNodeIn[]>();
  const top: ElkNodeIn[] = [];
  for (const n of g.nodes) {
    if (n.kind === "group") continue;
    const el: ElkNodeIn = { id: n.id, width: n.width, height: n.height };
    if (n.parent) {
      if (!byParent.has(n.parent)) byParent.set(n.parent, []);
      byParent.get(n.parent)!.push(el);
    } else top.push(el);
  }
  for (const n of g.nodes) {
    if (n.kind !== "group") continue;
    top.push({
      id: n.id,
      children: byParent.get(n.id) ?? [],
      layoutOptions: { "elk.padding": "[top=36,left=16,bottom=16,right=16]" },
    });
  }
  return {
    id: "root",
    layoutOptions: ROOT_OPTIONS,
    children: top,
    edges: g.edges.map((e) => ({ id: e.id, sources: [e.source], targets: [e.target] })),
  };
}

export function fromElk(root: ElkNodeOut): Positions {
  const out: Positions = { pos: {}, size: {} };
  const walk = (n: ElkNodeOut) => {
    for (const c of n.children ?? []) {
      out.pos[c.id] = { x: c.x ?? 0, y: c.y ?? 0 };
      if (c.children) out.size[c.id] = { width: c.width ?? 0, height: c.height ?? 0 };
      walk(c);
    }
  };
  walk(root);
  return out;
}

/**
 * Synchronous longest-path layering used before the worker answers, when it
 * fails or times out, and in tests. Groups are stacked vertically.
 */
export function simpleLayout(g: GraphModel): Positions {
  const out: Positions = { pos: {}, size: {} };
  const preds = new Map<string, string[]>();
  for (const e of g.edges) {
    if (!preds.has(e.target)) preds.set(e.target, []);
    preds.get(e.target)!.push(e.source);
  }
  const depth = new Map<string, number>();
  const visiting = new Set<string>();
  const d = (id: string): number => {
    if (depth.has(id)) return depth.get(id)!;
    if (visiting.has(id)) return 0; // defensive: the server rejects cycles
    visiting.add(id);
    const v = Math.max(-1, ...(preds.get(id) ?? []).map(d)) + 1;
    visiting.delete(id);
    depth.set(id, v);
    return v;
  };
  const leaves = g.nodes.filter((n) => n.kind !== "group");
  const groups = g.nodes.filter((n) => n.kind === "group");
  const place = (list: typeof leaves, ox: number, oy: number) => {
    const rows = new Map<number, number>();
    let maxX = 0;
    let maxY = 0;
    for (const n of list) {
      const col = d(n.id);
      const row = rows.get(col) ?? 0;
      rows.set(col, row + 1);
      const x = ox + col * 320;
      const y = oy + row * (n.height + 28);
      out.pos[n.id] = { x, y };
      maxX = Math.max(maxX, x + n.width);
      maxY = Math.max(maxY, y + n.height);
    }
    return { maxX, maxY };
  };
  if (groups.length === 0) {
    place(leaves, 0, 0);
    return out;
  }
  let y = 0;
  for (const grp of groups) {
    const kids = leaves.filter((n) => n.parent === grp.id);
    const { maxX, maxY } = place(kids, 16, 36);
    out.pos[grp.id] = { x: 0, y };
    const h = Math.max(maxY + 16, 80);
    out.size[grp.id] = { width: Math.max(maxX + 16, 280), height: h };
    y += h + 32;
  }
  return out;
}
