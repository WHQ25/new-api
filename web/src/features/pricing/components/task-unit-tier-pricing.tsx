import { useTranslation } from 'react-i18next'

import { getDisplayGroupRatio } from '../lib/model-helpers'
import {
  formatTaskUnitPrice,
  getTaskUnitTierCells,
  getTaskUnitTierCompactSummary,
} from '../lib/task-unit-tier-price'
import type { PricingModel } from '../types'

export function TaskUnitTierPriceSummary(props: {
  model: PricingModel
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
  const summary = getTaskUnitTierCompactSummary(props.model, {
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
        / {t('unit')}
        {summary.filled > 1 &&
          ` · ${t('{{count}} tiers', { count: summary.filled })}`}
      </div>
    </div>
  )
}

export function TaskUnitTierPriceGrid(props: {
  model: PricingModel
  showRechargePrice?: boolean
  priceRate?: number
  usdExchangeRate?: number
  groupRatioMultiplier?: number
}) {
  const { t } = useTranslation()
  const options = {
    showRechargePrice: props.showRechargePrice,
    priceRate: props.priceRate,
    usdExchangeRate: props.usdExchangeRate,
    groupRatioMultiplier: props.groupRatioMultiplier ?? 1,
  }
  const cells = getTaskUnitTierCells(props.model)

  if (cells.length === 0) {
    return (
      <p className='text-muted-foreground text-sm'>{t('Unset price')}</p>
    )
  }

  return (
    <div className='overflow-x-auto'>
      <table className='w-full min-w-[16rem] border-collapse text-sm'>
        <thead>
          <tr className='text-muted-foreground text-left'>
            <th className='px-2 py-2 font-medium'>{t('Tier key')}</th>
            <th className='px-2 py-2 font-medium'>{t('USD / unit')}</th>
          </tr>
        </thead>
        <tbody>
          {cells.map((cell) => (
            <tr key={cell.key} className='border-border/60 border-t'>
              <td className='px-2 py-2.5 font-mono'>{cell.key}</td>
              <td className='px-2 py-2.5 font-mono tabular-nums'>
                {formatTaskUnitPrice(cell.usdPerUnit, options)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className='text-muted-foreground/40 mt-1.5 text-[10px]'>
        {t('Prices shown per')} {t('unit')}
      </p>
    </div>
  )
}
