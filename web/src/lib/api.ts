export function getErrorMessage(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export async function apiRequest<T = any>(
  url: string,
  init?: RequestInit,
): Promise<T> {
  const resp = await fetch(url, init);
  if (!resp.ok) {
    const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
    throw new ApiError(data.error || `HTTP ${resp.status}`, resp.status);
  }
  // 204 No Content / empty body — e.g. DELETE operations
  if (resp.status === 204 || resp.headers.get("content-length") === "0") {
    return undefined as unknown as T;
  }
  const text = await resp.text();
  if (!text) return undefined as unknown as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    return text as unknown as T;
  }
}
