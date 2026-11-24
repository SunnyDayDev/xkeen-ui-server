type Range = { start: number; end: number; kind: string };

export class ExactSource {
  private past: string[] = [];
  private future: string[] = [];

  constructor(private raw: string) {}

  get value(): string { return this.raw; }

  get display(): string { return this.raw.replace(/\r\n?/g, '\n'); }

  replaceVisible(next: string, range?: Range): void {
    const current = this.display;
    if (current === next) return;
    if (range) {
      let { start, end, kind } = range;
      const removed = current.length - next.length;
      if (removed > 0 && start === end && kind.startsWith('delete')) {
        if (kind.endsWith('Backward')) start = Math.max(0, start - removed);
        if (kind.endsWith('Forward')) end = Math.min(current.length, end + removed);
      }
      const tail = current.length - end;
      if (next.length >= start + tail && next.startsWith(current.slice(0, start)) &&
          next.endsWith(current.slice(end))) {
        this.splice(start, end, next.slice(start, next.length - tail));
        return;
      }
    }
    let start = 0;
    while (start < current.length && start < next.length && current[start] === next[start]) start++;
    let last = current.length;
    let nextLast = next.length;
    while (last > start && nextLast > start && current[last - 1] === next[nextLast - 1]) {
      last--;
      nextLast--;
    }
    this.splice(start, last, next.slice(start, nextLast));
  }

  splice(start: number, end: number, insert: string): void {
    const from = this.rawOffset(start);
    const to = this.rawOffset(end);
    const changed = this.raw.slice(0, from) + insert + this.raw.slice(to);
    if (changed === this.raw) return;
    this.remember(this.past, this.raw);
    this.future = [];
    this.raw = changed;
  }

  undo(): void { this.move(this.past, this.future); }

  redo(): void { this.move(this.future, this.past); }

  private move(from: string[], to: string[]): void {
    const next = from.pop();
    if (next === undefined) return;
    this.remember(to, this.raw);
    this.raw = next;
  }

  private remember(history: string[], value: string): void {
    history.push(value);
    let chars = 0;
    for (let i = history.length - 1; i >= 0; i--) {
      chars += history[i].length;
      // Always allow at least the last change to be undone.
      if ((chars > 4 * 1024 * 1024 || history.length - i > 100) && i < history.length - 1) {
        history.splice(0, i + 1);
        break;
      }
    }
  }

  private rawOffset(visible: number): number {
    let raw = 0;
    for (let offset = 0; offset < visible && raw < this.raw.length; offset++, raw++) {
      if (this.raw[raw] === '\r' && this.raw[raw + 1] === '\n') raw++;
    }
    return raw;
  }
}

export function sourceLocation(source: string, byteOffset: number): {line: number; column: number; visibleOffset: number} {
  const bytes = new TextEncoder();
  let remaining = byteOffset;
  let line = 1;
  let column = 1;
  let previousCR = false;
  let visibleOffset = 0;
  for (const char of source) {
    const length = bytes.encode(char).length;
    if (remaining < length) break;
    remaining -= length;
    if (char === '\r') {
      line++;
      column = 1;
    } else if (char === '\n') {
      if (!previousCR) line++;
      column = 1;
    } else {
      column++;
    }
    if (!(char === '\n' && previousCR)) visibleOffset += char.length;
    previousCR = char === '\r';
  }
  return {line, column, visibleOffset};
}
