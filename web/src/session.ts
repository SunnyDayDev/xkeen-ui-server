export type Session = {
  state: 'setup_required' | 'signed_out' | 'active' | 'invalid_credentials' | 'invalid_csrf' | 'rate_limited' | 'unavailable';
  csrf?: string;
};

export async function sessionRequest(
  operation: 'session' | 'login' | 'logout',
  secret = ''
): Promise<Session> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const method = operation === 'session' ? 'GET' : 'POST';
  let body: string | undefined;
  if (operation === 'login') {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify({ password: secret });
  } else if (operation === 'logout') {
    headers['X-CSRF-Token'] = secret;
  }
  try {
    const response = await fetch('/ui/v1/' + operation, {
      method, headers, body, credentials: 'same-origin', cache: 'no-store', redirect: 'error',
      signal: AbortSignal.timeout(10000)
    });
    const result = await response.json() as Session;
    switch (result.state) {
      case 'active':
        return response.ok && typeof result.csrf === 'string' && result.csrf !== '' ? result : { state: 'unavailable' };
      case 'signed_out':
      case 'setup_required':
      case 'invalid_credentials':
      case 'invalid_csrf':
      case 'rate_limited':
        return result;
      default:
        return { state: 'unavailable' };
    }
  } catch {
    return { state: 'unavailable' };
  }
}
