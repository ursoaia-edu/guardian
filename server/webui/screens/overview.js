const { useMemo } = window.React

import { html, Card, Empty, Loading, ErrorBanner, Stat, Button } from '../ui.js'
import { usePoll } from '../hooks.js'
import { isOnline, roomPolicy, relativeTime, eventLabel, eventDetail } from '../format.js'
import { href, navigate } from '../router.js'

// The overview answers the question somebody opens the cabinet to ask: is
// everything where I left it? Rooms as tiles, each with the three facts that
// change — how many machines, how many are on, whether protection is running.
export function OverviewScreen({ api, role, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const rooms = usePoll(() => api.rooms(), { onAuthError })
  const computers = usePoll(() => api.computers(), { onAuthError })
  // The activity feed is manager-only; asking for it as a guest would be a
  // 403 on a screen where nothing is wrong.
  const events = usePoll(() => (manager ? api.events({ limit: 8 }) : Promise.resolve({ events: [] })), {
    deps: [manager],
    interval: 15000,
    onAuthError,
  })

  const roomList = (rooms.data && rooms.data.rooms) || []
  const computerList = (computers.data && computers.data.computers) || []
  const now = new Date()

  const byRoom = useMemo(() => {
    const map = new Map()
    for (const computer of computerList) {
      const key = computer.room_id || ''
      if (!map.has(key)) map.set(key, [])
      map.get(key).push(computer)
    }
    return map
  }, [computerList])

  const unassigned = byRoom.get('') || []
  const online = computerList.filter((c) => isOnline(c.last_seen_at, now)).length
  const locked = computerList.filter((c) => c.blocked).length

  if (rooms.loading && !rooms.data) return html`<${Loading} what="Loading your rooms" />`

  return html`
    <div class="stack">
      <${ErrorBanner} error=${rooms.error || computers.error} />

      <div class="stats">
        <${Stat} label="Computers" value=${computerList.length} />
        <${Stat} label="Online now" value=${online} tone=${online > 0 ? 'good' : null} />
        <${Stat} label="Rooms" value=${roomList.length} />
        ${locked > 0 && html`<${Stat} label="Locked" value=${locked} tone="warn" />`}
      </div>

      ${roomList.length === 0
        ? html`<${Card} title="No rooms yet">
            <${Empty} title="A room is where rules live">
              <p>
                Rules — what may run and what may not — are set per room, and computers are put
                into rooms. Most homes need one.
              </p>
              ${manager &&
              html`<${Button} kind="primary" onClick=${() => navigate('rooms')}>Create a room<//>`}
            <//>
          <//>`
        : html`<div class="tiles">
            ${roomList.map((room) => {
              const machines = byRoom.get(room.id) || []
              const up = machines.filter((c) => isOnline(c.last_seen_at, now)).length
              return html`
                <a class="tile" key=${room.id} href=${href('room', { roomID: room.id })}>
                  <div class="tile-head">
                    <h3>${room.name}</h3>
                    <span class=${'dot ' + (room.protection_enabled ? 'dot-on' : 'dot-off')}></span>
                  </div>
                  <p class="tile-policy">${roomPolicy(room)}</p>
                  <p class="tile-counts">
                    ${machines.length} ${machines.length === 1 ? 'computer' : 'computers'}
                    ${machines.length > 0 ? html`<span class="tile-online">· ${up} online</span>` : null}
                  </p>
                  ${!room.power_allowed &&
                  html`<p class="tile-flag">Shutdown blocked</p>`}
                </a>
              `
            })}
          </div>`}

      ${unassigned.length > 0 &&
      html`<${Card}
        title=${`${unassigned.length} ${unassigned.length === 1 ? 'computer is' : 'computers are'} in no room`}
        actions=${html`<${Button} onClick=${() => navigate('computers')}>Assign<//>`}
      >
        <p>
          A computer with no room enforces nothing at all — the agent reports in and waits. Put it
          in a room to start applying that room's rules.
        </p>
      <//>`}

      ${manager &&
      html`<${Card}
        title="Recent activity"
        actions=${html`<${Button} onClick=${() => navigate('activity')}>See all<//>`}
      >
        ${(events.data && events.data.events && events.data.events.length > 0)
          ? html`<ul class="feed">
              ${events.data.events.map(
                (event) => html`
                  <li key=${event.id}>
                    <span class="feed-what">${eventLabel(event.type)}</span>
                    <span class="feed-detail">${eventDetail(event.payload)}</span>
                    <span class="feed-when">${relativeTime(event.created_at, now)}</span>
                  </li>
                `,
              )}
            </ul>`
          : html`<${Empty} title="Nothing has happened yet" />`}
      <//>`}
    </div>
  `
}
