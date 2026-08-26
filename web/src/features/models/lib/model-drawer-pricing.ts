import type {
  ModelRatioData,
  PricingMode,
  TaskUnitTierPriceTable,
} from '@/features/system-settings/models/model-pricing-core'

export function isDrawerLockedPricingMode(mode?: string): boolean {
  return mode === 'video_token' || mode === 'tiered_expr'
}

export function drawerLockedPricingNoticeKey(mode?: string): string | null {
  if (mode === 'video_token') {
    return 'This model uses video-token billing. Edit it under System Settings → Model Pricing.'
  }
  if (mode === 'tiered_expr') {
    return 'This model uses expression billing. Edit it under System Settings → Model Pricing.'
  }
  return null
}

export function resolveDrawerCommitPricingData(args: {
  loaded: ModelRatioData | null
  name: string
  pricingMode: PricingMode
  values: {
    price?: string
    ratio?: string
    cacheRatio?: string
    completionRatio?: string
    imageRatio?: string
    audioRatio?: string
    audioCompletionRatio?: string
  }
  taskUnitTierPrice: TaskUnitTierPriceTable
}): ModelRatioData {
  const base: ModelRatioData = args.loaded
    ? { ...args.loaded, name: args.name }
    : { name: args.name }
  if (isDrawerLockedPricingMode(base.billingMode)) {
    return base
  }
  return {
    ...base,
    billingMode: args.pricingMode,
    price: args.values.price,
    ratio: args.values.ratio,
    cacheRatio: args.values.cacheRatio,
    completionRatio: args.values.completionRatio,
    imageRatio: args.values.imageRatio,
    audioRatio: args.values.audioRatio,
    audioCompletionRatio: args.values.audioCompletionRatio,
    taskUnitTierPrice: args.taskUnitTierPrice,
  }
}

export function shouldBlockModelDrawerSave(args: {
  optionsStatus: 'pending' | 'error' | 'success'
  hasSettings: boolean
  pricingMode: PricingMode
  rowErrors: Record<string, string>
}): boolean {
  if (args.optionsStatus !== 'success' || !args.hasSettings) return true
  if (
    args.pricingMode === 'task_unit_tier' &&
    Object.keys(args.rowErrors).length > 0
  ) {
    return true
  }
  return false
}

export function modelDrawerOptionsStatusMessage(
  status: 'pending' | 'error' | 'success',
  hasSettings: boolean
): string | null {
  if (status === 'pending') return 'Loading pricing settings...'
  if (status === 'error' || !hasSettings) {
    return 'Pricing settings are unavailable. Save is disabled.'
  }
  return null
}
