const { useState, useEffect } = window.React

import { html, Card, Button, Banner, Loading, BrandMark } from '../ui.js'
import { href, navigate } from '../router.js'

// The landing for a confirmation link. It runs before anybody signs in,
// because the link is usually opened in whatever browser the mail client
// hands it to.
export function VerifyScreen({ api, token, signedIn }) {
  const [state, setState] = useState('working') // working | done | failed
  const [error, setError] = useState(null)

  useEffect(() => {
    let alive = true
    api
      .verifyEmail(token)
      .then(() => alive && setState('done'))
      .catch((err) => {
        if (!alive) return
        setError(err)
        setState('failed')
      })
    return () => {
      alive = false
    }
  }, [api, token])

  return html`
    <div class="signin">
      <div class="signin-card">
        <div class="brand"><${BrandMark} /><span>Guardian</span></div>
        ${state === 'working' && html`<${Card}><${Loading} what="Confirming your address" /><//>`}
        ${state === 'done' &&
        html`<${Card} title="Address confirmed">
          <p>You can install the agent on a computer now.</p>
          <${Button} kind="primary" onClick=${() => navigate(signedIn ? 'install' : 'overview')}>
            ${signedIn ? 'Install the agent' : 'Sign in'}
          <//>
        <//>`}
        ${state === 'failed' &&
        html`<${Card} title="This link did not work">
          <${Banner} kind="warn">
            ${(error && error.message) || 'The link is not valid or has already been used.'}
          <//>
          <p class="hint">
            A confirmation link works once and lasts 48 hours. Sign in and ask for a new one from
            Settings.
          </p>
          <a href=${href('overview')}>Go to the cabinet</a>
        <//>`}
      </div>
    </div>
  `
}
