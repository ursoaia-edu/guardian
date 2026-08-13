const { useState } = window.React

import {
  html,
  Card,
  Button,
  Field,
  TextInput,
  Toggle,
  Empty,
  Loading,
  ErrorBanner,
  Banner,
  StateBadge,
  Confirm,
} from '../ui.js'
import { usePoll, useAction } from '../hooks.js'
import { computerName, computerState, relativeTime, roomPolicy } from '../format.js'
import { href, navigate, ROOM_TABS } from '../router.js'

export function RoomScreen({ api, role, roomID, tab, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const room = usePoll(() => api.room(roomID), { deps: [roomID], onAuthError })
  const data = room.data

  if (room.loading && !data) return html`<${Loading} what="Loading the room" />`
  if (room.error && !data) {
    return html`<${Card} title="Room not found">
      <p>
        This room is not in the account you are acting in, or it has not been shared with you.
      </p>
      <${Button} onClick=${() => navigate('rooms')}>Back to rooms<//>
    <//>`
  }
  if (!data) return null

  return html`
    <div class="stack">
      <${RoomHeader} api=${api} room=${data} manager=${manager} onChanged=${room.reload} />

      <nav class="tabs">
        ${ROOM_TABS.map(
          (name) => html`
            <a
              key=${name}
              class=${'tab' + (name === tab ? ' tab-active' : '')}
              href=${href('room', { roomID, tab: name })}
            >
              ${name[0].toUpperCase() + name.slice(1)}
            </a>
          `,
        )}
      </nav>

      ${tab === 'computers' && html`<${RoomComputers} api=${api} room=${data} onAuthError=${onAuthError} />`}
      ${tab === 'rules' && html`<${RoomRules} api=${api} room=${data} onAuthError=${onAuthError} />`}
      ${tab === 'power' && html`<${RoomPower} api=${api} room=${data} onChanged=${room.reload} />`}
      ${tab === 'members' &&
      html`<${RoomMembers} api=${api} room=${data} manager=${manager} onAuthError=${onAuthError} />`}
    </div>
  `
}

function RoomHeader({ api, room, manager, onChanged }) {
  const [renaming, setRenaming] = useState(false)
  const [name, setName] = useState(room.name)
  const save = useAction(onChanged)

  return html`
    <header class="room-head">
      <div class="room-title">
        ${renaming
          ? html`
              <form
                class="inline-form"
                onSubmit=${async (e) => {
                  e.preventDefault()
                  const done = await save.perform(() => api.patchRoom(room.id, { name: name.trim() }))
                  if (done) setRenaming(false)
                }}
              >
                <${TextInput} value=${name} onChange=${setName} />
                <${Button} type="submit" kind="primary" busy=${save.busy}>Save<//>
                <${Button} onClick=${() => { setRenaming(false); setName(room.name) }}>Cancel<//>
              </form>
            `
          : html`
              <h1>${room.name}</h1>
              ${manager && html`<button class="linkish" onClick=${() => setRenaming(true)}>Rename</button>`}
            `}
      </div>
      <p class="room-policy">${roomPolicy(room)}</p>
      <${ErrorBanner} error=${save.error} onDismiss=${save.clearError} />
    </header>
  `
}

function RoomComputers({ api, room, onAuthError }) {
  const computers = usePoll(() => api.computers(), { onAuthError })
  const act = useAction(() => computers.reload())
  const now = new Date()
  const list = ((computers.data && computers.data.computers) || []).filter((c) => c.room_id === room.id)

  return html`
    <${Card} title="Computers in this room">
      <${ErrorBanner} error=${computers.error || act.error} onDismiss=${act.clearError} />
      ${list.length === 0
        ? html`<${Empty} title="No computers here yet">
            <p>Move one in from the pool, or install the agent on a new machine.</p>
            <${Button} onClick=${() => navigate('computers')}>Open the computer pool<//>
          <//>`
        : html`<ul class="rows">
            ${list.map(
              (computer) => html`
                <li class="row" key=${computer.id}>
                  <a class="row-main" href=${href('computer', { computerID: computer.id })}>
                    <span class="row-title">${computerName(computer)}</span>
                    <span class="row-sub">
                      ${computer.os_name || 'Windows'} · last seen ${relativeTime(computer.last_seen_at, now)}
                    </span>
                  </a>
                  <div class="row-actions">
                    <${StateBadge} state=${computerState(computer, now)} />
                    <${Button}
                      busy=${act.busy}
                      onClick=${() => act.perform(() => api.patchComputer(computer.id, { blocked: !computer.blocked }))}
                    >
                      ${computer.blocked ? 'Unlock' : 'Lock'}
                    <//>
                  </div>
                </li>
              `,
            )}
          </ul>`}
    <//>
  `
}

function RoomRules({ api, room, onAuthError }) {
  const apps = usePoll(() => api.applications(room.id), { deps: [room.id], onAuthError })
  const act = useAction(() => apps.reload())
  const [name, setName] = useState('')
  const [list, setList] = useState(room.mode === 'whitelist' ? 'whitelist' : 'blacklist')

  const rows = (apps.data && apps.data.applications) || []
  const enforced = rows.filter((a) => a.list === room.mode)
  const idle = rows.filter((a) => a.list !== room.mode)

  async function add(event) {
    event.preventDefault()
    const typed = name.trim()
    if (!typed) return
    const created = await act.perform(() => api.addApplication(room.id, typed, list))
    if (created) setName('')
  }

  return html`
    <div class="stack">
      <${Card} title="Mode">
        <div class="mode-switch">
          ${['blacklist', 'whitelist'].map(
            (mode) => html`
              <button
                key=${mode}
                class=${'mode' + (room.mode === mode ? ' mode-active' : '')}
                onClick=${() => act.perform(() => api.patchRoom(room.id, { mode }))}
                disabled=${act.busy}
              >
                <strong>${mode === 'blacklist' ? 'Blacklist' : 'Whitelist'}</strong>
                <span>
                  ${mode === 'blacklist'
                    ? 'Everything runs except what is listed.'
                    : 'Nothing runs except what is listed.'}
                </span>
              </button>
            `,
          )}
        </div>
        <${Toggle}
          checked=${room.protection_enabled}
          disabled=${act.busy}
          onChange=${(v) => act.perform(() => api.patchRoom(room.id, { protection_enabled: v }))}
          label="Protection is ${room.protection_enabled ? 'on' : 'off'}"
        />
        ${!room.protection_enabled &&
        html`<${Banner} kind="warn">
          With protection off the agent enforces nothing at all, whatever is listed below.
        <//>`}
      <//>

      <${Card} title=${`Enforced now — the ${room.mode}`}>
        <${ErrorBanner} error=${apps.error || act.error} onDismiss=${act.clearError} />
        ${enforced.length === 0
          ? html`<${Empty}
              title=${room.mode === 'whitelist'
                ? 'Nothing is allowed yet'
                : 'Nothing is blocked yet'}
            >
              <p>
                ${room.mode === 'whitelist'
                  ? 'In whitelist mode an empty list means every application is closed except the ones the agent always protects.'
                  : 'Add the name of a program, exactly as it appears in Task Manager — steam.exe, for instance.'}
              </p>
            <//>`
          : html`<ul class="rows">
              ${enforced.map((app) => html`<${RuleRow} key=${app.id} app=${app} api=${api} room=${room} act=${act} />`)}
            </ul>`}

        <form class="inline-form" onSubmit=${add}>
          <${Field} label="Add a program" hint="The executable's name, as Task Manager shows it.">
            <${TextInput} value=${name} onChange=${setName} placeholder="steam.exe" />
          <//>
          <${Field} label="List">
            <select class="input" value=${list} onChange=${(e) => setList(e.target.value)}>
              <option value="blacklist">Blacklist</option>
              <option value="whitelist">Whitelist</option>
            </select>
          <//>
          <${Button} type="submit" kind="primary" busy=${act.busy}>Add<//>
        </form>
      <//>

      ${idle.length > 0 &&
      html`<${Card} title=${`Kept for the other mode — the ${room.mode === 'blacklist' ? 'whitelist' : 'blacklist'}`} muted=${true}>
        <p class="hint">
          These are not enforced while the room is in ${room.mode} mode. They are kept so switching
          modes does not mean typing everything again.
        </p>
        <ul class="rows">
          ${idle.map((app) => html`<${RuleRow} key=${app.id} app=${app} api=${api} room=${room} act=${act} />`)}
        </ul>
      <//>`}
    </div>
  `
}

function RuleRow({ app, api, room, act }) {
  return html`
    <li class=${'row' + (app.enabled ? '' : ' row-off')}>
      <div class="row-main">
        <span class="row-title">${app.name}</span>
        <span class="row-sub">${app.list}${app.enabled ? '' : ' · switched off'}</span>
      </div>
      <div class="row-actions">
        <${Toggle}
          checked=${app.enabled}
          disabled=${act.busy}
          label=${app.enabled ? 'On' : 'Off'}
          onChange=${(v) => act.perform(() => api.setApplicationEnabled(room.id, app.id, v))}
        />
        <${Button} kind="danger" busy=${act.busy} onClick=${() => act.perform(() => api.deleteApplication(room.id, app.id))}>
          Remove
        <//>
      </div>
    </li>
  `
}

function RoomPower({ api, room, onChanged }) {
  const act = useAction(onChanged)
  return html`
    <${Card} title="Power">
      <p>
        When shutdown is not permitted, the agent refuses the machine's own shutdown and restart
        commands — the usual way out of a locked computer.
      </p>
      <${Toggle}
        checked=${room.power_allowed}
        disabled=${act.busy}
        label=${room.power_allowed ? 'Shutdown is permitted' : 'Shutdown is blocked'}
        onChange=${(v) => act.perform(() => api.patchRoom(room.id, { power_allowed: v }))}
      />
      <${ErrorBanner} error=${act.error} onDismiss=${act.clearError} />
      ${!room.protection_enabled &&
      html`<${Banner} kind="warn">
        Protection is off for this room, so the power setting is not being applied either.
      <//>`}
    <//>
  `
}

function RoomMembers({ api, room, manager, onAuthError }) {
  const members = usePoll(() => api.roomMembers(room.id), { deps: [room.id], interval: 30000, onAuthError })
  const act = useAction(() => members.reload())
  const [email, setEmail] = useState('')
  const [removing, setRemoving] = useState(null)

  const list = (members.data && members.data.members) || []

  async function invite(event) {
    event.preventDefault()
    const typed = email.trim()
    if (!typed) return
    const done = await act.perform(() => api.addRoomMember(room.id, typed))
    if (done !== undefined) setEmail('')
  }

  return html`
    <div class="stack">
      <${Card} title="Who can see this room">
        <${ErrorBanner} error=${members.error || act.error} onDismiss=${act.clearError} />
        ${list.length === 0
          ? html`<${Empty} title="Only this account's owner and administrators" />`
          : html`<ul class="rows">
              ${list.map(
                (member) => html`
                  <li class="row" key=${member.id}>
                    <div class="row-main">
                      <span class="row-title">${member.name || member.email}</span>
                      <span class="row-sub">${member.email}</span>
                    </div>
                    ${manager &&
                    html`<div class="row-actions">
                      <${Button} kind="danger" onClick=${() => setRemoving(member)}>Revoke<//>
                    </div>`}
                  </li>
                `,
              )}
            </ul>`}
      <//>

      ${manager &&
      html`<${Card} title="Share this room">
        <p class="hint">
          Sharing grants one room, not the account: a guest sees this room and the computers in it,
          and nothing else. The person must already have a Guardian account — invitations by email
          are not built yet.
        </p>
        <form class="inline-form" onSubmit=${invite}>
          <${Field} label="Their email">
            <${TextInput} type="email" value=${email} onChange=${setEmail} placeholder="grandma@example.com" />
          <//>
          <${Button} type="submit" kind="primary" busy=${act.busy}>Share<//>
        </form>
      <//>`}

      <${Confirm}
        open=${!!removing}
        title=${removing ? `Revoke ${removing.email}?` : ''}
        body=${html`<p>They lose access to this room. Anything else shared with them is unaffected.</p>`}
        confirmLabel="Revoke"
        busy=${act.busy}
        onCancel=${() => setRemoving(null)}
        onConfirm=${async () => {
          const member = removing
          const done = await act.perform(() => api.removeRoomMember(room.id, member.id))
          if (done !== undefined) setRemoving(null)
        }}
      />
    </div>
  `
}
