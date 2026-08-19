// The cabinet's HTTP contract, driven against a fake fetch.
//
// api.js takes its fetch as an option precisely so this file can exist without
// a browser or a running server. What is pinned here is the part the server
// relies on — the account header, the credentials mode, and the fact that an
// error keeps the server's own words.

import test from 'node:test'
import assert from 'node:assert/strict'

import { createApi, ApiError, filenameFromDisposition } from '../webui/api.js'

// A fake fetch that records what it was asked and answers with what the test
// hands it. The responder is called per request, because a Response body can
// only be read once.
function fakeFetch(responder) {
  const calls = []
  const fetchImpl = async (url, init) => {
    calls.push({ url, init, headers: init.headers })
    return typeof responder === 'function' ? await responder(url, init) : responder()
  }
  return { calls, fetchImpl }
}

const json = (status, body) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const ok = (body) => json(200, body)

test('a call names its method, path and body', async () => {
  const { calls, fetchImpl } = fakeFetch(() => ok({ ok: true }))
  const api = createApi({ fetch: fetchImpl })

  await api.login('teacher@school.example', 'hunter2')

  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, '/api/v1/auth/login')
  assert.equal(calls[0].init.method, 'POST')
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    email: 'teacher@school.example',
    password: 'hunter2',
  })
  assert.equal(calls[0].headers['Content-Type'], 'application/json')
})

test('every request carries the session cookie and asks for JSON', async () => {
  const { calls, fetchImpl } = fakeFetch(() => ok({}))
  const api = createApi({ fetch: fetchImpl })

  await api.me()
  await api.rooms()
  await api.logout()

  for (const call of calls) {
    // The session is an HttpOnly cookie; without this it would simply not be
    // sent, and every request would be a 401.
    assert.equal(call.init.credentials, 'same-origin', `credentials for ${call.url}`)
    assert.equal(call.headers.Accept, 'application/json', `accept for ${call.url}`)
  }
  // A request with no body must not claim to have sent JSON.
  const logout = calls.find((c) => c.url.endsWith('/logout'))
  assert.equal(logout.headers['Content-Type'], undefined)
})

test('the account header appears only once an account has been chosen', async () => {
  const { calls, fetchImpl } = fakeFetch(() => ok({}))
  const api = createApi({ fetch: fetchImpl })

  await api.rooms()
  assert.equal(calls[0].headers['X-Guardian-Account'], undefined, 'the default account is the session’s')

  api.setAccount('acct-2')
  assert.equal(api.account(), 'acct-2')
  await api.rooms()
  assert.equal(calls[1].headers['X-Guardian-Account'], 'acct-2')

  api.setAccount(null)
  await api.rooms()
  assert.equal(calls[2].headers['X-Guardian-Account'], undefined, 'clearing must actually clear')
})

test('a 204 and an empty body are both null, not a parse error', async () => {
  const api = createApi({ fetch: fakeFetch(() => new Response(null, { status: 204 })).fetchImpl })
  assert.equal(await api.logout(), null)

  const empty = createApi({ fetch: fakeFetch(() => new Response('', { status: 200 })).fetchImpl })
  assert.equal(await empty.rooms(), null)
})

test('the server’s own error message survives to the screen', async () => {
  const api = createApi({ fetch: fakeFetch(() => json(409, { error: 'That room already exists' })).fetchImpl })

  await assert.rejects(
    () => api.createRoom('Kids room'),
    (err) => {
      assert.ok(err instanceof ApiError)
      assert.equal(err.status, 409)
      // Paraphrasing this in the client would lose the detail the server
      // bothered to produce.
      assert.equal(err.message, 'That room already exists')
      assert.deepEqual(err.body, { error: 'That room already exists' })
      return true
    },
  )
})

test('a reply that is not the API’s falls back to the status', async () => {
  // A proxy's own 502 page is HTML, and the status is all this client
  // honestly knows.
  const api = createApi({
    fetch: fakeFetch(() => new Response('<html>Bad Gateway</html>', { status: 502 })).fetchImpl,
  })
  await assert.rejects(() => api.rooms(), { message: 'The server answered 502', status: 502 })
})

test('a dropped connection is not a server error', async () => {
  const api = createApi({
    fetch: async () => {
      throw new TypeError('Failed to fetch')
    },
  })
  await assert.rejects(
    () => api.rooms(),
    (err) => {
      assert.equal(err.status, 0)
      assert.equal(err.isOffline, true)
      // Telling somebody "500" when their wifi died sends them to the wrong
      // place entirely.
      assert.equal(err.message, 'Cannot reach the server')
      return true
    },
  )
})

test('an abort stays an abort', async () => {
  const api = createApi({
    fetch: async () => {
      const err = new Error('The user aborted a request.')
      err.name = 'AbortError'
      throw err
    },
  })
  // usePoll aborts in flight on unmount; turning that into "cannot reach the
  // server" would flash an outage banner on every navigation.
  await assert.rejects(() => api.rooms(), { name: 'AbortError' })
})

test('ApiError classifies the statuses the screens branch on', () => {
  assert.equal(new ApiError(401, 'x').isAuth, true)
  assert.equal(new ApiError(403, 'x').isForbidden, true)
  assert.equal(new ApiError(402, 'x').isBilling, true)
  assert.equal(new ApiError(0, 'x').isOffline, true)
  const ordinary = new ApiError(500, 'x')
  assert.equal(ordinary.isAuth, false)
  assert.equal(ordinary.isOffline, false)
  assert.equal(ordinary.name, 'ApiError')
})

test('the activity feed pages by cursor, and omits an empty query', async () => {
  const { calls, fetchImpl } = fakeFetch(() => ok({ events: [] }))
  const api = createApi({ fetch: fetchImpl })

  await api.events()
  await api.events({ limit: 8 })
  await api.events({ cursor: 'opaque/cursor' })
  await api.events({ limit: 50, cursor: 'c2' })

  assert.equal(calls[0].url, '/api/v1/events', 'no parameters, no question mark')
  assert.equal(calls[1].url, '/api/v1/events?limit=8')
  assert.equal(calls[2].url, '/api/v1/events?cursor=opaque%2Fcursor')
  assert.equal(calls[3].url, '/api/v1/events?limit=50&cursor=c2')
})

test('ids go into the path encoded, so one segment stays one segment', async () => {
  const { calls, fetchImpl } = fakeFetch(() => new Response(null, { status: 204 }))
  const api = createApi({ fetch: fetchImpl })

  await api.deleteApplication('room/1', 'app 2')
  assert.equal(calls[0].url, '/api/v1/rooms/room%2F1/applications/app%202')

  await api.removeRoomMember('r1', 'user/2')
  assert.equal(calls[1].url, '/api/v1/rooms/r1/members/user%2F2')
})

test('baseUrl is joined without doubling the slash', async () => {
  const { calls, fetchImpl } = fakeFetch(() => ok({}))
  const api = createApi({ fetch: fetchImpl, baseUrl: 'https://guardian.example/' })
  await api.me()
  assert.equal(calls[0].url, 'https://guardian.example/api/v1/me')
})

test('the installer download carries the account and names the file', async () => {
  const { calls, fetchImpl } = fakeFetch(
    () =>
      new Response('PK', {
        status: 200,
        headers: { 'Content-Disposition': 'attachment; filename="Guardian-lincoln.zip"' },
      }),
  )
  const api = createApi({ fetch: fetchImpl, accountId: 'acct-2' })

  const { blob, filename } = await api.downloadInstaller()

  assert.equal(filename, 'Guardian-lincoln.zip')
  assert.equal(await blob.text(), 'PK')
  // A plain <a href> cannot carry this header, which is why the download is a
  // fetch: a manager in two accounts would otherwise get the other one's
  // installer, and the only difference is the token inside it.
  assert.equal(calls[0].headers['X-Guardian-Account'], 'acct-2')
  assert.equal(calls[0].init.credentials, 'same-origin')
})

test('a server with no installer archive says so through the same error type', async () => {
  const api = createApi({
    fetch: fakeFetch(() => json(503, { error: 'No installer archive on this server' })).fetchImpl,
  })
  await assert.rejects(() => api.downloadInstaller(), {
    status: 503,
    message: 'No installer archive on this server',
  })
})

test('filenameFromDisposition reads the header, or picks a name that is at least correct', () => {
  assert.equal(
    filenameFromDisposition('attachment; filename="Guardian-lincoln.zip"'),
    'Guardian-lincoln.zip',
  )
  assert.equal(filenameFromDisposition('attachment; filename=Guardian.zip'), 'Guardian.zip')
  assert.equal(filenameFromDisposition('attachment; filename=Guardian.zip; x=1'), 'Guardian.zip')
  assert.equal(filenameFromDisposition(null), 'Guardian.zip')
  assert.equal(filenameFromDisposition('attachment'), 'Guardian.zip')
})
