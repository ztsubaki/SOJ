"use client";

const expiryKey = "soj-session-expires-at";

export function observeSession(value: unknown) {
  if (!value || typeof value !== "object") return;
  const expiresAt = Date.parse(
    String((value as { expiresAt?: unknown }).expiresAt),
  );
  if (!Number.isFinite(expiresAt)) return;
  try {
    localStorage.setItem(expiryKey, String(expiresAt));
  } catch {
    /* Storage is optional. */
  }
}

export function clearSession() {
  try {
    localStorage.removeItem(expiryKey);
  } catch {
    /* Storage is optional. */
  }
}

// Called once when the page loads. The server remains the authority on expiry.
export async function refreshSessionIfNeeded() {
  let expiresAt: number;
  try {
    expiresAt = Number(localStorage.getItem(expiryKey));
  } catch {
    return;
  }
  const remaining = expiresAt - Date.now();
  if (remaining <= 0 || remaining >= 24 * 60 * 60 * 1_000) return;
  try {
    const response = await fetch("/api/v1/auth/refresh", {
      method: "POST",
      cache: "no-store",
    });
    if (response.ok) observeSession(await response.json());
    else if (response.status === 401) clearSession();
  } catch {
    // Do not retry a rotation. The normal auth/me request determines identity.
  }
}
