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
import { combineBillingExpr } from '@/features/pricing/lib/billing-expr'

import {
  parseTaskUnitTierPriceTable,
  parseVideoTokenPriceTable,
  serializeTaskUnitTierPriceTable,
  serializeVideoTokenPriceTable,
  type ModelRatioData,
  type PricingMode,
} from './model-pricing-core'

export type PricingOptionMaps = {
  price: Record<string, number>
  ratio: Record<string, number>
  cache: Record<string, number>
  createCache: Record<string, number>
  completion: Record<string, number>
  image: Record<string, number>
  audio: Record<string, number>
  audioCompletion: Record<string, number>
  billingMode: Record<string, string>
  billingExpr: Record<string, string>
  videoTokenPrice: Record<string, Record<string, number>>
  taskUnitTierPrice: Record<string, Record<string, number>>
}

export function resolveEditorPricingMode(data: {
  billingMode?: string
  price?: string
}): PricingMode {
  if (
    data.billingMode === 'tiered_expr' ||
    data.billingMode === 'video_token' ||
    data.billingMode === 'task_unit_tier'
  ) {
    return data.billingMode
  }
  if (data.price && data.price !== '') {
    return 'per-request'
  }
  return 'per-token'
}

function setIfPresent(
  target: Record<string, number>,
  name: string,
  value: string | undefined
) {
  if (!value || value === '') return
  const parsed = Number.parseFloat(value)
  if (Number.isFinite(parsed)) target[name] = parsed
}

function parseJsonMap<T>(raw?: string): Record<string, T> {
  if (!raw) return {}
  try {
    const parsed = JSON.parse(raw) as unknown
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, T>
    }
  } catch {
    return {}
  }
  return {}
}

export function mapsFromSettings(settings: {
  ModelPrice?: string
  ModelRatio?: string
  CacheRatio?: string
  CreateCacheRatio?: string
  CompletionRatio?: string
  ImageRatio?: string
  AudioRatio?: string
  AudioCompletionRatio?: string
  'billing_setting.billing_mode'?: string
  'billing_setting.billing_expr'?: string
  'billing_setting.video_token_price'?: string
  'billing_setting.task_unit_tier_price'?: string
}): PricingOptionMaps {
  return {
    price: parseJsonMap<number>(settings.ModelPrice),
    ratio: parseJsonMap<number>(settings.ModelRatio),
    cache: parseJsonMap<number>(settings.CacheRatio),
    createCache: parseJsonMap<number>(settings.CreateCacheRatio),
    completion: parseJsonMap<number>(settings.CompletionRatio),
    image: parseJsonMap<number>(settings.ImageRatio),
    audio: parseJsonMap<number>(settings.AudioRatio),
    audioCompletion: parseJsonMap<number>(settings.AudioCompletionRatio),
    billingMode: parseJsonMap<string>(settings['billing_setting.billing_mode']),
    billingExpr: parseJsonMap<string>(settings['billing_setting.billing_expr']),
    videoTokenPrice: parseJsonMap<Record<string, number>>(
      settings['billing_setting.video_token_price']
    ),
    taskUnitTierPrice: parseJsonMap<Record<string, number>>(
      settings['billing_setting.task_unit_tier_price']
    ),
  }
}

export function pricingMapsToOptionValues(maps: PricingOptionMaps) {
  return {
    ModelPrice: JSON.stringify(maps.price),
    ModelRatio: JSON.stringify(maps.ratio),
    CacheRatio: JSON.stringify(maps.cache),
    CreateCacheRatio: JSON.stringify(maps.createCache),
    CompletionRatio: JSON.stringify(maps.completion),
    ImageRatio: JSON.stringify(maps.image),
    AudioRatio: JSON.stringify(maps.audio),
    AudioCompletionRatio: JSON.stringify(maps.audioCompletion),
    'billing_setting.billing_mode': JSON.stringify(maps.billingMode),
    'billing_setting.billing_expr': JSON.stringify(maps.billingExpr),
    'billing_setting.video_token_price': JSON.stringify(maps.videoTokenPrice),
    'billing_setting.task_unit_tier_price': JSON.stringify(
      maps.taskUnitTierPrice
    ),
  }
}

export function emptyPricingMaps(): PricingOptionMaps {
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

export function deleteModelFromPricingMaps(
  maps: PricingOptionMaps,
  name: string
) {
  delete maps.price[name]
  delete maps.ratio[name]
  delete maps.cache[name]
  delete maps.createCache[name]
  delete maps.completion[name]
  delete maps.image[name]
  delete maps.audio[name]
  delete maps.audioCompletion[name]
  delete maps.billingMode[name]
  delete maps.billingExpr[name]
  delete maps.videoTokenPrice[name]
  delete maps.taskUnitTierPrice[name]
}

export function clonePricingMaps(maps: PricingOptionMaps): PricingOptionMaps {
  return {
    price: { ...maps.price },
    ratio: { ...maps.ratio },
    cache: { ...maps.cache },
    createCache: { ...maps.createCache },
    completion: { ...maps.completion },
    image: { ...maps.image },
    audio: { ...maps.audio },
    audioCompletion: { ...maps.audioCompletion },
    billingMode: { ...maps.billingMode },
    billingExpr: { ...maps.billingExpr },
    videoTokenPrice: { ...maps.videoTokenPrice },
    taskUnitTierPrice: { ...maps.taskUnitTierPrice },
  }
}

export function readModelRatioDataFromMaps(
  maps: PricingOptionMaps,
  name: string
): ModelRatioData {
  const billingMode = resolveEditorPricingMode({
    billingMode: maps.billingMode[name],
    price:
      maps.price[name] !== undefined ? String(maps.price[name]) : undefined,
  })
  return {
    name,
    billingMode,
    price: maps.price[name] !== undefined ? String(maps.price[name]) : '',
    ratio: maps.ratio[name] !== undefined ? String(maps.ratio[name]) : '',
    cacheRatio: maps.cache[name] !== undefined ? String(maps.cache[name]) : '',
    createCacheRatio:
      maps.createCache[name] !== undefined
        ? String(maps.createCache[name])
        : '',
    completionRatio:
      maps.completion[name] !== undefined ? String(maps.completion[name]) : '',
    imageRatio: maps.image[name] !== undefined ? String(maps.image[name]) : '',
    audioRatio: maps.audio[name] !== undefined ? String(maps.audio[name]) : '',
    audioCompletionRatio:
      maps.audioCompletion[name] !== undefined
        ? String(maps.audioCompletion[name])
        : '',
    billingExpr: maps.billingExpr[name] || '',
    videoTokenPrice: maps.videoTokenPrice[name]
      ? parseVideoTokenPriceTable(maps.videoTokenPrice[name])
      : undefined,
    taskUnitTierPrice: maps.taskUnitTierPrice[name]
      ? parseTaskUnitTierPriceTable(maps.taskUnitTierPrice[name])
      : undefined,
  }
}

function isLockedDrawerBillingMode(mode?: string) {
  return mode === 'video_token' || mode === 'tiered_expr'
}

type CapturedModelPricing = {
  price?: number
  ratio?: number
  cache?: number
  createCache?: number
  completion?: number
  image?: number
  audio?: number
  audioCompletion?: number
  billingMode?: string
  billingExpr?: string
  videoTokenPrice?: Record<string, number>
  taskUnitTierPrice?: Record<string, number>
}

function captureModelPricing(
  maps: PricingOptionMaps,
  name: string
): CapturedModelPricing {
  return {
    price: maps.price[name],
    ratio: maps.ratio[name],
    cache: maps.cache[name],
    createCache: maps.createCache[name],
    completion: maps.completion[name],
    image: maps.image[name],
    audio: maps.audio[name],
    audioCompletion: maps.audioCompletion[name],
    billingMode: maps.billingMode[name],
    billingExpr: maps.billingExpr[name],
    videoTokenPrice: maps.videoTokenPrice[name]
      ? { ...maps.videoTokenPrice[name] }
      : undefined,
    taskUnitTierPrice: maps.taskUnitTierPrice[name]
      ? { ...maps.taskUnitTierPrice[name] }
      : undefined,
  }
}

function restoreModelPricing(
  maps: PricingOptionMaps,
  name: string,
  captured: CapturedModelPricing
) {
  if (captured.price !== undefined) maps.price[name] = captured.price
  if (captured.ratio !== undefined) maps.ratio[name] = captured.ratio
  if (captured.cache !== undefined) maps.cache[name] = captured.cache
  if (captured.createCache !== undefined) {
    maps.createCache[name] = captured.createCache
  }
  if (captured.completion !== undefined) {
    maps.completion[name] = captured.completion
  }
  if (captured.image !== undefined) maps.image[name] = captured.image
  if (captured.audio !== undefined) maps.audio[name] = captured.audio
  if (captured.audioCompletion !== undefined) {
    maps.audioCompletion[name] = captured.audioCompletion
  }
  if (captured.billingMode !== undefined) {
    maps.billingMode[name] = captured.billingMode
  }
  if (captured.billingExpr !== undefined) {
    maps.billingExpr[name] = captured.billingExpr
  }
  if (captured.videoTokenPrice) {
    maps.videoTokenPrice[name] = captured.videoTokenPrice
  }
  if (captured.taskUnitTierPrice) {
    maps.taskUnitTierPrice[name] = captured.taskUnitTierPrice
  }
}

export function commitDrawerPricing(
  maps: PricingOptionMaps,
  args: {
    oldName?: string
    newName: string
    data: ModelRatioData
    loadedName: string
  }
): PricingOptionMaps {
  const next = clonePricingMaps(maps)
  const sourceName =
    args.oldName && args.oldName !== args.newName ? args.oldName : args.newName
  const captured = captureModelPricing(next, sourceName)
  if (args.oldName && args.oldName !== args.newName) {
    deleteModelFromPricingMaps(next, args.oldName)
  }
  if (
    isLockedDrawerBillingMode(captured.billingMode) &&
    args.data.billingMode !== captured.billingMode
  ) {
    deleteModelFromPricingMaps(next, args.newName)
    restoreModelPricing(next, args.newName, captured)
    return next
  }
  let data: ModelRatioData = { ...args.data, name: args.newName }
  if (
    captured.billingMode === 'video_token' &&
    data.billingMode === 'video_token' &&
    Object.keys(serializeVideoTokenPriceTable(data.videoTokenPrice)).length ===
      0 &&
    captured.videoTokenPrice
  ) {
    data = {
      ...data,
      videoTokenPrice: parseVideoTokenPriceTable(captured.videoTokenPrice),
    }
  }
  if (
    captured.billingMode === 'tiered_expr' &&
    data.billingMode === 'tiered_expr' &&
    !data.billingExpr &&
    captured.billingExpr
  ) {
    data = { ...data, billingExpr: captured.billingExpr }
  }
  const hasPricing =
    Boolean(data.price) ||
    Boolean(data.ratio) ||
    data.billingMode === 'task_unit_tier' ||
    data.billingMode === 'video_token' ||
    data.billingMode === 'tiered_expr'
  if (hasPricing || args.newName === args.loadedName) {
    applyModelRatioDataToMaps(data, next, [args.newName])
  }
  return next
}

export function applyModelRatioDataToMaps(
  data: ModelRatioData,
  maps: PricingOptionMaps,
  targetNames: string[] = [data.name]
): PricingOptionMaps {
  targetNames.forEach((name) => {
    delete maps.price[name]
    delete maps.ratio[name]
    delete maps.cache[name]
    delete maps.createCache[name]
    delete maps.completion[name]
    delete maps.image[name]
    delete maps.audio[name]
    delete maps.audioCompletion[name]
    delete maps.billingMode[name]
    delete maps.billingExpr[name]
    delete maps.videoTokenPrice[name]
    delete maps.taskUnitTierPrice[name]

    if (data.billingMode === 'video_token') {
      const table = serializeVideoTokenPriceTable(data.videoTokenPrice)
      maps.billingMode[name] = 'video_token'
      if (Object.keys(table).length > 0) {
        maps.videoTokenPrice[name] = table
      }
    } else if (data.billingMode === 'task_unit_tier') {
      const table = serializeTaskUnitTierPriceTable(data.taskUnitTierPrice)
      maps.billingMode[name] = 'task_unit_tier'
      if (Object.keys(table).length > 0) {
        maps.taskUnitTierPrice[name] = table
      }
    } else if (data.billingMode === 'tiered_expr') {
      const combined = combineBillingExpr(
        data.billingExpr || '',
        data.requestRuleExpr || ''
      )
      if (combined) {
        maps.billingMode[name] = 'tiered_expr'
        maps.billingExpr[name] = combined
      }
      setIfPresent(maps.price, name, data.price)
      setIfPresent(maps.ratio, name, data.ratio)
      setIfPresent(maps.cache, name, data.cacheRatio)
      setIfPresent(maps.createCache, name, data.createCacheRatio)
      setIfPresent(maps.completion, name, data.completionRatio)
      setIfPresent(maps.image, name, data.imageRatio)
      setIfPresent(maps.audio, name, data.audioRatio)
      setIfPresent(maps.audioCompletion, name, data.audioCompletionRatio)
    } else if (data.price && data.price !== '') {
      setIfPresent(maps.price, name, data.price)
    } else {
      setIfPresent(maps.ratio, name, data.ratio)
      setIfPresent(maps.cache, name, data.cacheRatio)
      setIfPresent(maps.createCache, name, data.createCacheRatio)
      setIfPresent(maps.completion, name, data.completionRatio)
      setIfPresent(maps.image, name, data.imageRatio)
      setIfPresent(maps.audio, name, data.audioRatio)
      setIfPresent(maps.audioCompletion, name, data.audioCompletionRatio)
    }
  })
  return maps
}
