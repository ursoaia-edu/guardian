// Hash routing. Every one of these is a URL somebody can bookmark, refresh on,
// or be handed in a message, so the interesting cases are the malformed ones.

import test from 'node:test'
import assert from 'node:assert/strict'

import { parseRoute, href, ROUTES, ROOM_TABS } from '../webui/router.js'

test('an empty or bare hash is the overview', () => {
  for (const hash of ['', '#', '#/', '/', '#//']) {
    assert.deepEqual(parseRoute(hash), { name: 'overview', params: {} }, `for ${JSON.stringify(hash)}`)
  }
  assert.deepEqual(parseRoute(null), { name: 'overview', params: {} })
  assert.deepEqual(parseRoute(undefined), { name: 'overview', params: {} })
})

test('the flat screens each parse to themselves', () => {
  for (const name of ['rooms', 'computers', 'install', 'activity', 'settings', 'welcome']) {
    assert.deepEqual(parseRoute(`#/${name}`), { name, params: {} })
  }
})

test('a room carries its id and lands on a tab', () => {
  assert.deepEqual(parseRoute('#/rooms/r1'), {
    name: 'room',
    params: { roomID: 'r1', tab: 'computers' },
  })
  assert.deepEqual(parseRoute('#/rooms/r1/rules'), {
    name: 'room',
    params: { roomID: 'r1', tab: 'rules' },
  })
  for (const tab of ROOM_TABS) {
    assert.equal(parseRoute(`#/rooms/r1/${tab}`).params.tab, tab)
  }
})

test('an unknown tab is the default tab, not a dead end', () => {
  // The id in the URL is the part worth keeping: somebody who mistypes the tab
  // still wanted that room.
  const route = parseRoute('#/rooms/r1/bogus')
  assert.equal(route.name, 'room')
  assert.equal(route.params.roomID, 'r1')
  assert.equal(route.params.tab, ROOM_TABS[0])
})

test('a computer carries its id', () => {
  assert.deepEqual(parseRoute('#/computers/c1'), { name: 'computer', params: { computerID: 'c1' } })
})

test('anything else is notfound rather than a guess', () => {
  for (const hash of ['#/nope', '#/rooms/r1/rules/extra', '#/computers/c1/x', '#/a/b/c/d']) {
    assert.equal(parseRoute(hash).name, 'notfound', `for ${hash}`)
  }
})

test('ids are decoded on the way in and encoded on the way out', () => {
  assert.equal(parseRoute('#/computers/a%20b').params.computerID, 'a b')
  assert.equal(href('computer', { computerID: 'a b' }), '#/computers/a%20b')
  assert.equal(href('room', { roomID: 'a b' }), '#/rooms/a%20b/computers')
})

test('href round-trips through parseRoute, separators included', () => {
  // A slash in an id is the case that breaks a hand-rolled router: it must not
  // turn one segment into two.
  const cases = [
    ['computer', { computerID: 'weird/id' }],
    ['computer', { computerID: 'a b' }],
    ['room', { roomID: 'weird/id', tab: 'rules' }],
    ['room', { roomID: 'plain', tab: 'members' }],
  ]
  for (const [name, params] of cases) {
    const route = parseRoute(href(name, params))
    assert.equal(route.name, name, `name for ${JSON.stringify(params)}`)
    for (const [key, value] of Object.entries(params)) {
      assert.equal(route.params[key], value, `${key} for ${JSON.stringify(params)}`)
    }
  }
})

test('href names the screens the shell links to', () => {
  assert.equal(href('overview'), '#/')
  assert.equal(href('rooms'), '#/rooms')
  assert.equal(href('computers'), '#/computers')
  assert.equal(href('settings'), '#/settings')
  assert.equal(href('room', { roomID: 'r1' }), '#/rooms/r1/computers', 'a room without a tab gets the first one')
})

test('every route in the table is reachable by its own href', () => {
  // A route added to ROUTES with no way to link to it is a screen nobody can
  // open, which is the failure this table makes easy to miss.
  const sample = { roomID: 'r1', computerID: 'c1', tab: ROOM_TABS[0] }
  for (const route of ROUTES) {
    const parsed = parseRoute(href(route.name, sample))
    assert.equal(parsed.name, route.name, `${route.name} did not survive its own href`)
  }
})

test('the links in an email are routes', () => {
  assert.deepEqual(parseRoute('#/verify/abc123'), { name: 'verify', params: { token: 'abc123' } })
  assert.deepEqual(parseRoute('#/reset/abc123'), { name: 'reset', params: { token: 'abc123' } })
  assert.deepEqual(parseRoute('#/forgot'), { name: 'forgot', params: {} })
})

test('a token with url-unsafe characters survives the round trip', () => {
  // The token is hex today, but a route that breaks on one is a trap set for
  // whoever changes the minting.
  for (const token of ['a b', 'a/b', 'a+b']) {
    const route = parseRoute(href('verify', { token }))
    assert.equal(route.name, 'verify')
    assert.equal(route.params.token, token)
  }
})
