// Client-side contract test over the shared fixtures (runtime/api/fixtures).
// Run with: node --test runtime/api/ts/contract.test.ts   (Node 22.18+ strips types)
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  AttemptStateValues,
  FreshnessValues,
  isApprovalState,
  isAttemptState,
  isCandidateState,
  isFreshness,
  isOperationState,
  known,
} from "./samsara-api.gen.ts";
import type {
  AttemptState,
  ErrorResponse,
  Freshness,
  Open,
  ProjectEvent,
  ProjectSnapshot,
} from "./samsara-api.gen.ts";

interface Payload {
  schema: string;
  note?: string;
  expect?: "" | "tolerant" | "reject";
  data: unknown;
}
interface Fixture {
  name: string;
  payloads: Payload[];
}

const dir = join(dirname(fileURLToPath(import.meta.url)), "..", "fixtures");
const fixtures = new Map<string, Fixture>(
  readdirSync(dir)
    .filter((f) => f.endsWith(".json"))
    .map((f) => {
      const fx = JSON.parse(readFileSync(join(dir, f), "utf8")) as Fixture;
      return [fx.name, fx];
    }),
);

function payloads(def: string, expect: Payload["expect"] = undefined): unknown[] {
  return [...fixtures.values()].flatMap((fx) =>
    fx.payloads.filter((p) => p.schema.endsWith(`/$defs/${def}`) && (p.expect ?? "") === (expect ?? "")).map((p) => p.data),
  );
}

// How a dashboard should label an enum it may not know (03 §12.1).
function attemptLabel(s: Open<AttemptState> | undefined): string {
  return known(AttemptStateValues, s) ?? "UNKNOWN";
}
function freshnessLabel(s: Open<Freshness>): Freshness {
  return known(FreshnessValues, s) ?? "UNAVAILABLE";
}

const decimal = /^-?[0-9]+$/;

test("every scenario fixture is present", () => {
  for (const name of ["abc-partial-block", "stale-approval", "merged-history", "unknown-delivery", "unknown-enum", "http-errors"]) {
    assert.ok(fixtures.has(name), name);
  }
});

test("int64 values arrive as decimal strings", () => {
  const snaps = payloads("ProjectSnapshot") as ProjectSnapshot[];
  assert.ok(snaps.length > 0);
  for (const s of snaps) {
    for (const v of [s.project_version, s.graph_version, s.project.version]) {
      assert.equal(typeof v, "string");
      assert.match(v, decimal);
    }
    for (const list of Object.values(s.entities)) {
      for (const e of list as { version?: unknown }[]) {
        if (e.version !== undefined) assert.match(String(e.version), decimal, JSON.stringify(e));
        assert.equal(typeof e.version === "number", false);
      }
    }
  }
  for (const ev of payloads("ProjectEvent") as ProjectEvent[]) {
    assert.match(ev.seq, decimal);
    assert.match(ev.previous_project_version, decimal);
    assert.equal(BigInt(ev.project_version), BigInt(ev.previous_project_version) + 1n);
  }
});

test("runtime-emitted enum values are all known to this build", () => {
  for (const s of payloads("ProjectSnapshot") as ProjectSnapshot[]) {
    for (const a of s.entities.attempts) assert.ok(isAttemptState(a.state), a.state);
    for (const a of s.entities.approvals) assert.ok(isApprovalState(a.state), a.state);
    for (const c of s.entities.merge_candidates) assert.ok(isCandidateState(c.state), c.state);
    for (const f of Object.values(s.freshness)) assert.ok(isFreshness(f), f);
  }
  for (const ev of payloads("ProjectEvent") as ProjectEvent[]) {
    for (const op of ev.payload.changes.upserts.operations ?? []) assert.ok(isOperationState(op.state), op.state);
  }
});

test("scenario facts are visible to the client", () => {
  const blocked = (payloads("ProjectSnapshot") as ProjectSnapshot[]).some((s) =>
    s.entities.issues.some((i) => i.display_status === "BLOCKED" && i.blocking_reasons.length > 0),
  );
  assert.ok(blocked, "A/B/C: B is blocked with a reason");
  const unknownOps = fixtures
    .get("unknown-delivery")!
    .payloads.flatMap((p) => ((p.data as ProjectEvent).payload?.changes.upserts.operations ?? []).filter((o) => o.state === "UNKNOWN"));
  assert.ok(unknownOps.length > 0, "UNKNOWN operation outcome is preserved");
  const merged = fixtures.get("merged-history")!.payloads[0].data as ProjectSnapshot;
  assert.ok(merged.entities.merge_candidates.some((c) => c.state === "MERGED"));
  const stale = fixtures.get("stale-approval")!.payloads[0].data as ProjectSnapshot;
  assert.ok(stale.entities.approvals.some((a) => a.state === "INVALIDATED"));
});

test("error bodies carry a stable code", () => {
  const errs = payloads("ErrorResponse") as ErrorResponse[];
  assert.ok(errs.length > 0);
  for (const e of errs) {
    assert.equal(typeof e.error.code, "string");
    assert.equal(typeof e.error.retryable, "boolean");
  }
});

test("unknown enum values from a newer runtime degrade to unknown instead of failing", () => {
  const [future] = payloads("ProjectSnapshot", "reject") as ProjectSnapshot[];
  assert.ok(future, "unknown-enum fixture");
  const attempt = future.entities.attempts[0];
  assert.equal(attempt.state, "HIBERNATING");
  assert.equal(isAttemptState(attempt.state), false);
  assert.equal(attemptLabel(attempt.state), "UNKNOWN");
  assert.equal(attemptLabel(future.entities.issues[0].execution_state), "UNKNOWN");
  assert.equal(freshnessLabel(future.freshness.github), "UNAVAILABLE");
  // Known values in the same payload are still read normally.
  assert.equal(attemptLabel(future.entities.attempts[1]?.state ?? "RUNNING") === "UNKNOWN", false);
});

test("additive optional fields are ignored by clients", () => {
  const [additive] = payloads("ProjectSnapshot", "tolerant") as ProjectSnapshot[];
  assert.ok(additive);
  assert.equal(typeof additive.project_id, "string");
  assert.ok(additive.entities.issues.length > 0);
});
