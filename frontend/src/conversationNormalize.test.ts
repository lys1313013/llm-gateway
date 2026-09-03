import { describe, expect, it } from 'vitest'
import {
  buildConversation,
  detectAnthropicRequest,
  extractImageSrc,
  normalizeAnthropicMessages,
  normalizeOpenAIMessages,
} from './conversationNormalize'

// ---------------------------------------------------------------------------
// detectAnthropicRequest — 协议探测
// ---------------------------------------------------------------------------

describe('detectAnthropicRequest', () => {
  it('有顶层 system 字段判定为 Anthropic', () => {
    expect(detectAnthropicRequest({ system: 'you are...', messages: [] })).toBe(true)
  })

  it('OpenAI 纯文本消息（字符串 content）判定为 OpenAI', () => {
    const req = {
      messages: [
        { role: 'system', content: 'sys' },
        { role: 'user', content: 'hello' },
      ],
    }
    expect(detectAnthropicRequest(req)).toBe(false)
  })

  it('OpenAI 多模态请求（数组 content + image_url）判定为 OpenAI —— 回归：曾误判为 Anthropic', () => {
    const req = {
      messages: [
        {
          role: 'user',
          content: [
            { type: 'text', text: '图里是啥' },
            { type: 'image_url', image_url: { url: 'data:image/png;base64,iVBOR' } },
          ],
        },
      ],
    }
    expect(detectAnthropicRequest(req)).toBe(false)
  })

  it('消息带 tool_calls 字段判定为 OpenAI', () => {
    const req = {
      messages: [
        { role: 'user', content: 'hi' },
        { role: 'assistant', content: null, tool_calls: [{ id: 'c1', function: { name: 'f', arguments: '{}' } }] },
      ],
    }
    expect(detectAnthropicRequest(req)).toBe(false)
  })

  it('Anthropic 数组 content（image/tool_use）判定为 Anthropic', () => {
    const req = {
      messages: [
        {
          role: 'user',
          content: [
            { type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'iVBOR' } },
            { type: 'text', text: 'describe' },
          ],
        },
      ],
    }
    expect(detectAnthropicRequest(req)).toBe(true)
  })

  it('只有 text part 的数组 content 按 Anthropic 处理（向后兼容旧行为）', () => {
    const req = {
      messages: [{ role: 'user', content: [{ type: 'text', text: 'hi' }] }],
    }
    expect(detectAnthropicRequest(req)).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// extractImageSrc — 图片地址提取
// ---------------------------------------------------------------------------

describe('extractImageSrc', () => {
  it('OpenAI image_url 对象形式', () => {
    expect(extractImageSrc({ type: 'image_url', image_url: { url: 'https://x/1.png' } }))
      .toEqual({ src: 'https://x/1.png' })
  })

  it('OpenAI image_url 字符串形式', () => {
    expect(extractImageSrc({ type: 'image_url', image_url: 'data:image/png;base64,iVBOR' }))
      .toEqual({ src: 'data:image/png;base64,iVBOR' })
  })

  it('Anthropic base64 source 拼成 data URI', () => {
    expect(extractImageSrc({ type: 'image', source: { type: 'base64', media_type: 'image/jpeg', data: 'QUJD' } }))
      .toEqual({ src: 'data:image/jpeg;base64,QUJD', mediaType: 'image/jpeg' })
  })

  it('Anthropic url source 原样透出', () => {
    expect(extractImageSrc({ type: 'image', source: { type: 'url', url: 'https://x/2.png' } }))
      .toEqual({ src: 'https://x/2.png', mediaType: undefined })
  })

  it('无可用地址时只回 mediaType，不回 src', () => {
    const r = extractImageSrc({ type: 'image', source: { type: 'base64', media_type: 'image/png' } })
    expect(r.src).toBeUndefined()
    expect(r.mediaType).toBe('image/png')
  })
})

// ---------------------------------------------------------------------------
// 消息归一化
// ---------------------------------------------------------------------------

describe('normalizeOpenAIMessages', () => {
  it('image_url part 归一化为带 src 的 image 块', () => {
    const [msg] = normalizeOpenAIMessages([
      {
        role: 'user',
        content: [
          { type: 'text', text: '看这张' },
          { type: 'image_url', image_url: { url: 'data:image/png;base64,iVBOR' } },
        ],
      },
    ])
    expect(msg.blocks).toEqual([
      { kind: 'text', text: '看这张' },
      { kind: 'image', src: 'data:image/png;base64,iVBOR' },
    ])
  })

  it('assistant 的 tool_calls 归一化为 tool_calls 块', () => {
    const [msg] = normalizeOpenAIMessages([
      {
        role: 'assistant',
        content: null,
        tool_calls: [{ id: 'call_1', function: { name: 'get_weather', arguments: '{"city":"sh"}' } }],
      },
    ])
    expect(msg.blocks).toEqual([
      { kind: 'tool_calls', calls: [{ name: 'get_weather', args: '{"city":"sh"}', id: 'call_1' }] },
    ])
  })
})

describe('normalizeAnthropicMessages', () => {
  it('base64 图片归一化为带 data URI 的 image 块', () => {
    const [msg] = normalizeAnthropicMessages([
      {
        role: 'user',
        content: [
          { type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'iVBOR' } },
        ],
      },
    ])
    expect(msg.blocks).toEqual([
      { kind: 'image', src: 'data:image/png;base64,iVBOR', mediaType: 'image/png' },
    ])
  })

  it('OpenAI 风格的 image_url 也能兜底解析', () => {
    const [msg] = normalizeAnthropicMessages([
      { role: 'user', content: [{ type: 'image_url', image_url: { url: 'https://x/1.png' } }] },
    ])
    expect(msg.blocks).toEqual([{ kind: 'image', src: 'https://x/1.png' }])
  })

  it('tool_result 数组内容中的图片拆分为独立 image 块', () => {
    const [msg] = normalizeAnthropicMessages([
      {
        role: 'user',
        content: [
          {
            type: 'tool_result',
            tool_use_id: 't1',
            content: [
              { type: 'text', text: '截图如下' },
              { type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'QUJD' } },
            ],
          },
        ],
      },
    ])
    expect(msg.blocks).toEqual([
      { kind: 'image', src: 'data:image/png;base64,QUJD', mediaType: 'image/png' },
      { kind: 'tool_result', content: '截图如下', isError: false, toolUseId: 't1' },
    ])
  })

  it('tool_result 字符串内容保持原样', () => {
    const [msg] = normalizeAnthropicMessages([
      { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't2', content: 'done', is_error: true }] },
    ])
    expect(msg.blocks).toEqual([
      { kind: 'tool_result', content: 'done', isError: true, toolUseId: 't2' },
    ])
  })
})

// ---------------------------------------------------------------------------
// buildConversation — 整体组装与 protocol 优先级
// ---------------------------------------------------------------------------

describe('buildConversation', () => {
  const openaiMultimodalReq = {
    messages: [
      {
        role: 'user',
        content: [
          { type: 'text', text: '图里是啥' },
          { type: 'image_url', image_url: { url: 'data:image/png;base64,iVBOR' } },
        ],
      },
    ],
  }
  const openaiResp = {
    choices: [{ message: { role: 'assistant', content: '一只猫' } }],
  }

  it('OpenAI 多模态请求还原出 image 块（协议探测路径）', () => {
    const msgs = buildConversation(openaiMultimodalReq, openaiResp)
    const user = msgs.find((m) => m.role === 'user')
    expect(user?.blocks).toContainEqual({ kind: 'image', src: 'data:image/png;base64,iVBOR' })
    expect(msgs.find((m) => m.role === 'assistant')?.blocks).toEqual([{ kind: 'text', text: '一只猫' }])
  })

  it('protocol=openai 时优先于内容探测，不走 Anthropic 解析', () => {
    // 即使内容长得像 Anthropic（顶层 system），protocol=openai 也按 OpenAI 解析
    const req = { system: 's', messages: [{ role: 'user', content: 'hi' }] }
    const msgs = buildConversation(req, null, 'openai')
    // OpenAI 解析没有 system 气泡，只有 user 一条
    expect(msgs).toEqual([{ role: 'user', blocks: [{ kind: 'text', text: 'hi' }] }])
  })

  it('protocol=anthropic 时按 Anthropic 解析，system 单独成气泡', () => {
    const req = { system: 'sys prompt', messages: [{ role: 'user', content: 'hi' }] }
    const msgs = buildConversation(req, null, 'anthropic')
    expect(msgs[0]).toEqual({ role: 'system', blocks: [{ kind: 'text', text: 'sys prompt' }] })
  })

  it('空 request/response 返回空数组', () => {
    expect(buildConversation(null, null)).toEqual([])
  })
})
