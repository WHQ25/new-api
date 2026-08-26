import { describe, expect, it } from 'vitest'

import { parseTaskUnitTierPriceTable } from '../model-pricing-core'
import {
  applyModelRatioDataToMaps,
  commitDrawerPricing,
  emptyPricingMaps,
  readModelRatioDataFromMaps,
  resolveEditorPricingMode,
  type PricingOptionMaps,
} from '../model-pricing-persist'

const TABLE = {
  '720p': 0.6,
  '720p_audio': 0.9,
  '1080p_voice': 1.4,
  '4k': 3.0,
}

function emptyMaps(): PricingOptionMaps {
  return {
    price: {},
    ratio: {},
    cache: {},
    createCache: {},
    completion: {},
    image: {},
    audio: {},
    audioCompletion: {},
    billingMode: {},
    billingExpr: {},
    videoTokenPrice: {},
    taskUnitTierPrice: {},
  }
}

describe('task unit tier visual editor round-trip', () => {
  it('opens an existing unit-tier model in unit-tier mode', () => {
    expect(
      resolveEditorPricingMode({
        billingMode: 'task_unit_tier',
        price: '0.01',
      })
    ).toBe('task_unit_tier')
  })

  it('keeps billing mode and the full tier table when saved without edits', () => {
    const maps = emptyMaps()
    maps.billingMode['kling-v3'] = 'task_unit_tier'
    maps.taskUnitTierPrice['kling-v3'] = { ...TABLE }
    maps.ratio['kling-v3'] = 99

    const openedMode = resolveEditorPricingMode({
      billingMode: 'task_unit_tier',
    })
    applyModelRatioDataToMaps(
      {
        name: 'kling-v3',
        billingMode: openedMode,
        taskUnitTierPrice: parseTaskUnitTierPriceTable(TABLE),
      },
      maps
    )

    expect(maps.billingMode['kling-v3']).toBe('task_unit_tier')
    expect(maps.taskUnitTierPrice['kling-v3']).toEqual(TABLE)
    expect(maps.ratio['kling-v3']).toBeUndefined()
    expect(maps.price['kling-v3']).toBeUndefined()
    expect(maps.videoTokenPrice['kling-v3']).toBeUndefined()
  })

  it('does not treat unit-tier as per-token when a leftover price exists', () => {
    const maps = emptyMaps()
    maps.billingMode['kling-v3'] = 'task_unit_tier'
    maps.taskUnitTierPrice['kling-v3'] = { ...TABLE }

    const wrongMode = resolveEditorPricingMode({
      billingMode: undefined,
      price: '0.01',
    })
    applyModelRatioDataToMaps(
      {
        name: 'kling-v3',
        billingMode: wrongMode,
        price: '0.01',
        taskUnitTierPrice: parseTaskUnitTierPriceTable(TABLE),
      },
      maps
    )

    expect(wrongMode).toBe('per-request')
    expect(maps.billingMode['kling-v3']).toBeUndefined()
    expect(maps.taskUnitTierPrice['kling-v3']).toBeUndefined()
  })
})

describe('models drawer pricing maps', () => {
  it('opens task-unit and saves without edits', () => {
    const maps = emptyPricingMaps()
    maps.billingMode['kling-v3'] = 'task_unit_tier'
    maps.taskUnitTierPrice['kling-v3'] = { ...TABLE }
    const opened = readModelRatioDataFromMaps(maps, 'kling-v3')
    expect(opened.billingMode).toBe('task_unit_tier')
    const next = commitDrawerPricing(maps, {
      oldName: 'kling-v3',
      newName: 'kling-v3',
      loadedName: 'kling-v3',
      data: opened,
    })
    expect(next.billingMode['kling-v3']).toBe('task_unit_tier')
    expect(next.taskUnitTierPrice['kling-v3']).toEqual(TABLE)
  })

  it('clears unit-tier maps when switching to fixed price', () => {
    const maps = emptyPricingMaps()
    maps.billingMode['kling-v3'] = 'task_unit_tier'
    maps.taskUnitTierPrice['kling-v3'] = { ...TABLE }
    const next = commitDrawerPricing(maps, {
      oldName: 'kling-v3',
      newName: 'kling-v3',
      loadedName: 'kling-v3',
      data: {
        name: 'kling-v3',
        billingMode: 'per-request',
        price: '0.02',
        taskUnitTierPrice: parseTaskUnitTierPriceTable(TABLE),
      },
    })
    expect(next.billingMode['kling-v3']).toBeUndefined()
    expect(next.taskUnitTierPrice['kling-v3']).toBeUndefined()
    expect(next.price['kling-v3']).toBe(0.02)
  })

  it('clears unit-tier maps when switching to ratio', () => {
    const maps = emptyPricingMaps()
    maps.billingMode['kling-v3'] = 'task_unit_tier'
    maps.taskUnitTierPrice['kling-v3'] = { ...TABLE }
    const next = commitDrawerPricing(maps, {
      oldName: 'kling-v3',
      newName: 'kling-v3',
      loadedName: 'kling-v3',
      data: {
        name: 'kling-v3',
        billingMode: 'per-token',
        ratio: '15',
        taskUnitTierPrice: parseTaskUnitTierPriceTable(TABLE),
      },
    })
    expect(next.billingMode['kling-v3']).toBeUndefined()
    expect(next.taskUnitTierPrice['kling-v3']).toBeUndefined()
    expect(next.ratio['kling-v3']).toBe(15)
  })

  it('preserves video_token maps when submit data drops the price table', () => {
    const maps = emptyPricingMaps()
    const table = { '480p': 1.8, '720p': 2.5, '1080p': 4.0 }
    maps.billingMode['doubao-seedance-1-0-pro'] = 'video_token'
    maps.videoTokenPrice['doubao-seedance-1-0-pro'] = { ...table }
    const next = commitDrawerPricing(maps, {
      oldName: 'doubao-seedance-1-0-pro',
      newName: 'doubao-seedance-1-0-pro',
      loadedName: 'doubao-seedance-1-0-pro',
      data: {
        name: 'doubao-seedance-1-0-pro',
        billingMode: 'video_token',
      },
    })
    expect(next.billingMode['doubao-seedance-1-0-pro']).toBe('video_token')
    expect(next.videoTokenPrice['doubao-seedance-1-0-pro']).toEqual(table)
  })

  it('preserves video_token maps when the pricing section is switched to per-token', () => {
    const maps = emptyPricingMaps()
    const table = { '480p': 1.8, '720p': 2.5, '1080p': 4.0 }
    maps.billingMode['doubao-seedance-1-0-pro'] = 'video_token'
    maps.videoTokenPrice['doubao-seedance-1-0-pro'] = { ...table }
    const opened = readModelRatioDataFromMaps(maps, 'doubao-seedance-1-0-pro')
    const next = commitDrawerPricing(maps, {
      oldName: 'doubao-seedance-1-0-pro',
      newName: 'doubao-seedance-1-0-pro',
      loadedName: 'doubao-seedance-1-0-pro',
      data: {
        ...opened,
        billingMode: 'per-token',
        price: '',
        ratio: '',
        taskUnitTierPrice: {},
      },
    })
    expect(next.billingMode['doubao-seedance-1-0-pro']).toBe('video_token')
    expect(next.videoTokenPrice['doubao-seedance-1-0-pro']).toEqual(table)
    expect(next.ratio['doubao-seedance-1-0-pro']).toBeUndefined()
    expect(next.price['doubao-seedance-1-0-pro']).toBeUndefined()
  })

  it('preserves tiered_expr maps when the pricing section is switched to per-token', () => {
    const maps = emptyPricingMaps()
    maps.billingMode['expr-model'] = 'tiered_expr'
    maps.billingExpr['expr-model'] = 'input * 0.001'
    const opened = readModelRatioDataFromMaps(maps, 'expr-model')
    const next = commitDrawerPricing(maps, {
      oldName: 'expr-model',
      newName: 'expr-model',
      loadedName: 'expr-model',
      data: {
        ...opened,
        billingMode: 'per-token',
        ratio: '12',
      },
    })
    expect(next.billingMode['expr-model']).toBe('tiered_expr')
    expect(next.billingExpr['expr-model']).toBe('input * 0.001')
    expect(next.ratio['expr-model']).toBeUndefined()
  })

  it('preserves existing video_token config when creating by typed name with null loaded data', () => {
    const maps = emptyPricingMaps()
    const table = { '480p': 1.8, '720p': 2.5, '1080p': 4.0 }
    maps.billingMode['doubao-seedance-1-0-pro'] = 'video_token'
    maps.videoTokenPrice['doubao-seedance-1-0-pro'] = { ...table }
    const next = commitDrawerPricing(maps, {
      newName: 'doubao-seedance-1-0-pro',
      loadedName: '',
      data: {
        name: 'doubao-seedance-1-0-pro',
        billingMode: 'per-token',
        ratio: '12',
      },
    })
    expect(next.billingMode['doubao-seedance-1-0-pro']).toBe('video_token')
    expect(next.videoTokenPrice['doubao-seedance-1-0-pro']).toEqual(table)
    expect(next.ratio['doubao-seedance-1-0-pro']).toBeUndefined()
  })

  it('renames all related maps atomically', () => {
    const maps = emptyPricingMaps()
    maps.billingMode['old-kling'] = 'task_unit_tier'
    maps.taskUnitTierPrice['old-kling'] = { ...TABLE }
    maps.billingExpr['old-kling'] = 'x'
    maps.videoTokenPrice['old-kling'] = { '720p': 1 }
    const next = commitDrawerPricing(maps, {
      oldName: 'old-kling',
      newName: 'new-kling',
      loadedName: 'old-kling',
      data: {
        name: 'new-kling',
        billingMode: 'task_unit_tier',
        taskUnitTierPrice: parseTaskUnitTierPriceTable(TABLE),
      },
    })
    expect(next.billingMode['old-kling']).toBeUndefined()
    expect(next.taskUnitTierPrice['old-kling']).toBeUndefined()
    expect(next.billingExpr['old-kling']).toBeUndefined()
    expect(next.videoTokenPrice['old-kling']).toBeUndefined()
    expect(next.billingMode['new-kling']).toBe('task_unit_tier')
    expect(next.taskUnitTierPrice['new-kling']).toEqual(TABLE)
  })
})
