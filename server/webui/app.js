// The cabinet's root: session, account scope, routing and the shell around
// every screen.
//
// React and ReactDOM arrive as globals from vendor/, and htm turns the
// template literals below into React.createElement calls in the browser. That
// is the whole toolchain: the files served are the files in the repository, so
// `go build` alone produces a working cabinet and there is no build output
// anyone has to remember to regenerate.
const React = window.React
const { useState, useEffect, useCallback } = React

import { createApi, ApiError } from './api.js'
import { html, ErrorBanner, Loading, Banner, BrandMark, PageHeader } from './ui.js'
import { parseRoute, href, navigate } from './router.js'
import { usePoll } from './hooks.js'
import { SignInScreen } from './screens/signin.js'
import { WelcomeScreen } from './screens/welcome.js'
import { OverviewScreen } from './screens/overview.js'
import { RoomsScreen } from './screens/rooms.js'
import { RoomScreen } from './screens/room.js'
import { ComputersScreen } from './screens/computers.js'
import { ComputerScreen } from './screens/computer.js'
import { InstallScreen } from './screens/install.js'
import { ActivityScreen } from './screens/activity.js'
import { SettingsScreen } from './screens/settings.js'

// ACCOUNT_KEY remembers which of the caller's accounts they were last acting
// in. It is a preference, not a credential: the server accepts the id only if
// the session already proves a membership in it, and answers 404 otherwise.
const ACCOUNT_KEY = 'guardian.account'

function readStoredAccount() {
  try {
    return window.localStorage.getItem(ACCOUNT_KEY)
  } catch {
    return null
  }
}

function storeAccount(id) {
  try {
    if (id) window.localStorage.setItem(ACCOUNT_KEY, id)
    else window.localStorage.removeItem(ACCOUNT_KEY)
  } catch {
    /* private mode, or storage disabled: the cabinet works without it */
  }
}

function useHashRoute() {
  const [route, setRoute] = useState(() => parseRoute(window.location.hash))
  useEffect(() => {
    const onChange = () => setRoute(parseRoute(window.location.hash))
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return route
}

function App({ api }) {
  const [status, setStatus] = useState('loading') // loading | signedout | ready
  const [me, setMe] = useState(null)
  const [error, setError] = useState(null)
  const [welcomeDone, setWelcomeDone] = useState(false)
  const route = useHashRoute()

  const onAuthError = useCallback(() => {
    setMe(null)
    setStatus('signedout')
  }, [])

  // loadMe is the one call that establishes who is signed in and which
  // accounts they may act in. A stored account preference is applied before
  // the second read so `role` belongs to the account actually being shown.
  const loadMe = useCallback(async () => {
    try {
      let identity = await api.me()
      const stored = readStoredAccount()
      const allowed = (identity.accounts || []).some((a) => a.account_id === stored)
      if (stored && allowed && stored !== identity.account_id) {
        api.setAccount(stored)
        identity = await api.me()
      } else if (!allowed) {
        // The stored id is no longer one of ours — a revoked room grant, or a
        // different person signing in on this browser.
        storeAccount(null)
        api.setAccount(null)
      }
      setMe(identity)
      setStatus('ready')
      setError(null)
      return identity
    } catch (err) {
      if (err instanceof ApiError && err.isAuth) {
        setStatus('signedout')
        setMe(null)
        return null
      }
      setError(err)
      setStatus('ready')
      return null
    }
  }, [api])

  useEffect(() => {
    loadMe()
  }, [loadMe])

  const switchAccount = useCallback(
    async (accountID) => {
      storeAccount(accountID)
      api.setAccount(accountID)
      setWelcomeDone(false)
      await loadMe()
      navigate('overview')
    },
    [api, loadMe],
  )

  const signOut = useCallback(async () => {
    try {
      await api.logout()
    } catch {
      // A logout that cannot reach the server still ends the session here;
      // the cookie is cleared server-side on the next successful call, and
      // leaving somebody staring at a cabinet they asked to leave is worse.
    }
    storeAccount(null)
    api.setAccount(null)
    setMe(null)
    setStatus('signedout')
    navigate('overview')
  }, [api])

  if (status === 'loading') return html`<div class="boot"><${Loading} what="Opening the cabinet" /></div>`

  if (status === 'signedout') {
    return html`<${SignInScreen}
      api=${api}
      onSignedIn=${async ({ fresh }) => {
        const identity = await loadMe()
        if (identity && fresh) navigate('welcome')
      }}
    />`
  }

  const role = (me && me.role) || 'member'
  return html`
    <${Shell}
      api=${api}
      me=${me}
      role=${role}
      route=${route}
      onSignOut=${signOut}
      onAuthError=${onAuthError}
    >
      <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
      <${Screen}
        api=${api}
        me=${me}
        role=${role}
        route=${route}
        onAuthError=${onAuthError}
        onAccountChange=${switchAccount}
        onSignOut=${signOut}
        welcomeDone=${welcomeDone}
        onWelcomeDone=${() => setWelcomeDone(true)}
      />
    <//>
  `
}

// Screen is the router's other half: it maps a parsed route to a screen, and
// it is also where the first-run redirect lives — an account with no computers
// is shown the wizard rather than an empty overview, once.
function Screen(props) {
  const { api, me, role, route, onAuthError, onAccountChange, onSignOut, welcomeDone, onWelcomeDone } = props
  const manager = role === 'owner' || role === 'admin'

  const firstRun = usePoll(() => (manager ? api.computers() : Promise.resolve(null)), {
    deps: [manager, me && me.account_id],
    interval: 60000,
    enabled: manager && !welcomeDone,
    onAuthError,
  })

  useEffect(() => {
    if (!manager || welcomeDone) return
    if (route.name !== 'overview') return
    const list = firstRun.data && firstRun.data.computers
    if (list && list.length === 0) navigate('welcome')
  }, [manager, welcomeDone, route.name, firstRun.data])

  switch (route.name) {
    case 'welcome':
      return html`<${WelcomeScreen} api=${api} onAuthError=${onAuthError} onDone=${onWelcomeDone} />`
    case 'overview':
      return html`<${OverviewScreen} api=${api} role=${role} onAuthError=${onAuthError} />`
    case 'rooms':
      return html`<${RoomsScreen} api=${api} role=${role} onAuthError=${onAuthError} />`
    case 'room':
      return html`<${RoomScreen}
        api=${api}
        role=${role}
        roomID=${route.params.roomID}
        tab=${route.params.tab}
        onAuthError=${onAuthError}
      />`
    case 'computers':
      return html`<${ComputersScreen} api=${api} role=${role} onAuthError=${onAuthError} />`
    case 'computer':
      return html`<${ComputerScreen}
        api=${api}
        role=${role}
        computerID=${route.params.computerID}
        onAuthError=${onAuthError}
      />`
    case 'install':
      return html`<${InstallScreen} api=${api} role=${role} />`
    case 'activity':
      return html`<${ActivityScreen} api=${api} role=${role} onAuthError=${onAuthError} />`
    case 'settings':
      return html`<${SettingsScreen}
        api=${api}
        me=${me}
        role=${role}
        onAccountChange=${onAccountChange}
        onSignOut=${onSignOut}
        onAuthError=${onAuthError}
      />`
    default:
      return html`<${Banner} kind="warn">
        There is no such page. <a href=${href('overview')}>Back to the overview</a>.
      <//>`
  }
}

// PAGE_TITLES are the screens the shell can name by itself. The room, the
// computer and the wizard are not here: the first two are named after a thing
// the server knows, and the wizard opens with a sentence rather than a label,
// so those screens write their own header.
const PAGE_TITLES = {
  overview: 'Overview',
  rooms: 'Rooms',
  computers: 'Computers',
  install: 'Install the agent',
  activity: 'Activity',
  settings: 'Settings',
}

// The rail is the cabinet's spine. It carries the six screens and, under
// them, the rooms themselves with a live dot each — the question "is that
// room still protected" is answerable from every screen, without navigating.
function Shell({ api, me, role, route, onSignOut, onAuthError, children }) {
  const manager = role === 'owner' || role === 'admin'
  const current = (me && (me.accounts || []).find((a) => a.account_id === me.account_id)) || null
  const rooms = usePoll(() => api.rooms(), { interval: 30000, onAuthError })
  const roomList = (rooms.data && rooms.data.rooms) || []

  const links = [
    { name: 'overview', label: 'Overview' },
    { name: 'computers', label: 'Computers' },
  ]
  if (manager) {
    links.push({ name: 'install', label: 'Install agent' })
    links.push({ name: 'activity', label: 'Activity' })
  }
  links.push({ name: 'settings', label: 'Settings' })

  const title = PAGE_TITLES[route.name]

  return html`
    <div class="shell">
      <nav class="rail">
        <a class="brand" href=${href('overview')}>
          <${BrandMark} />
          <span>Guardian</span>
        </a>

        <div class="rail-group">
          ${links.map(
            (link) => html`
              <a
                key=${link.name}
                class="rail-link"
                aria-current=${route.name === link.name ? 'page' : null}
                href=${href(link.name)}
              >
                <span class="rail-link-name">${link.label}</span>
              </a>
            `,
          )}
        </div>

        <div class="rail-group">
          <a class="rail-heading" href=${href('rooms')}>Rooms</a>
          ${roomList.map(
            (room) => html`
              <a
                key=${room.id}
                class="rail-link"
                aria-current=${route.name === 'room' && route.params.roomID === room.id ? 'page' : null}
                href=${href('room', { roomID: room.id })}
              >
                <span
                  class=${'dot dot-' + (room.protection_enabled ? 'online' : 'offline')}
                  title=${room.protection_enabled ? 'Protection is on' : 'Protection is off'}
                ></span>
                <span class="rail-link-name">${room.name}</span>
              </a>
            `,
          )}
          ${manager &&
          roomList.length === 0 &&
          html`<a class="rail-link" href=${href('rooms')}><span class="rail-link-name">Add a room</span></a>`}
        </div>

        <div class="rail-foot">
          ${current &&
          html`<a class="rail-account" href=${href('settings')} title=${`You are ${role} here`}>
            ${current.name}
          </a>`}
          <button class="linkish rail-signout" onClick=${onSignOut}>Sign out</button>
        </div>
      </nav>

      <main class="main">
        ${title && html`<${PageHeader} title=${title} />`}
        ${children}
      </main>
    </div>
  `
}

const api = createApi()
const root = window.ReactDOM.createRoot(document.getElementById('app'))
root.render(React.createElement(App, { api }))
