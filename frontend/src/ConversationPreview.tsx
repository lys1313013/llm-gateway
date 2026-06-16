import { useMemo } from 'react'
import { Empty, Space, Tag, Tooltip, Typography } from 'antd'
import {
  RobotOutlined, UserOutlined, ToolOutlined,
  PictureOutlined, CodeOutlined, BulbOutlined,
  WarningOutlined, FileSearchOutlined,
} from '@ant-design/icons'

const { Text } = Typography

type Role = 'system' | 'user' | 'assistant' | 'tool' | 'unknown'

type ToolCall = { name: string; args: string; id?: string }

type ContentBlock =
  | { kind: 'text'; text: string }
  | { kind: 'image'; mediaType?: string }
  | { kind: 'tool_use'; name: string; input: unknown; id?: string }
  | { kind: 'tool_result'; content: string; isError?: boolean; toolUseId?: string }
  | { kind: 'tool_calls'; calls: ToolCall[] }
  | { kind: 'thinking'; text: string }
  | { kind: 'unknown'; raw: unknown }

type Message = {
  role: Role
  blocks: ContentBlock[]
}

type Props = {
  requestData: unknown
  responseData: unknown
  protocol?: string | null
}

// ---------------------------------------------------------------------------
// Normalization helpers
// ---------------------------------------------------------------------------

type Dict = { [k: string]: any }

function asObject(v: unknown): Dict | null {
  if (v && typeof v === 'object' && !Array.isArray(v)) return v as Dict
  return null
}

function safeStringify(v: unknown, max = 4000): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  try {
    const s = JSON.stringify(v, null, 2)
    return s.length > max ? s.slice(0, max) + '\n…(truncated)' : s
  } catch {
    return String(v)
  }
}

function detectAnthropicRequest(req: Record<string, unknown>): boolean {
  if (typeof req.system === 'string' || Array.isArray(req.system)) return true
  if (Array.isArray(req.messages)) {
    return (req.messages as unknown[]).some((m) => {
      const obj = asObject(m)
      return Array.isArray(obj?.content)
    })
  }
  return false
}

function normalizeOpenAIMessages(messages: unknown[]): Message[] {
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
          blocks.push({ kind: 'image' })
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

function normalizeAnthropicMessages(messages: unknown[]): Message[] {
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
            blocks.push({ kind: 'image', mediaType: String(p.source?.media_type ?? p.media_type ?? '') })
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
            const content = typeof c === 'string' ? c : safeStringify(c)
            blocks.push({
              kind: 'tool_result',
              content,
              isError: Boolean(p.is_error),
              toolUseId: p.tool_use_id as string | undefined,
            })
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

function normalizeOpenAIResponse(resp: Record<string, unknown>): Message[] {
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
          else if (p.type === 'image_url' || p.type === 'image') blocks.push({ kind: 'image' })
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

function normalizeAnthropicResponse(resp: Record<string, unknown>): Message[] {
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
  return blocks.length > 0 ? [{ role: 'assistant', blocks }] : []
}

function buildConversation(
  requestData: unknown,
  responseData: unknown,
  protocol?: string | null,
): Message[] {
  const messages: Message[] = []
  const req = asObject(requestData)

  if (req) {
    const isAnthropic = protocol === 'anthropic' || detectAnthropicRequest(req)
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

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

const ROLE_META: Record<Role, { label: string; color: string; bg: string; avatarBg: string; icon: React.ReactNode; align: 'left' | 'right' | 'center' }> = {
  system:    { label: 'system',    color: '#6b7280', bg: '#f3f4f6', avatarBg: '#9ca3af', icon: <RobotOutlined />,         align: 'center' },
  user:      { label: 'user',      color: '#1d4ed8', bg: '#eff6ff', avatarBg: '#3b82f6', icon: <UserOutlined />,          align: 'right'  },
  assistant: { label: 'assistant', color: '#15803d', bg: '#f0fdf4', avatarBg: '#22c55e', icon: <RobotOutlined />,         align: 'left'   },
  tool:      { label: 'tool',      color: '#c2410c', bg: '#fff7ed', avatarBg: '#f97316', icon: <ToolOutlined />,          align: 'left'   },
  unknown:   { label: 'unknown',   color: '#6b7280', bg: '#f9fafb', avatarBg: '#9ca3af', icon: <FileSearchOutlined />,   align: 'left'   },
}

function AvatarBubble({ role }: { role: Role }) {
  const meta = ROLE_META[role]
  return (
    <div
      style={{
        width: 30,
        height: 30,
        borderRadius: '50%',
        background: meta.avatarBg,
        color: '#fff',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: 14,
        flexShrink: 0,
      }}
    >
      {meta.icon}
    </div>
  )
}

function RoleTag({ role }: { role: Role }) {
  const meta = ROLE_META[role]
  return (
    <Tag
      color={meta.color}
      style={{ marginRight: 6, fontSize: 11, lineHeight: '16px', padding: '0 6px' }}
    >
      {meta.label}
    </Tag>
  )
}

function TextBlock({ text }: { text: string }) {
  const trimmed = text || '(空)'
  return (
    <div
      style={{
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-word',
        fontSize: 13,
        lineHeight: 1.6,
        color: '#1f2937',
      }}
    >
      {trimmed}
    </div>
  )
}

function ImageBlock({ mediaType }: { mediaType?: string }) {
  return (
    <div
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        padding: '4px 10px',
        background: '#fef3c7',
        border: '1px solid #fde68a',
        borderRadius: 6,
        fontSize: 12,
        color: '#92400e',
      }}
    >
      <PictureOutlined />
      <span>图片{mediaType ? ` (${mediaType})` : ''}</span>
    </div>
  )
}

function ThinkingBlock({ text }: { text: string }) {
  if (!text) return null
  return (
    <div
      style={{
        padding: '6px 10px',
        background: '#f5f3ff',
        border: '1px dashed #c4b5fd',
        borderRadius: 6,
        fontSize: 12,
        color: '#5b21b6',
        fontStyle: 'italic',
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-word',
      }}
    >
      <BulbOutlined style={{ marginRight: 6 }} />
      {text}
    </div>
  )
}

function ToolUseBlock({ name, input, id }: { name: string; input: unknown; id?: string }) {
  return (
    <div
      style={{
        border: '1px solid #fcd34d',
        background: '#fffbeb',
        borderRadius: 6,
        padding: '8px 10px',
        fontSize: 12,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        <CodeOutlined style={{ color: '#b45309' }} />
        <Text strong style={{ color: '#92400e', fontSize: 12 }}>
          工具调用: {name}
        </Text>
        {id && (
          <Text type="secondary" style={{ fontSize: 11, fontFamily: 'monospace' }}>
            {id}
          </Text>
        )}
      </div>
      {input !== undefined && input !== null && (
        <pre
          style={{
            margin: 0,
            padding: '6px 8px',
            background: 'rgba(0,0,0,0.04)',
            borderRadius: 4,
            fontSize: 11.5,
            fontFamily: 'monospace',
            overflow: 'auto',
            maxHeight: 240,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
          }}
        >
          {safeStringify(input)}
        </pre>
      )}
    </div>
  )
}

function ToolCallsBlock({ calls }: { calls: ToolCall[] }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {calls.map((c, i) => (
        <div
          key={c.id ?? `${c.name}-${i}`}
          style={{
            border: '1px solid #fcd34d',
            background: '#fffbeb',
            borderRadius: 6,
            padding: '8px 10px',
            fontSize: 12,
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
            <CodeOutlined style={{ color: '#b45309' }} />
            <Text strong style={{ color: '#92400e', fontSize: 12 }}>
              工具调用: {c.name}
            </Text>
            {c.id && (
              <Text type="secondary" style={{ fontSize: 11, fontFamily: 'monospace' }}>
                {c.id}
              </Text>
            )}
          </div>
          {c.args && (
            <pre
              style={{
                margin: 0,
                padding: '6px 8px',
                background: 'rgba(0,0,0,0.04)',
                borderRadius: 4,
                fontSize: 11.5,
                fontFamily: 'monospace',
                overflow: 'auto',
                maxHeight: 240,
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
              }}
            >
              {c.args}
            </pre>
          )}
        </div>
      ))}
    </div>
  )
}

function ToolResultBlock({ content, isError, toolUseId }: { content: string; isError?: boolean; toolUseId?: string }) {
  return (
    <div
      style={{
        border: `1px solid ${isError ? '#fecaca' : '#fed7aa'}`,
        background: isError ? '#fef2f2' : '#fff7ed',
        borderRadius: 6,
        padding: '8px 10px',
        fontSize: 12,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        {isError ? (
          <WarningOutlined style={{ color: '#dc2626' }} />
        ) : (
          <ToolOutlined style={{ color: '#c2410c' }} />
        )}
        <Text strong style={{ color: isError ? '#991b1b' : '#9a3412', fontSize: 12 }}>
          工具结果{isError ? ' (错误)' : ''}
        </Text>
        {toolUseId && (
          <Text type="secondary" style={{ fontSize: 11, fontFamily: 'monospace' }}>
            {toolUseId}
          </Text>
        )}
      </div>
      <pre
        style={{
          margin: 0,
          padding: '6px 8px',
          background: 'rgba(0,0,0,0.04)',
          borderRadius: 4,
          fontSize: 11.5,
          fontFamily: 'monospace',
          overflow: 'auto',
          maxHeight: 240,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          color: isError ? '#7f1d1d' : '#1f2937',
        }}
      >
        {content || '(空)'}
      </pre>
    </div>
  )
}

function UnknownBlock({ raw }: { raw: unknown }) {
  return (
    <div
      style={{
        border: '1px dashed #d1d5db',
        background: '#f9fafb',
        borderRadius: 6,
        padding: '6px 8px',
        fontSize: 12,
      }}
    >
      <Text type="secondary" style={{ fontSize: 11 }}>未知类型内容块:</Text>
      <pre
        style={{
          margin: '4px 0 0 0',
          padding: '4px 6px',
          background: 'rgba(0,0,0,0.03)',
          borderRadius: 4,
          fontSize: 11,
          fontFamily: 'monospace',
          maxHeight: 200,
          overflow: 'auto',
          whiteSpace: 'pre-wrap',
        }}
      >
        {safeStringify(raw)}
      </pre>
    </div>
  )
}

function Block({ block }: { block: ContentBlock }) {
  switch (block.kind) {
    case 'text':        return <TextBlock text={block.text} />
    case 'image':       return <ImageBlock mediaType={block.mediaType} />
    case 'thinking':    return block.text ? <ThinkingBlock text={block.text} /> : null
    case 'tool_use':    return <ToolUseBlock name={block.name} input={block.input} id={block.id} />
    case 'tool_calls':  return <ToolCallsBlock calls={block.calls} />
    case 'tool_result': return <ToolResultBlock content={block.content} isError={block.isError} toolUseId={block.toolUseId} />
    case 'unknown':     return <UnknownBlock raw={block.raw} />
  }
}

function MessageBubble({ message }: { message: Message }) {
  const meta = ROLE_META[message.role]
  const isUser = meta.align === 'right'
  const isSystem = meta.align === 'center'
  const visibleBlocks = message.blocks.filter((b) => !(b.kind === 'text' && !b.text.trim()))

  if (isSystem) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', margin: '4px 0' }}>
        <div
          style={{
            maxWidth: '92%',
            background: meta.bg,
            border: `1px solid ${meta.color}22`,
            borderRadius: 8,
            padding: '8px 12px',
          }}
        >
          <div style={{ marginBottom: 4 }}>
            <RoleTag role={message.role} />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {visibleBlocks.map((b, i) => <Block key={i} block={b} />)}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: isUser ? 'row-reverse' : 'row',
        gap: 8,
        alignItems: 'flex-start',
        margin: '4px 0',
      }}
    >
      <AvatarBubble role={message.role} />
      <div
        style={{
          maxWidth: '78%',
          background: meta.bg,
          border: `1px solid ${meta.color}22`,
          borderRadius: 8,
          padding: '8px 12px',
        }}
      >
        <div style={{ marginBottom: 4 }}>
          <RoleTag role={message.role} />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {visibleBlocks.map((b, i) => <Block key={i} block={b} />)}
        </div>
      </div>
    </div>
  )
}

function ConversationPreview({ requestData, responseData, protocol }: Props) {
  const messages = useMemo(
    () => buildConversation(requestData, responseData, protocol),
    [requestData, responseData, protocol],
  )

  const stats = useMemo(() => {
    let textChars = 0
    let toolUse = 0
    let toolResult = 0
    let images = 0
    for (const m of messages) {
      for (const b of m.blocks) {
        if (b.kind === 'text') textChars += b.text.length
        else if (b.kind === 'image') images++
        else if (b.kind === 'tool_use' || b.kind === 'tool_calls') toolUse++
        else if (b.kind === 'tool_result') toolResult++
      }
    }
    return { textChars, toolUse, toolResult, images }
  }, [messages])

  return (
    <div style={{ flex: '1.2 1 460px', minWidth: 400, display: 'flex', flexDirection: 'column' }}>
      <div
        style={{
          marginBottom: 8,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Text strong>对话预览</Text>
          <Tooltip title="基于 request + response 自动还原的对话气泡视图">
            <Text type="secondary" style={{ fontSize: 11 }}>还原</Text>
          </Tooltip>
        </div>
        <Space size={4}>
          <Tag>{messages.length} 条消息</Tag>
          {stats.toolUse > 0 && <Tag color="gold">工具 × {stats.toolUse}</Tag>}
          {stats.toolResult > 0 && <Tag color="orange">结果 × {stats.toolResult}</Tag>}
          {stats.images > 0 && <Tag color="cyan">图片 × {stats.images}</Tag>}
        </Space>
      </div>
      <div
        style={{
          border: '1px solid #e5e7eb',
          borderRadius: 8,
          background: '#fafafa',
          padding: 12,
          flex: 1,
          minHeight: 320,
          maxHeight: '70vh',
          overflowY: 'auto',
        }}
      >
        {messages.length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description="无法从该请求/响应还原对话"
          />
        ) : (
          messages.map((m, i) => <MessageBubble key={i} message={m} />)
        )}
      </div>
    </div>
  )
}

export default ConversationPreview
