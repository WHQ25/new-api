/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
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

const MODEL = 'doubao-seedance-1-0-pro'
const UPSTREAM = 'upstream-a'
const TABLE = '{"1080p":7.7,"720p":7}'

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

describe('video tier price sync', () => {
  it('treats the tier table as tiered billing config, not a ratio', () => {
    expect(getBillingCategory('video_token_price')).toBe('tiered')
  })

  it('orders the tier table alongside the other billing fields', () => {
    expect(
      getOrderedRatioTypes({
        billing_mode: entry('video_token'),
        video_token_price: entry(TABLE),
        model_ratio: entry(1),
      })
    ).toEqual(['model_ratio', 'video_token_price', 'billing_mode'])
  })

  it('selects the video billing mode along with the tier table', () => {
    const resolved = applyResolutionSelection(
      {},
      differences({
        billing_mode: entry('video_token'),
        video_token_price: entry(TABLE),
      }),
      {
        model: MODEL,
        ratioType: 'video_token_price',
        value: TABLE,
        sourceName: UPSTREAM,
      }
    )

    expect(resolved[MODEL]).toEqual({
      video_token_price: TABLE,
      billing_mode: 'video_token',
    })
  })

  it('pulls in the tier table when the video billing mode is selected', () => {
    const resolved = applyResolutionSelection(
      {},
      differences({
        billing_mode: entry('video_token'),
        video_token_price: entry(TABLE),
      }),
      {
        model: MODEL,
        ratioType: 'billing_mode',
        value: 'video_token',
        sourceName: UPSTREAM,
      }
    )

    expect(resolved[MODEL]).toEqual({
      billing_mode: 'video_token',
      video_token_price: TABLE,
    })
  })

  it('keeps the video mode selectable when the local table already matches', () => {
    const resolved = applyResolutionSelection(
      {},
      differences({
        billing_mode: entry('video_token'),
        video_token_price: entry('same'),
      }),
      {
        model: MODEL,
        ratioType: 'billing_mode',
        value: 'video_token',
        sourceName: UPSTREAM,
      }
    )

    expect(resolved[MODEL]).toEqual({ billing_mode: 'video_token' })
  })

  it('drops the video billing mode when the tier table is unselected', () => {
    const selected = applyResolutionSelection(
      {},
      differences({
        billing_mode: entry('video_token'),
        video_token_price: entry(TABLE),
      }),
      {
        model: MODEL,
        ratioType: 'billing_mode',
        value: 'video_token',
        sourceName: UPSTREAM,
      }
    )

    expect(deleteResolutionField(selected, MODEL, 'video_token_price')).toEqual(
      {}
    )
    expect(deleteResolutionField(selected, MODEL, 'billing_mode')).toEqual({})
  })

  it('leaves a non-video billing mode alone when a tier table is unselected', () => {
    const resolutions = {
      [MODEL]: {
        billing_mode: 'tiered_expr',
        billing_expr: 'x',
        video_token_price: TABLE,
      },
    }

    expect(
      deleteResolutionField(resolutions, MODEL, 'video_token_price')
    ).toEqual({
      [MODEL]: { billing_mode: 'tiered_expr', billing_expr: 'x' },
    })
  })

  it('writes the tier table to its billing option, never a PascalCase key', () => {
    // `VideoTokenPrice` is not a real option key; writing there used to throw
    // because no such container exists on the sync payload.
    expect(optionKeyBySyncField('video_token_price')).toBe(
      'billing_setting.video_token_price'
    )
    expect(optionKeyBySyncField('billing_mode')).toBe(
      'billing_setting.billing_mode'
    )
    expect(optionKeyBySyncField('model_ratio')).toBe('ModelRatio')
  })

  it('decodes the canonical tier table and rejects unusable payloads', () => {
    expect(parseVideoTokenPriceTable(TABLE)).toEqual({
      '1080p': 7.7,
      '720p': 7,
    })
    expect(parseVideoTokenPriceTable('{}')).toBeNull()
    expect(parseVideoTokenPriceTable('not json')).toBeNull()
    expect(parseVideoTokenPriceTable('[1,2]')).toBeNull()
    expect(parseVideoTokenPriceTable('{"720p":"7"}')).toBeNull()
    expect(parseVideoTokenPriceTable(7)).toBeNull()
  })

  it('renders the tier table as a tier count instead of raw JSON', () => {
    const t = (key: string, options?: Record<string, unknown>) =>
      key.replace('{{tiers}}', String(options?.tiers))

    expect(formatSyncValueLabel('video_token_price', TABLE, t)).toBe(
      '2 tier prices'
    )
    expect(formatSyncValueLabel('video_token_price', 'not json', t)).toBe(
      'not json'
    )
    expect(formatSyncValueLabel('model_ratio', 1.5, t)).toBe('1.5')
  })
})
