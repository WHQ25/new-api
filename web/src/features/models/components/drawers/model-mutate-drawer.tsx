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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, Loader2 } from 'lucide-react'
import { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { JsonEditor } from '@/components/json-editor'
import { TagInput } from '@/components/tag-input'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import {
  useSystemOptions,
  getOptionValue,
} from '@/features/system-settings/hooks/use-system-options'

import {
  getTaskUnitTierRowErrors,
  getTaskUnitTierValidationError,
  taskUnitTierTableToRows,
  type ModelRatioData,
  type PricingMode,
  type TaskUnitTierPriceTable,
  type TaskUnitTierRow,
} from '@/features/system-settings/models/model-pricing-core'
import { TaskUnitTierPriceEditor } from '@/features/system-settings/models/model-pricing-inputs'
import {
  commitDrawerPricing,
  mapsFromSettings,
  pricingMapsToOptionValues,
  readModelRatioDataFromMaps,
} from '@/features/system-settings/models/model-pricing-persist'
import { normalizeJsonString } from '@/features/system-settings/models/utils'
import type { ModelSettings } from '@/features/system-settings/types'

import {
  getModel,
  getVendors,
  saveModelWithPricing,
} from '../../api'
import {
  drawerLockedPricingNoticeKey,
  isDrawerLockedPricingMode,
  modelDrawerOptionsStatusMessage,
  resolveDrawerCommitPricingData,
  shouldBlockModelDrawerSave,
} from '../../lib/model-drawer-pricing'
import { getNameRuleOptions, ENDPOINT_TEMPLATES } from '../../constants'
import { modelsQueryKeys, vendorsQueryKeys, parseModelTags } from '../../lib'
import type { Model } from '../../types'

// Extended schema for ratio configuration (internal form state only)
const extendedModelFormSchema = z.object({
  id: z.number().optional(),
  model_name: z.string().min(1, 'Model name is required'),
  description: z.string(),
  icon: z.string(),
  tags: z.array(z.string()),
  vendor_id: z.number().optional(),
  endpoints: z.string(),
  name_rule: z.number(),
  status: z.boolean(),
  sync_official: z.boolean(),
  price: z.string().optional(),
  ratio: z.string().optional(),
  cacheRatio: z.string().optional(),
  completionRatio: z.string().optional(),
  imageRatio: z.string().optional(),
  audioRatio: z.string().optional(),
  audioCompletionRatio: z.string().optional(),
})

type ExtendedModelFormValues = z.infer<typeof extendedModelFormSchema>

type PricingSubMode = 'ratio' | 'price'

type PricingFields = Pick<
  ExtendedModelFormValues,
  | 'price'
  | 'ratio'
  | 'cacheRatio'
  | 'completionRatio'
  | 'imageRatio'
  | 'audioRatio'
  | 'audioCompletionRatio'
>

// Form state describing the pricing currently configured for one model name.
type PricingConfig = {
  mode: PricingMode
  fields: PricingFields
  promptPrice: string
  completionPrice: string
  advancedOpen: boolean
  taskUnitTierPrice: TaskUnitTierPriceTable
  videoTokenPrice?: ModelRatioData['videoTokenPrice']
  billingExpr?: string
}

const EMPTY_PRICING_FIELDS: PricingFields = {
  price: '',
  ratio: '',
  cacheRatio: '',
  completionRatio: '',
  imageRatio: '',
  audioRatio: '',
  audioCompletionRatio: '',
}

const EMPTY_PRICING_CONFIG: PricingConfig = {
  mode: 'per-token',
  fields: EMPTY_PRICING_FIELDS,
  promptPrice: '',
  completionPrice: '',
  advancedOpen: false,
  taskUnitTierPrice: {},
}

function readPricingConfig(
  settings: ModelSettings | null,
  modelName: string
): PricingConfig {
  if (!settings || !modelName) return EMPTY_PRICING_CONFIG

  const maps = mapsFromSettings(settings)
  const data = readModelRatioDataFromMaps(maps, modelName)
  const ratio = data.ratio ? Number.parseFloat(data.ratio) : undefined
  const completionRatio = data.completionRatio
    ? Number.parseFloat(data.completionRatio)
    : undefined

  let promptPrice = ''
  let completionPrice = ''
  if (ratio !== undefined && !Number.isNaN(ratio)) {
    const tokenPrice = ratio * 2
    promptPrice = tokenPrice.toString()
    if (completionRatio !== undefined && !Number.isNaN(completionRatio)) {
      completionPrice = (tokenPrice * completionRatio).toString()
    }
  }

  return {
    mode: data.billingMode || 'per-token',
    fields: {
      price: data.price || '',
      ratio: data.ratio || '',
      cacheRatio: data.cacheRatio || '',
      completionRatio: data.completionRatio || '',
      imageRatio: data.imageRatio || '',
      audioRatio: data.audioRatio || '',
      audioCompletionRatio: data.audioCompletionRatio || '',
    },
    promptPrice,
    completionPrice,
    advancedOpen: Boolean(
      data.cacheRatio ||
        data.imageRatio ||
        data.audioRatio ||
        data.audioCompletionRatio
    ),
    taskUnitTierPrice: data.taskUnitTierPrice || {},
    videoTokenPrice: data.videoTokenPrice,
    billingExpr: data.billingExpr,
  }
}

type ModelMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: Model | null
}

export function ModelMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: ModelMutateDrawerProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currentModelId = currentRow?.id
  const isEditing = Boolean(currentModelId)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [pricingMode, setPricingMode] = useState<PricingMode>('per-token')
  const [pricingSubMode, setPricingSubMode] = useState<PricingSubMode>('ratio')
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [promptPrice, setPromptPrice] = useState('')
  const [completionPrice, setCompletionPrice] = useState('')
  const [oldModelName, setOldModelName] = useState<string>('')
  const [taskUnitTierRows, setTaskUnitTierRows] = useState<TaskUnitTierRow[]>(
    () => taskUnitTierTableToRows()
  )
  const taskUnitTierRowErrors = useMemo(
    () => getTaskUnitTierRowErrors(taskUnitTierRows),
    [taskUnitTierRows]
  )
  const [loadedPricingData, setLoadedPricingData] =
    useState<ModelRatioData | null>(null)
  // Model name whose pricing was read into the form when the drawer opened.
  // Submit may only rewrite pricing for this name, or for a name the user
  // explicitly priced; anything else it never saw and must leave alone.
  const [loadedPricingName, setLoadedPricingName] = useState<string>('')
  // Keep a ref so the load effect can read the latest modelSettings without
  // depending on it: modelSettings is a fresh object on every system-options
  // refetch, and including it in the deps would reset the form under the user.
  const modelSettingsRef = useRef<ModelSettings | null>(null)

  // Fetch vendors for dropdown
  const { data: vendorsData } = useQuery({
    queryKey: vendorsQueryKeys.list(),
    queryFn: () => getVendors({ page_size: 1000 }),
    enabled: open,
  })

  const vendors = vendorsData?.data?.items || []

  // Fetch model detail if editing
  const { data: modelData } = useQuery({
    queryKey: modelsQueryKeys.detail(currentModelId || 0),
    queryFn: () => {
      if (!currentModelId) {
        throw new Error('Model ID is required')
      }
      return getModel(currentModelId)
    },
    enabled: open && isEditing,
  })

  // Fetch system options for ratio configuration
  const optionsQuery = useSystemOptions()
  const systemOptionsData = optionsQuery.data



  // Get model settings from system options
  const modelSettings = useMemo(() => {
    if (!systemOptionsData?.data) return null
    const defaultModelSettings: ModelSettings = {
      'global.pass_through_request_enabled': false,
      'global.thinking_model_blacklist': '[]',
      'global.chat_completions_to_responses_policy': '{}',
      'general_setting.ping_interval_enabled': false,
      'general_setting.ping_interval_seconds': 60,
      'gemini.safety_settings': '',
      'gemini.version_settings': '',
      'gemini.supported_imagine_models': '',
      'gemini.thinking_adapter_enabled': false,
      'gemini.thinking_adapter_budget_tokens_percentage': 0.6,
      'gemini.function_call_thought_signature_enabled': false,
      'gemini.remove_function_response_id_enabled': true,
      'claude.model_headers_settings': '',
      'claude.default_max_tokens': '',
      'claude.thinking_adapter_enabled': true,
      'claude.thinking_adapter_budget_tokens_percentage': 0.8,
      ModelPrice: '',
      ModelRatio: '',
      CacheRatio: '',
      CompletionRatio: '',
      ImageRatio: '',
      AudioRatio: '',
      AudioCompletionRatio: '',
      ExposeRatioEnabled: false,
      'billing_setting.billing_mode': '{}',
      'billing_setting.billing_expr': '{}',
      'billing_setting.video_token_price': '{}',
      'billing_setting.task_unit_tier_price': '{}',
      'tool_price_setting.prices': '{}',
      TopupGroupRatio: '',
      GroupRatio: '',
      UserUsableGroups: '',
      GroupGroupRatio: '',
      AutoGroups: '',
      MaxTokenAutoGroups: 5,
      DefaultUseAutoGroup: false,
      CreateCacheRatio: '',
      'group_ratio_setting.group_special_usable_group': '{}',
      'grok.violation_deduction_enabled': false,
      'grok.violation_deduction_amount': 0,
      RetryTimes: 0,
      ChannelDisableThreshold: '',
      AutomaticDisableChannelEnabled: false,
      AutomaticEnableChannelEnabled: false,
      AutomaticDisableKeywords: '',
      AutomaticDisableStatusCodes: '401',
      AutomaticRetryStatusCodes:
        '100-199,300-399,401-407,409-499,500-503,505-523,525-599',
      'monitor_setting.auto_test_channel_enabled': false,
      'monitor_setting.auto_test_channel_minutes': 10,
      'monitor_setting.channel_test_mode': 'scheduled_all',
      'channel_affinity_setting.enabled': false,
      'channel_affinity_setting.switch_on_success': true,
      'channel_affinity_setting.keep_on_channel_disabled': false,
      'channel_affinity_setting.max_entries': 100000,
      'channel_affinity_setting.default_ttl_seconds': 3600,
      'channel_affinity_setting.rules': '[]',
      'model_deployment.ionet.api_key': '',
      'model_deployment.ionet.enabled': false,
    }
    return getOptionValue(systemOptionsData.data, defaultModelSettings)
  }, [systemOptionsData])

  // The load effect keys off this boolean, not the object: it re-runs once
  // when the settings first arrive (so a drawer opened before that still gets
  // its pricing prefilled), while later refetches only produce a new object
  // reference and must not reset a form the user may be editing.
  const hasModelSettings = modelSettings !== null
  useEffect(() => {
    modelSettingsRef.current = modelSettings
  })

  const form = useForm<ExtendedModelFormValues>({
    resolver: zodResolver(extendedModelFormSchema),
    defaultValues: {
      model_name: '',
      description: '',
      icon: '',
      tags: [],
      vendor_id: undefined,
      endpoints: '',
      name_rule: 0,
      status: true,
      sync_official: true,
      price: '',
      ratio: '',
      cacheRatio: '',
      completionRatio: '',
      imageRatio: '',
      audioRatio: '',
      audioCompletionRatio: '',
    },
  })

  const validateNumber = (value: string) => {
    if (value === '') return true
    return !Number.isNaN(Number.parseFloat(value))
  }

  const handlePromptPriceChange = (value: string) => {
    setPromptPrice(value)
    if (value && !Number.isNaN(Number.parseFloat(value))) {
      const ratio = Number.parseFloat(value) / 2
      form.setValue('ratio', ratio.toString())
    } else {
      form.setValue('ratio', '')
    }
  }

  const handleCompletionPriceChange = (value: string) => {
    setCompletionPrice(value)
    if (
      value &&
      !Number.isNaN(Number.parseFloat(value)) &&
      promptPrice &&
      !Number.isNaN(Number.parseFloat(promptPrice)) &&
      Number.parseFloat(promptPrice) > 0
    ) {
      const completionRatio =
        Number.parseFloat(value) / Number.parseFloat(promptPrice)
      form.setValue('completionRatio', completionRatio.toString())
    } else {
      form.setValue('completionRatio', '')
    }
  }

  // Load model data for editing and ratio configuration
  useEffect(() => {
    if (open && isEditing && modelData?.data) {
      const model = modelData.data
      setOldModelName(model.model_name)

      const pricing = readPricingConfig(
        modelSettingsRef.current,
        model.model_name
      )
      setLoadedPricingName(model.model_name)
      setPricingMode(pricing.mode)
      setPromptPrice(pricing.promptPrice)
      setCompletionPrice(pricing.completionPrice)
      setAdvancedOpen(pricing.advancedOpen)
      setTaskUnitTierRows(taskUnitTierTableToRows(pricing.taskUnitTierPrice))
      setLoadedPricingData(
        modelSettingsRef.current
          ? readModelRatioDataFromMaps(
              mapsFromSettings(modelSettingsRef.current),
              model.model_name
            )
          : null
      )
      form.reset({
        id: model.id,
        model_name: model.model_name,
        description: model.description || '',
        icon: model.icon || '',
        tags: parseModelTags(model.tags),
        vendor_id: model.vendor_id,
        endpoints: model.endpoints || '',
        name_rule: model.name_rule || 0,
        status: model.status === 1,
        sync_official: model.sync_official === 1,
        ...pricing.fields,
      })
    } else if (open && !isEditing) {
      // Pre-fill model name if passed from missing models, along with any
      // pricing that name already has, so the user edits it instead of being
      // shown an empty form that hides existing configuration.
      const modelName = currentRow?.model_name || ''
      const pricing = readPricingConfig(modelSettingsRef.current, modelName)
      setOldModelName('')
      setLoadedPricingName(modelName)
      setPricingSubMode('ratio')
      setPricingMode(pricing.mode)
      setPromptPrice(pricing.promptPrice)
      setCompletionPrice(pricing.completionPrice)
      setAdvancedOpen(pricing.advancedOpen)
      setTaskUnitTierRows(taskUnitTierTableToRows(pricing.taskUnitTierPrice))
      setLoadedPricingData(
        modelSettingsRef.current && modelName
          ? readModelRatioDataFromMaps(
              mapsFromSettings(modelSettingsRef.current),
              modelName
            )
          : null
      )
      form.reset({
        model_name: modelName,
        description: '',
        icon: '',
        tags: [],
        vendor_id: undefined,
        endpoints: '',
        name_rule: 0,
        status: true,
        sync_official: true,
        ...pricing.fields,
      })
    }
  }, [open, isEditing, modelData, currentRow, form, hasModelSettings])

  const onSubmit = useCallback(
    async (values: ExtendedModelFormValues): Promise<void> => {
      if (
        shouldBlockModelDrawerSave({
          optionsStatus: optionsQuery.isError
            ? 'error'
            : optionsQuery.isSuccess
              ? 'success'
              : 'pending',
          hasSettings: Boolean(modelSettings),
          pricingMode,
          rowErrors: taskUnitTierRowErrors,
        })
      ) {
        toast.error(
          t(
            modelDrawerOptionsStatusMessage(
              optionsQuery.isError
                ? 'error'
                : optionsQuery.isSuccess
                  ? 'success'
                  : 'pending',
              Boolean(modelSettings)
            ) || 'Unable to save model pricing'
          )
        )
        return
      }
      if (
        pricingMode === 'task_unit_tier' &&
        !isDrawerLockedPricingMode(loadedPricingData?.billingMode)
      ) {
        const unitError = getTaskUnitTierValidationError(taskUnitTierRows)
        if (unitError) {
          toast.error(t(unitError))
          return
        }
      }
      setIsSubmitting(true)
      try {
        const submitData = {
          ...values,
          id: isEditing ? currentModelId : undefined,
          tags: Array.isArray(values.tags) ? values.tags.join(',') : '',
          status: values.status ? 1 : 0,
          sync_official: values.sync_official ? 1 : 0,
        }

        const {
          price,
          ratio,
          cacheRatio,
          completionRatio,
          imageRatio,
          audioRatio,
          audioCompletionRatio,
          ...modelData
        } = submitData

        const finalModelName = values.model_name
        const table: Record<string, string> = {}
        for (const row of taskUnitTierRows) {
          const key = row.key.trim()
          if (key) table[key] = row.price
        }
        const pricingMaps = mapsFromSettings(modelSettings!)
        const existingLockedMode = pricingMaps.billingMode[finalModelName]
        const typedPricing = Boolean(
          values.price?.trim() || values.ratio?.trim()
        )
        const next = commitDrawerPricing(pricingMaps, {
          oldName: isEditing ? oldModelName : undefined,
          newName: finalModelName,
          loadedName: loadedPricingName,
          data: resolveDrawerCommitPricingData({
            loaded: loadedPricingData,
            name: finalModelName,
            pricingMode,
            values: {
              price: values.price,
              ratio: values.ratio,
              cacheRatio: values.cacheRatio,
              completionRatio: values.completionRatio,
              imageRatio: values.imageRatio,
              audioRatio: values.audioRatio,
              audioCompletionRatio: values.audioCompletionRatio,
            },
            taskUnitTierPrice: table,
          }),
        })
        const options: Record<string, string> = {}
        for (const [key, value] of Object.entries(
          pricingMapsToOptionValues(next)
        )) {
          options[key] = normalizeJsonString(value)
        }

        await saveModelWithPricing({
          model: {
            ...modelData,
            id: isEditing ? currentModelId : undefined,
          },
          options,
        })

        if (
          !isEditing &&
          !loadedPricingData &&
          typedPricing &&
          isDrawerLockedPricingMode(existingLockedMode)
        ) {
          const notice = drawerLockedPricingNoticeKey(existingLockedMode)
          if (notice) toast.warning(t(notice))
        }
        toast.success(
          isEditing
            ? 'Model updated successfully'
            : 'Model created successfully'
        )
        queryClient.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
        queryClient.invalidateQueries({ queryKey: ['system-options'] })
        onOpenChange(false)
      } catch (error: unknown) {
        toast.error((error as Error)?.message || 'Operation failed')
      } finally {
        setIsSubmitting(false)
      }
    },
    [
      isEditing,
      currentModelId,
      queryClient,
      onOpenChange,
      pricingMode,
      taskUnitTierRows,
      taskUnitTierRowErrors,
      loadedPricingData,
      oldModelName,
      loadedPricingName,
      modelSettings,
      optionsQuery.isError,
      optionsQuery.isSuccess,
      t,
    ]
  )

  const handleFillEndpointTemplate = (templateKey: string) => {
    const template = ENDPOINT_TEMPLATES[templateKey]
    if (template) {
      const templateJson = JSON.stringify({ [templateKey]: template }, null, 2)
      form.setValue('endpoints', templateJson)
    }
  }

  const lockedPricingNotice = drawerLockedPricingNoticeKey(
    loadedPricingData?.billingMode ?? pricingMode
  )

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-2xl')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isEditing ? t('Edit Model') : t('Create Model')}
          </SheetTitle>
          <SheetDescription>
            {isEditing
              ? t("Update model configuration and click save when you're done.")
              : t(
                  'Add a new model to the system by providing the necessary information.'
                )}
          </SheetDescription>
        </SheetHeader>

        <Form {...form}>
          <form
            id='model-form'
            onSubmit={form.handleSubmit(
              onSubmit as Parameters<typeof form.handleSubmit>[0]
            )}
            className={sideDrawerFormClassName()}
          >
            {/* Basic Information */}
            <SideDrawerSection>
              <h3 className='text-sm font-semibold'>
                {t('Basic Information')}
              </h3>

              <FormField
                control={form.control}
                name='model_name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Model Name *')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t('gpt-4, claude-3-opus, etc.')}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('The unique identifier for this model')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='description'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Description')}</FormLabel>
                    <FormControl>
                      <Textarea
                        placeholder={t('Describe this model...')}
                        rows={3}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='icon'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Icon')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t('OpenAI, Anthropic, etc.')}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription className='text-xs'>
                      {t('@lobehub/icons key')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='vendor_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Vendor')}</FormLabel>
                    <Select
                      items={vendors.map((vendor) => ({
                        value: String(vendor.id),
                        label: vendor.name,
                      }))}
                      onValueChange={(value) =>
                        field.onChange(
                          value ? Number.parseInt(value) : undefined
                        )
                      }
                      value={field.value ? String(field.value) : undefined}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue placeholder={t('Select vendor')} />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {vendors.map((vendor) => (
                            <SelectItem
                              key={vendor.id}
                              value={String(vendor.id)}
                            >
                              {vendor.name}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='tags'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Tags')}</FormLabel>
                    <FormControl>
                      <TagInput
                        value={field.value || []}
                        onChange={field.onChange}
                        placeholder={t('Add tags...')}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Press Enter or comma to add tags')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            {/* Matching Configuration */}
            <SideDrawerSection>
              <h3 className='text-sm font-semibold'>{t('Matching Rules')}</h3>

              <FormField
                control={form.control}
                name='name_rule'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Name Rule')}</FormLabel>
                    <FormControl>
                      <RadioGroup
                        onValueChange={(value) =>
                          field.onChange(Number.parseInt(value))
                        }
                        value={String(field.value)}
                        className='grid grid-cols-2 gap-4'
                      >
                        {getNameRuleOptions(t).map((option) => (
                          <div
                            key={option.value}
                            className='flex items-center space-x-2'
                          >
                            <RadioGroupItem
                              value={String(option.value)}
                              id={`rule-${option.value}`}
                            />
                            <Label
                              htmlFor={`rule-${option.value}`}
                              className='cursor-pointer font-normal'
                            >
                              {option.label}
                            </Label>
                          </div>
                        ))}
                      </RadioGroup>
                    </FormControl>
                    <FormDescription>
                      {t('How this model name should match requests')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            {/* Endpoints Configuration */}
            <SideDrawerSection>
              <div className='flex items-center justify-between'>
                <h3 className='text-sm font-semibold'>{t('Endpoints')}</h3>
                <Select<string>
                  items={Object.keys(ENDPOINT_TEMPLATES).map((key) => ({
                    value: key,
                    label: key,
                  }))}
                  onValueChange={(v) =>
                    v !== null && handleFillEndpointTemplate(v)
                  }
                >
                  <SelectTrigger size='sm' className='w-[200px]'>
                    <SelectValue placeholder={t('Load template...')} />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {Object.keys(ENDPOINT_TEMPLATES).map((key) => (
                        <SelectItem key={key} value={key}>
                          {key}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>

              <FormField
                control={form.control}
                name='endpoints'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Endpoint Configuration')}</FormLabel>
                    <FormControl>
                      <JsonEditor
                        value={field.value || ''}
                        onChange={field.onChange}
                        keyPlaceholder='endpoint_type'
                        valuePlaceholder='{"path": "/v1/...", "method": "POST"}'
                        keyLabel='Endpoint Type'
                        valueLabel='Configuration'
                        valueType='any'
                        emptyMessage={t(
                          'No endpoints configured. Switch to JSON mode or add rows to define endpoints.'
                        )}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Define API endpoints for this model (JSON format)')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            {/* Pricing Configuration */}
            <SideDrawerSection>
              <h3 className='text-sm font-semibold'>
                {t('Pricing Configuration')}
              </h3>

              {lockedPricingNotice ? (
                <p className='text-muted-foreground text-sm'>
                  {t(lockedPricingNotice)}
                </p>
              ) : (
                <>
              <div className='space-y-4'>
                <Label>{t('Pricing mode')}</Label>
                <RadioGroup
                  value={pricingMode}
                  onValueChange={(value) =>
                    setPricingMode(value as PricingMode)
                  }
                >
                  <div className='flex items-center space-x-2'>
                    <RadioGroupItem value='per-token' id='per-token' />
                    <Label htmlFor='per-token' className='font-normal'>
                      {t('Per-token (ratio based)')}
                    </Label>
                  </div>
                  <div className='flex items-center space-x-2'>
                    <RadioGroupItem value='per-request' id='per-request' />
                    <Label htmlFor='per-request' className='font-normal'>
                      {t('Per-request (fixed price)')}
                    </Label>
                  </div>
                  <div className='flex items-center space-x-2'>
                    <RadioGroupItem
                      value='task_unit_tier'
                      id='task_unit_tier'
                    />
                    <Label htmlFor='task_unit_tier' className='font-normal'>
                      {t('Unit tiers')}
                    </Label>
                  </div>
                </RadioGroup>
              </div>

              {pricingMode === 'task_unit_tier' ? (
                <TaskUnitTierPriceEditor
                  value={taskUnitTierRows}
                  onChange={setTaskUnitTierRows}
                  errors={taskUnitTierRowErrors}
                />
              ) : pricingMode === 'per-request' ? (
                <FormField
                  control={form.control}
                  name='price'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Fixed price (USD)')}</FormLabel>
                      <FormControl>
                        <Input
                          type='text'
                          placeholder='0.01'
                          {...field}
                          onChange={(e) => {
                            const value = e.target.value
                            if (validateNumber(value)) {
                              field.onChange(value)
                            }
                          }}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Cost in USD per request, regardless of tokens used.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ) : (
                <>
                  <div className='space-y-4'>
                    <Label>{t('Input mode')}</Label>
                    <RadioGroup
                      value={pricingSubMode}
                      onValueChange={(value) =>
                        setPricingSubMode(value as PricingSubMode)
                      }
                    >
                      <div className='flex items-center space-x-2'>
                        <RadioGroupItem value='ratio' id='ratio' />
                        <Label htmlFor='ratio' className='font-normal'>
                          {t('Ratio mode')}
                        </Label>
                      </div>
                      <div className='flex items-center space-x-2'>
                        <RadioGroupItem value='price' id='price' />
                        <Label htmlFor='price' className='font-normal'>
                          {t('Price mode (USD per 1M tokens)')}
                        </Label>
                      </div>
                    </RadioGroup>
                  </div>

                  {pricingSubMode === 'ratio' ? (
                    <>
                      <FormField
                        control={form.control}
                        name='ratio'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Model ratio')}</FormLabel>
                            <FormControl>
                              <Input
                                type='text'
                                placeholder='1.0'
                                {...field}
                                onChange={(e) => {
                                  const value = e.target.value
                                  if (validateNumber(value)) {
                                    field.onChange(value)
                                    if (value) {
                                      setPromptPrice(
                                        (
                                          Number.parseFloat(value) * 2
                                        ).toString()
                                      )
                                    } else {
                                      setPromptPrice('')
                                    }
                                  }
                                }}
                              />
                            </FormControl>
                            <FormDescription>
                              {field.value &&
                              !Number.isNaN(Number.parseFloat(field.value))
                                ? `Calculated price: $${(Number.parseFloat(field.value) * 2).toFixed(4)} per 1M tokens`
                                : t('Multiplier for prompt tokens.')}
                            </FormDescription>
                            <FormMessage />
                          </FormItem>
                        )}
                      />

                      <FormField
                        control={form.control}
                        name='completionRatio'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Completion ratio')}</FormLabel>
                            <FormControl>
                              <Input
                                type='text'
                                placeholder='1.0'
                                {...field}
                                onChange={(e) => {
                                  const value = e.target.value
                                  if (validateNumber(value)) {
                                    field.onChange(value)
                                    const ratio = form.getValues('ratio')
                                    if (value && ratio) {
                                      const compPrice =
                                        Number.parseFloat(ratio) *
                                        2 *
                                        Number.parseFloat(value)
                                      setCompletionPrice(compPrice.toString())
                                    } else {
                                      setCompletionPrice('')
                                    }
                                  }
                                }}
                              />
                            </FormControl>
                            <FormDescription>
                              {field.value &&
                              !Number.isNaN(Number.parseFloat(field.value)) &&
                              promptPrice &&
                              !Number.isNaN(Number.parseFloat(promptPrice))
                                ? `Calculated price: $${(Number.parseFloat(promptPrice) * Number.parseFloat(field.value)).toFixed(4)} per 1M tokens`
                                : t('Multiplier for completion tokens.')}
                            </FormDescription>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    </>
                  ) : (
                    <div className='space-y-4'>
                      <div className='space-y-2'>
                        <Label>{t('Prompt price ($/1M tokens)')}</Label>
                        <Input
                          type='text'
                          placeholder='2.0'
                          value={promptPrice}
                          onChange={(e) =>
                            handlePromptPriceChange(e.target.value)
                          }
                        />
                        <p className='text-muted-foreground text-sm'>
                          {promptPrice &&
                          !Number.isNaN(Number.parseFloat(promptPrice))
                            ? `Calculated ratio: ${(Number.parseFloat(promptPrice) / 2).toFixed(4)}`
                            : t('Enter Input price to calculate ratio')}
                        </p>
                      </div>

                      <div className='space-y-2'>
                        <Label>{t('Completion price ($/1M tokens)')}</Label>
                        <Input
                          type='text'
                          placeholder='4.0'
                          value={completionPrice}
                          onChange={(e) =>
                            handleCompletionPriceChange(e.target.value)
                          }
                        />
                        <p className='text-muted-foreground text-sm'>
                          {completionPrice &&
                          !Number.isNaN(Number.parseFloat(completionPrice)) &&
                          promptPrice &&
                          !Number.isNaN(Number.parseFloat(promptPrice)) &&
                          Number.parseFloat(promptPrice) > 0
                            ? `Calculated ratio: ${(Number.parseFloat(completionPrice) / Number.parseFloat(promptPrice)).toFixed(4)}`
                            : t('Enter Completion price to calculate ratio')}
                        </p>
                      </div>
                    </div>
                  )}

                  <Collapsible
                    open={advancedOpen}
                    onOpenChange={setAdvancedOpen}
                  >
                    <CollapsibleTrigger
                      render={
                        <Button
                          type='button'
                          variant='outline'
                          className='flex w-full items-center justify-between'
                        />
                      }
                    >
                      {t('Advanced options')}
                      <ChevronDown
                        className={`h-4 w-4 transition-transform duration-200 ${
                          advancedOpen ? 'rotate-180' : ''
                        }`}
                      />
                    </CollapsibleTrigger>
                    <CollapsibleContent className='flex flex-col gap-4 pt-4'>
                      <FormField
                        control={form.control}
                        name='cacheRatio'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Cache ratio')}</FormLabel>
                            <FormControl>
                              <Input
                                type='text'
                                placeholder='0.1'
                                {...field}
                                onChange={(e) => {
                                  const value = e.target.value
                                  if (validateNumber(value)) {
                                    field.onChange(value)
                                  }
                                }}
                              />
                            </FormControl>
                            <FormDescription>
                              {t('Discount ratio for cache hits.')}
                            </FormDescription>
                            <FormMessage />
                          </FormItem>
                        )}
                      />

                      <FormField
                        control={form.control}
                        name='imageRatio'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Image ratio')}</FormLabel>
                            <FormControl>
                              <Input
                                type='text'
                                placeholder='1.0'
                                {...field}
                                onChange={(e) => {
                                  const value = e.target.value
                                  if (validateNumber(value)) {
                                    field.onChange(value)
                                  }
                                }}
                              />
                            </FormControl>
                            <FormDescription>
                              {t('Multiplier for image processing.')}
                            </FormDescription>
                            <FormMessage />
                          </FormItem>
                        )}
                      />

                      <FormField
                        control={form.control}
                        name='audioRatio'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Audio ratio')}</FormLabel>
                            <FormControl>
                              <Input
                                type='text'
                                placeholder='1.0'
                                {...field}
                                onChange={(e) => {
                                  const value = e.target.value
                                  if (validateNumber(value)) {
                                    field.onChange(value)
                                  }
                                }}
                              />
                            </FormControl>
                            <FormDescription>
                              {t('Multiplier for audio inputs.')}
                            </FormDescription>
                            <FormMessage />
                          </FormItem>
                        )}
                      />

                      <FormField
                        control={form.control}
                        name='audioCompletionRatio'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Audio completion ratio')}</FormLabel>
                            <FormControl>
                              <Input
                                type='text'
                                placeholder='1.0'
                                {...field}
                                onChange={(e) => {
                                  const value = e.target.value
                                  if (validateNumber(value)) {
                                    field.onChange(value)
                                  }
                                }}
                              />
                            </FormControl>
                            <FormDescription>
                              {t('Multiplier for audio outputs.')}
                            </FormDescription>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    </CollapsibleContent>
                  </Collapsible>
                </>
              )}
                </>
              )}
            </SideDrawerSection>

            {/* Status & Sync */}
            <SideDrawerSection>
              <h3 className='text-sm font-semibold'>{t('Status & Sync')}</h3>

              <FormField
                control={form.control}
                name='status'
                render={({ field }) => (
                  <FormItem className={sideDrawerSwitchItemClassName()}>
                    <div className='flex flex-col gap-0.5'>
                      <FormLabel className='text-base'>
                        {t('Enabled')}
                      </FormLabel>
                      <FormDescription>
                        {t('Enable or disable this model')}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='sync_official'
                render={({ field }) => (
                  <FormItem className={sideDrawerSwitchItemClassName()}>
                    <div className='flex flex-col gap-0.5'>
                      <FormLabel className='text-base'>
                        {t('Official Sync')}
                      </FormLabel>
                      <FormDescription>
                        {t('Sync this model with official upstream')}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />
            </SideDrawerSection>
          </form>
        </Form>

        <SheetFooter className={sideDrawerFooterClassName()}>
          {modelDrawerOptionsStatusMessage(
            optionsQuery.isError
              ? 'error'
              : optionsQuery.isSuccess
                ? 'success'
                : 'pending',
            Boolean(modelSettings)
          ) ? (
            <p className='text-muted-foreground w-full text-xs'>
              {t(
                modelDrawerOptionsStatusMessage(
                  optionsQuery.isError
                    ? 'error'
                    : optionsQuery.isSuccess
                      ? 'success'
                      : 'pending',
                  Boolean(modelSettings)
                ) || ''
              )}
            </p>
          ) : null}
          <SheetClose
            render={<Button variant='outline' disabled={isSubmitting} />}
          >
            {t('Cancel')}
          </SheetClose>
          <Button
            form='model-form'
            type='submit'
            disabled={
              isSubmitting ||
              shouldBlockModelDrawerSave({
                optionsStatus: optionsQuery.isError
                  ? 'error'
                  : optionsQuery.isSuccess
                    ? 'success'
                    : 'pending',
                hasSettings: Boolean(modelSettings),
                pricingMode,
                rowErrors: taskUnitTierRowErrors,
              })
            }
          >
            {isSubmitting && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {isEditing ? t('Update Model') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
