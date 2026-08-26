import { formatBillingCurrencyFromUSD } from '@/lib/currency'

import type { PricingModel } from '../types'

export type TaskUnitTierCell = {
  key: string
  usdPerUnit: number
}

export function isTaskUnitTierPricingModel(model: PricingModel): boolean {
  return model.billing_mode === 'task_unit_tier'
}

export function getTaskUnitTierCells(model: PricingModel): TaskUnitTierCell[] {
  const table = model.task_unit_tier_price
  if (!table) return []
  const cells: TaskUnitTierCell[] = []
  for (const [key, raw] of Object.entries(table)) {
    const usdPerUnit = Number(raw)
    if (!key.trim() || !Number.isFinite(usdPerUnit) || usdPerUnit <= 0) continue
    cells.push({ key, usdPerUnit })
  }
  return cells.sort((a, b) => a.key.localeCompare(b.key))
}

export function getTaskUnitTierPriceRange(
  model: PricingModel
): { min: number; max: number } | null {
  const values = getTaskUnitTierCells(model).map((cell) => cell.usdPerUnit)
  if (values.length === 0) return null
  return { min: Math.min(...values), max: Math.max(...values) }
}

function formatTaskUnitPrice(
  usdPerUnit: number,
  options: {
    showRechargePrice?: boolean
    priceRate?: number
    usdExchangeRate?: number
    groupRatioMultiplier?: number
  }
): string {
  const groupRatio = options.groupRatioMultiplier ?? 1
  const priceRate = options.priceRate ?? 1
  const usdExchangeRate = options.usdExchangeRate ?? 1
  let display = usdPerUnit * groupRatio
  if (options.showRechargePrice) {
    display = display * priceRate * usdExchangeRate
  }
  return formatBillingCurrencyFromUSD(display, {
    digitsLarge: 4,
    digitsSmall: 6,
    abbreviate: false,
  })
}

export function getTaskUnitTierCompactSummary(
  model: PricingModel,
  options: {
    showRechargePrice?: boolean
    priceRate?: number
    usdExchangeRate?: number
    groupRatioMultiplier?: number
  }
): { formatted: string; filled: number } | null {
  const range = getTaskUnitTierPriceRange(model)
  if (!range) return null
  const filled = getTaskUnitTierCells(model).length
  const min = formatTaskUnitPrice(range.min, options)
  const max = formatTaskUnitPrice(range.max, options)
  return {
    formatted: range.min === range.max ? min : `${min} – ${max}`,
    filled,
  }
}

export { formatTaskUnitPrice }
