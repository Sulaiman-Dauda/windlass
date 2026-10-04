// .env text <-> rows, used by the Environment tab's text view. Lossless for
// any value the list view can hold.

export interface EnvRow {
  key: string;
  value: string;
}

const ESCAPES: Record<string, string> = { n: "\n", r: "\r", t: "\t", '"': '"', "\\": "\\" };

export function parseEnvBlock(input: string): { rows: EnvRow[]; errors: string[] } {
  const values = new Map<string, string>();
  const errors: string[] = [];

  input.split(/\r?\n/).forEach((source, index) => {
    let line = source.trim();
    if (!line || line.startsWith("#")) return;
    if (line.startsWith("export ")) line = line.slice(7).trimStart();

    const separator = line.indexOf("=");
    if (separator < 1) {
      errors.push(`Line ${index + 1}: expected KEY=value`);
      return;
    }

    const key = line.slice(0, separator).trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
      errors.push(`Line ${index + 1}: invalid variable name "${key}"`);
      return;
    }

    let value = line.slice(separator + 1).trim();
    if (
      value.length >= 2 &&
      ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))
    ) {
      const quote = value[0];
      value = value.slice(1, -1);
      // One pass, so an escaped backslash is never read as the start of
      // another escape ("\\n" is a backslash and an n, not a newline).
      if (quote === '"') value = value.replace(/\\([nrt"\\])/g, (_, c: string) => ESCAPES[c]);
    }

    values.set(key, value);
  });

  return { rows: Array.from(values, ([key, value]) => ({ key, value })), errors };
}

// serializeEnvValue is the inverse of parseEnvBlock's value parsing: quote
// only when needed for the round-trip to be lossless (leading/trailing
// whitespace, a line break, or a value that would otherwise look quoted).
function serializeEnvValue(value: string): string {
  if (value === value.trim() && !/^['"]/.test(value) && !/[\r\n]/.test(value)) return value;
  const escaped = value
    .replace(/\\/g, "\\\\")
    .replace(/"/g, '\\"')
    .replace(/\n/g, "\\n")
    .replace(/\r/g, "\\r")
    .replace(/\t/g, "\\t");
  return `"${escaped}"`;
}

export function serializeEnvBlock(rows: EnvRow[]): string {
  return rows
    .filter((row) => row.key)
    .map((row) => `${row.key}=${serializeEnvValue(row.value)}`)
    .join("\n");
}
