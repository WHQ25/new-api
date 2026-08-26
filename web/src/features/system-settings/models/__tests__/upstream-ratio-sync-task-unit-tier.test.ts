import { describe, expect, it } from 'vitest'

import type { RatioType } from '../../types'
import {
  applyResolutionSelection,
  deleteResolutionField,
  formatSyncValueLabel,
  getBillingCategory,
  getOrderedRatioTypes,
  optionKeyBySyncField,
  parseVideoTokenPriceTable,
  type RatioDifferenceEntry,
} from '../upstream-ratio-sync-helpers'

const MODEL = 'kling-v3'
const UPSTREAM = 'upstream-a'
const TABLE = '{"1080p_voice":1.4,"720p":0.6}'

function differences(
  entries: Partial<Record<RatioType, RatioDifferenceEntry>>
): Record<string, Partial<Record<RatioType, RatioDifferenceEntry>>> {
  return { [MODEL]: entries }
}

function entry(upstreamValue: number | string | 'same'): RatioDifferenceEntry {
  return {
    current: null,
    upstreams: { [UPSTREAM]: upstreamValue },
    confidence: { [UPSTREAM]: true },
  }
}

describe('task unit tier price sync', () => {
  it('treats the unit table as tiered billing config, not a ratio', () => {
    expect(getBillingCategory('task_unit_tier_price')).toBe('tiered')
  })

  it('orders the unit table alongside the other billing fields', () => {
    expect(
      getOrderedRatioTypes({
        billing_mode: entry('task_unit_tier'),
        task_unit_tier_price: entry(TABLE),
        model_ratio: entry(1),
      })
    ).toEqual(['model_ratio', 'task_unit_tier_price', 'billing_mode'])
  })

  it('selects the unit billing mode along with the tier table', () => {
    const resolved = applyResolutionSelection(
      {},
      differences({
        billing_mode: entry('task_unit_tier'),
        task_unit_tier_price: entry(TABLE),
      }),
      {
        model: MODEL,
        ratioType: 'task_unit_tier_price',
        value: TABLE,
        sourceName: UPSTREAM,
      }
    )

    expect(resolved[MODEL]).toEqual({
      task_unit_tier_price: TABLE,
      billing_mode: 'task_unit_tier',
    })
  })

  it('pulls in the tier table when the unit billing mode is selected', () => {
    const resolved = applyResolutionSelection(
      {},
      differences({
        billing_mode: entry('task_unit_tier'),
        task_unit_tier_price: entry(TABLE),
      }),
      {
        model: MODEL,
        ratioType: 'billing_mode',
        value: 'task_unit_tier',
        sourceName: UPSTREAM,
      }
    )

    expect(resolved[MODEL]).toEqual({
      billing_mode: 'task_unit_tier',
      task_unit_tier_price: TABLE,
    })
  })

  it('drops the unit billing mode when the tier table is unselected', () => {
    const selected = applyResolutionSelection(
      {},
      differences({
        billing_mode: entry('task_unit_tier'),
        task_unit_tier_price: entry(TABLE),
      }),
      {
        model: MODEL,
        ratioType: 'billing_mode',
        value: 'task_unit_tier',
        sourceName: UPSTREAM,
      }
    )

    expect(
      deleteResolutionField(selected, MODEL, 'task_unit_tier_price')
    ).toEqual({})
    expect(deleteResolutionField(selected, MODEL, 'billing_mode')).toEqual({})
  })

  it('writes the unit table to its billing option', () => {
    expect(optionKeyBySyncField('task_unit_tier_price')).toBe(
      'billing_setting.task_unit_tier_price'
    )
  })

  it('decodes the canonical unit table', () => {
    expect(parseVideoTokenPriceTable(TABLE)).toEqual({
      '1080p_voice': 1.4,
      '720p': 0.6,
    })
  })

  it('renders the unit table as a tier count instead of raw JSON', () => {
    const t = (key: string, options?: Record<string, unknown>) =>
      key.replace('{{tiers}}', String(options?.tiers))

    expect(formatSyncValueLabel('task_unit_tier_price', TABLE, t)).toBe(
      '2 tier prices'
    )
  })
})
