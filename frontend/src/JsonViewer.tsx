import Editor from '@monaco-editor/react'
import { Typography } from 'antd'

const { Text } = Typography

type JsonViewerProps = {
  title: string
  value: unknown
  height?: number | string
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

function JsonViewer({ title, value, height = '70vh' }: JsonViewerProps) {
  const viewerContent = parseViewerContent(value)

  return (
    <div style={{ flex: '1 1 520px', minWidth: 420 }}>
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

export default JsonViewer
