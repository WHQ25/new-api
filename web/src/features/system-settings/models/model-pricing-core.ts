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
import * as z from 'zod'

import { combineBillingExpr } from '@/features/pricing/lib/billing-expr'

import { formatPricingNumber } from './pricing-format'

export const createModelPricingSchema = (t: (key: string) => string) =>
  z.object({
    name: z.string().min(1, t('Model name is required')),
    price: z.string().optional(),
    ratio: z.string().optional(),
    cacheRatio: z.string().optional(),
    createCacheRatio: z.string().optional(),
    completionRatio: z.string().optional(),
    imageRatio: z.string().optional(),
    audioRatio: z.string().optional(),
    audioCompletionRatio: z.string().optional(),
  })

export type ModelPricingFormValues = z.infer<
  ReturnType<typeof createModelPricingSchema>
>

export type PricingMode =
  | 'per-token'
  | 'per-request'
  | 'tiered_expr'
  | 'video_token'
  | 'task_unit_tier'

export const VIDEO_TOKEN_RESOLUTIONS = ['480p', '720p', '1080p', '4k'] as const

export type VideoTokenResolution = (typeof VIDEO_TOKEN_RESOLUTIONS)[number]

export type VideoTokenPriceTable = Partial<
  Record<`${VideoTokenResolution}` | `${VideoTokenResolution}_video`, string>
>

export const emptyVideoTokenPriceTable = (): VideoTokenPriceTable => ({
  '480p': '',
  '480p_video': '',
  '720p': '',
  '720p_video': '',
  '1080p': '',
  '1080p_video': '',
  '4k': '',
  '4k_video': '',
})

export const videoTokenPriceKeys: Array<keyof VideoTokenPriceTable> = [
  '480p',
  '480p_video',
  '720p',
  '720p_video',
  '1080p',
  '1080p_video',
  '4k',
  '4k_video',
]

export const parseVideoTokenPriceTable = (
  raw?: Record<string, number> | VideoTokenPriceTable | null
): VideoTokenPriceTable => {
  const table = emptyVideoTokenPriceTable()
  if (!raw) return table
  for (const key of videoTokenPriceKeys) {
    const value = raw[key]
    if (value === undefined || value === null || value === '') continue
    const numeric = typeof value === 'number' ? value : Number(value)
    if (Number.isFinite(numeric) && numeric > 0) {
      table[key] = formatPricingNumber(numeric)
    }
  }
  return table
}

export const serializeVideoTokenPriceTable = (
  table?: VideoTokenPriceTable
): Record<string, number> => {
  const out: Record<string, number> = {}
  if (!table) return out
  for (const key of videoTokenPriceKeys) {
    const numeric = toNumberOrNull(table[key] || '')
    if (numeric !== null && numeric > 0) out[key] = numeric
  }
  return out
}

export const countVideoTokenPrices = (table?: VideoTokenPriceTable) =>
  table
    ? videoTokenPriceKeys.filter((key) => {
        const numeric = toNumberOrNull(table[key] || '')
        return numeric !== null && numeric > 0
      }).length
    : 0

export type TaskUnitTierPriceTable = Record<string, string>

export type TaskUnitTierRow = { id: string; key: string; price: string }

export function emptyTaskUnitTierRows(): TaskUnitTierRow[] {
  return [{ id: 'row-0', key: '', price: '' }]
}

export function taskUnitTierTableToRows(
  table?: TaskUnitTierPriceTable
): TaskUnitTierRow[] {
  const entries = Object.entries(table || {})
  if (entries.length === 0) {
    return emptyTaskUnitTierRows()
  }
  return entries.map(([key, price], index) => ({
    id: `row-${index}-${key}`,
    key,
    price,
  }))
}

export function getTaskUnitTierRowErrors(
  rows: TaskUnitTierRow[]
): Record<string, string> {
  const errors: Record<string, string> = {}
  const seen = new Map<string, string>()
  for (const row of rows) {
    const trimmed = row.key.trim()
    const hasPrice = row.price.trim() !== ''
    if (!trimmed && !hasPrice) continue
    if (!trimmed) {
      errors[row.id] = 'Tier keys must be unique and non-empty.'
      continue
    }
    const firstId = seen.get(trimmed)
    if (firstId) {
      errors[row.id] = 'Tier keys must be unique and non-empty.'
      errors[firstId] = 'Tier keys must be unique and non-empty.'
      continue
    }
    seen.set(trimmed, row.id)
    const numeric = toNumberOrNull(row.price)
    if (numeric === null || numeric <= 0) {
      errors[row.id] = 'Unit tier prices must be finite positive numbers.'
    }
  }
  return errors
}

export const parseTaskUnitTierPriceTable = (
  raw?: Record<string, number> | TaskUnitTierPriceTable | null
): TaskUnitTierPriceTable => {
  const table: TaskUnitTierPriceTable = {}
  if (!raw) return table
  for (const [key, value] of Object.entries(raw)) {
    const trimmed = key.trim()
    if (!trimmed) continue
    const numeric = typeof value === 'number' ? value : Number(value)
    if (Number.isFinite(numeric) && numeric > 0) {
      table[trimmed] = formatPricingNumber(numeric)
    }
  }
  return table
}

export const serializeTaskUnitTierPriceTable = (
  table?: TaskUnitTierPriceTable
): Record<string, number> => {
  const out: Record<string, number> = {}
  if (!table) return out
  for (const [key, value] of Object.entries(table)) {
    const trimmed = key.trim()
    if (!trimmed) continue
    const numeric = toNumberOrNull(value)
    if (numeric !== null && numeric > 0) out[trimmed] = numeric
  }
  return out
}

export const countTaskUnitTierPrices = (table?: TaskUnitTierPriceTable) =>
  Object.keys(serializeTaskUnitTierPriceTable(table)).length

export function getTaskUnitTierValidationError(
  rows: Array<{ key: string; price: string }>
): string | null {
  const seen = new Set<string>()
  let filled = 0
  for (const row of rows) {
    const trimmed = row.key.trim()
    const hasPrice = row.price.trim() !== ''
    if (!trimmed && !hasPrice) continue
    if (!trimmed) return 'Tier keys must be unique and non-empty.'
    if (seen.has(trimmed)) return 'Tier keys must be unique and non-empty.'
    seen.add(trimmed)
    const numeric = toNumberOrNull(row.price)
    if (numeric === null || numeric <= 0) {
      return 'Unit tier prices must be finite positive numbers.'
    }
    filled += 1
  }
  if (filled === 0) return 'Fill at least one unit tier price before saving.'
  return null
}

export type LaneKey =
  | 'completion'
  | 'cache'
  | 'createCache'
  | 'image'
  | 'audioInput'
  | 'audioOutput'

export type ModelRatioData = {
  name: string
  price?: string
  ratio?: string
  cacheRatio?: string
  createCacheRatio?: string
  completionRatio?: string
  imageRatio?: string
  audioRatio?: string
  audioCompletionRatio?: string
  billingMode?: PricingMode
  billingExpr?: string
  requestRuleExpr?: string
  videoTokenPrice?: VideoTokenPriceTable
  taskUnitTierPrice?: TaskUnitTierPriceTable
}

export type PreviewRow = {
  key: string
  label: string
  value: string
  multiline?: boolean
}

export const numericDraftRegex = /^(\d+(\.\d*)?|\.\d*)?$/

export const EMPTY_LANE_PRICES: Record<LaneKey, string> = {
  completion: '',
  cache: '',
  createCache: '',
  image: '',
  audioInput: '',
  audioOutput: '',
}

export const EMPTY_LANE_ENABLED: Record<LaneKey, boolean> = {
  completion: false,
  cache: false,
  createCache: false,
  image: false,
  audioInput: false,
  audioOutput: false,
}

export const ratioFieldByLane: Record<LaneKey, keyof ModelPricingFormValues> = {
  completion: 'completionRatio',
  cache: 'cacheRatio',
  createCache: 'createCacheRatio',
  image: 'imageRatio',
  audioInput: 'audioRatio',
  audioOutput: 'audioCompletionRatio',
}

export const laneConfigs: Array<{
  key: LaneKey
  titleKey: string
  descriptionKey: string
  placeholder: string
}> = [
  {
    key: 'completion',
    titleKey: 'Completion price',
    descriptionKey: 'Output token price for generated tokens.',
    placeholder: '15',
  },
  {
    key: 'cache',
    titleKey: 'Cache read price',
    descriptionKey: 'Token price for cache reads.',
    placeholder: '0.3',
  },
  {
    key: 'createCache',
    titleKey: 'Cache write price',
    descriptionKey: 'Token price for creating cache entries.',
    placeholder: '3.75',
  },
  {
    key: 'image',
    titleKey: 'Image input price',
    descriptionKey: 'Token price for image input.',
    placeholder: '2.5',
  },
  {
    key: 'audioInput',
    titleKey: 'Audio input price',
    descriptionKey: 'Token price for audio input.',
    placeholder: '3.81',
  },
  {
    key: 'audioOutput',
    titleKey: 'Audio output price',
    descriptionKey: 'Token price for audio output.',
    placeholder: '15.11',
  },
]

export function hasValue(value: unknown): boolean {
  return (
    value !== '' && value !== null && value !== undefined && value !== false
  )
}

export function toNumberOrNull(value: unknown): number | null {
  if (!hasValue(value) && value !== 0) return null
  const num = Number(value)
  return Number.isFinite(num) ? num : null
}

function ratioToBasePrice(ratio: unknown): string {
  const num = toNumberOrNull(ratio)
  if (num === null) return ''
  return formatPricingNumber(num * 2)
}

function deriveLanePrice(
  ratio: unknown,
  denominator: unknown,
  fallback = ''
): string {
  const ratioNumber = toNumberOrNull(ratio)
  const denominatorNumber = toNumberOrNull(denominator)
  if (ratioNumber === null || denominatorNumber === null) return fallback
  return formatPricingNumber(ratioNumber * denominatorNumber)
}

export function createInitialLaneState(data?: ModelRatioData | null) {
  if (!data) {
    return {
      promptPrice: '',
      prices: { ...EMPTY_LANE_PRICES },
      enabled: { ...EMPTY_LANE_ENABLED },
    }
  }

  const promptPrice = ratioToBasePrice(data.ratio)
  const audioInputPrice = deriveLanePrice(data.audioRatio, promptPrice)
  const prices: Record<LaneKey, string> = {
    completion: deriveLanePrice(data.completionRatio, promptPrice),
    cache: deriveLanePrice(data.cacheRatio, promptPrice),
    createCache: deriveLanePrice(data.createCacheRatio, promptPrice),
    image: deriveLanePrice(data.imageRatio, promptPrice),
    audioInput: audioInputPrice,
    audioOutput: deriveLanePrice(data.audioCompletionRatio, audioInputPrice),
  }

  return {
    promptPrice,
    prices,
    enabled: {
      completion: hasValue(data.completionRatio),
      cache: hasValue(data.cacheRatio),
      createCache: hasValue(data.createCacheRatio),
      image: hasValue(data.imageRatio),
      audioInput: hasValue(data.audioRatio),
      audioOutput: hasValue(data.audioCompletionRatio),
    },
  }
}

export function buildPreviewRows(
  values: ModelPricingFormValues,
  mode: PricingMode,
  billingExpr: string,
  requestRuleExpr: string,
  promptPrice: string,
  lanePrices: Record<LaneKey, string>,
  laneEnabled: Record<LaneKey, boolean>,
  t: (key: string) => string
): PreviewRow[] {
  if (mode === 'video_token') {
    return [
      { key: 'mode', label: 'BillingMode', value: 'video_token' },
      {
        key: 'hint',
        label: t('Video tiers'),
        value: t('USD price per 1M tokens for each resolution and input type.'),
        multiline: true,
      },
    ]
  }

  if (mode === 'task_unit_tier') {
    return [
      { key: 'mode', label: 'BillingMode', value: 'task_unit_tier' },
      {
        key: 'hint',
        label: t('Unit tiers'),
        value: t('USD per unit for each billing tier key.'),
        multiline: true,
      },
    ]
  }

  if (mode === 'tiered_expr') {
    const effectiveExpr = combineBillingExpr(billingExpr, requestRuleExpr)
    return [
      { key: 'mode', label: 'BillingMode', value: 'tiered_expr' },
      {
        key: 'expr',
        label: t('Expression'),
        value: effectiveExpr || t('Empty'),
        multiline: true,
      },
    ]
  }

  if (mode === 'per-request') {
    return [
      {
        key: 'price',
        label: 'ModelPrice',
        value: values.price || t('Empty'),
      },
    ]
  }

  return [
    {
      key: 'inputPrice',
      label: t('Input price'),
      value: promptPrice ? `$${promptPrice}` : t('Empty'),
    },
    {
      key: 'completion',
      label: t('Completion price'),
      value:
        laneEnabled.completion && lanePrices.completion
          ? `$${lanePrices.completion}`
          : t('Empty'),
    },
    {
      key: 'cache',
      label: t('Cache read price'),
      value:
        laneEnabled.cache && lanePrices.cache
          ? `$${lanePrices.cache}`
          : t('Empty'),
    },
    {
      key: 'createCache',
      label: t('Cache write price'),
      value:
        laneEnabled.createCache && lanePrices.createCache
          ? `$${lanePrices.createCache}`
          : t('Empty'),
    },
    {
      key: 'image',
      label: t('Image input price'),
      value:
        laneEnabled.image && lanePrices.image
          ? `$${lanePrices.image}`
          : t('Empty'),
    },
    {
      key: 'audio',
      label: t('Audio input price'),
      value:
        laneEnabled.audioInput && lanePrices.audioInput
          ? `$${lanePrices.audioInput}`
          : t('Empty'),
    },
    {
      key: 'audioCompletion',
      label: t('Audio output price'),
      value:
        laneEnabled.audioOutput && lanePrices.audioOutput
          ? `$${lanePrices.audioOutput}`
          : t('Empty'),
    },
  ]
}
