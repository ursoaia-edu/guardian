const { useState, useEffect, useCallback } = window.React

import { html, Card, Button, Empty, Loading, ErrorBanner } from '../ui.js'
import { eventLabel, eventDetail, formatDateTime, relativeTime } from '../format.js'

const PAGE_SIZE = 50

// The activity feed, paged by the API's opaque cursor rather than by an
// offset: rows arriving while somebody reads cannot make a page repeat or skip.
export function ActivityScreen({ api, role, onAuthError }) {
  const manager = role === 'owner' || role === 'admin'
  const [events, setEvents] = useState([])
  const [cursor, setCursor] = useState(null)
  const [done, setDone] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  const load = useCallback(
    async (from) => {
      setLoading(true)
      setError(null)
      try {
        const page = await api.events({ limit: PAGE_SIZE, cursor: from || undefined })
        const rows = (page && page.events) || []
        setEvents((current) => (from ? current.concat(rows) : rows))
        setCursor(page && page.next_cursor ? page.next_cursor : null)
        // No next_cursor means the end of the feed, which is how the API says
        // "that was the last page" — an empty page would be a guess.
        setDone(!(page && page.next_cursor))
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
    [api, onAuthError],
  )

  useEffect(() => {
    if (manager) load(null)
    else setLoading(false)
  }, [manager, load])

  if (!manager) {
    return html`<${Card} title="Activity">
      <p>The account's activity feed is visible to its owner and administrators.</p>
    <//>`
  }

  const now = new Date()

  return html`
    <div class="stack">
      <${Card}
        title="Activity"
        actions=${html`<${Button} onClick=${() => load(null)} busy=${loading && events.length === 0}>Refresh<//>`}
      >
        <${ErrorBanner} error=${error} onDismiss=${() => setError(null)} />
        ${events.length === 0 && loading
          ? html`<${Loading} what="Loading the feed" />`
          : events.length === 0
            ? html`<${Empty} title="Nothing has happened in this account yet" />`
            : html`<ul class="feed feed-full">
                ${events.map(
                  (event) => html`
                    <li key=${event.id}>
                      <span class="feed-what">${eventLabel(event.type)}</span>
                      <span class="feed-detail">${eventDetail(event.payload)}</span>
                      <span class="feed-when" title=${formatDateTime(event.created_at)}>
                        ${relativeTime(event.created_at, now)}
                      </span>
                    </li>
                  `,
                )}
              </ul>`}

        ${!done && events.length > 0 &&
        html`<div class="row-actions">
          <${Button} busy=${loading} onClick=${() => load(cursor)}>Load more<//>
        </div>`}
        ${done && events.length > 0 && html`<p class="hint">That is the whole feed. Events older than 180 days are purged.</p>`}
      <//>
    </div>
  `
}
