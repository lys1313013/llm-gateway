import { useCallback, useEffect, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Card,
  Col,
  Drawer,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Progress,
  Row,
  Select,
  Skeleton,
  Space,
  Statistic,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from 'antd'
import type { TableColumnsType } from 'antd'
import dayjs from 'dayjs'
import { apiFetch, getCurrentUser } from './api'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type ProviderRecord = {
  id: number
  name: string
  openai_base_url: string | null
  anthropic_base_url: string | null
  api_key: string | null
  remark: string | null
  quota_url: string | null
  quota_format: string | null
  create_time: string
  update_time: string
}

type QuotaModel = {
  model_name: string
  status: number
  status_text: string
  interval_usage_count?: number
  interval_total_count?: number
  interval_used_percent: number
  interval_remains_ms?: number
  interval_start_time?: string
  interval_end_time?: string
  weekly_usage_count?: number
  weekly_total_count?: number
  weekly_used_percent: number
  weekly_remains_ms?: number
  weekly_start_time?: string
  weekly_end_time?: string
}

type QuotaBalance = {
  is_available: boolean
  currency: string
  total: string
  granted: string
  topped_up: string
}

type QuotaSnapshot = {
  display_type: 'model_remains' | 'balance' | ''
  models?: QuotaModel[]
  balance?: QuotaBalance
  last_error?: string
  fetched_at: string
}

type ProviderQuotaEntry = {
  provider_id: number
  provider_name: string
  has_config: boolean
  present: boolean
  snapshot: QuotaSnapshot
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const QUOTA_FORMATS = [
  { value: 'minimax', label: 'MiniMax (按模型配额)' },
  { value: 'deepseek', label: 'DeepSeek (账户余额)' },
] as const

const QUOTA_POLL_INTERVAL_MS = 30_000

type ProviderPreset = {
  id: string
  name: string
  description?: string
  openai_base_url?: string
  anthropic_base_url?: string
  quota_url?: string
  quota_format?: string
  remark?: string
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const formatTime = (ms?: number): string => {
  if (!ms || ms <= 0) return '无限制'
  const totalSec = Math.floor(ms / 1000)
  const min = Math.floor(totalSec / 60)
  const hr = Math.floor(min / 60)
  const day = Math.floor(hr / 24)
  const month = Math.floor(day / 30)
  if (month > 0) return `${month}月${day % 30}天`
  if (day > 0) return `${day}天${hr % 24}时`
  if (hr > 0) return `${hr}小时${min % 60}分`
  if (min > 0) return `${min}分${totalSec % 60}秒`
  return `${totalSec}秒`
}

const quotaStatusColor = (pct: number): string => {
  if (pct >= 90) return '#EF4444'
  if (pct >= 70) return '#F59E0B'
  if (pct >= 30) return '#3B82F6'
  return '#22C55E'
}

const summarizeSnapshot = (s?: QuotaSnapshot): { text: string; tone: 'success' | 'warning' | 'error' | 'default' } => {
  if (!s) return { text: '—', tone: 'default' }
  if (s.last_error) return { text: '拉取失败', tone: 'error' }
  if (s.display_type === 'balance' && s.balance) {
    return { text: `${s.balance.currency} ${s.balance.total}`, tone: 'success' }
  }
  if (s.display_type === 'model_remains' && s.models && s.models.length > 0) {
    const intervalMax = s.models.reduce((m, x) => Math.max(m, x.interval_used_percent), 0)
    const weeklyMax = s.models.reduce((m, x) => Math.max(m, x.weekly_used_percent), 0)
    const worst = Math.max(intervalMax, weeklyMax)
    return {
      text: `5h ${intervalMax}% · 本周 ${weeklyMax}%`,
      tone: worst >= 90 ? 'error' : worst >= 70 ? 'warning' : 'success',
    }
  }
  return { text: '—', tone: 'default' }
}

// Aggregates per-model quota into a single 5h / weekly view. Uses the
// max-used-percent across models so the worst-case cycle is what shows.
type AggregatedCycle = {
  usedPct: number
  usage?: number
  total?: number
  start?: string
  end?: string
  remainsMs?: number
}

type AggregatedQuota = {
  interval: AggregatedCycle
  weekly: AggregatedCycle
}

const aggregateModels = (models: QuotaModel[]): AggregatedQuota => {
  const pickMax = (ms: QuotaModel[], key: 'interval_used_percent' | 'weekly_used_percent'): QuotaModel =>
    ms.reduce((acc, x) => (x[key] > acc[key] ? x : acc), ms[0])
  const interval = pickMax(models, 'interval_used_percent')
  const weekly = pickMax(models, 'weekly_used_percent')
  return {
    interval: {
      usedPct: interval.interval_used_percent,
      usage: interval.interval_usage_count,
      total: interval.interval_total_count,
      start: interval.interval_start_time,
      end: interval.interval_end_time,
      remainsMs: interval.interval_remains_ms,
    },
    weekly: {
      usedPct: weekly.weekly_used_percent,
      usage: weekly.weekly_usage_count,
      total: weekly.weekly_total_count,
      start: weekly.weekly_start_time,
      end: weekly.weekly_end_time,
      remainsMs: weekly.weekly_remains_ms,
    },
  }
}

// ---------------------------------------------------------------------------
// Subcomponent: quota overview card
// ---------------------------------------------------------------------------

type OverviewProps = {
  entries: ProviderQuotaEntry[]
  loading: boolean
  onRefreshOne: (providerId: number) => Promise<void>
  onOpenDetail: (providerId: number) => void
}

const QuotaOverviewCard = ({ entries, loading, onRefreshOne, onOpenDetail }: OverviewProps) => {
  const configured = entries.filter((e) => e.has_config)
  const successCount = configured.filter((e) => e.snapshot && !e.snapshot.last_error && e.snapshot.display_type).length
  const errorCount = configured.filter((e) => e.snapshot?.last_error).length
  const pendingCount = configured.length - successCount - errorCount

  return (
    <Card
      title="配额概览"
      variant="borderless"
      style={{ marginBottom: 16 }}
      extra={loading ? <Skeleton.Button active size="small" style={{ width: 80 }} /> : null}
    >
      {configured.length === 0 ? (
        <Empty
          description="暂无产商配置配额（编辑产商，填写 Quota URL + Quota Format 即可启用）"
          image={Empty.PRESENTED_IMAGE_SIMPLE}
        />
      ) : (
        <>
          <Row gutter={16} style={{ marginBottom: 12 }}>
            <Col span={6}><Statistic title="已配置" value={configured.length} /></Col>
            <Col span={6}><Statistic title="正常" value={successCount} valueStyle={{ color: '#16A34A' }} /></Col>
            <Col span={6}><Statistic title="拉取中" value={pendingCount} valueStyle={{ color: '#64748B' }} /></Col>
            <Col span={6}><Statistic title="异常" value={errorCount} valueStyle={{ color: errorCount > 0 ? '#EF4444' : '#64748B' }} /></Col>
          </Row>
          <Row gutter={[12, 12]}>
            {configured.map((e) => {
              const sum = summarizeSnapshot(e.snapshot)
              const isErr = !!e.snapshot?.last_error
              return (
                <Col key={e.provider_id} xs={24} sm={12} md={8} lg={6}>
                  <Card size="small" hoverable onClick={() => onOpenDetail(e.provider_id)}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                      <Typography.Text strong>{e.provider_name}</Typography.Text>
                      <Tag color={e.snapshot ? (isErr ? 'red' : sum.tone === 'success' ? 'green' : sum.tone === 'warning' ? 'orange' : 'default') : 'default'}>
                        {e.snapshot ? (isErr ? '异常' : sum.tone === 'success' ? '正常' : sum.tone === 'warning' ? '注意' : '—') : '拉取中'}
                      </Tag>
                    </div>
                    {e.snapshot?.display_type === 'balance' && e.snapshot.balance ? (
                      <div style={{ fontSize: 18, fontWeight: 600 }}>
                        {e.snapshot.balance.currency} {e.snapshot.balance.total}
                      </div>
                    ) : e.snapshot?.display_type === 'model_remains' && e.snapshot.models && e.snapshot.models.length > 0 ? (
                      <DualProgressLine models={e.snapshot.models} />
                    ) : (
                      <Typography.Text type="secondary">—</Typography.Text>
                    )}
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 8 }}>
                      <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                        {e.snapshot?.fetched_at ? dayjs(e.snapshot.fetched_at).format('HH:mm:ss') : '尚未拉取'}
                      </Typography.Text>
                      <Button
                        type="link"
                        size="small"
                        onClick={(ev) => {
                          ev.stopPropagation()
                          void onRefreshOne(e.provider_id)
                        }}
                      >
                        刷新
                      </Button>
                    </div>
                    {isErr && (
                      <Tooltip title={e.snapshot!.last_error}>
                        <Typography.Text type="danger" style={{ fontSize: 11 }} ellipsis>
                          {e.snapshot!.last_error}
                        </Typography.Text>
                      </Tooltip>
                    )}
                  </Card>
                </Col>
              )
            })}
          </Row>
        </>
      )}
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Subcomponent: detail drawer
// ---------------------------------------------------------------------------

type DetailProps = {
  providerId: number | null
  providerName?: string
  onClose: () => void
  onRefresh: (providerId: number) => Promise<void>
}

const QuotaDetailDrawer = ({ providerId, providerName, onClose, onRefresh }: DetailProps) => {
  const [snapshot, setSnapshot] = useState<QuotaSnapshot | null>(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    if (providerId == null) return
    setLoading(true)
    try {
      const res = await apiFetch(`/api/provider/${providerId}/quota`)
      const json = await res.json()
      if (json.success) {
        setSnapshot(json.data.snapshot)
      } else if (json.message === 'no quota snapshot cached for this provider') {
        setSnapshot(null)
      } else {
        message.error(json.message || '加载失败')
      }
    } catch {
      message.error('加载失败')
    } finally {
      setLoading(false)
    }
  }, [providerId])

  useEffect(() => {
    void load()
  }, [load])

  if (providerId == null) return null

  return (
    <Drawer
      title={`配额详情 — ${providerName ?? providerId}`}
      open={providerId != null}
      onClose={onClose}
      width={560}
      extra={
        <Button type="primary" loading={loading} onClick={() => void onRefresh(providerId).then(load)}>
          立即刷新
        </Button>
      }
    >
      {loading && !snapshot ? (
        <Skeleton active />
      ) : !snapshot ? (
        <Empty description="暂无缓存数据，请点击「立即刷新」拉取" />
      ) : snapshot.last_error ? (
        <Alert type="error" showIcon message="拉取失败" description={snapshot.last_error} />
      ) : snapshot.display_type === 'balance' && snapshot.balance ? (
        <BalanceView balance={snapshot.balance} />
      ) : snapshot.display_type === 'model_remains' && snapshot.models ? (
        <ModelRemainsView models={snapshot.models} />
      ) : (
        <Empty description="未知快照类型" />
      )}
      {snapshot && (
        <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginTop: 16 }}>
          上次更新：{dayjs(snapshot.fetched_at).format('YYYY-MM-DD HH:mm:ss')}
        </Typography.Text>
      )}
    </Drawer>
  )
}

const BalanceView = ({ balance }: { balance: QuotaBalance }) => (
  <div>
    <Badge status={balance.is_available ? 'success' : 'error'} text={balance.is_available ? '账户可用' : '账户不可用'} />
    <Row gutter={16} style={{ marginTop: 16 }}>
      <Col span={8}><Statistic title="总额" value={balance.total} prefix={balance.currency} /></Col>
      <Col span={8}><Statistic title="充值" value={balance.topped_up} prefix={balance.currency} /></Col>
      <Col span={8}><Statistic title="赠送" value={balance.granted} prefix={balance.currency} /></Col>
    </Row>
  </div>
)

const ModelRemainsView = ({ models }: { models: QuotaModel[] }) => {
  if (models.length === 0) return <Empty description="暂无模型数据" />
  const agg = aggregateModels(models)
  return (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <ProgressBlock
        label="5 小时用量"
        pct={agg.interval.usedPct}
        start={agg.interval.start}
        end={agg.interval.end}
        usage={agg.interval.usage}
        total={agg.interval.total}
        remainsMs={agg.interval.remainsMs}
      />
      <ProgressBlock
        label="本周用量"
        pct={agg.weekly.usedPct}
        start={agg.weekly.start}
        end={agg.weekly.end}
        usage={agg.weekly.usage}
        total={agg.weekly.total}
        remainsMs={agg.weekly.remainsMs}
      />
    </Space>
  )
}

const DualProgressLine = ({ models }: { models: QuotaModel[] }) => {
  const agg = aggregateModels(models)
  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 2 }}>
        <Typography.Text type="secondary" style={{ fontSize: 11 }}>5h</Typography.Text>
        <Typography.Text style={{ fontSize: 11 }}>{agg.interval.usedPct}%</Typography.Text>
      </div>
      <Progress
        percent={agg.interval.usedPct}
        strokeColor={quotaStatusColor(agg.interval.usedPct)}
        size="small"
        showInfo={false}
      />
      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6, marginBottom: 2 }}>
        <Typography.Text type="secondary" style={{ fontSize: 11 }}>本周</Typography.Text>
        <Typography.Text style={{ fontSize: 11 }}>{agg.weekly.usedPct}%</Typography.Text>
      </div>
      <Progress
        percent={agg.weekly.usedPct}
        strokeColor={quotaStatusColor(agg.weekly.usedPct)}
        size="small"
        showInfo={false}
      />
    </div>
  )
}

const ProgressBlock = ({
  label, pct, start, end, usage, total, remainsMs,
}: {
  label: string
  pct: number
  start?: string
  end?: string
  usage?: number
  total?: number
  remainsMs?: number
}) => (
  <div style={{ marginBottom: 12 }}>
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 4 }}>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>{label}</Typography.Text>
      <Typography.Text style={{ fontSize: 12 }}>剩余 {formatTime(remainsMs)}</Typography.Text>
    </div>
    <Progress
      percent={pct}
      strokeColor={quotaStatusColor(pct)}
      format={() => `${usage ?? '—'}/${total ?? '—'} (${pct}%)`}
    />
    {(start || end) && (
      <Typography.Text type="secondary" style={{ fontSize: 11 }}>
        {start ? dayjs(start).format('MM-DD HH:mm') : '—'} 至 {end ? dayjs(end).format('MM-DD HH:mm') : '—'}
      </Typography.Text>
    )}
  </div>
)

// ---------------------------------------------------------------------------
// Main: ConfigProvider
// ---------------------------------------------------------------------------

const supportedProtocols = (p: ProviderRecord) => {
  const tags: { color: string; label: string }[] = []
  if (p.openai_base_url) tags.push({ color: 'green', label: 'OpenAI' })
  if (p.anthropic_base_url) tags.push({ color: 'orange', label: 'Anthropic' })
  return tags
}

const ConfigProvider = () => {
  const currentUser = getCurrentUser()
  const isRoot = (currentUser?.role ?? 99) === 1
  const [data, setData] = useState<ProviderRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [editingRecord, setEditingRecord] = useState<ProviderRecord | null>(null)
  const [form] = Form.useForm()

  const [presets, setPresets] = useState<ProviderPreset[]>([])
  const [selectedPresetId, setSelectedPresetId] = useState<string | undefined>(undefined)

  // Quota state — fully independent from the provider table state.
  const [quotaEntries, setQuotaEntries] = useState<ProviderQuotaEntry[]>([])
  const [quotaLoading, setQuotaLoading] = useState(false)
  const [detailProviderId, setDetailProviderId] = useState<number | null>(null)

  // ---- Self-test state (one-button connectivity probe) ----
  // Reads unsaved form values, backend auto-detects the protocol.
  const [testLoading, setTestLoading] = useState(false)
  const [testResults, setTestResults] = useState<Record<string, {
    ok: boolean; model?: string; latency_ms?: number; status?: number; response?: string; error?: string;
  }> | null>(null)
  const [testModalOpen, setTestModalOpen] = useState(false)

  const fetchData = async () => {
    setLoading(true)
    try {
      const res = await apiFetch('/api/provider')
      const json = await res.json()
      if (json.success) setData(json.data)
    } catch {
      message.error('获取产商列表失败')
    } finally {
      setLoading(false)
    }
  }

  const fetchQuota = useCallback(async () => {
    setQuotaLoading(true)
    try {
      const res = await apiFetch('/api/provider/quota')
      const json = await res.json()
      if (json.success) setQuotaEntries(json.data)
    } catch {
      // Silent — quota card shows its own error state.
    } finally {
      setQuotaLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchData()
  }, [])

  useEffect(() => {
    void fetchQuota()
    const t = setInterval(() => void fetchQuota(), QUOTA_POLL_INTERVAL_MS)
    return () => clearInterval(t)
  }, [fetchQuota])

  // Load preset catalog once. Missing file is fine — selector just won't show.
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const res = await apiFetch('/api/provider/presets')
        const json = await res.json()
        if (cancelled) return
        if (json.success && Array.isArray(json.data)) {
          setPresets(json.data)
        }
      } catch {
        // Silent — empty preset list is acceptable.
      }
    })()
    return () => { cancelled = true }
  }, [])

  const applyPreset = (preset: ProviderPreset) => {
    form.setFieldsValue({
      name: preset.name,
      openai_base_url: preset.openai_base_url || undefined,
      anthropic_base_url: preset.anthropic_base_url || undefined,
      quota_url: preset.quota_url || undefined,
      quota_format: preset.quota_format || undefined,
      remark: preset.remark || undefined,
      // api_key intentionally left untouched — always user-supplied.
    })
  }

  const handleRefreshOne = useCallback(async (providerId: number) => {
    try {
      const res = await apiFetch(`/api/provider/${providerId}/quota/refresh`, { method: 'POST' })
      const json = await res.json()
      if (json.success) {
        message.success('已刷新')
      } else {
        message.error(json.message || '刷新失败')
      }
    } catch {
      message.error('刷新失败')
    }
    // Re-fetch to pick up the new state regardless of success/fail.
    void fetchQuota()
  }, [fetchQuota])

  const handleAdd = () => {
    setEditingRecord(null)
    setSelectedPresetId(undefined)
    form.resetFields()
    setModalVisible(true)
  }

  const handleEdit = (record: ProviderRecord) => {
    setEditingRecord(record)
    setSelectedPresetId(undefined)
    form.setFieldsValue({
      name: record.name,
      openai_base_url: record.openai_base_url,
      anthropic_base_url: record.anthropic_base_url,
      api_key: record.api_key,
      remark: record.remark,
      quota_url: record.quota_url,
      quota_format: record.quota_format,
    })
    setModalVisible(true)
  }

  const handleDelete = async (id: number) => {
    try {
      const res = await apiFetch(`/api/provider/${id}`, { method: 'DELETE' })
      const json = await res.json()
      if (json.success) {
        message.success('删除成功')
        void fetchData()
        void fetchQuota()
      } else {
        message.error('删除失败: ' + json.message)
      }
    } catch {
      message.error('删除失败')
    }
  }

  const handleSave = async () => {
    try {
      const values = await form.validateFields()
      if (!values.openai_base_url && !values.anthropic_base_url) {
        message.error('请至少填写一个协议的 Base URL')
        return
      }
      if (!values.api_key) {
        message.error('请填写 API Key')
        return
      }
      if (values.quota_url && !values.quota_format) {
        message.error('填写了 Quota URL 时必须同时选择 Quota Format')
        return
      }

      const isEdit = !!editingRecord
      const url = isEdit ? `/api/provider/${editingRecord.id}` : '/api/provider'
      const method = isEdit ? 'PUT' : 'POST'

      const res = await apiFetch(url, {
        method,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(values),
      })
      const json = await res.json()
      if (json.success) {
        message.success('保存成功')
        setModalVisible(false)
        void fetchData()
        void fetchQuota()
      } else {
        message.error('保存失败: ' + json.message)
      }
    } catch (e) {
      console.error(e)
    }
  }

  const quotaByProviderId = (id: number): ProviderQuotaEntry | undefined =>
    quotaEntries.find((e) => e.provider_id === id)

  // ---- One-button connectivity test ----
  // Reads unsaved form values; the backend auto-detects which protocol
  // is configured and runs a full list+chat cycle for it.
  const handleTestConnection = async () => {
    const v = form.getFieldsValue() as {
      openai_base_url?: string
      anthropic_base_url?: string
      api_key?: string
    }
    if (!v.api_key) {
      message.error('请先填写 API Key')
      return
    }
    if (!v.openai_base_url && !v.anthropic_base_url) {
      message.error('请至少填写一个协议的 Base URL')
      return
    }
    setTestLoading(true)
    setTestResults(null)
    try {
      const res = await apiFetch('/api/provider/test/connect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          openai_base_url: v.openai_base_url || undefined,
          anthropic_base_url: v.anthropic_base_url || undefined,
          api_key: v.api_key,
        }),
      })
      const json = await res.json()
      if (!json.success) {
        message.error(json.message || '测试失败')
        return
      }
      setTestResults(json.data.results || {})
      setTestModalOpen(true)
    } catch (e) {
      message.error(e instanceof Error ? e.message : '测试失败')
    } finally {
      setTestLoading(false)
    }
  }

  useEffect(() => {
    if (!modalVisible) {
      setTestResults(null)
      setTestModalOpen(false)
    }
  }, [modalVisible])

  const columns: TableColumnsType<ProviderRecord> = [
    { title: '产商名称', dataIndex: 'name' },
    {
      title: '支持协议',
      dataIndex: 'id',
      width: 200,
      render: (_, record) => {
        const tags = supportedProtocols(record)
        if (tags.length === 0) return <Tag color="default">未配置</Tag>
        return (
          <Space size={4}>
            {tags.map((t) => (
              <Tag key={t.label} color={t.color}>{t.label}</Tag>
            ))}
          </Space>
        )
      },
    },
    { title: '备注', dataIndex: 'remark', ellipsis: true },
    {
      title: '配额摘要',
      key: 'quota',
      width: 220,
      render: (_, record) => {
        const entry = quotaByProviderId(record.id)
        if (!entry || !entry.has_config) return <Typography.Text type="secondary">未配置</Typography.Text>
        const sum = summarizeSnapshot(entry.snapshot)
        return (
          <Space size={4}>
            <Tag color={sum.tone === 'success' ? 'green' : sum.tone === 'warning' ? 'orange' : sum.tone === 'error' ? 'red' : 'default'}>
              {sum.text}
            </Tag>
            {entry.snapshot?.fetched_at && (
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                {dayjs(entry.snapshot.fetched_at).format('HH:mm:ss')}
              </Typography.Text>
            )}
          </Space>
        )
      },
    },
    { title: '更新时间', dataIndex: 'update_time', width: 170, render: (t) => dayjs(t).format('YYYY-MM-DD HH:mm:ss') },
    {
      title: '操作',
      width: 200,
      render: (_, record) => (
        <Space>
          <Button type="link" size="small" onClick={() => setDetailProviderId(record.id)}>配额</Button>
          {isRoot && <Button type="link" size="small" onClick={() => handleEdit(record)}>编辑</Button>}
          {isRoot && <Popconfirm title="确定删除吗？" onConfirm={() => handleDelete(record.id)}>
            <Button type="link" danger size="small">删除</Button>
          </Popconfirm>}
        </Space>
      ),
    },
  ]

  return (
    <>
      <QuotaOverviewCard
        entries={quotaEntries}
        loading={quotaLoading}
        onRefreshOne={handleRefreshOne}
        onOpenDetail={setDetailProviderId}
      />

      <Card
        title="大模型产商配置"
        extra={isRoot ? <Button type="primary" onClick={handleAdd}>新增产商</Button> : null}
        variant="borderless"
      >
        <Table
          columns={columns}
          dataSource={data}
          rowKey="id"
          loading={loading}
          pagination={{ pageSize: 10 }}
          size="middle"
        />

        <Modal
          title={editingRecord ? '编辑产商' : '新增产商'}
          open={modalVisible}
          onOk={handleSave}
          onCancel={() => setModalVisible(false)}
          destroyOnHidden
          width={720}
        >
          <Form form={form} layout="vertical">
            {!editingRecord && presets.length > 0 && (
              <Form.Item label="选择预置产商" tooltip="选填；选择后会自动填入接口地址和配额查询，仍可手动修改">
                <Select
                  allowClear
                  showSearch
                  placeholder={presets.length === 0 ? '暂无预置' : '从预置列表中选择（可选）'}
                  value={selectedPresetId}
                  onChange={(id: string | undefined) => {
                    setSelectedPresetId(id)
                    if (!id) return
                    const p = presets.find((x) => x.id === id)
                    if (p) applyPreset(p)
                  }}
                  options={presets.map((p) => ({
                    value: p.id,
                    label: p.name,
                    title: p.description || p.name,
                  }))}
                  optionFilterProp="label"
                />
              </Form.Item>
            )}
            <Form.Item name="name" label="产商名称" rules={[{ required: true }]}>
              <Input placeholder="如: DashScope" />
            </Form.Item>
            <Form.Item name="remark" label="备注">
              <Input.TextArea placeholder="输入备注信息..." rows={2} />
            </Form.Item>

            <Form.Item style={{ marginBottom: 8 }}>
              <Typography.Text strong>接口地址</Typography.Text>
            </Form.Item>
            <Form.Item
              name="openai_base_url"
              label="OpenAI Base URL"
              tooltip="留空表示该厂商不提供 OpenAI 协议"
            >
              <Input placeholder="如: https://dashscope.aliyuncs.com/compatible-mode/v1" />
            </Form.Item>
            <Form.Item
              name="anthropic_base_url"
              label="Anthropic Base URL"
              tooltip="留空表示该厂商不提供 Anthropic 协议"
            >
              <Input placeholder="如: https://dashscope.aliyuncs.com/apps/anthropic" />
            </Form.Item>

            <Form.Item style={{ marginBottom: 8 }}>
              <Typography.Text strong>认证</Typography.Text>
            </Form.Item>
            <Form.Item
              name="api_key"
              label="API Key"
              tooltip="同一厂商对两种协议共用一个 Key，配额查询也复用此 Key"
              rules={[{ required: true }]}
            >
              <Input.Password placeholder="输入 API Key" />
            </Form.Item>

            <Form.Item style={{ marginBottom: 8 }}>
              <Typography.Text strong>配额查询（可选）</Typography.Text>
            </Form.Item>
            <Form.Item
              name="quota_url"
              label="Quota URL"
              tooltip="留空表示不查询此产商的配额"
            >
              <Input placeholder="如: https://www.minimaxi.com/v1/api/openplatform/coding_plan/remains" />
            </Form.Item>
            <Form.Item
              name="quota_format"
              label="Quota Format"
              tooltip="选择此产商配额的响应格式"
            >
              <Select
                allowClear
                placeholder="选择配额格式"
                options={QUOTA_FORMATS as unknown as { value: string; label: string }[]}
              />
            </Form.Item>

          </Form>

          <div style={{ marginTop: 16 }}>
            <Button
              type="primary"
              onClick={handleTestConnection}
              loading={testLoading}
            >
              测试连接
            </Button>
          </div>
        </Modal>

        <Modal
          title="测试连接结果"
          open={testModalOpen}
          onCancel={() => setTestModalOpen(false)}
          footer={<Button onClick={() => setTestModalOpen(false)}>关闭</Button>}
          width={600}
          centered
        >
          {testResults && Object.entries(testResults).map(([proto, r]) => {
            const protoLabel = proto === 'openai' ? 'OpenAI' : 'Anthropic'
            return (
              <Alert
                key={proto}
                style={{ marginBottom: 12 }}
                type={r.ok ? 'success' : 'error'}
                showIcon
                message={
                  <Space size="small" wrap>
                    <Tag color={r.ok ? 'success' : 'error'}>{protoLabel}</Tag>
                    {r.model && <Typography.Text type="secondary" style={{ fontSize: 12 }}>model: {r.model}</Typography.Text>}
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>{r.latency_ms}ms</Typography.Text>
                    {r.status ? <Tag>{r.status}</Tag> : null}
                  </Space>
                }
                description={
                  r.ok ? (
                    <Typography.Paragraph
                      copyable
                      style={{ whiteSpace: 'pre-wrap', marginBottom: 0, marginTop: 4 }}
                    >
                      {r.response || '（空响应）'}
                    </Typography.Paragraph>
                  ) : (
                    <Typography.Paragraph
                      copyable={{ tooltips: ['复制错误'] }}
                      style={{ whiteSpace: 'pre-wrap', marginBottom: 0, marginTop: 4, color: '#ff4d4f' }}
                    >
                      {r.error || '未知错误'}
                    </Typography.Paragraph>
                  )
                }
              />
            )
          })}
        </Modal>
      </Card>

      <QuotaDetailDrawer
        providerId={detailProviderId}
        providerName={detailProviderId != null ? data.find((p) => p.id === detailProviderId)?.name : undefined}
        onClose={() => setDetailProviderId(null)}
        onRefresh={handleRefreshOne}
      />
    </>
  )
}

export default ConfigProvider
