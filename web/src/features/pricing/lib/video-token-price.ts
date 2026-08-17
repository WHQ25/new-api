import type { PricingModel, TokenUnit } from '../types'

import { formatDynamicUnitPrice } from './dynamic-price'

export const VIDEO_TOKEN_RESOLUTIONS = ['480p', '720p', '1080p', '4k'] as const

export type VideoTokenResolution = (typeof VIDEO_TOKEN_RESOLUTIONS)[number]

export type VideoTokenCell = {
  key: string
  resolution: VideoTokenResolution
  hasVideo: boolean
  usdPerM: number
}

type VideoTokenFormatOptions = {
  tokenUnit: TokenUnit
  showRechargePrice?: boolean
  priceRate?: number
  usdExchangeRate?: number
  groupRatioMultiplier?: number
}

export function isVideoTokenPricingModel(model: PricingModel): boolean {
  return (
    model.billing_mode === 'video_token' &&
    getVideoTokenCells(model).length > 0
  )
}

export function getVideoTokenCells(model: PricingModel): VideoTokenCell[] {
  const table = model.video_token_price
  if (!table) return []

  const cells: VideoTokenCell[] = []
  for (const resolution of VIDEO_TOKEN_RESOLUTIONS) {
    for (const hasVideo of [false, true]) {
      const key = hasVideo ? `${resolution}_video` : resolution
      const usdPerM = Number(table[key])
      if (!Number.isFinite(usdPerM) || usdPerM <= 0) continue
      cells.push({ key, resolution, hasVideo, usdPerM })
    }
  }
  return cells
}

export function getVideoTokenPriceRange(
  model: PricingModel
): { min: number; max: number } | null {
  const values = getVideoTokenCells(model).map((cell) => cell.usdPerM)
  if (values.length === 0) return null
  return { min: Math.min(...values), max: Math.max(...values) }
}

export function formatVideoTokenUnitPrice(
  usdPerM: number,
  options: VideoTokenFormatOptions
): string {
  return formatDynamicUnitPrice(usdPerM, options)
}

export function getVideoTokenCompactSummary(
  model: PricingModel,
  options: VideoTokenFormatOptions
): { formatted: string; filled: number } | null {
  const range = getVideoTokenPriceRange(model)
  if (!range) return null
  const filled = getVideoTokenCells(model).length
  const min = formatVideoTokenUnitPrice(range.min, options)
  const max = formatVideoTokenUnitPrice(range.max, options)
  return {
    formatted: range.min === range.max ? min : `${min} – ${max}`,
    filled,
  }
}
