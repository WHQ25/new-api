import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { TaskUnitTierPriceEditor } = await import('../model-pricing-inputs')
const { getTaskUnitTierRowErrors } = await import('../model-pricing-core')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Tier key': 'Tier key',
        'USD / unit': 'USD / unit',
        'Add tier': 'Add tier',
        Delete: 'Delete',
        'per unit': 'per unit',
        'Tier keys must be unique and non-empty.':
          'Tier keys must be unique and non-empty.',
      },
    },
  },
})

describe('TaskUnitTierPriceEditor rows', () => {
  it('keeps duplicate keys as two rows and surfaces errors', () => {
    const rows = [
      { id: 'row-a', key: '720p', price: '0.6' },
      { id: 'row-b', key: '720p', price: '0.8' },
    ]
    const onChange = vi.fn()
    render(
      <I18nextProvider i18n={i18n}>
        <TaskUnitTierPriceEditor
          value={rows}
          onChange={onChange}
          errors={getTaskUnitTierRowErrors(rows)}
        />
      </I18nextProvider>
    )
    expect(screen.getAllByDisplayValue('720p')).toHaveLength(2)
    expect(
      screen.getAllByText('Tier keys must be unique and non-empty.')
    ).toHaveLength(2)
    fireEvent.click(screen.getByText('Add tier'))
    expect(onChange).toHaveBeenCalled()
    const next = onChange.mock.calls[0][0] as Array<{ key: string }>
    expect(next).toHaveLength(3)
  })
})
