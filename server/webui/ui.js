// The small set of components every screen is built from.
//
// htm binds JSX-like template literals to React.createElement, which is what
// lets this cabinet be React without a build step: the browser parses the
// template, no transform runs, and the file on disk is the file that ships.
const React = window.React
export const html = window.htm.bind(React.createElement)

export function Card({ title, actions, children, muted }) {
  return html`
    <section class=${'card' + (muted ? ' card-muted' : '')}>
      ${title &&
      html`<header class="card-head">
        <h2>${title}</h2>
        <div class="card-actions">${actions}</div>
      </header>`}
      <div class="card-body">${children}</div>
    </section>
  `
}

export function Button({ children, onClick, kind = 'default', busy, disabled, type = 'button', title }) {
  return html`
    <button
      type=${type}
      class=${`btn btn-${kind}`}
      title=${title}
      disabled=${busy || disabled}
      onClick=${onClick}
    >
      ${busy ? html`<span class="spinner" aria-hidden="true"></span>` : null}
      ${children}
    </button>
  `
}

export function Field({ label, hint, children }) {
  return html`
    <label class="field">
      <span class="field-label">${label}</span>
      ${children}
      ${hint && html`<span class="field-hint">${hint}</span>`}
    </label>
  `
}

export function TextInput({ value, onChange, placeholder, type = 'text', autoComplete, required, name }) {
  return html`
    <input
      class="input"
      type=${type}
      name=${name}
      value=${value}
      placeholder=${placeholder}
      autoComplete=${autoComplete}
      required=${required}
      onInput=${(e) => onChange(e.target.value)}
    />
  `
}

export function Toggle({ checked, onChange, label, disabled }) {
  return html`
    <label class=${'toggle' + (disabled ? ' toggle-disabled' : '')}>
      <input
        type="checkbox"
        checked=${!!checked}
        disabled=${disabled}
        onChange=${(e) => onChange(e.target.checked)}
      />
      <span class="toggle-track"><span class="toggle-thumb"></span></span>
      <span class="toggle-label">${label}</span>
    </label>
  `
}

// StateBadge shows the one word computerState settled on, with the colour
// carried by a class rather than by the text, so it stays readable to anyone
// who cannot tell the colours apart.
export function StateBadge({ state }) {
  const labels = {
    online: 'Online',
    offline: 'Offline',
    locked: 'Locked',
    unassigned: 'No room',
    unknown: 'Unknown',
  }
  return html`<span class=${`badge badge-${state}`}>${labels[state] || state}</span>`
}

// Banner is how every failure reaches a person. The message is the server's
// own words wherever there are any — the API answers {"error": "..."} for
// exactly this reason, and paraphrasing it here would lose the detail.
export function Banner({ kind = 'error', children, onDismiss }) {
  return html`
    <div class=${`banner banner-${kind}`} role=${kind === 'error' ? 'alert' : 'status'}>
      <div class="banner-text">${children}</div>
      ${onDismiss && html`<button class="banner-close" onClick=${onDismiss} aria-label="Dismiss">×</button>`}
    </div>
  `
}

export function ErrorBanner({ error, onDismiss }) {
  if (!error) return null
  const offline = error.isOffline
  return html`<${Banner} kind=${offline ? 'warn' : 'error'} onDismiss=${onDismiss}>
    ${offline
      ? 'Cannot reach the server. The cabinet will keep trying.'
      : error.message || String(error)}
  <//>`
}

export function Empty({ title, children }) {
  return html`
    <div class="empty">
      <p class="empty-title">${title}</p>
      ${children && html`<div class="empty-body">${children}</div>`}
    </div>
  `
}

export function Loading({ what = 'Loading' }) {
  return html`<div class="loading"><span class="spinner"></span> ${what}…</div>`
}

// Confirm is a deliberate speed bump in front of the destructive actions:
// deleting a room, unenrolling a machine, revoking every installer token.
export function Confirm({ open, title, body, confirmLabel = 'Confirm', onConfirm, onCancel, busy }) {
  if (!open) return null
  return html`
    <div class="modal-backdrop" onClick=${onCancel}>
      <div class="modal" onClick=${(e) => e.stopPropagation()} role="dialog" aria-modal="true">
        <h3>${title}</h3>
        <div class="modal-body">${body}</div>
        <div class="modal-actions">
          <${Button} onClick=${onCancel}>Cancel<//>
          <${Button} kind="danger" busy=${busy} onClick=${onConfirm}>${confirmLabel}<//>
        </div>
      </div>
    </div>
  `
}

export function Stat({ label, value, tone }) {
  return html`
    <div class=${'stat' + (tone ? ` stat-${tone}` : '')}>
      <span class="stat-value">${value}</span>
      <span class="stat-label">${label}</span>
    </div>
  `
}
