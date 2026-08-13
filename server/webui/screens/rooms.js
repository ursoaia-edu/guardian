const { useState } = window.React

import { html, Card, Button, Field, TextInput, Empty, Loading, ErrorBanner, Confirm } from '../ui.js'
import { usePoll, useAction } from '../hooks.js'
import { roomPolicy } from '../format.js'
import { href, navigate } from '../router.js'

export function RoomsScreen({ api, role, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const rooms = usePoll(() => api.rooms(), { onAuthError })
  const [newName, setNewName] = useState('')
  const [deleting, setDeleting] = useState(null)

  const create = useAction(() => rooms.reload())
  const remove = useAction(() => rooms.reload())

  const list = (rooms.data && rooms.data.rooms) || []

  async function submit(event) {
    event.preventDefault()
    const name = newName.trim()
    if (!name) return
    const created = await create.perform(() => api.createRoom(name))
    if (created) setNewName('')
  }

  if (rooms.loading && !rooms.data) return html`<${Loading} what="Loading rooms" />`

  return html`
    <div class="stack">
      <${ErrorBanner} error=${rooms.error} />

      <${Card} title="Rooms">
        ${list.length === 0
          ? html`<${Empty} title="No rooms yet">
              <p>Rules are set per room; computers are put into rooms.</p>
            <//>`
          : html`<ul class="rows">
              ${list.map(
                (room) => html`
                  <li class="row" key=${room.id}>
                    <a class="row-main" href=${href('room', { roomID: room.id })}>
                      <span class="row-title">${room.name}</span>
                      <span class="row-sub">${roomPolicy(room)}</span>
                    </a>
                    <div class="row-actions">
                      <${Button} onClick=${() => navigate('room', { roomID: room.id })}>Open<//>
                      ${manager &&
                      html`<${Button} kind="danger" onClick=${() => setDeleting(room)}>Delete<//>`}
                    </div>
                  </li>
                `,
              )}
            </ul>`}
      <//>

      ${manager &&
      html`<${Card} title="New room">
        <form class="inline-form" onSubmit=${submit}>
          <${Field} label="Name">
            <${TextInput} value=${newName} onChange=${setNewName} placeholder="Kids room" />
          <//>
          <${Button} type="submit" kind="primary" busy=${create.busy}>Create<//>
        </form>
        <${ErrorBanner} error=${create.error} onDismiss=${create.clearError} />
      <//>`}

      <${Confirm}
        open=${!!deleting}
        title=${deleting ? `Delete ${deleting.name}?` : ''}
        body=${html`<p>
          The computers in it are not deleted — they go back to the pool with no room, and stop
          enforcing anything until they are placed somewhere else.
        </p>`}
        confirmLabel="Delete the room"
        busy=${remove.busy}
        onCancel=${() => setDeleting(null)}
        onConfirm=${async () => {
          const room = deleting
          const done = await remove.perform(() => api.deleteRoom(room.id))
          if (done !== undefined) setDeleting(null)
        }}
      />
      <${ErrorBanner} error=${remove.error} onDismiss=${remove.clearError} />
    </div>
  `
}
