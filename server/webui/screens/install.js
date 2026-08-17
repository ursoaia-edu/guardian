const { useState } = window.React

import { html, Card, Button, Banner, ErrorBanner, Confirm } from '../ui.js'

// DownloadInstaller is the one button the whole product depends on: it fetches
// the personalised archive and hands it to the browser as a file.
//
// It is a fetch rather than a plain link on purpose. A link cannot carry
// X-Guardian-Account, so a person who manages two accounts would download the
// wrong account's installer with no way to tell — the token inside is the only
// thing that differs, and it is not visible until a machine enrols into the
// wrong fleet.
export function DownloadInstaller({ api, onDownloaded, kind = 'primary', label = 'Download the installer' }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  async function download() {
    setBusy(true)
    setError(null)
    try {
      const { blob, filename } = await api.downloadInstaller()
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = filename
      document.body.appendChild(link)
      link.click()
      link.remove()
      // Revoking immediately would race the download in some browsers; a
      // minute is longer than any of them need and the blob is freed either
      // way when the tab closes.
      setTimeout(() => URL.revokeObjectURL(url), 60000)
      if (onDownloaded) onDownloaded()
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  return html`
    <div class="download">
      <${Button} kind=${kind} busy=${busy} onClick=${download}>${label}<//>
      ${error &&
      html`<div class="download-error">
        <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
        ${error.status === 503 &&
        html`<p class="hint">
          This server has no installer archive yet. An administrator builds it with
          <code>go run ./tools/mkinstaller</code> and points <code>INSTALLER_ARCHIVE</code> at it.
        </p>`}
      </div>`}
    </div>
  `
}

export function InstallScreen({ api, role }) {
  const [revoking, setRevoking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState(null)
  const [error, setError] = useState(null)

  async function revokeAll() {
    setBusy(true)
    setError(null)
    try {
      await api.revokeBindingTokens()
      setNotice(
        'Every installer token for this account is now dead. Computers already enrolled are ' +
          'unaffected — they carry their own credential. Download a fresh installer for the next machine.',
      )
      setRevoking(false)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  if (role === 'member') {
    return html`<${Card} title="Install the agent">
      <p>Installing the agent is done by the account's owner or an administrator.</p>
    <//>`
  }

  return html`
    <div class="stack">
      <${Card} title="Four steps">
        <ol class="steps">
          <li>
            <strong>Download the installer.</strong> The archive is personalised for this account:
            it carries the token a new machine uses to enrol, and nothing else about it differs
            from anyone else's copy.
            <div class="step-action"><${DownloadInstaller} api=${api} /></div>
          </li>
          <li>
            <strong>Extract the whole archive.</strong> Right-click the ZIP and choose
            “Extract All…”. Running <code>Guardian.exe</code> straight from inside the archive is
            the most common mistake — Windows copies only that one file to a temporary folder and
            the agent binaries stay behind. The console now says so instead of failing obscurely.
          </li>
          <li>
            <strong>Run <code>Guardian.exe</code> as administrator</strong> from the extracted
            folder and press <em>Install</em>. It picks the 32- or 64-bit agent to match the
            machine, installs the <code>ProcSentinelAgent</code> service and starts it.
          </li>
          <li>
            <strong>The machine appears here</strong> within a few seconds of the first sync, in
            the computer pool with no room. Put it in a room and the room's rules start applying.
          </li>
        </ol>
      <//>

      <${Card} title="When antivirus complains">
        <p>
          The agent is a service that closes other programs and defends itself from being stopped.
          That is the behaviour antivirus heuristics are built to notice, and the installer is not
          yet signed with an EV certificate, so SmartScreen may say “unknown publisher”.
        </p>
        <p>
          Choose <em>More info → Run anyway</em>, and if the local antivirus quarantines
          <code>procsentinel-agent64.exe</code>, add the installation folder
          (<code>C:\\Windows\\System32\\ProcSentinel\\agent</code>) to its exclusions.
        </p>
      <//>

      <${Card} title="Installer token">
        <p>
          Every download mints a fresh token, valid for a year, and any of them can enrol a
          machine into this account. If an installer has gone somewhere it should not have,
          revoke them all.
        </p>
        ${notice && html`<${Banner} kind="info" onDismiss=${() => setNotice(null)}>${notice}<//>`}
        <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
        <${Button} kind="danger" onClick=${() => setRevoking(true)}>Revoke every installer token<//>
      <//>

      <${Confirm}
        open=${revoking}
        title="Revoke every installer token?"
        body=${html`<p>
          Installers already downloaded stop being able to enrol new machines. Computers that have
          already enrolled keep working — they hold their own per-machine credential.
        </p>`}
        confirmLabel="Revoke them"
        busy=${busy}
        onConfirm=${revokeAll}
        onCancel=${() => setRevoking(false)}
      />
    </div>
  `
}
