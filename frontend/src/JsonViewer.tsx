import Editor, { DiffEditor } from '@monaco-editor/react'
import { Typography } from 'antd'

const { Text } = Typography

type JsonViewerProps = {
  title: string
  value: unknown
  height?: number | string
  style?: React.CSSProperties
}

type DiffJsonViewerProps = {
  title?: string
  leftLabel: string
  rightLabel: string
  left: unknown
  right: unknown
  height?: number | string
  style?: React.CSSProperties
}

type ParsedContent = {
  language: 'json' | 'plaintext'
  content: string
  isJson: boolean
}

function parseViewerContent(value: unknown): ParsedContent {
  if (value === null || value === undefined) {
    return {
      language: 'plaintext',
      content: '',
      isJson: false,
    }
  }

  if (typeof value === 'object') {
    return {
      language: 'json',
      content: JSON.stringify(value, null, 2),
      isJson: true,
    }
  }

  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) {
      return {
        language: 'plaintext',
        content: value,
        isJson: false,
      }
    }

    try {
      const parsed = JSON.parse(trimmed)
      return {
        language: 'json',
        content: JSON.stringify(parsed, null, 2),
        isJson: true,
      }
    } catch {
      return {
        language: 'plaintext',
        content: value,
        isJson: false,
      }
    }
  }

  return {
    language: 'plaintext',
    content: String(value),
    isJson: false,
  }
}

function viewerStringify(value: unknown): string {
  const parsed = parseViewerContent(value)
  return parsed.content
}

function JsonViewer({ title, value, height = '70vh', style }: JsonViewerProps) {
  const viewerContent = parseViewerContent(value)

  return (
    <div style={{ flex: '1 1 520px', minWidth: 420, ...style }}>
      <div
        style={{
          marginBottom: 8,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        <Text strong>{title}</Text>
      </div>
      <div
        style={{
          border: '1px solid #e5e7eb',
          borderRadius: 8,
          overflow: 'hidden',
          background: '#ffffff',
        }}
      >
        <Editor
          height={height}
          defaultLanguage={viewerContent.language}
          language={viewerContent.language}
          value={viewerContent.content}
          options={{
            readOnly: true,
            minimap: { enabled: false },
            folding: true,
            showFoldingControls: 'always',
            scrollBeyondLastLine: false,
            wordWrap: 'on',
            automaticLayout: true,
            fontSize: 13,
            lineNumbersMinChars: 3,
            renderValidationDecorations: 'off',
            tabSize: 2,
          }}
          theme="vs"
        />
      </div>
    </div>
  )
}

// Side-by-side JSON diff using Monaco's built-in DiffEditor. Falls back to
// plaintext when one side isn't valid JSON (e.g. raw error bodies).
function DiffJsonViewer({
  title,
  leftLabel,
  rightLabel,
  left,
  right,
  height = '70vh',
  style,
}: DiffJsonViewerProps) {
  const leftParsed = parseViewerContent(left)
  const rightParsed = parseViewerContent(right)
  const language: 'json' | 'plaintext' =
    leftParsed.language === 'json' && rightParsed.language === 'json' ? 'json' : 'plaintext'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0, ...style }}>
      {(title || leftLabel || rightLabel) && (
        <div
          style={{
            marginBottom: 8,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 12,
          }}
        >
          {title ? <Text strong>{title}</Text> : <span />}
          <div style={{ display: 'flex', gap: 8, fontSize: 12 }}>
            <Text type="secondary">A: {leftLabel}</Text>
            <Text type="secondary">B: {rightLabel}</Text>
          </div>
        </div>
      )}
      <div
        style={{
          border: '1px solid #e5e7eb',
          borderRadius: 8,
          overflow: 'hidden',
          background: '#ffffff',
        }}
      >
        <DiffEditor
          height={height}
          language={language}
          original={viewerStringify(left)}
          modified={viewerStringify(right)}
          options={{
            readOnly: true,
            minimap: { enabled: false },
            folding: true,
            showFoldingControls: 'always',
            scrollBeyondLastLine: false,
            wordWrap: 'on',
            automaticLayout: true,
            fontSize: 13,
            lineNumbersMinChars: 3,
            renderValidationDecorations: 'off',
            renderSideBySide: true,
            ignoreTrimWhitespace: false,
            originalEditable: false,
          }}
          theme="vs"
        />
      </div>
    </div>
  )
}

export default JsonViewer
export { DiffJsonViewer }
