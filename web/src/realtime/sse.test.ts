import { describe, expect, test } from "vitest";
import { SSEParser, type SSEMessage } from "./sse";

function parse(chunks: (string | Uint8Array)[]): SSEMessage[] {
  const out: SSEMessage[] = [];
  const p = new SSEParser((m) => out.push(m));
  const enc = new TextEncoder();
  for (const c of chunks) p.push(typeof c === "string" ? enc.encode(c) : c);
  p.end();
  return out;
}

describe("SSEParser", () => {
  test("parses id, event and data", () => {
    expect(parse(["id: c1\nevent: project.change\ndata: {\"a\":1}\n\n"])).toEqual([
      { id: "c1", event: "project.change", data: '{"a":1}' },
    ]);
  });

  test("joins multiple data lines and ignores comments and unknown fields", () => {
    expect(parse([": heartbeat\n\n", "data: a\nfoo: x\ndata: b\nretry: 10\n\n"])).toEqual([
      { id: null, event: "message", data: "a\nb" },
    ]);
  });

  test("handles CRLF split across chunks", () => {
    expect(parse(["data: x\r", "\n\r", "\n"])).toEqual([{ id: null, event: "message", data: "x" }]);
  });

  test("restores a UTF-8 character split across chunks (UI-T19)", () => {
    const bytes = new TextEncoder().encode('data: {"title":"한글"}\n\n');
    const cut = bytes.indexOf(0xed) + 1; // inside the first Hangul code unit
    expect(parse([bytes.slice(0, cut), bytes.slice(cut)])).toEqual([
      { id: null, event: "message", data: '{"title":"한글"}' },
    ]);
  });

  test("restores JSON split mid-body byte by byte", () => {
    const text = 'id: z\nevent: project.change\ndata: {"seq":"9007199254740993"}\n\n';
    const bytes = new TextEncoder().encode(text);
    const chunks = Array.from(bytes, (b) => new Uint8Array([b]));
    expect(parse(chunks)).toEqual([{ id: "z", event: "project.change", data: '{"seq":"9007199254740993"}' }]);
  });

  test("drops an unterminated trailing event", () => {
    expect(parse(["data: done\n\ndata: partial"])).toEqual([{ id: null, event: "message", data: "done" }]);
  });
});
