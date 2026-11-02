/** One dispatched server-sent event. */
export interface SSEMessage {
  id: string | null;
  event: string;
  data: string;
}

/**
 * Incremental WHATWG event-stream parser (html.spec.whatwg.org §9.2.6).
 * Feed it raw bytes in any chunking: UTF-8 sequences, CRLF pairs and JSON
 * bodies may be split across chunks (02 §15.4).
 */
export class SSEParser {
  private decoder = new TextDecoder("utf-8");
  private buf = "";
  private pendingCR = false;
  private data: string[] = [];
  private event = "";
  private id: string | null = null;
  private hasData = false;

  constructor(private readonly onMessage: (m: SSEMessage) => void) {}

  push(chunk: Uint8Array): void {
    this.feed(this.decoder.decode(chunk, { stream: true }));
  }

  /** Flush at end of stream. An unterminated event is discarded per spec. */
  end(): void {
    this.feed(this.decoder.decode());
  }

  private feed(text: string): void {
    let s = text;
    if (this.pendingCR && s.startsWith("\n")) s = s.slice(1);
    this.pendingCR = false;
    this.buf += s;
    let start = 0;
    for (let i = 0; i < this.buf.length; i++) {
      const c = this.buf[i];
      if (c !== "\n" && c !== "\r") continue;
      this.line(this.buf.slice(start, i));
      if (c === "\r") {
        if (i + 1 === this.buf.length) this.pendingCR = true;
        else if (this.buf[i + 1] === "\n") i++;
      }
      start = i + 1;
    }
    this.buf = this.buf.slice(start);
  }

  private line(l: string): void {
    if (l === "") {
      this.dispatch();
      return;
    }
    if (l.startsWith(":")) return; // comment / heartbeat
    const colon = l.indexOf(":");
    let field = l;
    let value = "";
    if (colon >= 0) {
      field = l.slice(0, colon);
      value = l.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
    }
    switch (field) {
      case "data":
        this.data.push(value);
        this.hasData = true;
        break;
      case "event":
        this.event = value;
        break;
      case "id":
        if (!value.includes("\0")) this.id = value;
        break;
      default:
        break; // retry and unknown fields are ignored
    }
  }

  private dispatch(): void {
    if (this.hasData) {
      this.onMessage({ id: this.id, event: this.event || "message", data: this.data.join("\n") });
    }
    this.data = [];
    this.hasData = false;
    this.event = "";
    // id persists as the last event id (spec), but we report it per message.
  }
}
