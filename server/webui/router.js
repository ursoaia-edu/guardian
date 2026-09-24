// Hash routing, kept pure so it can be tested without a browser.
//
// The cabinet uses the hash rather than the History API for one reason: the
// SPA is served by the same Go binary that serves the API, and a hash route
// never reaches the server at all. There is no way for a deep link to be
// answered by a 404 from a proxy that does not know about the cabinet.

// Every screen, with the shape of its path. Order matters only for reading.
export const ROUTES = [
  { name: 'overview', pattern: [] },
  { name: 'welcome', pattern: ['welcome'] },
  { name: 'rooms', pattern: ['rooms'] },
  { name: 'room', pattern: ['rooms', ':roomID'] },
  { name: 'room', pattern: ['rooms', ':roomID', ':tab'] },
  { name: 'computers', pattern: ['computers'] },
  { name: 'computer', pattern: ['computers', ':computerID'] },
  { name: 'install', pattern: ['install'] },
  { name: 'activity', pattern: ['activity'] },
  { name: 'settings', pattern: ['settings'] },
  // Reached from a link in an email, by somebody who is very often not signed
  // in. See app.js, which handles these before the sign-in gate.
  { name: 'forgot', pattern: ['forgot'] },
  { name: 'verify', pattern: ['verify', ':token'] },
  { name: 'reset', pattern: ['reset', ':token'] },
]

// The tabs of the room screen, which is the one screen with any.
export const ROOM_TABS = ['computers', 'rules', 'log', 'power', 'members']

export function parseRoute(hash) {
  const raw = String(hash || '').replace(/^#/, '')
  const segments = raw.split('/').filter((s) => s.length > 0).map(decodeURIComponent)

  for (const route of ROUTES) {
    if (route.pattern.length !== segments.length) continue
    const params = {}
    let matched = true
    for (let i = 0; i < route.pattern.length; i++) {
      const part = route.pattern[i]
      if (part.startsWith(':')) {
        params[part.slice(1)] = segments[i]
      } else if (part !== segments[i]) {
        matched = false
        break
      }
    }
    if (!matched) continue
    if (route.name === 'room') {
      // An unknown tab is the room's default tab rather than a dead end: the
      // id in the URL is the part worth keeping.
      params.tab = ROOM_TABS.includes(params.tab) ? params.tab : ROOM_TABS[0]
    }
    return { name: route.name, params }
  }
  return { name: 'notfound', params: {} }
}

export function href(name, params = {}) {
  switch (name) {
    case 'overview':
      return '#/'
    case 'room':
      return `#/rooms/${encodeURIComponent(params.roomID)}/${params.tab || ROOM_TABS[0]}`
    case 'computer':
      return `#/computers/${encodeURIComponent(params.computerID)}`
    case 'verify':
      return `#/verify/${encodeURIComponent(params.token)}`
    case 'reset':
      return `#/reset/${encodeURIComponent(params.token)}`
    default:
      return `#/${name}`
  }
}

export function navigate(name, params) {
  window.location.hash = href(name, params)
}
