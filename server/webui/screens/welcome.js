const { useState } = window.React

import { html, Card, Button, Field, TextInput, ErrorBanner, Loading } from '../ui.js'
import { usePoll, useAction } from '../hooks.js'
import { computerName } from '../format.js'
import { navigate } from '../router.js'
import { DownloadInstaller } from './install.js'

// The first-computer wizard. It is the first screen a new account sees rather
// than something in a settings menu, because getting one machine enrolled is
// the entire product: an account with no computers does nothing at all.
//
// The three steps are the three things that can be waited on — the download,
// the machine calling home, and the machine being put somewhere — and the
// screen advances itself as each happens rather than asking anyone to refresh.
export function WelcomeScreen({ api, onAuthError, onDone }) {
  const [downloaded, setDownloaded] = useState(false)
  const [roomName, setRoomName] = useState('Kids room')
  const [machineName, setMachineName] = useState('')

  const computers = usePoll(() => api.computers(), { interval: 5000, onAuthError })
  const rooms = usePoll(() => api.rooms(), { interval: 30000, onAuthError })

  const list = (computers.data && computers.data.computers) || []
  const machine = list[0]
  const roomList = (rooms.data && rooms.data.rooms) || []

  const finish = useAction(async () => {
    await Promise.all([computers.reload(), rooms.reload()])
    onDone()
    navigate('overview')
  })

  async function placeMachine() {
    await finish.perform(async () => {
      let room = roomList[0]
      if (!room) {
        const name = roomName.trim() || 'Home'
        room = await api.createRoom(name)
      }
      const patch = { room_id: room.id }
      const typed = machineName.trim()
      if (typed) patch.display_name = typed
      await api.patchComputer(machine.id, patch)
    })
  }

  const step = machine ? 3 : downloaded ? 2 : 1

  return html`
    <div class="welcome">
      <header class="welcome-head">
        <h1>Let's get the first computer protected</h1>
        <p>Three steps. The last two happen on the computer you want to manage.</p>
      </header>

      <ol class="wizard">
        <li class=${'wizard-step' + (step > 1 ? ' done' : step === 1 ? ' active' : '')}>
          <h2>1. Download the installer</h2>
          <p>
            The archive is personalised for this account. Copy it to the computer you want to
            manage — a USB stick is fine.
          </p>
          <${DownloadInstaller} api=${api} onDownloaded=${() => setDownloaded(true)} />
        </li>

        <li class=${'wizard-step' + (step > 2 ? ' done' : step === 2 ? ' active' : '')}>
          <h2>2. Extract it and run Guardian.exe as administrator</h2>
          <p>
            Extract the <em>whole</em> archive first — running the program from inside the ZIP
            leaves the agent files behind. Then press <em>Install</em> in the console.
          </p>
          ${step === 2 &&
          html`<div class="waiting">
            <${Loading} what="Waiting for the computer to call home" />
            <p class="hint">
              This page notices by itself, usually within ten seconds of the service starting.
            </p>
          </div>`}
        </li>

        <li class=${'wizard-step' + (step === 3 ? ' active' : '')}>
          <h2>3. Name it and put it in a room</h2>
          ${!machine
            ? html`<p class="hint">As soon as the agent enrols, the computer shows up here.</p>`
            : html`
                <p class="found">
                  Found <strong>${computerName(machine)}</strong> — ${machine.os_name || 'Windows'}
                  ${machine.arch ? ` · ${machine.arch}` : ''}
                </p>
                <${Field} label="Call this computer" hint="Optional. The hostname is used otherwise.">
                  <${TextInput}
                    value=${machineName}
                    onChange=${setMachineName}
                    placeholder=${machine.hostname || 'Living room PC'}
                  />
                <//>
                ${roomList.length === 0 &&
                html`<${Field} label="First room" hint="Rules are set per room, not per computer.">
                  <${TextInput} value=${roomName} onChange=${setRoomName} />
                <//>`}
                <${ErrorBanner} error=${finish.error} onDismiss=${finish.clearError} />
                <${Button} kind="primary" busy=${finish.busy} onClick=${placeMachine}>
                  ${roomList.length === 0 ? 'Create the room and finish' : 'Put it in ' + roomList[0].name}
                <//>
              `}
        </li>
      </ol>

      <${ErrorBanner} error=${computers.error} />

      <p class="welcome-skip">
        <button class="linkish" onClick=${() => { onDone(); navigate('overview') }}>
          Skip for now
        </button>
      </p>
    </div>
  `
}
