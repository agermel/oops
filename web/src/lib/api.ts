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
  return resp.json();
}
