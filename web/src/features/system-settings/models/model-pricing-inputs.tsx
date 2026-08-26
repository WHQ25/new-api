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
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'
import { cn } from '@/lib/utils'

import {
  SettingsControlGroup,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import {
  VIDEO_TOKEN_RESOLUTIONS,
  emptyTaskUnitTierRows,
  numericDraftRegex,
  type TaskUnitTierRow,
  type VideoTokenPriceTable,
  type VideoTokenResolution,
} from './model-pricing-core'

export function VideoTokenPriceGrid(props: {
  value: VideoTokenPriceTable
  onChange: (next: VideoTokenPriceTable) => void
}) {
  const { t } = useTranslation()

  const updateCell = (
    resolution: VideoTokenResolution,
    hasVideo: boolean,
    raw: string
  ) => {
    if (!numericDraftRegex.test(raw)) return
    const key = hasVideo ? `${resolution}_video` : resolution
    props.onChange({ ...props.value, [key]: raw })
  }

  return (
    <div className='overflow-x-auto'>
      <table className='w-full min-w-[28rem] border-collapse text-sm'>
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
              <td className='px-2 py-3 font-medium uppercase'>{resolution}</td>
              <td className='px-2 py-3'>
                <PriceInput
                  value={props.value[resolution] || ''}
                  placeholder='7'
                  onChange={(value) => updateCell(resolution, false, value)}
                />
              </td>
              <td className='px-2 py-3'>
                <PriceInput
                  value={props.value[`${resolution}_video`] || ''}
                  placeholder='4.2'
                  onChange={(value) => updateCell(resolution, true, value)}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function TaskUnitTierPriceEditor(props: {
  value: TaskUnitTierRow[]
  onChange: (next: TaskUnitTierRow[]) => void
  errors?: Record<string, string>
}) {
  const { t } = useTranslation()
  const rows = props.value.length > 0 ? props.value : emptyTaskUnitTierRows()
  const emit = (nextRows: TaskUnitTierRow[]) => {
    props.onChange(nextRows)
  }

  return (
    <div className='space-y-3'>
      <div className='grid grid-cols-[minmax(0,1fr)_8rem_auto] gap-2'>
        <div className='text-muted-foreground text-xs font-medium'>
          {t('Tier key')}
        </div>
        <div className='text-muted-foreground text-xs font-medium'>
          {t('USD / unit')}
        </div>
        <div />
      </div>
      {rows.map((row) => (
        <div key={row.id} className='space-y-1'>
          <div className='grid grid-cols-[minmax(0,1fr)_8rem_auto] items-center gap-2'>
            <Input
              value={row.key}
              placeholder='720p_audio'
              aria-invalid={Boolean(props.errors?.[row.id])}
              onChange={(event) =>
                emit(
                  rows.map((item) =>
                    item.id === row.id
                      ? { ...item, key: event.target.value }
                      : item
                  )
                )
              }
            />
            <PriceInput
              value={row.price}
              placeholder='0.6'
              unitLabel={t('per unit')}
              onChange={(value) => {
                if (!numericDraftRegex.test(value)) return
                emit(
                  rows.map((item) =>
                    item.id === row.id ? { ...item, price: value } : item
                  )
                )
              }}
            />
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='text-destructive h-8 w-8 p-0'
              onClick={() => {
                const next = rows.filter((item) => item.id !== row.id)
                emit(next.length > 0 ? next : emptyTaskUnitTierRows())
              }}
              aria-label={t('Delete')}
            >
              <Trash2 className='h-4 w-4' />
            </Button>
          </div>
          {props.errors?.[row.id] ? (
            <p className='text-destructive text-xs'>{t(props.errors[row.id])}</p>
          ) : null}
        </div>
      ))}
      <Button
        type='button'
        variant='outline'
        size='sm'
        onClick={() =>
          emit([...rows, { id: `row-${Date.now()}`, key: '', price: '' }])
        }
      >
        <Plus className='mr-2 h-4 w-4' />
        {t('Add tier')}
      </Button>
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
        {props.unitLabel || '$/1M'}
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
