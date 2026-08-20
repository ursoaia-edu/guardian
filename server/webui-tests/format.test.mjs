// The presentation rules that are easy to get subtly wrong.
//
// These live outside server/webui/ because that directory is embedded whole
// (`go:embed all:webui`) and served: a test file in there would ship inside
// the binary and be downloadable from the cabinet.

import test from 'node:test'
import assert from 'node:assert/strict'

import {
  ONLINE_AFTER_SECONDS,
  isOnline,
  computerName,
  computerState,
  roomPolicy,
  roomMode,
  relativeTime,
  lastSeenMillis,
  formatDate,
  formatDateTime,
  eventLabel,
  eventDetail,
} from '../webui/format.js'

const NOW = new Date('2026-09-16T12:00:00Z')
const secondsAgo = (s) => new Date(NOW.getTime() - s * 1000).toISOString()

test('isOnline holds the line at two minutes', () => {
  assert.equal(ONLINE_AFTER_SECONDS, 120)
  assert.equal(isOnline(secondsAgo(0), NOW), true)
  assert.equal(isOnline(secondsAgo(119), NOW), true)
  assert.equal(isOnline(secondsAgo(120), NOW), true, 'exactly the limit is still online')
  assert.equal(isOnline(secondsAgo(121), NOW), false)
})

test('isOnline treats a machine that never reported as offline', () => {
  assert.equal(isOnline(null, NOW), false)
  assert.equal(isOnline(undefined, NOW), false)
  assert.equal(isOnline('', NOW), false)
  assert.equal(isOnline('not a date', NOW), false)
})

test('computerName prefers what a person typed, then what the machine calls itself', () => {
  const machine = { display_name: 'Teacher station', hostname: 'LAB-A-01', machine_guid: '9f8e7d6c-1000' }
  assert.equal(computerName(machine), 'Teacher station')
  assert.equal(computerName({ ...machine, display_name: '   ' }), 'LAB-A-01', 'blank is not a name')
  assert.equal(computerName({ ...machine, display_name: '', hostname: '' }), 'Machine 9f8e7d6c')
  assert.equal(computerName({ display_name: '', hostname: '', machine_guid: '' }), 'Unnamed computer')
  assert.equal(computerName(null), 'Unknown computer')
})

test('computerState ranks locked above every other state', () => {
  const inRoom = { room_id: 'r1', blocked: false }
  assert.equal(computerState({ ...inRoom, last_seen_at: secondsAgo(10) }, NOW), 'online')
  assert.equal(computerState({ ...inRoom, last_seen_at: secondsAgo(600) }, NOW), 'offline')
  assert.equal(computerState({ room_id: null, last_seen_at: secondsAgo(10) }, NOW), 'unassigned')
  // A locked machine that is also offline is still locked, and that is the
  // more surprising fact about it.
  assert.equal(
    computerState({ room_id: 'r1', blocked: true, last_seen_at: secondsAgo(99999) }, NOW),
    'locked',
  )
  assert.equal(computerState({ room_id: null, blocked: true }, NOW), 'locked')
  assert.equal(computerState(null, NOW), 'unknown')
})

test('roomPolicy says what the room is doing, protection first', () => {
  assert.equal(
    roomPolicy({ protection_enabled: false, mode: 'whitelist' }),
    'Protection off — nothing is enforced',
    'protection off outranks the mode, because the mode is not being applied',
  )
  assert.equal(
    roomPolicy({ protection_enabled: true, mode: 'whitelist' }),
    'Whitelist — only listed applications may run',
  )
  assert.equal(
    roomPolicy({ protection_enabled: true, mode: 'blacklist' }),
    'Blacklist — listed applications are closed',
  )
  assert.equal(roomPolicy(null), 'Unknown')
})

test('roomMode is the same judgement in one word', () => {
  assert.equal(roomMode({ protection_enabled: false, mode: 'whitelist' }), 'Not enforced')
  assert.equal(roomMode({ protection_enabled: true, mode: 'whitelist' }), 'Whitelist')
  assert.equal(roomMode({ protection_enabled: true, mode: 'blacklist' }), 'Blacklist')
  assert.equal(roomMode(null), 'Unknown')
})

test('relativeTime climbs through the units', () => {
  assert.equal(relativeTime(secondsAgo(5), NOW), 'just now')
  assert.equal(relativeTime(secondsAgo(44), NOW), 'just now')
  assert.equal(relativeTime(secondsAgo(60), NOW), '1 min ago')
  assert.equal(relativeTime(secondsAgo(60 * 59), NOW), '59 min ago')
  assert.equal(relativeTime(secondsAgo(3600 * 5), NOW), '5 h ago')
  assert.equal(relativeTime(secondsAgo(86400 * 3), NOW), '3 d ago')
})

test('relativeTime falls back to a date once a month has passed', () => {
  const old = relativeTime(secondsAgo(86400 * 400), NOW)
  assert.ok(!old.includes('ago'), `expected a date, got ${old}`)
  assert.ok(old.includes('2025'), `expected the year, got ${old}`)
})

test('relativeTime never reports the future or a missing timestamp as an age', () => {
  assert.equal(relativeTime(null, NOW), 'never')
  assert.equal(relativeTime('not a date', NOW), 'never')
  // A machine whose clock runs ahead must not be "in -3 minutes".
  assert.equal(relativeTime(secondsAgo(-180), NOW), 'just now')
})

test('lastSeenMillis sorts newest first and puts the silent machines last', () => {
  const machines = [
    { id: 'old', last_seen_at: secondsAgo(86400) },
    { id: 'never', last_seen_at: null },
    { id: 'fresh', last_seen_at: secondsAgo(5) },
  ]
  // The column sorts on the negated value, exactly as screens/computers.js does.
  const order = [...machines]
    .sort((a, b) => -lastSeenMillis(a.last_seen_at) - -lastSeenMillis(b.last_seen_at))
    .map((m) => m.id)
  assert.deepEqual(order, ['fresh', 'old', 'never'])
})

test('a date that is not a date renders as a dash, not as year zero', () => {
  assert.equal(formatDate(null), '—')
  assert.equal(formatDate('not a date'), '—')
  assert.equal(formatDateTime(null), '—')
  assert.equal(formatDateTime('not a date'), '—')
  assert.ok(formatDate('2026-09-16T12:00:00Z').includes('2026'))
  assert.ok(formatDateTime('2026-09-16T12:00:00Z').includes('2026'))
})

test('eventLabel falls through to the raw type rather than hiding the event', () => {
  assert.equal(eventLabel('computer.enrolled'), 'Computer enrolled')
  assert.equal(eventLabel('binding_token.revoked'), 'Installer tokens revoked')
  // An unlabelled event is a missing line in EVENT_LABELS, not a missing fact.
  assert.equal(eventLabel('something.new'), 'something.new')
})

test('eventDetail renders the scalar keys and skips what it cannot show', () => {
  assert.equal(
    eventDetail({ room: 'Computer lab A', application: 'Scratch' }),
    'room: Computer lab A · application: Scratch',
  )
  assert.equal(eventDetail({ agent_version: '1.4.1' }), 'agent version: 1.4.1')
  assert.equal(eventDetail({ a: 1, skipped: null, blank: '', nested: { x: 1 } }), 'a: 1')
  assert.equal(eventDetail({ enabled: false }), 'enabled: false', 'false is a value, not an absence')
  assert.equal(eventDetail(null), '')
  assert.equal(eventDetail('a string'), '')
})
