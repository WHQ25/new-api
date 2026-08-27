import { useTranslation } from 'react-i18next'

import { getDisplayGroupRatio } from '../lib/model-helpers'
import {
  VIDEO_TOKEN_RESOLUTIONS,
  VIDEO_TOKEN_VARIANT_LABELS,
  formatVideoTokenUnitPrice,
  getVideoTokenCompactSummary,
  getVideoTokenUnit,
  getVideoTokenVariantColumns,
  type VideoTokenResolution,
  type VideoTokenUnit,
  type VideoTokenVariant,
} from '../lib/video-token-price'
import type { PricingModel, TokenUnit } from '../types'

type VideoTokenFormatOptions = {
  tokenUnit: TokenUnit
  showRechargePrice?: boolean
  priceRate?: number
  usdExchangeRate?: number
  groupRatioMultiplier?: number
}

function unitSuffix(unit: VideoTokenUnit, tokenUnit: TokenUnit): string {
  if (unit === 'per_second') return 's'
  return `${tokenUnit === 'K' ? '1K' : '1M'} tokens`
}

export function VideoTokenPriceSummary(props: {
  model: PricingModel
  tokenUnit: TokenUnit
  showRechargePrice?: boolean
  priceRate?: number
  usdExchangeRate?: number
  selectedGroup?: string
}) {
  const { t } = useTranslation()
  const groupRatioMultiplier = getDisplayGroupRatio(
    props.model,
    props.selectedGroup
  )
  const summary = getVideoTokenCompactSummary(props.model, {
    tokenUnit: props.tokenUnit,
    showRechargePrice: props.showRechargePrice,
    priceRate: props.priceRate,
    usdExchangeRate: props.usdExchangeRate,
    groupRatioMultiplier,
  })

  if (!summary) {
    return (
      <span className='text-muted-foreground text-sm'>{t('Unset price')}</span>
    )
  }

  return (
    <div className='max-w-full min-w-0'>
      <span className='font-mono text-sm font-semibold tabular-nums'>
        {summary.formatted}
      </span>
      <div className='text-muted-foreground/50 text-[10px]'>
        / {unitSuffix(summary.unit, props.tokenUnit)}
        {summary.filled > 1 &&
          ` · ${t('{{count}} tiers', { count: summary.filled })}`}
      </div>
    </div>
  )
}

export function VideoTokenPriceGrid(props: {
  model: PricingModel
  tokenUnit: TokenUnit
  showRechargePrice?: boolean
  priceRate?: number
  usdExchangeRate?: number
  groupRatioMultiplier?: number
}) {
  const { t } = useTranslation()
  const options: VideoTokenFormatOptions = {
    tokenUnit: props.tokenUnit,
    showRechargePrice: props.showRechargePrice,
    priceRate: props.priceRate,
    usdExchangeRate: props.usdExchangeRate,
    groupRatioMultiplier: props.groupRatioMultiplier ?? 1,
  }
  const table = props.model.video_token_price || {}
  const unit = getVideoTokenUnit(props.model)
  const columns = getVideoTokenVariantColumns(props.model)

  const cellKey = (
    resolution: VideoTokenResolution,
    variants: VideoTokenVariant[]
  ) =>
    `${unit === 'per_second' ? 'sec:' : ''}${resolution}${variants
      .map((variant) => `_${variant}`)
      .join('')}`

  const columnLabel = (variants: VideoTokenVariant[]) =>
    variants.length === 0
      ? t('Base')
      : variants
          .map((variant) => t(VIDEO_TOKEN_VARIANT_LABELS[variant]))
          .join(' + ')

  const formatCell = (
    resolution: VideoTokenResolution,
    variants: VideoTokenVariant[]
  ) => {
    const price = Number(table[cellKey(resolution, variants)])
    if (!Number.isFinite(price) || price <= 0) return t('Not priced')
    return formatVideoTokenUnitPrice(price, unit, options)
  }

  return (
    <div className='overflow-x-auto'>
      <table className='w-full min-w-[22rem] border-collapse text-sm'>
        <thead>
          <tr className='text-muted-foreground text-left'>
            <th className='px-2 py-2 font-medium'>{t('Resolution')}</th>
            {columns.map((variants) => (
              <th key={columnLabel(variants)} className='px-2 py-2 font-medium'>
                {columnLabel(variants)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {VIDEO_TOKEN_RESOLUTIONS.map((resolution) => (
            <tr key={resolution} className='border-border/60 border-t'>
              <td className='px-2 py-2.5 font-medium uppercase'>
                {resolution}
              </td>
              {columns.map((variants) => (
                <td
                  key={columnLabel(variants)}
                  className='px-2 py-2.5 font-mono tabular-nums'
                >
                  {formatCell(resolution, variants)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <p className='text-muted-foreground/40 mt-1.5 text-[10px]'>
        {t('Prices shown per')} {unitSuffix(unit, props.tokenUnit)}
      </p>
    </div>
  )
}
