import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatDate(value?: string | null) {
  if (!value) {
    return "Unknown";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

export function stringifyJson(value: unknown) {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function formatCompactTokens(value?: number | null) {
  const amount = Math.max(0, Math.trunc(value ?? 0));

  if (amount < 1000) {
    return String(amount);
  }
  if (amount < 1_000_000) {
    const compact = amount / 1000;
    return `${compact % 1 === 0 ? compact.toFixed(0) : compact.toFixed(1)}k`;
  }
  const compact = amount / 1_000_000;
  return `${compact % 1 === 0 ? compact.toFixed(0) : compact.toFixed(1)}m`;
}
