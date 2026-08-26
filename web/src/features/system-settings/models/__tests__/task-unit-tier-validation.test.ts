import { describe, expect, it } from 'vitest'

import {
  getTaskUnitTierRowErrors,
  getTaskUnitTierValidationError,
  parseTaskUnitTierPriceTable,
  serializeTaskUnitTierPriceTable,
} from '../model-pricing-core'

describe('task unit tier price table', () => {
  it('serializes finite positive prices and skips empty rows', () => {
    expect(
      serializeTaskUnitTierPriceTable({
        '720p': '0.6',
        '720p_audio': '',
        '': '1',
        '4k_voice': '2.4',
      })
    ).toEqual({ '720p': 0.6, '4k_voice': 2.4 })
  })

  it('parses only finite positive prices', () => {
    expect(
      parseTaskUnitTierPriceTable({
        '720p': 0.6,
        bad: 0,
        neg: -1,
        inf: Number.POSITIVE_INFINITY,
      })
    ).toEqual({ '720p': '0.6' })
  })

  it('rejects empty, duplicate, and non-positive keys', () => {
    expect(getTaskUnitTierValidationError([])).toBe(
      'Fill at least one unit tier price before saving.'
    )
    expect(
      getTaskUnitTierValidationError([{ key: '', price: '0.6' }])
    ).toBe('Tier keys must be unique and non-empty.')
    expect(
      getTaskUnitTierValidationError([
        { key: '720p', price: '0.6' },
        { key: '720p', price: '0.8' },
      ])
    ).toBe('Tier keys must be unique and non-empty.')
    expect(
      getTaskUnitTierValidationError([{ key: '720p', price: '0' }])
    ).toBe('Unit tier prices must be finite positive numbers.')
    expect(
      getTaskUnitTierValidationError([{ key: '720p', price: '0.6' }])
    ).toBeNull()
  })

  it('keeps duplicate keys as separate row errors before object fold', () => {
    const errors = getTaskUnitTierRowErrors([
      { id: 'a', key: '720p', price: '0.6' },
      { id: 'b', key: '720p', price: '0.8' },
    ])
    expect(errors.a).toBe('Tier keys must be unique and non-empty.')
    expect(errors.b).toBe('Tier keys must be unique and non-empty.')
  })
})
