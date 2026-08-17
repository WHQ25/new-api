import { useTranslation } from 'react-i18next'

import { getDisplayGroupRatio } from '../lib/model-helpers'
import {
  VIDEO_TOKEN_RESOLUTIONS,
  formatVideoTokenUnitPrice,
  getVideoTokenCompactSummary,
  type VideoTokenResolution,
} from '../lib/video-token-price'
import type { PricingModel, TokenUnit } from '../types'

type VideoTokenFormatOptions = {
  tokenUnit: TokenUnit
  showRechargePrice?: boolean
  priceRate?: number
  usdExchangeRate?: number
  groupRatioMultiplier?: number
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
  const tokenUnitLabel = props.tokenUnit === 'K' ? '1K' : '1M'
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
        / {tokenUnitLabel} tokens
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
  const tokenUnitLabel = props.tokenUnit === 'K' ? '1K' : '1M'
  const options: VideoTokenFormatOptions = {
    tokenUnit: props.tokenUnit,
    showRechargePrice: props.showRechargePrice,
    priceRate: props.priceRate,
    usdExchangeRate: props.usdExchangeRate,
    groupRatioMultiplier: props.groupRatioMultiplier ?? 1,
  }
  const table = props.model.video_token_price || {}

  const formatCell = (resolution: VideoTokenResolution, hasVideo: boolean) => {
    const key = hasVideo ? `${resolution}_video` : resolution
    const usdPerM = Number(table[key])
    if (!Number.isFinite(usdPerM) || usdPerM <= 0) {
      return t('Not priced')
    }
    return formatVideoTokenUnitPrice(usdPerM, options)
  }

  return (
    <div className='overflow-x-auto'>
      <table className='w-full min-w-[22rem] border-collapse text-sm'>
        <thead>
          <tr className='text-muted-foreground text-left'>
            <th className='px-2 py-2 font-medium'>{t('Resolution')}</th>
            <th className='px-2 py-2 font-medium'>{t('No video input')}</th>
            <th className='px-2 py-2 font-medium'>{t('With video input')}</th>
          </tr>
        </thead>
        <tbody>
          {VIDEO_TOKEN_RESOLUTIONS.map((resolution) => (
            <tr key={resolution} className='border-border/60 border-t'>
              <td className='px-2 py-2.5 font-medium uppercase'>
                {resolution}
              </td>
              <td className='px-2 py-2.5 font-mono tabular-nums'>
                {formatCell(resolution, false)}
              </td>
              <td className='px-2 py-2.5 font-mono tabular-nums'>
                {formatCell(resolution, true)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className='text-muted-foreground/40 mt-1.5 text-[10px]'>
        {t('Prices shown per')} {tokenUnitLabel} tokens
      </p>
    </div>
  )
}
