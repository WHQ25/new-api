import type { PricingModel, TokenUnit } from '../types'
import { formatDynamicUnitPrice } from './dynamic-price'

export const VIDEO_TOKEN_RESOLUTIONS = ['480p', '720p', '1080p', '4k'] as const
export const VIDEO_TOKEN_VARIANTS = ['video', 'audio'] as const
export const VIDEO_TOKEN_SECOND_PREFIX = 'sec:'

export type VideoTokenResolution = (typeof VIDEO_TOKEN_RESOLUTIONS)[number]
export type VideoTokenVariant = (typeof VIDEO_TOKEN_VARIANTS)[number]
export type VideoTokenUnit = 'per_token' | 'per_second'

export const VIDEO_TOKEN_VARIANT_LABELS: Record<VideoTokenVariant, string> = {
  video: 'With video input',
  audio: 'With audio',
}

export type VideoTokenCell = {
  key: string
  resolution: VideoTokenResolution
  variants: VideoTokenVariant[]
  price: number
}

type VideoTokenFormatOptions = {
  tokenUnit: TokenUnit
  showRechargePrice?: boolean
  priceRate?: number
  usdExchangeRate?: number
  groupRatioMultiplier?: number
}

/**
 * Builds the canonical tariff cell key. This is the single definition of the key
 * grammar shared with the backend's VideoTokenPriceKey: unit prefix, resolution,
 * then the variant flags in VIDEO_TOKEN_VARIANTS order, each at most once.
 */
export function videoTokenCellKey(
  unit: VideoTokenUnit,
  resolution: VideoTokenResolution,
  variants: readonly VideoTokenVariant[]
): string {
  const suffix = VIDEO_TOKEN_VARIANTS.filter((variant) =>
    variants.includes(variant)
  )
    .map((variant) => `_${variant}`)
    .join('')
  const prefix = unit === 'per_second' ? VIDEO_TOKEN_SECOND_PREFIX : ''
  return `${prefix}${resolution}${suffix}`
}

/**
 * Splits a tariff cell key into its dimensions, rejecting anything the backend
 * would never build. A key is valid only if it round-trips through
 * videoTokenCellKey unchanged, so "sec:1080p_audio_video" and "720p_audio_audio"
 * are refused: the backend only ever looks up "sec:1080p_video_audio" and
 * "720p_audio", and accepting the non-canonical spellings would show an operator
 * a configured price that every request then rejects with a 400.
 */
export function parseVideoTokenKey(key: string): VideoTokenCell | null {
  const perSecond = key.startsWith(VIDEO_TOKEN_SECOND_PREFIX)
  const [resolution, ...variants] = (
    perSecond ? key.slice(VIDEO_TOKEN_SECOND_PREFIX.length) : key
  ).split('_')
  if (
    !VIDEO_TOKEN_RESOLUTIONS.includes(resolution as VideoTokenResolution) ||
    !variants.every((v) => VIDEO_TOKEN_VARIANTS.includes(v as VideoTokenVariant))
  ) {
    return null
  }
  const cell: VideoTokenCell = {
    key,
    resolution: resolution as VideoTokenResolution,
    variants: variants as VideoTokenVariant[],
    price: 0,
  }
  const unit: VideoTokenUnit = perSecond ? 'per_second' : 'per_token'
  if (videoTokenCellKey(unit, cell.resolution, cell.variants) !== key) {
    return null
  }
  return cell
}

export function getVideoTokenUnit(model: PricingModel): VideoTokenUnit {
  return Object.keys(model.video_token_price ?? {}).some((key) =>
    key.startsWith(VIDEO_TOKEN_SECOND_PREFIX)
  )
    ? 'per_second'
    : 'per_token'
}

export function isVideoTokenPricingModel(model: PricingModel): boolean {
  return (
    model.billing_mode === 'video_token' && getVideoTokenCells(model).length > 0
  )
}

export function getVideoTokenCells(model: PricingModel): VideoTokenCell[] {
  const table = model.video_token_price
  if (!table) return []

  const cells: VideoTokenCell[] = []
  for (const [key, raw] of Object.entries(table)) {
    const cell = parseVideoTokenKey(key)
    if (!cell) continue
    const price = Number(raw)
    if (!Number.isFinite(price) || price <= 0) continue
    cells.push({ ...cell, price })
  }
  return cells
}

/**
 * Columns of a tariff grid: every variant combination the backend can look up.
 * The dimensions are independent — a request can carry video input without
 * audio, audio without video input, or both — so the grid is the full subset
 * lattice of the selected variants, not a prefix ladder.
 */
export function videoTokenVariantColumns(
  variants: readonly VideoTokenVariant[]
): VideoTokenVariant[][] {
  return VIDEO_TOKEN_VARIANTS.filter((variant) => variants.includes(variant))
    .reduce<VideoTokenVariant[][]>(
      (columns, variant) => [
        ...columns,
        ...columns.map((column) => [...column, variant]),
      ],
      [[]]
    )
    .sort((a, b) => a.length - b.length)
}

/**
 * Variant columns present in the table, ordered least to most qualified. The
 * tie-break keeps same-width columns in VIDEO_TOKEN_VARIANTS order so this table
 * and the admin editor's grid lay the combinations out identically, instead of
 * inheriting whatever order the serialized price map happened to arrive in.
 */
export function getVideoTokenVariantColumns(
  model: PricingModel
): VideoTokenVariant[][] {
  const seen = new Map<string, VideoTokenVariant[]>()
  for (const cell of getVideoTokenCells(model)) {
    seen.set(cell.variants.join('_'), cell.variants)
  }
  const rank = (variants: VideoTokenVariant[]) =>
    variants.reduce(
      (acc, variant) => acc + (1 << VIDEO_TOKEN_VARIANTS.indexOf(variant)),
      0
    )
  return [...seen.values()].sort(
    (a, b) => a.length - b.length || rank(a) - rank(b)
  )
}

export function getVideoTokenPriceRange(
  model: PricingModel
): { min: number; max: number } | null {
  const values = getVideoTokenCells(model).map((cell) => cell.price)
  if (values.length === 0) return null
  return { min: Math.min(...values), max: Math.max(...values) }
}

/**
 * Per-second prices are absolute: they must not be rescaled by the 1K/1M token
 * unit selector the way per-1M-token prices are.
 */
export function formatVideoTokenUnitPrice(
  price: number,
  unit: VideoTokenUnit,
  options: VideoTokenFormatOptions
): string {
  return formatDynamicUnitPrice(price, {
    ...options,
    tokenUnit: unit === 'per_second' ? 'M' : options.tokenUnit,
  })
}

export function getVideoTokenCompactSummary(
  model: PricingModel,
  options: VideoTokenFormatOptions
): { formatted: string; filled: number; unit: VideoTokenUnit } | null {
  const range = getVideoTokenPriceRange(model)
  if (!range) return null
  const unit = getVideoTokenUnit(model)
  const filled = getVideoTokenCells(model).length
  const min = formatVideoTokenUnitPrice(range.min, unit, options)
  const max = formatVideoTokenUnitPrice(range.max, unit, options)
  return {
    formatted: range.min === range.max ? min : `${min} – ${max}`,
    filled,
    unit,
  }
}
