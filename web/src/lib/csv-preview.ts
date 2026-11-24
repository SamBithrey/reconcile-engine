export interface ParsedCsvPreview {
  headers: string[];
  rows: string[][];
  error?: string;
}

/**
 * Deterministic RFC-4180 CSV parser for rendering supplier CSV manifests cleanly
 * in the HITL source document viewer without external libraries.
 */
export function parseCsvForPreview(raw: string): ParsedCsvPreview {
  const input = raw.replace(/^\uFEFF/, "");
  const records: string[][] = [];
  let currentRow: string[] = [];
  let currentField = "";
  let inQuotes = false;

  for (let i = 0; i < input.length; i += 1) {
    const ch = input[i];

    if (inQuotes) {
      if (ch === '"') {
        if (input[i + 1] === '"') {
          currentField += '"';
          i += 1;
        } else {
          inQuotes = false;
        }
      } else {
        currentField += ch;
      }
      continue;
    }

    if (ch === '"') {
      inQuotes = true;
    } else if (ch === ",") {
      currentRow.push(currentField);
      currentField = "";
    } else if (ch === "\r") {
      if (input[i + 1] === "\n") {
        i += 1;
      }
      currentRow.push(currentField);
      records.push(currentRow);
      currentRow = [];
      currentField = "";
    } else if (ch === "\n") {
      currentRow.push(currentField);
      records.push(currentRow);
      currentRow = [];
      currentField = "";
    } else {
      currentField += ch;
    }
  }

  if (inQuotes) {
    return {
      headers: [],
      rows: [],
      error: "Unterminated quoted string in CSV source file",
    };
  }

  if (currentField.length > 0 || currentRow.length > 0) {
    currentRow.push(currentField);
    records.push(currentRow);
  }

  const nonEmpty = records.filter(
    (row) => row.length > 1 || (row.length === 1 && row[0].trim() !== ""),
  );
  if (nonEmpty.length === 0) {
    return { headers: [], rows: [], error: "CSV file is empty" };
  }

  const headers = nonEmpty[0];
  const rows = nonEmpty.slice(1);
  return { headers, rows };
}
