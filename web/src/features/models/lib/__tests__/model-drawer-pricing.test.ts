import { describe, expect, it } from 'vitest'

import {
  drawerLockedPricingNoticeKey,
  isDrawerLockedPricingMode,
  resolveDrawerCommitPricingData,
  shouldBlockModelDrawerSave,
} from '../model-drawer-pricing'

describe('shouldBlockModelDrawerSave', () => {
  it('blocks save while options are loading or errored', () => {
    expect(
      shouldBlockModelDrawerSave({
        optionsStatus: 'pending',
        hasSettings: false,
        pricingMode: 'per-token',
        rowErrors: {},
      })
    ).toBe(true)
    expect(
      shouldBlockModelDrawerSave({
        optionsStatus: 'error',
        hasSettings: false,
        pricingMode: 'per-token',
        rowErrors: {},
      })
    ).toBe(true)
  })

  it('blocks save when unit-tier rows have duplicate keys', () => {
    expect(
      shouldBlockModelDrawerSave({
        optionsStatus: 'success',
        hasSettings: true,
        pricingMode: 'task_unit_tier',
        rowErrors: { a: 'Tier keys must be unique and non-empty.' },
      })
    ).toBe(true)
  })

  it('allows save when options are ready', () => {
    expect(
      shouldBlockModelDrawerSave({
        optionsStatus: 'success',
        hasSettings: true,
        pricingMode: 'task_unit_tier',
        rowErrors: {},
      })
    ).toBe(false)
  })
})

describe('drawer locked pricing modes', () => {
  const table = { '480p': '1.8', '720p': '2.5', '1080p': '4' }

  it('locks video_token and tiered_expr', () => {
    expect(isDrawerLockedPricingMode('video_token')).toBe(true)
    expect(isDrawerLockedPricingMode('tiered_expr')).toBe(true)
    expect(isDrawerLockedPricingMode('per-token')).toBe(false)
    expect(isDrawerLockedPricingMode('task_unit_tier')).toBe(false)
    expect(
      drawerLockedPricingNoticeKey('video_token')
    ).toBe(
      'This model uses video-token billing. Edit it under System Settings → Model Pricing.'
    )
    expect(
      drawerLockedPricingNoticeKey('tiered_expr')
    ).toBe(
      'This model uses expression billing. Edit it under System Settings → Model Pricing.'
    )
  })

  it('preserves video_token when the pricing radio is switched to per-token', () => {
    const loaded = {
      name: 'doubao-seedance-1-0-pro',
      billingMode: 'video_token' as const,
      videoTokenPrice: table,
    }
    const submitted = resolveDrawerCommitPricingData({
      loaded,
      name: 'doubao-seedance-1-0-pro',
      pricingMode: 'per-token',
      values: { price: '', ratio: '12' },
      taskUnitTierPrice: {},
    })
    expect(submitted.billingMode).toBe('video_token')
    expect(submitted.videoTokenPrice).toEqual(table)
    expect(submitted.ratio).toBeUndefined()
  })
})
