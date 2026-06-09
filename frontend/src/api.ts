const TOKEN_KEY = 'llm_gateway_token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
}

export function removeToken(): void {
  localStorage.removeItem(TOKEN_KEY)
}

export function isAuthenticated(): boolean {
  const token = getToken()
  if (!token) return false

  // Check if JWT is expired by decoding the payload
  try {
    const parts = token.split('.')
    if (parts.length !== 3) return false
    const payload = JSON.parse(atob(parts[1]))
    if (payload.exp && payload.exp * 1000 < Date.now()) {
      removeToken()
      return false
    }
    return true
  } catch {
    removeToken()
    return false
  }
}

export function getCurrentUser(): { id: number; username: string } | null {
  const token = getToken()
  if (!token) return null
  try {
    const parts = token.split('.')
    const payload = JSON.parse(atob(parts[1]))
    return { id: Number(payload.sub), username: payload.username }
  } catch {
    return null
  }
}

export async function apiFetch(
  url: string,
  options?: RequestInit,
): Promise<Response> {
  const token = getToken()
  const headers = new Headers(options?.headers)

  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }

  const response = await fetch(url, { ...options, headers })

  if (response.status === 401 && !url.startsWith('/api/test/')) {
    removeToken()
    window.dispatchEvent(new CustomEvent('auth:expired'))
    window.location.hash = '#/login'
  }

  return response
}
