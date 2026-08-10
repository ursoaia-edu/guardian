const { useState, useMemo } = window.React

import { html, Card, Button, Empty, Loading, ErrorBanner, StateBadge } from '../ui.js'
import { usePoll, useAction } from '../hooks.js'
import { computerName, computerState, relativeTime } from '../format.js'
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

export function ComputersScreen({ api, role, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const computers = usePoll(() => api.computers(), { onAuthError })
  const rooms = usePoll(() => api.rooms(), { interval: 30000, onAuthError })
  const act = useAction(() => computers.reload())

  const [filters, setFilters] = useState({ room: 'all', state: 'all', version: 'all', text: '' })
  const [selected, setSelected] = useState(() => new Set())

  const all = (computers.data && computers.data.computers) || []
  const roomList = (rooms.data && rooms.data.rooms) || []
  const now = new Date()
  const shown = useMemo(() => filterComputers(all, filters, now), [all, filters])
  const versions = useMemo(
    () => Array.from(new Set(all.map((c) => c.agent_version).filter(Boolean))).sort(),
    [all],
  )

  const roomName = (id) => {
    const room = roomList.find((r) => r.id === id)
    return room ? room.name : id ? 'Another room' : 'No room'
  }

  function toggle(id) {
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

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

  return html`
    <div class="stack">
      <${ErrorBanner} error=${computers.error || act.error} onDismiss=${act.clearError} />

      <${Card} title=${`Computers (${shown.length}${shown.length !== all.length ? ` of ${all.length}` : ''})`}>
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
          <span>${selected.size} selected</span>
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
          <${Button} onClick=${() => setSelected(new Set())}>Clear<//>
        </div>`}

        ${all.length === 0
          ? html`<${Empty} title="No computers enrolled yet">
              <p>Install the agent on a machine and it appears here on its first sync.</p>
              <${Button} kind="primary" onClick=${() => navigate('install')}>Install the agent<//>
            <//>`
          : shown.length === 0
            ? html`<${Empty} title="Nothing matches those filters" />`
            : html`<ul class="rows">
                ${shown.map(
                  (computer) => html`
                    <li class="row" key=${computer.id}>
                      <input
                        type="checkbox"
                        class="row-check"
                        checked=${selected.has(computer.id)}
                        onChange=${() => toggle(computer.id)}
                        aria-label=${`Select ${computerName(computer)}`}
                      />
                      <a class="row-main" href=${href('computer', { computerID: computer.id })}>
                        <span class="row-title">${computerName(computer)}</span>
                        <span class="row-sub">
                          ${roomName(computer.room_id)} · ${computer.os_name || 'Windows'}
                          ${computer.agent_version ? ` · agent ${computer.agent_version}` : ''} ·
                          last seen ${relativeTime(computer.last_seen_at, now)}
                        </span>
                      </a>
                      <div class="row-actions">
                        <${StateBadge} state=${computerState(computer, now)} />
                      </div>
                    </li>
                  `,
                )}
              </ul>`}
      <//>
    </div>
  `
}
