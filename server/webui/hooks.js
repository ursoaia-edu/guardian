// React hooks shared by the screens.
//
// React arrives as a global from vendor/react.production.min.js rather than an
// import: the cabinet ships as source with no build step, so there is no
// bundler to resolve a bare specifier and no import map to maintain.
const { useState, useEffect, useRef, useCallback } = window.React

import { ApiError } from './api.js'

// POLL_INTERVAL_MS is the cabinet's refresh rate on the active screen. The
// design spec settles the question: commands take twenty seconds to reach a
// machine anyway, so a WebSocket buys nothing a person can perceive, and this
// costs one cheap request per screen.
export const POLL_INTERVAL_MS = 7000

// MAX_BACKOFF_MS bounds what a failing server costs. A tab left open overnight
// against a server that is down must not spend the night making thousands of
// requests — it retries every minute and no more.
export const MAX_BACKOFF_MS = 60000

// backoffDelay doubles the interval per consecutive failure. Exported for its
// own test: a backoff that quietly stops backing off is invisible until it is
// somebody's outage.
export function backoffDelay(interval, failures, max = MAX_BACKOFF_MS) {
  if (failures <= 0) return interval
  const delay = interval * Math.pow(2, Math.min(failures, 10))
  return Math.min(delay, max)
}

// usePoll loads data now and keeps it fresh.
//
// Three behaviours that matter more than the fetching:
//   - a hidden tab does not poll at all, and refreshes the moment it is shown;
//   - consecutive failures back off, and one success resets that;
//   - a 401 is not this hook's business — it is re-thrown to the caller, whose
//     job is to send the person back to the sign-in screen.
export function usePoll(loader, options = {}) {
  const { deps = [], interval = POLL_INTERVAL_MS, enabled = true, onAuthError } = options

  const [data, setData] = useState(null)
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(enabled)
  const failures = useRef(0)
  const timer = useRef(null)
  const alive = useRef(true)
  const loaderRef = useRef(loader)
  loaderRef.current = loader
  const authRef = useRef(onAuthError)
  authRef.current = onAuthError

  const run = useCallback(async () => {
    if (!enabled) return
    try {
      const result = await loaderRef.current()
      if (!alive.current) return
      failures.current = 0
      setData(result)
      setError(null)
    } catch (err) {
      if (!alive.current) return
      if (err instanceof ApiError && err.isAuth && authRef.current) {
        authRef.current(err)
        return
      }
      failures.current += 1
      setError(err)
    } finally {
      if (alive.current) setLoading(false)
    }
  }, [enabled])

  useEffect(() => {
    alive.current = true
    let stopped = false

    const tick = async () => {
      if (document.visibilityState === 'hidden') {
        schedule(interval)
        return
      }
      await run()
      schedule(backoffDelay(interval, failures.current))
    }

    const schedule = (delay) => {
      if (stopped) return
      clearTimeout(timer.current)
      timer.current = setTimeout(tick, delay)
    }

    setLoading(true)
    run().then(() => schedule(backoffDelay(interval, failures.current)))

    const onVisible = () => {
      if (document.visibilityState === 'visible') {
        schedule(0)
      }
    }
    document.addEventListener('visibilitychange', onVisible)

    return () => {
      stopped = true
      alive.current = false
      clearTimeout(timer.current)
      document.removeEventListener('visibilitychange', onVisible)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, enabled, interval])

  const reload = useCallback(() => {
    failures.current = 0
    return run()
  }, [run])

  return { data, error, loading, reload, setData }
}

// useAction wraps a one-shot mutation: it keeps the button busy, keeps the
// error next to the thing that failed, and reloads afterwards. Screens that
// hand-rolled this all ended up forgetting one of the three.
export function useAction(onDone) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  const perform = useCallback(
    async (fn) => {
      setBusy(true)
      setError(null)
      try {
        const result = await fn()
        if (onDone) await onDone(result)
        return result
      } catch (err) {
        setError(err)
        return undefined
      } finally {
        setBusy(false)
      }
    },
    [onDone],
  )

  return { busy, error, perform, clearError: () => setError(null) }
}
