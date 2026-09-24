const { useState } = window.React

import {
  html,
  Card,
  Button,
  Field,
  TextInput,
  Empty,
  ErrorBanner,
  Banner,
  Confirm,
} from '../ui.js'
import { usePoll, useAction } from '../hooks.js'
import { formatDate } from '../format.js'
import { ChangePasswordCard } from './password.js'

export function SettingsScreen({ api, me, role, onAccountChange, onSignOut, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const accounts = (me && me.accounts) || []

  return html`
    <div class="stack">
      <${Card} title="You">
        <dl class="passport">
          <dt>Signed in as</dt><dd>${me ? me.user_id : '—'}</dd>
          <dt>Role in this account</dt><dd>${role || '—'}</dd>
        </dl>
        <${Button} onClick=${onSignOut}>Sign out<//>
      <//>

      <${ChangePasswordCard} api=${api} />

      ${accounts.length > 1 &&
      html`<${Card} title="Accounts you can act in">
        <ul class="rows">
          ${accounts.map(
            (account) => html`
              <li class=${'row' + (account.account_id === me.account_id ? ' row-current' : '')} key=${account.account_id}>
                <div class="row-main">
                  <span class="row-title">${account.name}</span>
                  <span class="row-sub">${account.role}</span>
                </div>
                <div class="row-actions">
                  ${account.account_id === me.account_id
                    ? html`<span class="badge badge-on">Current</span>`
                    : html`<${Button} onClick=${() => onAccountChange(account.account_id)}>Switch<//>`}
                </div>
              </li>
            `,
          )}
        </ul>
        <p class="hint">
          A guest room somebody shared with you is an account you can act in, with only that room
          visible inside it.
        </p>
      <//>`}

      ${manager && html`<${AccountMembers} api=${api} me=${me} onAuthError=${onAuthError} />`}

      <${Card} title="Billing" muted=${true}>
        <p class="hint">
          Plans, the computer limit and invoices are the next piece of work. Until then every
          account runs on the default limit the server was installed with.
        </p>
      <//>
    </div>
  `
}

function AccountMembers({ api, me, onAuthError }) {
  const members = usePoll(() => api.accountMembers(), { interval: 30000, onAuthError })
  const act = useAction(() => members.reload())
  const [email, setEmail] = useState('')
  const [removing, setRemoving] = useState(null)

  const list = (members.data && members.data.members) || []

  async function add(event) {
    event.preventDefault()
    const typed = email.trim()
    if (!typed) return
    const done = await act.perform(() => api.addAccountMember(typed))
    if (done !== undefined) setEmail('')
  }

  return html`
    <${Card} title="Administrators">
      <${ErrorBanner} error=${members.error || act.error} onDismiss=${act.clearError} />
      ${list.length === 0
        ? html`<${Empty} title="Nobody yet" />`
        : html`<ul class="rows">
            ${list.map(
              (member) => html`
                <li class="row" key=${member.id}>
                  <div class="row-main">
                    <span class="row-title">${member.name || member.email}</span>
                    <span class="row-sub">
                      ${member.email} · ${member.role}${member.added_at ? ` · since ${formatDate(member.added_at)}` : ''}
                    </span>
                  </div>
                  <div class="row-actions">
                    ${member.role === 'owner'
                      ? html`<span class="badge badge-unassigned">Owner</span>`
                      : html`<${Button} kind="danger" onClick=${() => setRemoving(member)}>Revoke<//>`}
                  </div>
                </li>
              `,
            )}
          </ul>`}

      <${Banner} kind="info">
        An administrator can do everything the owner can except be billed: every room, the whole
        computer pool, installers and sharing. Ownership itself cannot be transferred yet.
      <//>

      <form class="inline-form" onSubmit=${add}>
        <${Field} label="Add an administrator" hint="They must already have a Guardian account.">
          <${TextInput} type="email" value=${email} onChange=${setEmail} placeholder="partner@example.com" />
        <//>
        <${Button} type="submit" kind="primary" busy=${act.busy}>Add<//>
      </form>

      <${Confirm}
        open=${!!removing}
        title=${removing ? `Revoke ${removing.email}?` : ''}
        body=${html`<p>They keep their own account and anything shared with them individually.</p>`}
        confirmLabel="Revoke"
        busy=${act.busy}
        onCancel=${() => setRemoving(null)}
        onConfirm=${async () => {
          const member = removing
          const done = await act.perform(() => api.removeAccountMember(member.id))
          if (done !== undefined) setRemoving(null)
        }}
      />
    <//>
  `
}
