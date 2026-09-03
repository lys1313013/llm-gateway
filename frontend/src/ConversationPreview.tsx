import { useMemo } from 'react'
import { Empty, Image, Space, Tag, Tooltip, Typography, theme } from 'antd'
import {
  RobotOutlined, UserOutlined, ToolOutlined,
  PictureOutlined, CodeOutlined, BulbOutlined,
  WarningOutlined, FileSearchOutlined,
} from '@ant-design/icons'
import { useTheme } from './theme/ThemeContext'
import {
  buildConversation, safeStringify,
  type ContentBlock, type Message, type Role, type ToolCall,
} from './conversationNormalize'

const { Text } = Typography

type Props = {
  requestData: unknown
  responseData: unknown
  protocol?: string | null
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

type RoleMeta = {
  label: string
  color: string
  bg: string
  avatarBg: string
  icon: React.ReactNode
  align: 'left' | 'right' | 'center'
}

const ROLE_META_LIGHT: Record<Role, RoleMeta> = {
  system:    { label: 'system',    color: '#6b7280', bg: '#f3f4f6', avatarBg: '#9ca3af', icon: <RobotOutlined />,         align: 'center' },
  user:      { label: 'user',      color: '#1d4ed8', bg: '#eff6ff', avatarBg: '#3b82f6', icon: <UserOutlined />,          align: 'right'  },
  assistant: { label: 'assistant', color: '#15803d', bg: '#f0fdf4', avatarBg: '#22c55e', icon: <RobotOutlined />,         align: 'left'   },
  tool:      { label: 'tool',      color: '#c2410c', bg: '#fff7ed', avatarBg: '#f97316', icon: <ToolOutlined />,          align: 'left'   },
  unknown:   { label: 'unknown',   color: '#6b7280', bg: '#f9fafb', avatarBg: '#9ca3af', icon: <FileSearchOutlined />,   align: 'left'   },
}

const ROLE_META_DARK: Record<Role, RoleMeta> = {
  system:    { label: 'system',    color: '#9ca3af', bg: '#27272a', avatarBg: '#52525b', icon: <RobotOutlined />,         align: 'center' },
  user:      { label: 'user',      color: '#60a5fa', bg: '#172554', avatarBg: '#1d4ed8', icon: <UserOutlined />,          align: 'right'  },
  assistant: { label: 'assistant', color: '#4ade80', bg: '#14532d', avatarBg: '#15803d', icon: <RobotOutlined />,         align: 'left'   },
  tool:      { label: 'tool',      color: '#fb923c', bg: '#431407', avatarBg: '#c2410c', icon: <ToolOutlined />,          align: 'left'   },
  unknown:   { label: 'unknown',   color: '#9ca3af', bg: '#27272a', avatarBg: '#52525b', icon: <FileSearchOutlined />,   align: 'left'   },
}

function getRoleMeta(role: Role, isDark: boolean): RoleMeta {
  return (isDark ? ROLE_META_DARK : ROLE_META_LIGHT)[role]
}

function AvatarBubble({ role }: { role: Role }) {
  const { isDark } = useTheme()
  const meta = getRoleMeta(role, isDark)
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
  const { isDark } = useTheme()
  const meta = getRoleMeta(role, isDark)
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
  const { token } = theme.useToken()
  const trimmed = text || '(空)'
  return (
    <div
      style={{
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-word',
        fontSize: 13,
        lineHeight: 1.6,
        color: token.colorText,
      }}
    >
      {trimmed}
    </div>
  )
}

function ImageBlock({ mediaType, src }: { mediaType?: string; src?: string }) {
  const { isDark } = useTheme()
  if (src) {
    return (
      <div>
        <Image
          src={src}
          alt={mediaType ? `图片 (${mediaType})` : '图片'}
          style={{ maxWidth: 320, maxHeight: 320, borderRadius: 6, objectFit: 'contain' }}
          preview={{ mask: <PictureOutlined /> }}
        />
      </div>
    )
  }
  return (
    <div
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        padding: '4px 10px',
        background: isDark ? '#422006' : '#fef3c7',
        border: `1px solid ${isDark ? '#713f12' : '#fde68a'}`,
        borderRadius: 6,
        fontSize: 12,
        color: isDark ? '#fbbf24' : '#92400e',
      }}
    >
      <PictureOutlined />
      <span>图片{mediaType ? ` (${mediaType})` : ''}</span>
    </div>
  )
}

function ThinkingBlock({ text }: { text: string }) {
  const { isDark } = useTheme()
  if (!text) return null
  return (
    <div
      style={{
        padding: '6px 10px',
        background: isDark ? '#2e1065' : '#f5f3ff',
        border: `1px dashed ${isDark ? '#6d28d9' : '#c4b5fd'}`,
        borderRadius: 6,
        fontSize: 12,
        color: isDark ? '#c4b5fd' : '#5b21b6',
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
  const { isDark } = useTheme()
  const { token } = theme.useToken()
  return (
    <div
      style={{
        border: `1px solid ${isDark ? '#78350f' : '#fcd34d'}`,
        background: isDark ? '#451a03' : '#fffbeb',
        borderRadius: 6,
        padding: '8px 10px',
        fontSize: 12,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        <CodeOutlined style={{ color: isDark ? '#fbbf24' : '#b45309' }} />
        <Text strong style={{ color: isDark ? '#fcd34d' : '#92400e', fontSize: 12 }}>
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
            background: isDark ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.04)',
            borderRadius: 4,
            fontSize: 11.5,
            fontFamily: 'monospace',
            overflow: 'auto',
            maxHeight: 240,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            color: token.colorText,
          }}
        >
          {safeStringify(input)}
        </pre>
      )}
    </div>
  )
}

function ToolCallsBlock({ calls }: { calls: ToolCall[] }) {
  const { isDark } = useTheme()
  const { token } = theme.useToken()
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {calls.map((c, i) => (
        <div
          key={c.id ?? `${c.name}-${i}`}
          style={{
            border: `1px solid ${isDark ? '#78350f' : '#fcd34d'}`,
            background: isDark ? '#451a03' : '#fffbeb',
            borderRadius: 6,
            padding: '8px 10px',
            fontSize: 12,
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
            <CodeOutlined style={{ color: isDark ? '#fbbf24' : '#b45309' }} />
            <Text strong style={{ color: isDark ? '#fcd34d' : '#92400e', fontSize: 12 }}>
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
                background: isDark ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.04)',
                borderRadius: 4,
                fontSize: 11.5,
                fontFamily: 'monospace',
                overflow: 'auto',
                maxHeight: 240,
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                color: token.colorText,
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
  const { isDark } = useTheme()
  const { token } = theme.useToken()
  return (
    <div
      style={{
        border: `1px solid ${isDark ? (isError ? '#7f1d1d' : '#78350f') : (isError ? '#fecaca' : '#fed7aa')}`,
        background: isDark ? (isError ? '#450a0a' : '#451a03') : (isError ? '#fef2f2' : '#fff7ed'),
        borderRadius: 6,
        padding: '8px 10px',
        fontSize: 12,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        {isError ? (
          <WarningOutlined style={{ color: isDark ? '#f87171' : '#dc2626' }} />
        ) : (
          <ToolOutlined style={{ color: isDark ? '#fb923c' : '#c2410c' }} />
        )}
        <Text strong style={{ color: isDark ? (isError ? '#fca5a5' : '#fcd34d') : (isError ? '#991b1b' : '#9a3412'), fontSize: 12 }}>
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
          background: isDark ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.04)',
          borderRadius: 4,
          fontSize: 11.5,
          fontFamily: 'monospace',
          overflow: 'auto',
          maxHeight: 240,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          color: isError ? (isDark ? '#fecaca' : '#7f1d1d') : token.colorText,
        }}
      >
        {content || '(空)'}
      </pre>
    </div>
  )
}

function UnknownBlock({ raw }: { raw: unknown }) {
  const { isDark } = useTheme()
  const { token } = theme.useToken()
  return (
    <div
      style={{
        border: `1px dashed ${isDark ? token.colorBorder : '#d1d5db'}`,
        background: isDark ? '#1f1f1f' : '#f9fafb',
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
          background: isDark ? 'rgba(255,255,255,0.04)' : 'rgba(0,0,0,0.03)',
          borderRadius: 4,
          fontSize: 11,
          fontFamily: 'monospace',
          maxHeight: 200,
          overflow: 'auto',
          whiteSpace: 'pre-wrap',
          color: token.colorText,
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
    case 'image':       return <ImageBlock mediaType={block.mediaType} src={block.src} />
    case 'thinking':    return block.text ? <ThinkingBlock text={block.text} /> : null
    case 'tool_use':    return <ToolUseBlock name={block.name} input={block.input} id={block.id} />
    case 'tool_calls':  return <ToolCallsBlock calls={block.calls} />
    case 'tool_result': return <ToolResultBlock content={block.content} isError={block.isError} toolUseId={block.toolUseId} />
    case 'unknown':     return <UnknownBlock raw={block.raw} />
  }
}

function MessageBubble({ message }: { message: Message }) {
  const { isDark } = useTheme()
  const meta = getRoleMeta(message.role, isDark)
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
            border: `1px solid ${meta.color}33`,
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
          border: `1px solid ${meta.color}33`,
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
  const { token } = theme.useToken()
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
          border: `1px solid ${token.colorBorder}`,
          borderRadius: 8,
          background: token.colorBgContainer,
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
