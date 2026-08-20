import test from 'node:test'
import assert from 'node:assert/strict'

import { WILD_LAYERS, matchesWildPet } from '../src/pages/map/wildFilter.js'

const select = (...keys) => WILD_LAYERS.filter((layer) => keys.includes(layer.k))
const pet = (...kinds) => ({ kinds })

test('AND intersects weight and voice groups', () => {
  const and = select('weight-big', 'weight-small', 'voice-high', 'voice-low')
  assert.equal(matchesWildPet(pet('weight-big', 'voice-low'), [], [], and), true)
  assert.equal(matchesWildPet(pet('weight-small', 'voice-high'), [], [], and), true)
  assert.equal(matchesWildPet(pet('weight-big'), [], [], and), false)
  assert.equal(matchesWildPet(pet('voice-low'), [], [], and), false)
})

test('MAX conditions can participate in AND groups', () => {
  const and = select('weight-big-max', 'weight-small-max', 'voice-high-max', 'voice-low-max')
  assert.equal(matchesWildPet(pet('weight-big-max'), [], [], and), false)
  assert.equal(matchesWildPet(pet('voice-low-max'), [], [], and), false)
  assert.equal(matchesWildPet(pet('weight-big-max', 'voice-low-max'), [], [], and), true)
  assert.equal(matchesWildPet(pet('weight-small-max', 'voice-high-max'), [], [], and), true)
})

test('mutation and pollution are standalone additions', () => {
  const standalone = select('mutation', 'pollution')
  const and = select('weight-big', 'voice-high')
  for (const kind of ['shiny', 'colorful', 'pollution']) {
    assert.equal(matchesWildPet(pet(kind), standalone, [], and), true, kind)
  }
  assert.equal(matchesWildPet(pet('weight-big'), standalone, [], and), false)
})

test('OR and AND condition sets contribute simultaneously', () => {
  const or = select('weight-big-max', 'voice-low-max')
  const and = select('weight-big', 'weight-small', 'voice-high', 'voice-low')
  assert.equal(matchesWildPet(pet('weight-big-max'), [], or, and), true)
  assert.equal(matchesWildPet(pet('voice-low-max'), [], or, and), true)
  assert.equal(matchesWildPet(pet('weight-small', 'voice-high'), [], or, and), true)
  assert.equal(matchesWildPet(pet('weight-big'), [], or, and), false)
  assert.equal(matchesWildPet(pet('pollution'), [], or, and), false)
  assert.equal(matchesWildPet(pet('weight-big'), [], [], []), false)
})
