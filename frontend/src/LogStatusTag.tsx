import { Tag, Tooltip } from 'antd'

export default function LogStatusTag({ statusCode, errorMessage }: {
  statusCode?: number | null
  errorMessage?: string | null
}) {
  if (!statusCode && !errorMessage) return <>-</>
  return (
    <Tooltip title={errorMessage || undefined}>
      <Tag color={statusCode === 200 && !errorMessage ? 'success' : 'error'}>
        {statusCode ?? '-'}{errorMessage && statusCode === 200 ? ' · 异常' : ''}
      </Tag>
    </Tooltip>
  )
}
