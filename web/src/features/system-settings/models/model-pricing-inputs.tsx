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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Checkbox } from '@/components/ui/checkbox'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'
import { Label } from '@/components/ui/label'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { cn } from '@/lib/utils'

import {
  SettingsControlGroup,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import {
  VIDEO_TOKEN_RESOLUTIONS,
  VIDEO_TOKEN_VARIANTS,
  VIDEO_TOKEN_VARIANT_LABELS,
  numericDraftRegex,
  videoTokenCellKey,
  videoTokenTableShape,
  videoTokenVariantColumns,
  type VideoTokenPriceTable,
  type VideoTokenResolution,
  type VideoTokenUnit,
  type VideoTokenVariant,
} from './model-pricing-core'

export function VideoTokenPriceGrid(props: {
  value: VideoTokenPriceTable
  onChange: (next: VideoTokenPriceTable) => void
}) {
  const { t } = useTranslation()
  const shape = videoTokenTableShape(props.value)
  const [unit, setUnit] = useState<VideoTokenUnit>(shape.unit)
  const [variants, setVariants] = useState<VideoTokenVariant[]>(shape.variants)
  const columns = videoTokenVariantColumns(variants)

  // 改单位等于换一整张价目表：$/1M tokens 和 $/秒 差六个数量级，把旧数字原样留在
  // 新单位下会直接算错账，所以清空重填。
  const changeUnit = (next: VideoTokenUnit) => {
    setUnit(next)
    props.onChange({})
  }

  const toggleVariant = (variant: VideoTokenVariant, enabled: boolean) => {
    const next = VIDEO_TOKEN_VARIANTS.filter((candidate) =>
      candidate === variant ? enabled : variants.includes(candidate)
    )
    setVariants(next)
    // 去掉一个维度后，它对应的格子不能留在表里：后端按 key 精确匹配，
    // 残留的 _audio 格子会让请求落到一个界面上已经看不见的价格上。
    props.onChange(
      Object.fromEntries(
        Object.entries(props.value).filter(([key]) =>
          key
            .split('_')
            .slice(1)
            .every((token) => next.includes(token as VideoTokenVariant))
        )
      )
    )
  }

  const updateCell = (
    resolution: VideoTokenResolution,
    column: VideoTokenVariant[],
    raw: string
  ) => {
    if (!numericDraftRegex.test(raw)) return
    props.onChange({
      ...props.value,
      [videoTokenCellKey(unit, resolution, column)]: raw,
    })
  }

  const columnLabel = (column: VideoTokenVariant[]) =>
    column.length === 0
      ? t('Base')
      : column.map((v) => t(VIDEO_TOKEN_VARIANT_LABELS[v])).join(' + ')

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center gap-x-4 gap-y-2'>
        <ToggleGroup
          value={[unit]}
          onValueChange={(value) => {
            const next = value.find((item) => item !== unit)
            if (next) changeUnit(next as VideoTokenUnit)
          }}
          aria-label={t('Billing unit')}
          variant='outline'
          spacing={2}
        >
          <ToggleGroupItem value='per_token'>
            {t('Per 1M tokens')}
          </ToggleGroupItem>
          <ToggleGroupItem value='per_second'>
            {t('Per second')}
          </ToggleGroupItem>
        </ToggleGroup>
        <div className='flex flex-wrap items-center gap-3'>
          {VIDEO_TOKEN_VARIANTS.map((variant) => (
            <Label
              key={variant}
              className='text-muted-foreground flex items-center gap-1.5 text-xs font-normal'
            >
              <Checkbox
                checked={variants.includes(variant)}
                onCheckedChange={(checked) =>
                  toggleVariant(variant, checked === true)
                }
              />
              {t(VIDEO_TOKEN_VARIANT_LABELS[variant])}
            </Label>
          ))}
        </div>
      </div>
      <div className='overflow-x-auto'>
        <table className='w-full min-w-[28rem] border-collapse text-sm'>
          <thead>
            <tr className='text-muted-foreground text-left'>
              <th className='px-2 py-2 font-medium'>{t('Resolution')}</th>
              {columns.map((column) => (
                <th
                  key={columnLabel(column)}
                  className='px-2 py-2 font-medium'
                >
                  {columnLabel(column)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {VIDEO_TOKEN_RESOLUTIONS.map((resolution) => (
              <tr key={resolution} className='border-border/60 border-t'>
                <td className='px-2 py-3 font-medium uppercase'>
                  {resolution}
                </td>
                {columns.map((column) => (
                  <td key={columnLabel(column)} className='px-2 py-3'>
                    <PriceInput
                      value={
                        props.value[
                          videoTokenCellKey(unit, resolution, column)
                        ] || ''
                      }
                      placeholder={unit === 'per_second' ? '0.6' : '7'}
                      unitLabel={unit === 'per_second' ? '$/s' : '$/1M'}
                      onChange={(value) =>
                        updateCell(resolution, column, value)
                      }
                    />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export function PriceInput(props: {
  value: string
  placeholder?: string
  disabled?: boolean
  unitLabel?: string
  onChange: (value: string) => void
}) {
  return (
    <InputGroup>
      <InputGroupAddon>$</InputGroupAddon>
      <InputGroupInput
        inputMode='decimal'
        value={props.value}
        placeholder={props.placeholder}
        disabled={props.disabled}
        onChange={(event) => props.onChange(event.target.value)}
      />
      <InputGroupAddon align='inline-end'>
        {props.unitLabel ?? '$/1M'}
      </InputGroupAddon>
    </InputGroup>
  )
}

export function PriceLane(props: {
  title: string
  description: string
  placeholder: string
  value: string
  enabled: boolean
  disabled?: boolean
  onEnabledChange: (checked: boolean) => void
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const effectiveDisabled = props.disabled || !props.enabled

  return (
    <SettingsControlGroup
      className={cn('space-y-3', effectiveDisabled && 'opacity-75')}
      data-disabled={effectiveDisabled || undefined}
    >
      <SettingsSwitchField
        checked={props.enabled}
        disabled={props.disabled}
        onCheckedChange={props.onEnabledChange}
        label={props.title}
        description={props.description}
        aria-label={props.title}
      />
      <PriceInput
        value={props.value}
        placeholder={props.placeholder}
        disabled={effectiveDisabled}
        onChange={props.onChange}
      />
      <p className='text-muted-foreground text-xs'>
        {props.enabled
          ? t('USD price per 1M tokens.')
          : t('Disabled lanes are omitted on save.')}
      </p>
    </SettingsControlGroup>
  )
}
