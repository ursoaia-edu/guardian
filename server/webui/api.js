// The cabinet's whole HTTP surface, in one place.
//
// It is a plain ES module with no imports: the cabinet ships as source, is
// embedded in the server binary, and has no build step, so anything it needs
// it either does itself or does without. The only outside thing it touches is
// fetch, and that is injectable so this file can be tested under `node --test`
// without a browser or a server.
//
// Two rules the API enforces that this client must respect:
//   - account_id is never sent as a parameter. The account a request acts in
//     is either the session's default or named by X-Guardian-Account, and that
//     header may only carry an id GET /api/v1/me already returned.
//   - the session lives in an HttpOnly cookie. There is no token in
//     JavaScript's reach here, on purpose, so every request just says
//     credentials: 'same-origin'.

export class ApiError extends Error {
  constructor(status, message, body) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }

  // Not signed in, or the session expired underneath us.
  get isAuth() {
    return this.status === 401
  }

  // Signed in, but a guest reaching for something account-wide.
  get isForbidden() {
    return this.status === 403
  }

  // The account is at its computer limit. Nothing in the cabinet can fix it
  // yet — billing is a later plan — but the message must not look like a bug.
  get isBilling() {
    return this.status === 402
  }

  // Status 0 is this client's own: the request never reached a server.
  get isOffline() {
    return this.status === 0
  }
}

const ACCOUNT_HEADER = 'X-Guardian-Account'

export function createApi(options = {}) {
  const fetchImpl = options.fetch || globalThis.fetch
  const baseUrl = (options.baseUrl || '').replace(/\/$/, '')
  let accountId = options.accountId || null

  async function request(method, path, opts = {}) {
    const headers = { Accept: 'application/json' }
    if (accountId) headers[ACCOUNT_HEADER] = accountId
    const init = {
      method,
      headers,
      credentials: 'same-origin',
      signal: opts.signal,
    }
    if (opts.body !== undefined) {
      headers['Content-Type'] = 'application/json'
      init.body = JSON.stringify(opts.body)
    }

    let response
    try {
      response = await fetchImpl(baseUrl + path, init)
    } catch (err) {
      if (err && err.name === 'AbortError') throw err
      // A dropped connection is not a server error, and telling somebody
      // "500" when their wifi died sends them to the wrong place entirely.
      throw new ApiError(0, 'Cannot reach the server', null)
    }

    if (response.status === 204) return null

    const text = await response.text()
    let parsed = null
    if (text) {
      try {
        parsed = JSON.parse(text)
      } catch {
        parsed = null
      }
    }

    if (!response.ok) {
      // The API answers every error as {"error": "..."}. When it does not —
      // a proxy's own 502 page, say — the status is all we honestly have.
      const message =
        (parsed && typeof parsed.error === 'string' && parsed.error) ||
        `The server answered ${response.status}`
      throw new ApiError(response.status, message, parsed)
    }
    return parsed
  }

  // The binary download is a fetch rather than a link because a link cannot
  // carry X-Guardian-Account, and a manager acting in a second account would
  // otherwise silently download the first account's installer.
  async function downloadInstaller() {
    const headers = {}
    if (accountId) headers[ACCOUNT_HEADER] = accountId
    let response
    try {
      response = await fetchImpl(baseUrl + '/api/v1/installer', {
        method: 'GET',
        headers,
        credentials: 'same-origin',
      })
    } catch {
      throw new ApiError(0, 'Cannot reach the server', null)
    }
    if (!response.ok) {
      let message = `The server answered ${response.status}`
      try {
        const body = await response.json()
        if (body && body.error) message = body.error
      } catch {
        /* keep the status-based message */
      }
      throw new ApiError(response.status, message, null)
    }
    const name = filenameFromDisposition(response.headers.get('Content-Disposition'))
    return { blob: await response.blob(), filename: name }
  }

  return {
    setAccount(id) {
      accountId = id || null
    },
    account() {
      return accountId
    },

    // Auth
    register: (email, password, name) =>
      request('POST', '/api/v1/auth/register', { body: { email, password, name } }),
    login: (email, password) =>
      request('POST', '/api/v1/auth/login', { body: { email, password } }),
    logout: () => request('POST', '/api/v1/auth/logout'),
    me: (opts) => request('GET', '/api/v1/me', opts),

    // Rooms
    rooms: (opts) => request('GET', '/api/v1/rooms', opts),
    room: (id, opts) => request('GET', `/api/v1/rooms/${encodeURIComponent(id)}`, opts),
    createRoom: (name) => request('POST', '/api/v1/rooms', { body: { name } }),
    patchRoom: (id, patch) =>
      request('PATCH', `/api/v1/rooms/${encodeURIComponent(id)}`, { body: patch }),
    deleteRoom: (id) => request('DELETE', `/api/v1/rooms/${encodeURIComponent(id)}`),

    // Rules
    applications: (roomID, opts) =>
      request('GET', `/api/v1/rooms/${encodeURIComponent(roomID)}/applications`, opts),
    addApplication: (roomID, name, list) =>
      request('POST', `/api/v1/rooms/${encodeURIComponent(roomID)}/applications`, {
        body: { name, list },
      }),
    setApplicationEnabled: (roomID, appID, enabled) =>
      request(
        'PATCH',
        `/api/v1/rooms/${encodeURIComponent(roomID)}/applications/${encodeURIComponent(appID)}`,
        { body: { enabled } },
      ),
    deleteApplication: (roomID, appID) =>
      request(
        'DELETE',
        `/api/v1/rooms/${encodeURIComponent(roomID)}/applications/${encodeURIComponent(appID)}`,
      ),

    // Room guests
    roomMembers: (roomID, opts) =>
      request('GET', `/api/v1/rooms/${encodeURIComponent(roomID)}/members`, opts),
    addRoomMember: (roomID, email) =>
      request('POST', `/api/v1/rooms/${encodeURIComponent(roomID)}/members`, { body: { email } }),
    removeRoomMember: (roomID, userID) =>
      request(
        'DELETE',
        `/api/v1/rooms/${encodeURIComponent(roomID)}/members/${encodeURIComponent(userID)}`,
      ),

    // Computers
    computers: (opts) => request('GET', '/api/v1/computers', opts),
    computer: (id, opts) => request('GET', `/api/v1/computers/${encodeURIComponent(id)}`, opts),
    patchComputer: (id, patch) =>
      request('PATCH', `/api/v1/computers/${encodeURIComponent(id)}`, { body: patch }),
    unenrolComputer: (id) => request('DELETE', `/api/v1/computers/${encodeURIComponent(id)}`),

    // Account members
    accountMembers: (opts) => request('GET', '/api/v1/account/members', opts),
    addAccountMember: (email) => request('POST', '/api/v1/account/members', { body: { email } }),
    removeAccountMember: (userID) =>
      request('DELETE', `/api/v1/account/members/${encodeURIComponent(userID)}`),

    // Installer credentials
    mintBindingToken: () => request('POST', '/api/v1/binding-tokens'),
    revokeBindingTokens: () => request('DELETE', '/api/v1/binding-tokens'),
    downloadInstaller,

    // The blocking log: what a policy actually closed. Two endpoints rather
    // than one with a filter, because a machine in no room has room_id NULL on
    // every row and the room query can never match it.
    roomProcessEvents: (roomID, params = {}, opts) =>
      request('GET', `/api/v1/rooms/${encodeURIComponent(roomID)}/process-events${processEventQuery(params)}`, opts),
    computerProcessEvents: (computerID, params = {}, opts) =>
      request('GET', `/api/v1/computers/${encodeURIComponent(computerID)}/process-events${processEventQuery(params)}`, opts),

    // Activity
    events: (params = {}, opts) => {
      const query = new URLSearchParams()
      if (params.limit) query.set('limit', String(params.limit))
      if (params.cursor) query.set('cursor', params.cursor)
      const qs = query.toString()
      return request('GET', '/api/v1/events' + (qs ? `?${qs}` : ''), opts)
    },
  }
}

// processEventQuery builds the blocking log's query string. An empty filter is
// left out entirely rather than sent blank: the server ignores a malformed one,
// but a request that says nothing is easier to read in a log.
export function processEventQuery(params = {}) {
  const query = new URLSearchParams()
  for (const key of ['process', 'reason', 'computer_id', 'since', 'until', 'cursor']) {
    if (params[key]) query.set(key, String(params[key]))
  }
  if (params.limit) query.set('limit', String(params.limit))
  const qs = query.toString()
  return qs ? `?${qs}` : ''
}

// filenameFromDisposition pulls Guardian-<account>.zip out of the header the
// installer endpoint sets, falling back to a name that is at least correct.
export function filenameFromDisposition(header) {
  if (!header) return 'Guardian.zip'
  const quoted = header.match(/filename="([^"]+)"/i)
  if (quoted) return quoted[1]
  const bare = header.match(/filename=([^;]+)/i)
  if (bare) return bare[1].trim()
  return 'Guardian.zip'
}
