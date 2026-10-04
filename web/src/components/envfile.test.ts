import { describe, expect, it } from "vitest";
import { parseEnvBlock, serializeEnvBlock } from "./envfile";

describe("env text view", () => {
  it("parses KEY=value lines, skipping comments, blanks and export", () => {
    const { rows, errors } = parseEnvBlock("# db\nexport A=1\n\nB = two \nC='x y'\n");
    expect(errors).toEqual([]);
    expect(rows).toEqual([
      { key: "A", value: "1" },
      { key: "B", value: "two" },
      { key: "C", value: "x y" },
    ]);
  });

  it("reports bad lines by number", () => {
    expect(parseEnvBlock("A=1\nnot a line\n9X=2").errors).toEqual([
      "Line 2: expected KEY=value",
      'Line 3: invalid variable name "9X"',
    ]);
  });

  it("round-trips values that need quoting", () => {
    const rows = [
      { key: "PLAIN", value: "abc" },
      { key: "SPACES", value: "  padded  " },
      { key: "QUOTED", value: '"looks quoted"' },
      { key: "MULTI", value: "line1\nline2\ttab \\ slash" },
    ];
    expect(parseEnvBlock(serializeEnvBlock(rows)).rows).toEqual(rows);
  });

  it("keeps a literal backslash before n, r, t or a quote", () => {
    const rows = [
      { key: "PEM", value: '{\n  "key": "-----BEGIN\\nABC"\n}' },
      { key: "WIN", value: "  C:\\new\\table\\r  " },
      { key: "ESC", value: ' \\" ' },
    ];
    expect(parseEnvBlock(serializeEnvBlock(rows)).rows).toEqual(rows);
  });
});
