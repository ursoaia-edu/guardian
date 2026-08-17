const { useState, useMemo } = window.React

import { html, Card, Button, Empty, Loading, ErrorBanner, StateBadge } from '../ui.js'
import { usePoll, useAction } from '../hooks.js'
import { computerName, computerState, relativeTime, lastSeenMillis } from '../format.js'
import { href, navigate } from '../router.js'

// filterComputers is separated from the rendering because it is the part with
// rules in it, and the part a test can hold to them.
export function filterComputers(computers, filters, now = new Date()) {
  const text = (filters.text || '').trim().toLowerCase()
  return computers.filter((computer) => {
    if (filters.room === 'none' && computer.room_id) return false
    if (filters.room && filters.room !== 'all' && filters.room !== 'none' && computer.room_id !== filters.room) {
      return false
    }
    if (filters.state && filters.state !== 'all' && computerState(computer, now) !== filters.state) {
      return false
    }
    if (filters.version && filters.version !== 'all' && (computer.agent_version || '') !== filters.version) {
      return false
    }
    if (text) {
      const haystack = [computer.display_name, computer.hostname, computer.machine_guid]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      if (!haystack.includes(text)) return false
    }
    return true
  })
}

// The columns, and how each one sorts. Keeping the sort key next to the column
// is what stops a header from claiming to sort by something it does not.
const COLUMNS = [
  { key: 'name', label: 'Computer', sort: (c) => computerName(c).toLowerCase() },
  { key: 'room', label: 'Room', sort: (c, ctx) => ctx.roomName(c.room_id).toLowerCase() },
  { key: 'state', label: 'State', sort: (c, ctx) => computerState(c, ctx.now) },
  { key: 'agent', label: 'Agent', sort: (c) => c.agent_version || '' },
  // Newest first is what "sort by last seen" means to a person watching a
  // fleet, so this one sorts on the raw age rather than the rendered words.
  { key: 'seen', label: 'Last seen', sort: (c) => -lastSeenMillis(c.last_seen_at) },
]

export function sortComputers(computers, key, direction, ctx) {
  const column = COLUMNS.find((c) => c.key === key)
  if (!column) return computers
  const sign = direction === 'desc' ? -1 : 1
  return [...computers].sort((a, b) => {
    const left = column.sort(a, ctx)
    const right = column.sort(b, ctx)
    if (left < right) return -sign
    if (left > right) return sign
    return computerName(a).localeCompare(computerName(b))
  })
}

// The computer pool is the one screen that is a table, because it is the one
// screen somebody scans: forty machines, looking for the row that is wrong.
// Columns align; a sentence of facts joined by middle dots does not.
export function ComputersScreen({ api, role, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const computers = usePoll(() => api.computers(), { onAuthError })
  const rooms = usePoll(() => api.rooms(), { interval: 30000, onAuthError })
  const act = useAction(() => computers.reload())

  const [filters, setFilters] = useState({ room: 'all', state: 'all', version: 'all', text: '' })
  const [sort, setSort] = useState({ key: 'name', direction: 'asc' })
  const [selected, setSelected] = useState(() => new Set())

  const all = (computers.data && computers.data.computers) || []
  const roomList = (rooms.data && rooms.data.rooms) || []
  const now = new Date()

  const roomName = (id) => {
    const room = roomList.find((r) => r.id === id)
    return room ? room.name : id ? 'Another room' : 'No room'
  }

  const shown = useMemo(() => {
    const matched = filterComputers(all, filters, now)
    return sortComputers(matched, sort.key, sort.direction, { now, roomName })
  }, [all, filters, sort, roomList])

  const versions = useMemo(
    () => Array.from(new Set(all.map((c) => c.agent_version).filter(Boolean))).sort(),
    [all],
  )

  function toggleSort(key) {
    setSort((current) =>
      current.key === key
        ? { key, direction: current.direction === 'asc' ? 'desc' : 'asc' }
        : { key, direction: 'asc' },
    )
  }

  function toggle(id) {
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const allShownSelected = shown.length > 0 && shown.every((c) => selected.has(c.id))

  async function bulk(patch) {
    const ids = Array.from(selected)
    await act.perform(async () => {
      // One request per machine: the API has no bulk endpoint, and inventing
      // one in the client by firing them all at once would make a partial
      // failure impossible to report honestly.
      for (const id of ids) {
        await api.patchComputer(id, patch)
      }
      setSelected(new Set())
      return true
    })
  }

  if (computers.loading && !computers.data) return html`<${Loading} what="Loading the computer pool" />`

  const counted =
    shown.length === all.length
      ? `${all.length} ${all.length === 1 ? 'computer' : 'computers'}`
      : `${shown.length} of ${all.length}`

  return html`
    <div class="stack">
      <${ErrorBanner} error=${computers.error || act.error} onDismiss=${act.clearError} />

      <${Card} title=${counted} flush>
        <div class="filters">
          <input
            class="input"
            type="search"
            placeholder="Search by name or hostname"
            value=${filters.text}
            onInput=${(e) => setFilters({ ...filters, text: e.target.value })}
          />
          <select class="input" value=${filters.room} onChange=${(e) => setFilters({ ...filters, room: e.target.value })}>
            <option value="all">Every room</option>
            <option value="none">No room</option>
            ${roomList.map((room) => html`<option key=${room.id} value=${room.id}>${room.name}</option>`)}
          </select>
          <select class="input" value=${filters.state} onChange=${(e) => setFilters({ ...filters, state: e.target.value })}>
            <option value="all">Any state</option>
            <option value="online">Online</option>
            <option value="offline">Offline</option>
            <option value="locked">Locked</option>
            <option value="unassigned">Unassigned</option>
          </select>
          ${versions.length > 1 &&
          html`<select
            class="input"
            value=${filters.version}
            onChange=${(e) => setFilters({ ...filters, version: e.target.value })}
          >
            <option value="all">Any agent version</option>
            ${versions.map((v) => html`<option key=${v} value=${v}>${v}</option>`)}
          </select>`}
        </div>

        ${selected.size > 0 &&
        html`<div class="bulk">
          <span class="bulk-count">${selected.size} selected</span>
          <select
            class="input"
            value=""
            onChange=${(e) => {
              const value = e.target.value
              if (!value) return
              bulk({ room_id: value === 'none' ? null : value })
              e.target.value = ''
            }}
          >
            <option value="">Move to…</option>
            ${roomList.map((room) => html`<option key=${room.id} value=${room.id}>${room.name}</option>`)}
            ${manager && html`<option value="none">No room</option>`}
          </select>
          <${Button} busy=${act.busy} onClick=${() => bulk({ blocked: true })}>Lock<//>
          <${Button} busy=${act.busy} onClick=${() => bulk({ blocked: false })}>Unlock<//>
          <${Button} kind="quiet" onClick=${() => setSelected(new Set())}>Clear<//>
        </div>`}

        ${all.length === 0
          ? html`<${Empty} title="No computers enrolled yet">
              <p>Install the agent on a machine and it appears here on its first sync.</p>
              <${Button} kind="primary" onClick=${() => navigate('install')}>Install the agent<//>
            <//>`
          : shown.length === 0
            ? html`<${Empty} title="Nothing matches those filters">
                <${Button}
                  onClick=${() => setFilters({ room: 'all', state: 'all', version: 'all', text: '' })}
                >
                  Clear the filters
                <//>
              <//>`
            : html`<div class="table-scroll">
                <table class="table">
                  <thead>
                    <tr>
                      <th class="cell-check">
                        <input
                          class="check"
                          type="checkbox"
                          checked=${allShownSelected}
                          onChange=${() =>
                            setSelected(allShownSelected ? new Set() : new Set(shown.map((c) => c.id)))}
                          aria-label="Select every computer shown"
                        />
                      </th>
                      ${COLUMNS.map(
                        (column) => html`
                          <th
                            key=${column.key}
                            class="sortable"
                            aria-sort=${sort.key === column.key
                              ? sort.direction === 'asc'
                                ? 'ascending'
                                : 'descending'
                              : null}
                            onClick=${() => toggleSort(column.key)}
                          >
                            ${column.label}
                          </th>
                        `,
                      )}
                    </tr>
                  </thead>
                  <tbody>
                    ${shown.map((computer) => {
                      const state = computerState(computer, now)
                      return html`
                        <tr
                          key=${computer.id}
                          class=${[
                            selected.has(computer.id) && 'is-selected',
                            state === 'offline' && 'row-quiet',
                          ]
                            .filter(Boolean)
                            .join(' ')}
                        >
                          <td class="cell-check">
                            <input
                              class="check"
                              type="checkbox"
                              checked=${selected.has(computer.id)}
                              onChange=${() => toggle(computer.id)}
                              aria-label=${`Select ${computerName(computer)}`}
                            />
                          </td>
                          <td>
                            <a class="cell-name" href=${href('computer', { computerID: computer.id })}>
                              ${computerName(computer)}
                            </a>
                            ${computer.display_name && computer.hostname
                              ? html`<span class="cell-alias">${computer.hostname}</span>`
                              : null}
                          </td>
                          <td class="cell-dim">${roomName(computer.room_id)}</td>
                          <td><${StateBadge} state=${state} /></td>
                          <td class="mono">${computer.agent_version || '—'}</td>
                          <td class="mono">${relativeTime(computer.last_seen_at, now)}</td>
                        </tr>
                      `
                    })}
                  </tbody>
                </table>
              </div>`}
      <//>
    </div>
  `
}
