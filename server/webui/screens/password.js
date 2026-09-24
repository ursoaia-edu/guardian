const { useState } = window.React

import { html, Card, Button, Field, TextInput, Banner, ErrorBanner, BrandMark } from '../ui.js'
import { href, navigate } from '../router.js'

// "I forgot it". Signed out, so it draws its own page rather than living in
// the shell.
export function ForgotScreen({ api }) {
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState(null)

  async function submit(event) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api.forgotPassword(email.trim())
      setSent(true)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  return html`
    <div class="signin">
      <div class="signin-card">
        <div class="brand"><${BrandMark} /><span>Guardian</span></div>
        <h1>Reset your password</h1>
        ${sent
          ? html`<${Card} title="Check your email">
              <p>
                If that address has an account, a link is on its way. It works once and lasts an
                hour.
              </p>
              <p class="hint">
                Nothing arrived? Look in spam, then try again — the answer here is the same whether
                or not the address is registered, on purpose.
              </p>
              <a href=${href('overview')}>Back to sign in</a>
            <//>`
          : html`
              <p class="signin-lede">We will email you a link to set a new one.</p>
              <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
              <form onSubmit=${submit}>
                <${Field} label="Email">
                  <${TextInput}
                    type="email"
                    value=${email}
                    onChange=${setEmail}
                    autoComplete="email"
                    required=${true}
                  />
                <//>
                <${Button} type="submit" kind="primary" busy=${busy}>Email me a link<//>
              </form>
              <p class="signin-switch"><a href=${href('overview')}>Back to sign in</a></p>
            `}
      </div>
    </div>
  `
}

// The landing for a reset link.
export function ResetScreen({ api, token }) {
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState(null)

  async function submit(event) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api.resetPassword(token, password)
      setDone(true)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  return html`
    <div class="signin">
      <div class="signin-card">
        <div class="brand"><${BrandMark} /><span>Guardian</span></div>
        <h1>Set a new password</h1>
        ${done
          ? html`<${Card} title="Password changed">
              <${Banner} kind="info">
                Every device has been signed out. Sign in again with the new password.
              <//>
              <${Button} kind="primary" onClick=${() => navigate('overview')}>Sign in<//>
            <//>`
          : html`
              <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
              <form onSubmit=${submit}>
                <${Field} label="New password" hint="At least 8 characters.">
                  <${TextInput}
                    type="password"
                    value=${password}
                    onChange=${setPassword}
                    autoComplete="new-password"
                    required=${true}
                  />
                <//>
                <${Button} type="submit" kind="primary" busy=${busy}>Set the password<//>
              </form>
              <p class="signin-switch">
                Link expired? <a href=${href('forgot')}>Ask for a new one</a>
              </p>
            `}
      </div>
    </div>
  `
}

// The change form, for somebody already signed in. Lives in Settings.
export function ChangePasswordCard({ api }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState(null)

  async function submit(event) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    setDone(false)
    try {
      await api.changePassword(current, next)
      setCurrent('')
      setNext('')
      setDone(true)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  return html`
    <${Card} title="Password">
      <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
      ${done && html`<${Banner} kind="info">Changed. Every other device has been signed out.<//>`}
      <form class="form-column" onSubmit=${submit}>
        <${Field} label="Current password">
          <${TextInput} type="password" value=${current} onChange=${setCurrent} autoComplete="current-password" />
        <//>
        <${Field} label="New password" hint="At least 8 characters.">
          <${TextInput} type="password" value=${next} onChange=${setNext} autoComplete="new-password" />
        <//>
        <${Button} type="submit" kind="primary" busy=${busy}>Change it<//>
      </form>
    <//>
  `
}
