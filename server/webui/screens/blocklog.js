const { useState, useEffect, useCallback, useMemo } = window.React

import { html, Card, Button, Empty, Loading, ErrorBanner } from '../ui.js'
import { computerName, relativeTime, formatDateTime, reasonLabel } from '../format.js'

const PAGE_SIZE = 50

// PERIODS are the ranges worth one click. The default is the last day, because
// the question this screen answers is almost always "what happened today".
const PERIODS = [
  { key: '24h', label: 'Last 24 hours', hours: 24 },
  { key: '7d', label: 'Last 7 days', hours: 24 * 7 },
  { key: '30d', label: 'Last 30 days', hours: 24 * 30 },
  { key: 'all', label: 'Everything kept', hours: null },
]

// BlockLog is the blocking log's one table, used twice: as the room's Log tab
// and as a section of a computer's passport. The caller supplies the loader,
// because the two endpoints are separate — a machine in no room has room_id
// NULL on every row and the room query can never match it.
export function BlockLog({ load, computers, onAuthError, title = 'Blocking log' }) {
  const [filters, setFilters] = useState({ process: '', reason: 'all', computer: 'all', period: '24h' })
  const [rows, setRows] = useState([])
  const [cursor, setCursor] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  // The query the server actually gets. Kept apart from the form state so a
  // half-typed process name does not become a request on every keystroke.
  const query = useMemo(() => {
    const period = PERIODS.find((p) => p.key === filters.period)
    const params = { limit: PAGE_SIZE }
    if (filters.process.trim()) params.process = filters.process.trim()
    if (filters.reason !== 'all') params.reason = filters.reason
    if (filters.computer !== 'all') params.computer_id = filters.computer
    if (period && period.hours) {
      params.since = new Date(Date.now() - period.hours * 3600 * 1000).toISOString()
    }
    return params
  }, [filters])

  const fetchPage = useCallback(
    async (from) => {
      setLoading(true)
      setError(null)
      try {
        const page = await load({ ...query, cursor: from || undefined })
        const got = (page && page.process_events) || []
        setRows((current) => (from ? current.concat(got) : got))
        // No next_cursor means the end of the feed, which is how the API says
        // "that was the last page" — an empty page would be a guess.
        setCursor(page && page.next_cursor ? page.next_cursor : null)
      } catch (err) {
        if (err.isAuth && onAuthError) {
          onAuthError(err)
          return
        }
        setError(err)
      } finally {
        setLoading(false)
      }
    },
    [load, query, onAuthError],
  )

  useEffect(() => {
    fetchPage(null)
  }, [fetchPage])

  const now = new Date()
  const byRoom = computers && computers.length > 0

  return html`
    <${Card}
      title=${title}
      flush
      actions=${html`<${Button} kind="quiet" busy=${loading && rows.length === 0} onClick=${() => fetchPage(null)}>
        Refresh
      <//>`}
    >
      <div class="filters">
        <input
          class="input input-mono"
          type="search"
          placeholder="Filter by program"
          value=${filters.process}
          onInput=${(e) => setFilters({ ...filters, process: e.target.value })}
        />
        ${byRoom &&
        html`<select
          class="input"
          value=${filters.computer}
          onChange=${(e) => setFilters({ ...filters, computer: e.target.value })}
        >
          <option value="all">Every computer</option>
          ${computers.map((c) => html`<option key=${c.id} value=${c.id}>${computerName(c)}</option>`)}
        </select>`}
        <select
          class="input"
          value=${filters.reason}
          onChange=${(e) => setFilters({ ...filters, reason: e.target.value })}
        >
          <option value="all">Any reason</option>
          <option value="blacklist">Blocked by the list</option>
          <option value="whitelist">Not on the list</option>
          <option value="locked">Computer locked</option>
          <option value="overflow">Dropped by the agent</option>
        </select>
        <select
          class="input"
          value=${filters.period}
          onChange=${(e) => setFilters({ ...filters, period: e.target.value })}
        >
          ${PERIODS.map((p) => html`<option key=${p.key} value=${p.key}>${p.label}</option>`)}
        </select>
      </div>

      ${error && html`<div class="card-body"><${ErrorBanner} error=${error} onDismiss=${() => setError(null)} /></div>`}

      ${rows.length === 0 && loading
        ? html`<${Loading} what="Reading the log" />`
        : rows.length === 0
          ? html`<${Empty} title="Nothing was closed in this period">
              <p>
                Either nothing tried to run against the rules, or the machines have not reported
                since. An agent ships what it killed with its next sync.
              </p>
            <//>`
          : html`<div class="table-scroll">
              <table class="table">
                <thead>
                  <tr>
                    <th>When</th>
                    ${byRoom && html`<th>Computer</th>`}
                    <th>Program</th>
                    <th>Why</th>
                    <th class="num">Times</th>
                  </tr>
                </thead>
                <tbody>
                  ${rows.map(
                    (row) => html`
                      <tr key=${row.id}>
                        <td class="mono" title=${formatDateTime(row.created_at)}>
                          ${relativeTime(row.created_at, now)}
                        </td>
                        ${byRoom && html`<td class="cell-dim">${row.computer_name}</td>`}
                        <td class="mono">${row.process}</td>
                        <td>
                          <span class=${'state state-' + (row.reason === 'locked' ? 'locked' : 'offline')}>
                            ${reasonLabel(row.reason)}
                          </span>
                        </td>
                        <td class="num mono">${row.count}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>`}

      ${rows.length > 0 &&
      html`<div class="card-body inline-form">
        ${cursor
          ? html`<${Button} busy=${loading} onClick=${() => fetchPage(cursor)}>Load more<//>`
          : html`<p class="hint">That is everything in this period. The log keeps 30 days.</p>`}
      </div>`}
    <//>
  `
}
