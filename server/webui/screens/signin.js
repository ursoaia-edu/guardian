const { useState } = window.React

import { html, Button, Field, TextInput, ErrorBanner } from '../ui.js'

// The sign-in screen doubles as registration, because a brand-new customer
// arrives here with nothing: registering creates the user AND the account they
// own, and the phone app can do the same, so whichever they reach first works.
export function SignInScreen({ api, onSignedIn }) {
  const [mode, setMode] = useState('signin')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const registering = mode === 'register'

  async function submit(event) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      if (registering) {
        // Registration does not sign anybody in — the API says so plainly —
        // so the two calls are one action here rather than two screens.
        await api.register(email, password, name)
      }
      await api.login(email, password)
      await onSignedIn({ fresh: registering })
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  return html`
    <div class="signin">
      <div class="signin-card">
        <div class="brand">
          <span class="brand-mark" aria-hidden="true"></span>
          <span class="brand-name">Guardian</span>
        </div>
        <h1>${registering ? 'Create an account' : 'Sign in'}</h1>
        <p class="signin-lede">
          ${registering
            ? 'One account holds your rooms, your computers and the people you share them with.'
            : 'Manage the computers running the ProcSentinel agent.'}
        </p>

        <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />

        <form onSubmit=${submit}>
          ${registering &&
          html`<${Field} label="Your name" hint="Used to name the account. Optional.">
            <${TextInput} value=${name} onChange=${setName} autoComplete="name" name="name" />
          <//>`}

          <${Field} label="Email">
            <${TextInput}
              type="email"
              value=${email}
              onChange=${setEmail}
              autoComplete="username"
              name="email"
              required=${true}
            />
          <//>

          <${Field}
            label="Password"
            hint=${registering ? 'At least 8 characters.' : null}
          >
            <${TextInput}
              type="password"
              value=${password}
              onChange=${setPassword}
              autoComplete=${registering ? 'new-password' : 'current-password'}
              name="password"
              required=${true}
            />
          <//>

          <${Button} type="submit" kind="primary" busy=${busy}>
            ${registering ? 'Create account' : 'Sign in'}
          <//>
        </form>

        <p class="signin-switch">
          ${registering ? 'Already have an account?' : 'No account yet?'}
          ${' '}
          <button
            class="linkish"
            onClick=${() => {
              setMode(registering ? 'signin' : 'register')
              setError(null)
            }}
          >
            ${registering ? 'Sign in' : 'Create one'}
          </button>
        </p>
      </div>
    </div>
  `
}
