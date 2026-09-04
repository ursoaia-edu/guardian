const { useState, useCallback } = window.React

import {
  html,
  Card,
  Button,
  Field,
  TextInput,
  Loading,
  ErrorBanner,
  Banner,
  StateBadge,
  Confirm,
  PageHeader,
} from '../ui.js'
import { usePoll, useAction } from '../hooks.js'
import { computerName, computerState, relativeTime, formatDateTime } from '../format.js'
import { navigate } from '../router.js'
import { BlockLog } from './blocklog.js'

// The same table as the room's Log tab, pre-filtered to this machine — which
// is also the only way to read the history of a machine in no room, since
// every one of its rows has room_id NULL.
function ComputerLog({ api, computer, onAuthError }) {
  const load = useCallback((params) => api.computerProcessEvents(computer.id, params), [api, computer.id])
  return html`<${BlockLog} load=${load} onAuthError=${onAuthError} title="What this computer has closed" />`
}

// The machine's passport: what it is, where it is, what it has been doing, and
// the three things that can be done to it — rename, move, lock — plus the one
// that cannot be undone.
export function ComputerScreen({ api, role, computerID, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const computer = usePoll(() => api.computer(computerID), { deps: [computerID], onAuthError })
  const rooms = usePoll(() => api.rooms(), { interval: 30000, onAuthError })
  const act = useAction(() => computer.reload())
  const [name, setName] = useState(null)
  const [unenrolling, setUnenrolling] = useState(false)

  const data = computer.data
  const roomList = (rooms.data && rooms.data.rooms) || []
  const now = new Date()

  if (computer.loading && !data) return html`<${Loading} what="Loading the computer" />`
  if (!data) {
    return html`<${Card} title="Computer not found">
      <p>It is not in the account you are acting in, or it is not in a room shared with you.</p>
      <${Button} onClick=${() => navigate('computers')}>Back to the pool<//>
    <//>`
  }

  const typedName = name === null ? data.display_name || '' : name
  const hardware = data.hardware && typeof data.hardware === 'object' ? data.hardware : {}
  const runtime = data.runtime && typeof data.runtime === 'object' ? data.runtime : {}

  return html`
    <div class="stack">
      <${PageHeader}
        title=${computerName(data)}
        sub=${html`<span class="mono">${data.hostname || data.machine_guid}</span>`}
        actions=${html`<${StateBadge} state=${computerState(data, now)} />`}
      />

      <${ErrorBanner} error=${computer.error || act.error} onDismiss=${act.clearError} />

      ${data.blocked &&
      html`<${Banner} kind="warn">
        This machine is locked: the agent is told to allow nothing at all, whatever room it is in.
      <//>`}

      <${Card} title="Placement">
        <${Field} label="Name" hint="What this computer is called here. The hostname is used when empty.">
          <${TextInput} value=${typedName} onChange=${setName} placeholder=${data.hostname || ''} />
        <//>
        <div class="row-actions">
          <${Button}
            kind="primary"
            busy=${act.busy}
            onClick=${async () => {
              const done = await act.perform(() => api.patchComputer(data.id, { display_name: typedName.trim() }))
              if (done) setName(null)
            }}
          >
            Save the name
          <//>
        </div>

        <${Field} label="Room" hint="A computer in no room enforces nothing.">
          <select
            class="input"
            value=${data.room_id || ''}
            onChange=${(e) => {
              const value = e.target.value
              act.perform(() => api.patchComputer(data.id, { room_id: value === '' ? null : value }))
            }}
            disabled=${act.busy}
          >
            ${manager && html`<option value="">No room</option>`}
            ${!manager && !data.room_id && html`<option value="">No room</option>`}
            ${roomList.map((room) => html`<option key=${room.id} value=${room.id}>${room.name}</option>`)}
          </select>
        <//>

        <div class="row-actions">
          <${Button} busy=${act.busy} onClick=${() => act.perform(() => api.patchComputer(data.id, { blocked: !data.blocked }))}>
            ${data.blocked ? 'Unlock this computer' : 'Lock this computer'}
          <//>
        </div>
      <//>

      <${Card} title="Passport">
        <dl class="passport">
          <dt>Operating system</dt><dd>${data.os_name || '—'} ${data.os_build ? `(build ${data.os_build})` : ''}</dd>
          <dt>Architecture</dt><dd>${data.arch || '—'}</dd>
          <dt>Agent version</dt><dd>${data.agent_version || '—'}</dd>
          <dt>Enrolled</dt><dd>${formatDateTime(data.enrolled_at)}</dd>
          <dt>Last seen</dt><dd>${relativeTime(data.last_seen_at, now)} · ${formatDateTime(data.last_seen_at)}</dd>
          ${Object.entries(hardware).map(
            ([key, value]) =>
              html`<dt key=${'h' + key}>${key.replace(/_/g, ' ')}</dt><dd key=${'hv' + key}>${String(value)}</dd>`,
          )}
          ${Object.entries(runtime).map(
            ([key, value]) =>
              html`<dt key=${'r' + key}>${key.replace(/_/g, ' ')}</dt><dd key=${'rv' + key}>${String(value)}</dd>`,
          )}
        </dl>
      <//>

      <${ComputerLog} api=${api} computer=${data} onAuthError=${onAuthError} />

      ${manager &&
      html`<${Card} title="Unenrol">
        <p>
          Unenrolling revokes this one machine's credential. Its next sync is refused — and the
          agent keeps enforcing the last policy it had, so the computer does not come unprotected
          by being removed here. Bringing it back means running the installer again.
        </p>
        <${Button} kind="danger" onClick=${() => setUnenrolling(true)}>Unenrol this computer<//>
      <//>`}

      <${Confirm}
        open=${unenrolling}
        title=${`Unenrol ${computerName(data)}?`}
        body=${html`<p>
          The machine's credential is revoked and it disappears from this account. It keeps
          enforcing its last known policy until somebody uninstalls the agent on the machine itself.
        </p>`}
        confirmLabel="Unenrol"
        busy=${act.busy}
        onCancel=${() => setUnenrolling(false)}
        onConfirm=${async () => {
          const done = await act.perform(async () => {
            await api.unenrolComputer(data.id)
            return true
          })
          if (done) {
            setUnenrolling(false)
            navigate('computers')
          }
        }}
      />
    </div>
  `
}
