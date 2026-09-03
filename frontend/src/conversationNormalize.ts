// 对话预览的归一化逻辑：把 OpenAI / Anthropic 两种协议的 request + response
// 统一还原成 Message[] 供 ConversationPreview 渲染。
// 本文件只含纯函数，不依赖 React / antd，方便单元测试。

export type Role = 'system' | 'user' | 'assistant' | 'tool' | 'unknown'

export type ToolCall = { name: string; args: string; id?: string }

export type ContentBlock =
  | { kind: 'text'; text: string }
  | { kind: 'image'; mediaType?: string; src?: string }
  | { kind: 'tool_use'; name: string; input: unknown; id?: string }
  | { kind: 'tool_result'; content: string; isError?: boolean; toolUseId?: string }
  | { kind: 'tool_calls'; calls: ToolCall[] }
  | { kind: 'thinking'; text: string }
  | { kind: 'unknown'; raw: unknown }

export type Message = {
  role: Role
  blocks: ContentBlock[]
}

export type Dict = { [k: string]: unknown }

export function asObject(v: unknown): Dict | null {
  if (v && typeof v === 'object' && !Array.isArray(v)) return v as Dict
  return null
}

export function safeStringify(v: unknown, max = 4000): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  try {
    const s = JSON.stringify(v, null, 2)
    return s.length > max ? s.slice(0, max) + '\n…(truncated)' : s
  } catch {
    return String(v)
  }
}

// 从 OpenAI image_url / Anthropic image source 中提取可直接 <img> 展示的地址：
// 优先 base64 data URI，其次是 http(s) URL。拿不到可展示的地址时返回 undefined。
export function extractImageSrc(p: Dict): { src?: string; mediaType?: string } {
  // OpenAI: { type: 'image_url', image_url: { url } | 'url-string' }
  const imageUrl = p.image_url
  if (typeof imageUrl === 'string') return { src: imageUrl }
  const urlObj = asObject(imageUrl)
  if (urlObj && typeof urlObj.url === 'string' && urlObj.url) {
    return { src: urlObj.url }
  }
  // Anthropic: { type: 'image', source: { type: 'base64', media_type, data } | { type: 'url', url } }
  const source = asObject(p.source)
  if (source) {
    if (source.type === 'base64' && typeof source.data === 'string' && source.data) {
      const mediaType = String(source.media_type ?? 'image/png')
      return { src: `data:${mediaType};base64,${source.data}`, mediaType }
    }
    if (typeof source.url === 'string' && source.url) {
      return { src: source.url, mediaType: source.media_type ? String(source.media_type) : undefined }
    }
    return { mediaType: source.media_type ? String(source.media_type) : undefined }
  }
  return { mediaType: p.media_type ? String(p.media_type) : undefined }
}

// OpenAI 多模态消息的 content 也是数组，不能仅凭"数组 content"判定为 Anthropic，
// 要看 part 的具体 type：image_url/input_audio/file 是 OpenAI 独有；
// image/tool_use/tool_result/thinking 是 Anthropic 风格。
const OPENAI_PART_TYPES = new Set(['image_url', 'input_audio', 'file', 'refusal'])

export function detectAnthropicRequest(req: Record<string, unknown>): boolean {
  if (typeof req.system === 'string' || Array.isArray(req.system)) return true
  if (!Array.isArray(req.messages)) return false
  let sawArrayContent = false
  for (const m of req.messages as unknown[]) {
    const obj = asObject(m)
    if (!obj) continue
    if (Array.isArray(obj.tool_calls)) return false // OpenAI 字段
    if (!Array.isArray(obj.content)) continue
    sawArrayContent = true
    for (const part of obj.content as unknown[]) {
      const p = asObject(part)
      if (!p || typeof p.type !== 'string') continue
      if (OPENAI_PART_TYPES.has(p.type)) return false
      if (p.type !== 'text') return true // Anthropic 风格 part
    }
  }
  return sawArrayContent
}

export function normalizeOpenAIMessages(messages: unknown[]): Message[] {
  return messages.map((raw) => {
    const m = asObject(raw) ?? {}
    const role = (typeof m.role === 'string' ? m.role : 'unknown') as Role
    const blocks: ContentBlock[] = []

    if (typeof m.content === 'string') {
      blocks.push({ kind: 'text', text: m.content })
    } else if (Array.isArray(m.content)) {
      for (const part of m.content) {
        if (typeof part === 'string') {
          blocks.push({ kind: 'text', text: part })
          continue
        }
        const p = asObject(part)
        if (!p) continue
        if (p.type === 'text' || typeof p.text === 'string') {
          blocks.push({ kind: 'text', text: String(p.text ?? '') })
        } else if (p.type === 'image_url' || p.type === 'image') {
          blocks.push({ kind: 'image', ...extractImageSrc(p) })
        } else {
          blocks.push({ kind: 'unknown', raw: p })
        }
      }
    } else if (m.content !== null && m.content !== undefined) {
      blocks.push({ kind: 'text', text: String(m.content) })
    }

    if (Array.isArray(m.tool_calls) && m.tool_calls.length > 0) {
      const calls: ToolCall[] = (m.tool_calls as unknown[]).map((tc) => {
        const t = asObject(tc) ?? {}
        const fn = asObject(t.function) ?? {}
        const rawArgs = fn.arguments ?? t.arguments
        const args = typeof rawArgs === 'string' ? rawArgs : safeStringify(rawArgs)
        return { name: String(fn.name ?? t.name ?? 'unknown'), args, id: t.id as string | undefined }
      })
      blocks.push({ kind: 'tool_calls', calls })
    }

    return { role, blocks }
  })
}

export function normalizeAnthropicMessages(messages: unknown[]): Message[] {
  return messages.map((raw) => {
    const m = asObject(raw) ?? {}
    const role = (typeof m.role === 'string' ? m.role : 'unknown') as Role
    const blocks: ContentBlock[] = []

    if (typeof m.content === 'string') {
      blocks.push({ kind: 'text', text: m.content })
    } else if (Array.isArray(m.content)) {
      for (const part of m.content) {
        const p = asObject(part)
        if (!p) continue
        switch (p.type) {
          case 'text':
            blocks.push({ kind: 'text', text: String(p.text ?? '') })
            break
          case 'image':
          case 'image_url': // 兜底：客户端按 OpenAI 格式发的图片
            blocks.push({ kind: 'image', ...extractImageSrc(p) })
            break
          case 'tool_use':
            blocks.push({
              kind: 'tool_use',
              name: String(p.name ?? 'unknown'),
              input: p.input,
              id: p.id as string | undefined,
            })
            break
          case 'tool_result': {
            const c = p.content
            const toolUseId = p.tool_use_id as string | undefined
            const isError = Boolean(p.is_error)
            if (Array.isArray(c)) {
              // 工具结果可以是数组（文本 + 图片，如截图工具），图片单独渲染成 image 块
              const texts: string[] = []
              for (const part of c) {
                const cp = asObject(part)
                if (!cp) continue
                if (cp.type === 'text') texts.push(String(cp.text ?? ''))
                else if (cp.type === 'image') blocks.push({ kind: 'image', ...extractImageSrc(cp) })
                else texts.push(safeStringify(cp))
              }
              blocks.push({ kind: 'tool_result', content: texts.join('\n'), isError, toolUseId })
            } else {
              const content = typeof c === 'string' ? c : safeStringify(c)
              blocks.push({ kind: 'tool_result', content, isError, toolUseId })
            }
            break
          }
          case 'thinking':
            blocks.push({ kind: 'thinking', text: String(p.thinking ?? '') })
            break
          default:
            blocks.push({ kind: 'unknown', raw: p })
        }
      }
    } else if (m.content !== null && m.content !== undefined) {
      blocks.push({ kind: 'text', text: String(m.content) })
    }

    return { role, blocks }
  })
}

export function normalizeOpenAIResponse(resp: Record<string, unknown>): Message[] {
  const out: Message[] = []
  const choices = resp.choices
  if (Array.isArray(choices)) {
    for (const choice of choices) {
      const m = asObject(choice)?.message
      if (!m) continue
      const blocks: ContentBlock[] = []
      if (typeof m.content === 'string') {
        blocks.push({ kind: 'text', text: m.content })
      } else if (Array.isArray(m.content)) {
        for (const part of m.content) {
          const p = asObject(part)
          if (!p) continue
          if (p.type === 'text') blocks.push({ kind: 'text', text: String(p.text ?? '') })
          else if (p.type === 'refusal') blocks.push({ kind: 'text', text: String(p.refusal ?? '') })
          else if (p.type === 'image_url' || p.type === 'image') blocks.push({ kind: 'image', ...extractImageSrc(p) })
          else blocks.push({ kind: 'unknown', raw: p })
        }
      }
      if (Array.isArray(m.tool_calls) && m.tool_calls.length > 0) {
        const calls: ToolCall[] = (m.tool_calls as unknown[]).map((tc) => {
          const t = asObject(tc) ?? {}
          const fn = asObject(t.function) ?? {}
          const rawArgs = fn.arguments ?? t.arguments
          return {
            name: String(fn.name ?? t.name ?? 'unknown'),
            args: typeof rawArgs === 'string' ? rawArgs : safeStringify(rawArgs),
            id: t.id as string | undefined,
          }
        })
        blocks.push({ kind: 'tool_calls', calls })
      }
      out.push({ role: 'assistant', blocks })
    }
  }
  return out
}

export function normalizeAnthropicResponse(resp: Record<string, unknown>): Message[] {
  const blocks: ContentBlock[] = []
  const content = resp.content
  if (typeof content === 'string') {
    blocks.push({ kind: 'text', text: content })
  } else if (Array.isArray(content)) {
    for (const part of content) {
      const p = asObject(part)
      if (!p) continue
      switch (p.type) {
        case 'text':
          blocks.push({ kind: 'text', text: String(p.text ?? '') })
          break
        case 'tool_use':
          blocks.push({
            kind: 'tool_use',
            name: String(p.name ?? 'unknown'),
            input: p.input,
            id: p.id as string | undefined,
          })
          break
        case 'thinking':
          blocks.push({ kind: 'thinking', text: String(p.thinking ?? '') })
          break
        default:
          blocks.push({ kind: 'unknown', raw: p })
      }
    }
  }
  // OpenAI 流式聚合后 response_data 是裸消息（无 choices 包装），工具调用
  // 存在顶层 tool_calls 字段里——这里要认出来，否则最后一步的工具会丢。
  if (Array.isArray(resp.tool_calls) && resp.tool_calls.length > 0) {
    const calls: ToolCall[] = (resp.tool_calls as unknown[]).map((tc) => {
      const t = asObject(tc) ?? {}
      const fn = asObject(t.function) ?? {}
      const rawArgs = fn.arguments ?? t.arguments
      return {
        name: String(fn.name ?? t.name ?? 'unknown'),
        args: typeof rawArgs === 'string' ? rawArgs : safeStringify(rawArgs),
        id: t.id as string | undefined,
      }
    })
    blocks.push({ kind: 'tool_calls', calls })
  }
  return blocks.length > 0 ? [{ role: 'assistant', blocks }] : []
}

export function buildConversation(
  requestData: unknown,
  responseData: unknown,
  protocol?: string | null,
): Message[] {
  const messages: Message[] = []
  const req = asObject(requestData)

  if (req) {
    // protocol 字段明确时以它为准，否则靠内容特征探测
    const isAnthropic =
      protocol === 'anthropic' ||
      (protocol !== 'openai' && detectAnthropicRequest(req))
    if (isAnthropic) {
      if (typeof req.system === 'string' && req.system.trim()) {
        messages.push({ role: 'system', blocks: [{ kind: 'text', text: req.system }] })
      } else if (Array.isArray(req.system)) {
        const text = (req.system as unknown[])
          .map((p) => {
            const obj = asObject(p)
            return obj?.type === 'text' ? String(obj.text ?? '') : safeStringify(p)
          })
          .join('\n')
        if (text.trim()) {
          messages.push({ role: 'system', blocks: [{ kind: 'text', text }] })
        }
      }
      if (Array.isArray(req.messages)) {
        messages.push(...normalizeAnthropicMessages(req.messages))
      }
    } else if (Array.isArray(req.messages)) {
      messages.push(...normalizeOpenAIMessages(req.messages))
    }
  }

  const resp = asObject(responseData)
  if (resp) {
    const isAnthropicResp =
      protocol === 'anthropic' &&
      (resp.role === 'assistant' || resp.type === 'message') &&
      !Array.isArray(resp.choices)

    if (isAnthropicResp) {
      messages.push(...normalizeAnthropicResponse(resp))
    } else if (Array.isArray(resp.choices)) {
      messages.push(...normalizeOpenAIResponse(resp))
    } else if (resp.role === 'assistant' && resp.content !== undefined) {
      messages.push(...normalizeAnthropicResponse(resp))
    } else {
      const oa = normalizeOpenAIResponse(resp)
      if (oa.length > 0) messages.push(...oa)
      else messages.push(...normalizeAnthropicResponse(resp))
    }
  }

  return messages
}
