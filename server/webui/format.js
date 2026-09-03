// Presentation helpers. Pure functions, so they carry the rules that are easy
// to get subtly wrong — what "online" means, what a machine is called — and
// can be tested without a DOM.

// ONLINE_AFTER_SECONDS is how long a machine may be silent before the cabinet
// stops calling it online. The agent syncs every 10 seconds in console mode
// and every 20 in service mode, so two minutes is several missed polls: long
// enough that a slow network does not flap the badge, short enough that a
// machine somebody just switched off does not stay green for a coffee break.
export const ONLINE_AFTER_SECONDS = 120

export function isOnline(lastSeenAt, now = new Date()) {
  if (!lastSeenAt) return false
  const seen = new Date(lastSeenAt).getTime()
  if (Number.isNaN(seen)) return false
  return (now.getTime() - seen) / 1000 <= ONLINE_AFTER_SECONDS
}

// computerName is what to call a machine on screen. display_name is what a
// person typed; hostname is what the machine calls itself; the machine guid is
// the last resort, shortened, because a full one is unreadable and unhelpful.
export function computerName(computer) {
  if (!computer) return 'Unknown computer'
  const display = (computer.display_name || '').trim()
  if (display) return display
  const hostname = (computer.hostname || '').trim()
  if (hostname) return hostname
  const guid = (computer.machine_guid || '').trim()
  return guid ? `Machine ${guid.slice(0, 8)}` : 'Unnamed computer'
}

// lastSeenMillis is the raw age a sort needs. relativeTime renders the same
// fact for a person; sorting on its words would put "9 min" before "3 h".
export function lastSeenMillis(iso) {
  if (!iso) return Number.NEGATIVE_INFINITY
  const then = new Date(iso).getTime()
  return Number.isNaN(then) ? Number.NEGATIVE_INFINITY : then
}

export function relativeTime(iso, now = new Date()) {
  if (!iso) return 'never'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return 'never'
  const seconds = Math.round((now.getTime() - then) / 1000)
  if (seconds < 0) return 'just now'
  if (seconds < 45) return 'just now'
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours} h ago`
  const days = Math.round(hours / 24)
  if (days < 30) return `${days} d ago`
  return formatDate(iso)
}

export function formatDate(iso) {
  if (!iso) return '—'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

export function formatDateTime(iso) {
  if (!iso) return '—'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// roomPolicy says, in one sentence, what a room is currently doing to the
// machines in it — the same four cases /agent/sync evaluates, in the same
// order, so the cabinet cannot claim something the agent will not do.
export function roomPolicy(room) {
  if (!room) return 'Unknown'
  if (!room.protection_enabled) return 'Protection off — nothing is enforced'
  if (room.mode === 'whitelist') return 'Whitelist — only listed applications may run'
  return 'Blacklist — listed applications are closed'
}

// roomMode is the policy in one word, for the places a whole sentence does not
// fit — a panel head, a rail. roomPolicy is the sentence.
export function roomMode(room) {
  if (!room) return 'Unknown'
  if (!room.protection_enabled) return 'Not enforced'
  return room.mode === 'whitelist' ? 'Whitelist' : 'Blacklist'
}

// computerState folds the machine's row into the one word a list should show.
// Blocked outranks offline: a locked machine that is also offline is still
// locked, and that is the more surprising fact.
export function computerState(computer, now = new Date()) {
  if (!computer) return 'unknown'
  if (computer.blocked) return 'locked'
  if (!computer.room_id) return 'unassigned'
  return isOnline(computer.last_seen_at, now) ? 'online' : 'offline'
}

// REASON_LABELS says why a process was closed, in the words somebody who did
// not design the schema would use. "whitelist" as a reason reads like the
// opposite of what happened: the program was killed for being absent from it.
const REASON_LABELS = {
  blacklist: 'Blocked by the list',
  whitelist: 'Not on the list',
  locked: 'Computer locked',
  overflow: 'Dropped by the agent',
}

export function reasonLabel(reason) {
  return REASON_LABELS[reason] || reason
}

// EVENT_LABELS turns the API's event types into something a person reads.
// A type with no entry falls back to the raw string rather than being hidden:
// an unlabelled event in the feed is a missing line here, not a missing fact.
const EVENT_LABELS = {
  'room.created': 'Room created',
  'room.updated': 'Room changed',
  'room.deleted': 'Room deleted',
  'application.added': 'Rule added',
  'application.updated': 'Rule switched',
  'application.removed': 'Rule removed',
  'computer.enrolled': 'Computer enrolled',
  'computer.token_rotated': 'Computer re-enrolled',
  'computer.blocked': 'Computer locked',
  'computer.unblocked': 'Computer unlocked',
  'computer.assigned': 'Computer moved',
  'computer.renamed': 'Computer renamed',
  'computer.removed': 'Computer unenrolled',
  'room_member.granted': 'Room shared',
  'room_member.revoked': 'Room sharing revoked',
  'account_member.added': 'Administrator added',
  'account_member.removed': 'Administrator removed',
  'binding_token.created': 'Installer token created',
  'binding_token.revoked': 'Installer tokens revoked',
}

export function eventLabel(type) {
  return EVENT_LABELS[type] || type
}

// eventDetail renders the payload's useful keys without pretending to know
// every shape: the server records what changed, and new keys should show up
// here on their own rather than wait for this file to learn about them.
export function eventDetail(payload) {
  if (!payload || typeof payload !== 'object') return ''
  const parts = []
  for (const [key, value] of Object.entries(payload)) {
    if (value === null || value === undefined || value === '') continue
    if (typeof value === 'object') continue
    parts.push(`${key.replace(/_/g, ' ')}: ${value}`)
  }
  return parts.join(' · ')
}
