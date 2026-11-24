/**
 * Integer minor-unit (pence) and basis-point (bps) financial math & formatting.
 *
 * Strictly zero IEEE-754 floating-point arithmetic (`float32`/`float64` or decimal division)
 * is used for money or tax rates. All values are converted to `bigint` integers.
 */

const gbpIntegerFormatter = new Intl.NumberFormat("en-GB", {
  useGrouping: true,
});

export function toSafeBigInt(value: number | bigint): bigint {
  if (typeof value === "bigint") {
    return value;
  }
  if (!Number.isFinite(value) || !Number.isInteger(value)) {
    throw new RangeError(`Expected integer minor-unit amount, received ${String(value)}`);
  }
  return BigInt(value);
}

function formatMinorUnitsWithSymbol(
  minorUnits: number | bigint,
  symbol: string,
  options?: { showSign?: boolean; trailingSpaceAfterSymbol?: boolean },
): string {
  const v = toSafeBigInt(minorUnits);
  const isNegative = v < 0n;
  const abs = isNegative ? -v : v;
  const major = abs / 100n;
  const minor = abs % 100n;

  const majorFormatted = gbpIntegerFormatter.format(major);
  const minorStr = minor.toString().padStart(2, "0");

  let sign = "";
  if (isNegative) {
    sign = "-";
  } else if (options?.showSign && v > 0n) {
    sign = "+";
  }

  const sep = options?.trailingSpaceAfterSymbol ? " " : "";
  return `${sign}${symbol}${sep}${majorFormatted}.${minorStr}`;
}

/**
 * Formats integer pence into a GBP currency string (e.g. 25050 -> "£250.50", -300 -> "-£3.00")
 * using pure BigInt integer division and remainder, then Intl.NumberFormat grouping on whole pounds.
 */
export function formatPenceToGBP(
  pence: number | bigint,
  options?: { showSign?: boolean },
): string {
  return formatMinorUnitsWithSymbol(pence, "£", options);
}

/**
 * Formats integer minor units with currency-appropriate symbol/prefix.
 */
export function formatPenceCurrency(
  pence: number | bigint,
  currency = "GBP",
  options?: { showSign?: boolean },
): string {
  const code = currency.trim().toUpperCase();
  switch (code) {
    case "":
    case "GBP":
      return formatMinorUnitsWithSymbol(pence, "£", options);
    case "EUR":
      return formatMinorUnitsWithSymbol(pence, "€", options);
    case "USD":
      return formatMinorUnitsWithSymbol(pence, "$", options);
    default:
      return formatMinorUnitsWithSymbol(pence, code, {
        ...options,
        trailingSpaceAfterSymbol: true,
      });
  }
}

/**
 * Formats integer pence as a plain 2-decimal string without currency symbol or commas
 * (e.g. 25050 -> "250.50", -300 -> "-3.00") for deterministic CSV/JSON export.
 */
export function formatPenceDecimal(pence: number | bigint): string {
  const v = toSafeBigInt(pence);
  const sign = v < 0n ? "-" : "";
  const abs = v < 0n ? -v : v;
  const major = abs / 100n;
  const minor = abs % 100n;
  return `${sign}${major.toString()}.${minor.toString().padStart(2, "0")}`;
}

/**
 * Formats basis points (bps) as a fixed 2-decimal percentage string (e.g. 2000 -> "20.00%").
 */
export function formatBPS(bps: number | bigint): string {
  const v = toSafeBigInt(bps);
  const sign = v < 0n ? "-" : "";
  const abs = v < 0n ? -v : v;
  const wholePct = abs / 100n;
  const fracPct = abs % 100n;
  return `${sign}${wholePct.toString()}.${fracPct.toString().padStart(2, "0")}%`;
}

export function absPence(pence: number | bigint): bigint {
  const v = toSafeBigInt(pence);
  return v < 0n ? -v : v;
}

export function sumPence(values: ReadonlyArray<number | bigint>): bigint {
  let total = 0n;
  for (const val of values) {
    total += toSafeBigInt(val);
  }
  return total;
}

/**
 * Computes line net pence (`quantity * unitPricePence`) using integer arithmetic.
 */
export function calculateLineNetPence(
  qty: number | bigint,
  unitPricePence: number | bigint,
): bigint {
  return toSafeBigInt(qty) * toSafeBigInt(unitPricePence);
}

/**
 * Computes line VAT pence using half-up integer rounding matching Go `domain.CalculateVATPence`:
 * `(line_net_pence * vat_rate_bps + 5000) / 10000`.
 */
export function calculateLineVATPence(
  lineNetPence: number | bigint,
  vatRateBPS: number | bigint,
): bigint {
  const net = toSafeBigInt(lineNetPence);
  const bps = toSafeBigInt(vatRateBPS);
  if (net < 0n) {
    return -((-net * bps + 5000n) / 10000n);
  }
  return (net * bps + 5000n) / 10000n;
}

/**
 * Mirrors backend rule 3WAY-08 (`PRICE_VARIANCE`):
 * abs(inv - po) > 50p AND abs(inv - po) * 10000 > po * 100 (1.00%).
 */
export function isPriceOutsideTolerance(
  invUnitPricePence: number | bigint,
  poUnitPricePence: number | bigint,
): boolean {
  const inv = toSafeBigInt(invUnitPricePence);
  const po = toSafeBigInt(poUnitPricePence);
  const diff = inv - po;
  const absDiff = diff < 0n ? -diff : diff;
  return absDiff > 50n && absDiff * 10000n > po * 100n;
}
