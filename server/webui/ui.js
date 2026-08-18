// The small set of components every screen is built from.
//
// htm binds JSX-like template literals to React.createElement, which is what
// lets this cabinet be React without a build step: the browser parses the
// template, no transform runs, and the file on disk is the file that ships.
const React = window.React
const { useState, useEffect, useRef } = React
export const html = window.htm.bind(React.createElement)

import { useFreshness } from './hooks.js'

// The mark is inline rather than an <img> so it inherits the page's own
// rendering and costs no second request. It is the same shape as
// docs/assets/guardian-mark.png — the cabinet and the website wear one badge.
export function BrandMark() {
  return html`
    <svg class="brand-mark" viewBox="0 0 32 32" aria-hidden="true">
      <path
        d="M16 2.5 28.5 7v9.1c0 7.2-5.1 11.9-12.5 13.9C8.6 28 3.5 23.3 3.5 16.1V7L16 2.5z"
        fill="#4ADE80"
      />
      <path d="M16 4.6v24M4.8 15.2h22.4" stroke="#07090A" stroke-width="2.4" />
    </svg>
  `
}

export function Card({ title, actions, children, muted, flush, className }) {
  return html`
    <section class=${['card', muted && 'card-muted', className].filter(Boolean).join(' ')}>
      ${title &&
      html`<header class="card-head">
        ${typeof title === 'string' ? html`<h2>${title}</h2>` : title}
        <div class="card-actions">${actions}</div>
      </header>`}
      <div class=${'card-body' + (flush ? ' card-body-flush' : '')}>${children}</div>
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

// mono marks an input whose content is a machine's own spelling — an
// executable's filename, a hostname — so what is typed looks like what the
// machine will report back.
export function TextInput({
  value,
  onChange,
  placeholder,
  type = 'text',
  autoComplete,
  required,
  name,
  mono,
}) {
  return html`
    <input
      class=${'input' + (mono ? ' input-mono' : '')}
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

const STATE_LABELS = {
  online: 'Online',
  offline: 'Offline',
  locked: 'Locked',
  unassigned: 'No room',
  unknown: 'Unknown',
}

// StateBadge pairs a coloured dot with the word, because colour alone is not
// a label to anyone who cannot tell green from amber.
export function StateBadge({ state }) {
  return html`
    <span class=${`state state-${state}`}>
      <span class=${`dot dot-${state}`}></span>
      ${STATE_LABELS[state] || state}
    </span>
  `
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

// Tally is the one line of numbers a screen opens with. It replaced a row of
// boxes: the numbers are small facts about one fleet, not four separate
// metrics, and reading them as a sentence takes one glance instead of four.
export function Tally({ items }) {
  return html`
    <div class="tally">
      ${items.map(
        (item) => html`
          <span class=${'tally-item' + (item.tone ? ` tally-${item.tone}` : '')} key=${item.label}>
            <span class="tally-value">${item.value}</span>
            <span>${item.label}</span>
          </span>
        `,
      )}
    </div>
  `
}

// PageHeader is the top line of every screen: what this is, one line of
// context, the actions that belong to the whole screen, and how fresh it is.
export function PageHeader({ title, sub, actions }) {
  const at = useFreshness()
  return html`
    <div class="topline">
      <div class="topline-text">
        <h1>${title}</h1>
        ${sub && html`<span class="topline-sub">${sub}</span>`}
      </div>
      <div class="topline-actions">
        ${actions}
        <${Live} at=${at} />
      </div>
    </div>
  `
}

// Live says how fresh the screen is. The dot beats once when a refresh
// actually landed — the only thing in the cabinet that moves on its own, and
// it moves because a fact arrived, not to decorate the header.
export function Live({ at }) {
  const [, tick] = useState(0)
  const [beat, setBeat] = useState(false)
  const previous = useRef(at)

  useEffect(() => {
    const timer = setInterval(() => tick((n) => n + 1), 1000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    if (at === previous.current) return
    previous.current = at
    setBeat(true)
    const timer = setTimeout(() => setBeat(false), 900)
    return () => clearTimeout(timer)
  }, [at])

  const seconds = Math.max(0, Math.round((Date.now() - at) / 1000))
  const stale = seconds > 60
  return html`
    <span class=${'live' + (stale ? ' live-stale' : '')} title="The cabinet refreshes itself">
      <span class=${'live-dot' + (beat ? ' live-beat' : '')}></span>
      ${stale ? 'no answer' : seconds < 2 ? 'live' : `${seconds}s ago`}
    </span>
  `
}
