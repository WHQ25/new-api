import { describe, expect, it } from 'vitest'

import {
  countVideoTokenPrices,
  parseVideoTokenPriceTable,
  serializeVideoTokenPriceTable,
  videoTokenCellKey,
  videoTokenTableShape,
  videoTokenVariantColumns,
} from '../model-pricing-core'

// The tariff cell key is the wire format shared with the backend: the "sec:"
// prefix is what tells both sides the price is per second rather than per 1M
// tokens, and the variant suffixes must stay in the canonical order or the
// backend lookup misses the cell the operator filled in.
describe('video token tariff cell keys', () => {
  it('builds keys with the unit prefix and canonical variant order', () => {
    expect(videoTokenCellKey('per_token', '720p', [])).toBe('720p')
    expect(videoTokenCellKey('per_token', '720p', ['video'])).toBe('720p_video')
    expect(videoTokenCellKey('per_second', '1080p', [])).toBe('sec:1080p')
    expect(videoTokenCellKey('per_second', '1080p', ['audio', 'video'])).toBe(
      'sec:1080p_video_audio'
    )
  })

  it('reads the unit and priced variants back out of an existing table', () => {
    expect(videoTokenTableShape({ '720p': '7', '720p_video': '4.2' })).toEqual({
      unit: 'per_token',
      variants: ['video'],
    })
    expect(
      videoTokenTableShape({
        'sec:720p': '0.6',
        'sec:720p_audio': '0.9',
        'sec:720p_video_audio': '1.1',
      })
    ).toEqual({ unit: 'per_second', variants: ['video', 'audio'] })
    expect(videoTokenTableShape({})).toEqual({
      unit: 'per_token',
      variants: [],
    })
  })
})

describe('video token table parsing', () => {
  it('keeps only well-formed keys with usable prices', () => {
    expect(
      parseVideoTokenPriceTable({
        '480p': 6.7,
        '720p': 7,
        '4k': 0,
        '1440p': 9,
        '720p_bogus': 3,
      })
    ).toEqual({ '480p': '6.7', '720p': '7' })
  })

  // 后端只会按 video → audio 的规范序、每个变体至多一次去查表，
  // 非规范拼法配了也永远查不到，留在表里只会让运营以为「已配置」而请求全被 400。
  it('rejects keys the backend would never look up', () => {
    expect(
      parseVideoTokenPriceTable({
        'sec:1080p_video_audio': 1.4,
        'sec:1080p_audio_video': 9,
        'sec:720p_audio_audio': 9,
      })
    ).toEqual({ 'sec:1080p_video_audio': '1.4' })
    expect(
      serializeVideoTokenPriceTable({ 'sec:1080p_audio_video': '9' })
    ).toEqual({})
  })

  // 一张表只能有一个计量单位：后端对混合表直接报错，编辑器必须只加载会生效的那一套，
  // 否则管理员看到的价格和实际计费不是同一张表。
  it('drops the cells that do not belong to the resolved unit', () => {
    expect(
      parseVideoTokenPriceTable({ '720p': 7, 'sec:1080p_audio': 1.2 })
    ).toEqual({ 'sec:1080p_audio': '1.2' })
  })

  it('round-trips a per-second table back to numbers', () => {
    const table = parseVideoTokenPriceTable({
      'sec:720p': 0.6,
      'sec:720p_audio': 0.9,
    })
    expect(serializeVideoTokenPriceTable(table)).toEqual({
      'sec:720p': 0.6,
      'sec:720p_audio': 0.9,
    })
    expect(countVideoTokenPrices(table)).toBe(2)
  })
})

// 每个变体都是独立维度：可灵按有无声音加价而与视频输入无关，Seedance 按有无视频
// 输入加价而与声音无关，同一个模型两种维度都开时必须能分别定价。旧实现只生成选中
// 变体的前缀链，配了 video+audio 就永远出不来「基础+有声」这一格，请求落到
// sec:720p_audio 上直接 400。
describe('video token tariff grid columns', () => {
  it('spans every combination the backend can look up', () => {
    expect(videoTokenVariantColumns([])).toEqual([[]])
    expect(videoTokenVariantColumns(['audio'])).toEqual([[], ['audio']])
    expect(videoTokenVariantColumns(['video', 'audio'])).toEqual([
      [],
      ['video'],
      ['audio'],
      ['video', 'audio'],
    ])
  })

  it('builds a lookupable key for every column', () => {
    expect(
      videoTokenVariantColumns(['video', 'audio']).map((column) =>
        videoTokenCellKey('per_second', '720p', column)
      )
    ).toEqual([
      'sec:720p',
      'sec:720p_video',
      'sec:720p_audio',
      'sec:720p_video_audio',
    ])
  })
})
