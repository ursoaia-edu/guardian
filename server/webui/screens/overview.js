const { useMemo } = window.React

import { html, Card, Empty, Loading, ErrorBanner, Tally, Button } from '../ui.js'
import { usePoll } from '../hooks.js'
import { isOnline, computerName, computerState, roomPolicy, roomMode, relativeTime, eventLabel, eventDetail } from '../format.js'
import { href, navigate } from '../router.js'

// The overview answers the question somebody opens the cabinet to ask: is
// everything where I left it?
//
// It answers it the way the customer thinks about their fleet — as rooms full
// of machines. Each room is a panel, each machine a cell in it, and the cell's
// left edge carries its state. Six machines or sixty, the shape is the same
// and the eye goes straight to the edge that is the wrong colour.
export function OverviewScreen({ api, role, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const rooms = usePoll(() => api.rooms(), { onAuthError })
  const computers = usePoll(() => api.computers(), { onAuthError })
  // The activity feed is manager-only; asking for it as a guest would be a
  // 403 on a screen where nothing is wrong.
  const events = usePoll(() => (manager ? api.events({ limit: 7 }) : Promise.resolve({ events: [] })), {
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

  const tally = [
    { label: computerList.length === 1 ? 'computer' : 'computers', value: computerList.length },
    { label: 'online', value: online, tone: online > 0 ? 'good' : null },
    { label: roomList.length === 1 ? 'room' : 'rooms', value: roomList.length },
  ]
  if (locked > 0) tally.push({ label: 'locked', value: locked, tone: 'warn' })

  return html`
    <div class="stack">
      <${ErrorBanner} error=${rooms.error || computers.error} />
      <${Tally} items=${tally} />

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
        : roomList.map(
            (room) => html`<${RoomRack}
              key=${room.id}
              room=${room}
              machines=${byRoom.get(room.id) || []}
              now=${now}
            />`,
          )}

      ${unassigned.length > 0 &&
      html`<${Card}
        className="room-panel"
        title=${html`<span class="room-panel-title">
          <h2>No room</h2>
          <span class="room-panel-meta">nothing is enforced here</span>
        </span>`}
        actions=${html`<${Button} onClick=${() => navigate('computers')}>Assign to a room<//>`}
      >
        <${Rack} machines=${unassigned} now=${now} />
      <//>`}

      ${manager &&
      html`<${Card}
        title="Recent activity"
        flush
        actions=${html`<${Button} kind="quiet" onClick=${() => navigate('activity')}>See all<//>`}
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

// One room, with its machines. The head carries the three facts that change:
// what the policy is, whether it is being enforced, and how much of the room
// is switched on right now.
function RoomRack({ room, machines, now }) {
  const up = machines.filter((c) => isOnline(c.last_seen_at, now)).length
  const off = !room.protection_enabled

  return html`
    <${Card}
      className="room-panel"
      title=${html`
        <span class="room-panel-title">
          <h2><a href=${href('room', { roomID: room.id })}>${room.name}</a></h2>
          <span class="room-panel-meta">
            <span class=${off ? 'badge badge-warn' : 'badge badge-on'}>
              ${off ? 'Not enforced' : roomMode(room)}
            </span>
            ${!room.power_allowed && html`<span class="badge">Shutdown blocked</span>`}
          </span>
        </span>
      `}
      actions=${html`<span class="room-panel-count">
        ${machines.length > 0 ? `${up}/${machines.length} online` : 'empty'}
      </span>`}
    >
      ${machines.length === 0
        ? html`<${Empty} title="No computers in this room">
            <p>${roomPolicy(room)} — but there is nothing here to enforce it on yet.</p>
            <${Button} onClick=${() => navigate('computers')}>Move a computer here<//>
          <//>`
        : html`<${Rack} machines=${machines} now=${now} />`}
    <//>
  `
}

// The rack itself: one cell per machine, sorted so the ones that need looking
// at come first. A locked machine outranks an offline one, and an offline one
// outranks a machine that is quietly doing its job.
function Rack({ machines, now }) {
  const order = { locked: 0, offline: 1, unassigned: 2, unknown: 3, online: 4 }
  const sorted = [...machines].sort((a, b) => {
    const rank = order[computerState(a, now)] - order[computerState(b, now)]
    return rank !== 0 ? rank : computerName(a).localeCompare(computerName(b))
  })

  return html`
    <div class="rack">
      ${sorted.map((computer) => {
        const state = computerState(computer, now)
        return html`
          <a
            class=${`node node-${state}`}
            key=${computer.id}
            href=${href('computer', { computerID: computer.id })}
            title=${`${computerName(computer)} — ${state}`}
          >
            <span class="node-name">${computerName(computer)}</span>
            <span class="node-when">
              ${state === 'locked' ? 'locked' : relativeTime(computer.last_seen_at, now)}
            </span>
          </a>
        `
      })}
    </div>
  `
}
